package sshclient

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestReview_HandshakeHonorsDeadline(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := Dial(ctx, Config{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, User: "review", Password: "review", Timeout: 50 * time.Millisecond})
		done <- err
	}()
	var conn net.Conn
	select {
	case conn = <-accepted:
	case <-time.After(time.Second):
		t.Fatal("no connection")
	}
	defer conn.Close()
	select {
	case <-done:
	case <-time.After(300 * time.Millisecond):
		t.Error("SSH handshake still blocked after context deadline and configured timeout")
	}
}
