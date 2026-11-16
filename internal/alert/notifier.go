package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// Notifier sends alert notifications. The first implementation is a simple
// webhook/ntfy HTTP POST ? the design doc's "do this first" channel.
type Notifier interface {
	Notify(ctx context.Context, a Alert) error
}

// WebhookNotifier POSTs alerts as JSON to a URL. Works for ntfy
// (https://ntfy.sh/topic) and generic webhook receivers.
type WebhookNotifier struct {
	URL     string
	Headers map[string]string
	Timeout time.Duration
	log     *slog.Logger
}

func NewWebhookNotifier(url string, headers map[string]string, log *slog.Logger) *WebhookNotifier {
	if log == nil {
		log = slog.Default()
	}
	return &WebhookNotifier{
		URL:     url,
		Headers: headers,
		Timeout: 10 * time.Second,
		log:     log,
	}
}

func (w *WebhookNotifier) Notify(ctx context.Context, a Alert) error {
	if w.URL == "" {
		return nil
	}
	body, err := json.Marshal(a)
	if err != nil {
		return err
	}

	reqCtx, cancel := context.WithTimeout(ctx, w.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, w.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range w.Headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("webhook notify: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	w.log.Info("alert notified", "node", a.NodeID, "rule", a.Rule.Metric, "level", a.Level)
	return nil
}

// NtfyNotifier is a convenience for ntfy.sh: it sends the message as the
// request body (plain text) with a Title header, which is what ntfy expects.
type NtfyNotifier struct {
	Topic   string
	Server  string // default: https://ntfy.sh
	Timeout time.Duration
	log     *slog.Logger
}

func NewNtfyNotifier(topic, server string, log *slog.Logger) *NtfyNotifier {
	if server == "" {
		server = "https://ntfy.sh"
	}
	if log == nil {
		log = slog.Default()
	}
	return &NtfyNotifier{
		Topic:   topic,
		Server:  server,
		Timeout: 10 * time.Second,
		log:     log,
	}
}

func (n *NtfyNotifier) Notify(ctx context.Context, a Alert) error {
	url := fmt.Sprintf("%s/%s", n.Server, n.Topic)
	priority := "default"
	if a.Level == LevelCritical {
		priority = "urgent"
	}
	body := fmt.Sprintf("[%s] %s: %s", a.Level, a.NodeLabel, a.Message)

	reqCtx, cancel := context.WithTimeout(ctx, n.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader([]byte(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Title", "sshx alert: "+a.NodeLabel)
	req.Header.Set("Priority", priority)
	req.Header.Set("Tags", "warning")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("ntfy notify: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("ntfy returned %d", resp.StatusCode)
	}
	n.log.Info("alert notified via ntfy", "node", a.NodeID, "topic", n.Topic)
	return nil
}

// MultiNotifier fans out to multiple notifiers; a failure in one does not
// stop the others.
type MultiNotifier struct {
	notifiers []Notifier
	log       *slog.Logger
}

func NewMultiNotifier(log *slog.Logger, notifiers ...Notifier) *MultiNotifier {
	if log == nil {
		log = slog.Default()
	}
	return &MultiNotifier{notifiers: notifiers, log: log}
}

func (m *MultiNotifier) Notify(ctx context.Context, a Alert) error {
	var errs []error
	for _, n := range m.notifiers {
		if err := n.Notify(ctx, a); err != nil {
			m.log.Warn("notifier failed", "err", err)
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%d/%d notifiers failed", len(errs), len(m.notifiers))
	}
	return nil
}

// noopNotifier discards alerts; used when no notifier is configured.
type noopNotifier struct{}

func (noopNotifier) Notify(context.Context, Alert) error { return nil }

// NoopNotifier returns a notifier that discards everything.
func NoopNotifier() Notifier { return noopNotifier{} }
