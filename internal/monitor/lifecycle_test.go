package monitor

import (
	"context"
	"io"
	"sshx/internal/session"
	"sync"
	"testing"
	"time"
)

type monitorTerminal struct{ *io.PipeReader }

func (m monitorTerminal) Write(p []byte) (int, error) { return len(p), nil }
func (m monitorTerminal) Resize(int, int) error       { return nil }

type observedReader struct {
	*io.PipeReader
	closed chan struct{}
	once   sync.Once
}

func (r *observedReader) Close() error {
	r.once.Do(func() { close(r.closed) })
	return r.PipeReader.Close()
}

type monitorBackend struct {
	term   monitorTerminal
	stream *observedReader
}

func (b *monitorBackend) Kind() string                                      { return "test" }
func (b *monitorBackend) Target() string                                    { return "test" }
func (b *monitorBackend) Close() error                                      { b.stream.Close(); return b.term.Close() }
func (b *monitorBackend) OpenTerminal(int, int) (session.TerminalIO, error) { return b.term, nil }
func (b *monitorBackend) Exec(context.Context, string) ([]byte, []byte, int, error) {
	return nil, nil, 0, nil
}
func (b *monitorBackend) ExecStream(context.Context, string) (io.ReadCloser, error) {
	return b.stream, nil
}
func TestStopClosesBlockedCollectorStream(t *testing.T) {
	tr, tw := io.Pipe()
	defer tw.Close()
	sr, sw := io.Pipe()
	defer sw.Close()
	backend := &monitorBackend{term: monitorTerminal{tr}, stream: &observedReader{PipeReader: sr, closed: make(chan struct{})}}
	mgr := session.NewManager()
	defer mgr.CloseAll()
	sess, err := mgr.Create(backend, session.Options{})
	if err != nil {
		t.Fatal(err)
	}
	c := NewCollector(sess, time.Second, nil)
	c.Start()
	c.Stop()
	select {
	case <-backend.stream.closed:
	case <-time.After(time.Second):
		t.Fatal("collector Stop did not close blocked stream")
	}
}
