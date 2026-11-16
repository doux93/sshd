package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"sshx/internal/alert"
	"sshx/internal/session"
	"sshx/internal/store"
)

// Manager owns the lifecycle of all Collectors. It starts a collector when
// a session is created (if the node has monitoring enabled), stops it when
// the session closes, and exposes metrics to the server layer.
type Manager struct {
	mu         sync.Mutex
	collectors map[session.ID]*Collector
	alertState *alert.StateTracker
	notifier   alert.Notifier
	rules      map[string][]alert.Rule // nodeID ? rules (resolved at start)
	log        *slog.Logger

	// OnSample is called (under no lock) when a new sample is pushed. The
	// server wires this to broadcast metrics over WebSocket.
	OnSample func(sessionID session.ID, nodeID string, s Sample)
	// OnAlert is called when an alert fires. The server broadcasts it.
	OnAlert func(nodeID string, a alert.Alert)
}

func NewManager(notifier alert.Notifier, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	if notifier == nil {
		notifier = alert.NoopNotifier()
	}
	return &Manager{
		collectors: make(map[session.ID]*Collector),
		alertState: alert.NewStateTracker(30 * time.Minute),
		notifier:   notifier,
		rules:      make(map[string][]alert.Rule),
		log:        log,
	}
}

// StartCollector begins monitoring a session. nodeID ties the session to its
// node config (for alert rules and the REST metrics API); it may be "" for
// quick-connect sessions without a stored node.
func (m *Manager) StartCollector(sess *session.Session, nodeID string, cfg *store.MonitorConfig, rules []alert.Rule) {
	if cfg == nil || !cfg.Enabled {
		return
	}
	interval := time.Duration(cfg.Interval) * time.Second
	if interval < MinInterval {
		interval = DefaultInterval
	}
	c := NewCollector(sess, interval, m.log)
	c.Start()

	m.mu.Lock()
	m.collectors[sess.ID()] = c
	if nodeID != "" {
		m.rules[nodeID] = rules
	}
	m.mu.Unlock()

	// Background goroutine: read samples from the series, evaluate alerts,
	// and notify listeners. The collector pushes into the series; this
	// goroutine polls it at the collection interval.
	go m.watch(sess.ID(), nodeID, c, interval, rules)
}

func (m *Manager) watch(sid session.ID, nodeID string, c *Collector, interval time.Duration, rules []alert.Rule) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var lastSample time.Time
	for {
		select {
		case <-ticker.C:
			s, ok := c.Series().Latest()
			if !ok || !s.TS.After(lastSample) {
				continue
			}
			lastSample = s.TS
			if m.OnSample != nil {
				m.OnSample(sid, nodeID, s)
			}
			if nodeID != "" && len(rules) > 0 {
				extractor := sampleExtractor(s)
				alerts := m.alertState.EvalAll(nodeID, rules, extractor, time.Now())
				for _, a := range alerts {
					a.NodeID = nodeID
					a.NodeLabel = sessLabel(c)
					if m.OnAlert != nil {
						m.OnAlert(nodeID, a)
					}
					m.mu.Lock()
					notifier := m.notifier
					m.mu.Unlock()
					go notifier.Notify(context.Background(), a)
				}
			}
		case <-c.done():
			return
		}
	}
}

// StopCollector tears down the collector for a session.
func (m *Manager) StopCollector(sid session.ID) {
	m.mu.Lock()
	c, ok := m.collectors[sid]
	delete(m.collectors, sid)
	m.mu.Unlock()
	if ok {
		c.Stop()
	}
}

// Metrics returns the time series for a session, or nil if not monitored.
func (m *Manager) Metrics(sid session.ID) *TimeSeries {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.collectors[sid]
	if !ok {
		return nil
	}
	select {
	case <-c.done():
		return nil
	default:
		return c.Series()
	}
}

// StopAll tears down every collector (server shutdown).
func (m *Manager) StopAll() {
	m.mu.Lock()
	collectors := make([]*Collector, 0, len(m.collectors))
	for _, c := range m.collectors {
		collectors = append(collectors, c)
	}
	m.collectors = map[session.ID]*Collector{}
	m.mu.Unlock()
	for _, c := range collectors {
		c.Stop()
	}
}

// sessLabel extracts the session label for alert messages.
func sessLabel(c *Collector) string {
	return c.session.Info().Label
}

// sampleExtractor returns a function that maps metric names to values from
// a Sample. This bridges monitor ? alert without alert importing monitor.
func sampleExtractor(s Sample) func(string) (float64, error) {
	return func(metric string) (float64, error) {
		switch metric {
		case "cpu", "cpuUsage":
			return s.CPUUsage, nil
		case "mem", "memUsage":
			return s.MemUsage, nil
		case "load", "load1":
			return s.Load1, nil
		case "disk":
			return s.MaxDiskUsage(), nil
		case "host":
			return 1, nil
		default:
			return 0, fmt.Errorf("unknown metric %q", metric)
		}
	}
}

// SetNotifier swaps the alert notifier at runtime. The original wire-up
// happens in NewManager; this is the post-construction injection point
// so main.go can defer the VAPID key setup until after the auth
// bootstrap has finished.
func (m *Manager) SetNotifier(n alert.Notifier) {
	if n == nil {
		n = alert.NoopNotifier()
	}
	m.mu.Lock()
	m.notifier = n
	m.mu.Unlock()
}
