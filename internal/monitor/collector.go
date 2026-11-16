package monitor

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"sshx/internal/session"
)

// Collector runs a single long-lived exec channel that periodically dumps
// /proc stats. Each iteration's output is parsed into a Sample and pushed
// into the TimeSeries. The collector has its own reconnect loop: if the
// stream dies (network drop, session closed) it stops cleanly.
type Collector struct {
	session  *session.Session
	series   *TimeSeries
	interval time.Duration
	log      *slog.Logger

	mu      sync.Mutex
	stop    chan struct{}
	stopped bool
	lastCPU cpuSnapshot // for delta-based CPU calculation
}

// cpuSnapshot stores the previous /proc/stat aggregate values so we can
// compute CPU usage as a delta between samples rather than a boot average.
type cpuSnapshot struct {
	total uint64
	idle  uint64
	valid bool
}

// MinInterval is the floor for the collection interval (design doc: 2s).
const MinInterval = 2 * time.Second
const DefaultInterval = 5 * time.Second

// NewCollector creates a collector bound to a session. It does not start
// collecting until Start is called.
func NewCollector(sess *session.Session, interval time.Duration, log *slog.Logger) *Collector {
	if interval < MinInterval {
		interval = DefaultInterval
	}
	if log == nil {
		log = slog.Default()
	}
	return &Collector{
		session:  sess,
		series:   NewTimeSeries(),
		interval: interval,
		log:      log,
		stop:     make(chan struct{}),
	}
}

// Series returns the time series for reading. It is safe to call before
// Start or after Stop.
func (c *Collector) Series() *TimeSeries { return c.series }

// Start launches the collection loop in a goroutine. It returns immediately.
func (c *Collector) Start() {
	go c.run()
}

// Stop terminates the collection loop. Safe to call multiple times.
func (c *Collector) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return
	}
	c.stopped = true
	close(c.stop)
}

// done returns a channel that is closed when the collector is stopped.
// Used by the Manager's watch goroutine to know when to exit.
func (c *Collector) done() <-chan struct{} {
	return c.stop
}

// run is the collection loop. It opens an ExecStream with a shell loop that
// dumps /proc every `interval` seconds, separated by a marker line.
func (c *Collector) run() {
	defer c.Stop()
	cmd := fmt.Sprintf(
		"while :; do cat /proc/stat /proc/meminfo /proc/loadavg /proc/net/dev; df -P; echo '%s'; sleep %d; done",
		marker, int(c.interval.Seconds()),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		<-c.stop
		cancel()
	}()

	rc, err := c.session.ExecStream(ctx, cmd)
	if err != nil {
		c.log.Warn("monitor: start exec stream", "err", err, "target", c.session.Info().Target)
		return
	}
	defer rc.Close()
	stopClose := context.AfterFunc(ctx, func() { rc.Close() })
	defer stopClose()

	c.readLoop(rc)
}

const marker = "---SSHX---"

// readLoop reads the exec stream, splitting on the marker line. Each chunk
// between markers is one sample.
func (c *Collector) readLoop(rc io.Reader) {
	scanner := bufio.NewScanner(rc)
	// A single /proc dump can be large-ish; raise the per-line limit.
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)

	var buf strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if line == marker {
			if buf.Len() > 0 {
				s := ParseProcOutput(buf.String(), time.Now())
				c.applyCPUDelta(&s)
				c.series.Push(s)
				buf.Reset()
			}
			continue
		}
		if buf.Len()+len(line) > 1<<20 {
			c.log.Warn("monitor: sample exceeds 1 MiB")
			return
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		c.log.Debug("monitor: stream ended", "err", err, "target", c.session.Info().Target)
	}
}

// applyCPUDelta converts the boot-cumulative CPU values from /proc/stat into
// a delta-based usage percentage. On the first sample the boot average
// (computed by ParseProcOutput) stays — there is no previous snapshot to
// subtract. On every subsequent sample the delta between this and the last
// snapshot is used, which is the only correct way to read CPU usage off
// /proc/stat (the columns are cumulative since boot).
func (c *Collector) applyCPUDelta(s *Sample) {
	if s.cpuTotal == 0 {
		return // no cpu line this round; keep the boot average
	}
	if c.lastCPU.valid {
		s.CPUUsage = cpuUsageFromDelta(c.lastCPU.total, c.lastCPU.idle, s.cpuTotal, s.cpuIdle)
	}
	c.lastCPU = cpuSnapshot{total: s.cpuTotal, idle: s.cpuIdle, valid: true}
}
