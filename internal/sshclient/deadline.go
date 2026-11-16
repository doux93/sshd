package sshclient

import (
	"context"
	"net"
	"time"
)

// Close on cancellation as well as setting a deadline: tunneled SSH connections
// need not implement SetDeadline. Successful handshakes clear temporary deadlines.
func guardHandshake(ctx context.Context, conn net.Conn, timeout time.Duration) func() {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { conn.Close(); close(done) })
	return func() {
		if !stop() {
			<-done
		}
		cancel()
		conn.SetDeadline(time.Time{})
	}
}
func retryDelay(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
