package sshclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// startTestSSHServer runs a minimal in-process SSH server that accepts
// password auth (user "test" / "secret") and echoes terminal input back.
func startTestSSHServer(t *testing.T) (host string, port int) {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}

	cfg := &ssh.ServerConfig{
		PasswordCallback: func(meta ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if meta.User() == "test" && string(pass) == "secret" {
				return nil, nil
			}
			return nil, io.EOF
		},
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, chans, reqs, err := ssh.NewServerConn(conn, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for newCh := range chans {
					if newCh.ChannelType() != "session" {
						newCh.Reject(ssh.UnknownChannelType, "unsupported")
						continue
					}
					ch, chReqs, err := newCh.Accept()
					if err != nil {
						continue
					}
					go func() {
						for req := range chReqs {
							switch req.Type {
							case "pty-req", "shell", "window-change":
								req.Reply(true, nil)
							case "exec":
								// SSH exec payload: uint32 length + command string.
								if len(req.Payload) >= 4 {
									n := binary.BigEndian.Uint32(req.Payload)
									if int(4+n) <= len(req.Payload) {
										cmd := string(req.Payload[4 : 4+n])
										req.Reply(true, nil)
										fmt.Fprintf(ch, "exec:%s\n", cmd)
										// Send exit-status 0 so sess.Wait() returns cleanly.
										ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{0}))
										ch.Close()
										continue
									}
								}
								req.Reply(false, nil)
							default:
								req.Reply(false, nil)
							}
						}
					}()
					go io.Copy(ch, ch) // echo shell
				}
			}()
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port
}

func TestDialAndTerminalEcho(t *testing.T) {
	host, port := startTestSSHServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := Dial(ctx, Config{Host: host, Port: port, User: "test", Password: "secret"})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	if client.Kind() != "ssh" {
		t.Errorf("kind = %q", client.Kind())
	}
	wantTarget := "test@127.0.0.1:" + strconv.Itoa(port)
	if client.Target() != wantTarget {
		t.Errorf("target = %q, want %q", client.Target(), wantTarget)
	}

	tio, err := client.OpenTerminal(80, 24)
	if err != nil {
		t.Fatalf("open terminal: %v", err)
	}
	defer tio.Close()

	msg := []byte("hello-sshx\n")
	if _, err := tio.Write(msg); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := make([]byte, 0, len(msg))
	buf := make([]byte, 64)
	deadline := time.Now().Add(5 * time.Second)
	for len(got) < len(msg) && time.Now().Before(deadline) {
		n, err := tio.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil {
			break
		}
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("echo = %q, want %q", got, msg)
	}

	if err := tio.Resize(120, 40); err != nil {
		t.Errorf("resize: %v", err)
	}
}

func TestDialBadPassword(t *testing.T) {
	host, port := startTestSSHServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := Dial(ctx, Config{Host: host, Port: port, User: "test", Password: "wrong"}); err == nil {
		t.Fatal("expected auth failure")
	}
}

func TestExecReturnsOutputAndExitCode(t *testing.T) {
	host, port := startTestSSHServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := Dial(ctx, Config{Host: host, Port: port, User: "test", Password: "secret"})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	stdout, stderr, exitCode, err := client.Exec(ctx, "uname -a")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("exitCode = %d, want 0", exitCode)
	}
	if string(stdout) != "exec:uname -a\n" {
		t.Errorf("stdout = %q, want %q", stdout, "exec:uname -a\n")
	}
	if len(stderr) != 0 {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

func TestExecStreamReadsLiveOutput(t *testing.T) {
	host, port := startTestSSHServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := Dial(ctx, Config{Host: host, Port: port, User: "test", Password: "secret"})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	rc, err := client.ExecStream(ctx, "monitor-loop")
	if err != nil {
		t.Fatalf("exec stream: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "exec:monitor-loop\n" {
		t.Errorf("stream = %q, want %q", got, "exec:monitor-loop\n")
	}
}
