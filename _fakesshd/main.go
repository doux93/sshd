// Throwaway SSH server for manually driving the app. Two modes:
//
//   - Default: a minimal "shell" that echoes input, understands `cd PATH` /
//     `pwd`, and emits OSC 7 (file://host/path BEL) after every cd and at
//     every prompt. That matches how real bash with PROMPT_COMMAND behaves
//     and lets sshx's file-follow be verified end-to-end without a real box.
//
//   - The ruler() helper used to be the whole server. It is still here as
//     a debug tool: if you set FAKE_SSH_RULER=1, the session echoes the
//     PTY dimensions instead of running a shell, useful for layout checks.
//
// Lives under _fakesshd so the go tool's ./... patterns skip it.
// Run: go run _fakesshd/main.go -addr 127.0.0.1:2222
package main

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"flag"
	"fmt"
	"github.com/pkg/sftp"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:2222", "listen address")
	root := flag.String("root", ".", "test-only SFTP working directory")
	flag.Parse()
	dir, err := filepath.Abs(*root)
	if err != nil {
		log.Fatal(err)
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		log.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(m ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if m.User() == "test" && string(pass) == "secret" {
				return nil, nil
			}
			return nil, fmt.Errorf("denied")
		},
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("fake sshd on %s (test/secret)", ln.Addr())

	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go serve(c, cfg, dir)
	}
}

func serve(c net.Conn, cfg *ssh.ServerConfig, root string) {
	sc, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	defer sc.Close()
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
				case "pty-req":
					p := req.Payload
					if len(p) >= 4 {
						n := binary.BigEndian.Uint32(p)
						if int(4+n+8) <= len(p) {
							cols := binary.BigEndian.Uint32(p[4+n:])
							rows := binary.BigEndian.Uint32(p[4+n+4:])
							if os.Getenv("FAKE_SSH_RULER") == "1" {
								ruler(ch, int(cols), int(rows), "pty-req")
							}
						}
					}
					req.Reply(true, nil)
				case "window-change":
					if len(req.Payload) >= 8 {
						cols := binary.BigEndian.Uint32(req.Payload)
						rows := binary.BigEndian.Uint32(req.Payload[4:])
						if os.Getenv("FAKE_SSH_RULER") == "1" {
							ruler(ch, int(cols), int(rows), "window-change")
						}
					}
					req.Reply(true, nil)
				case "shell":
					req.Reply(true, nil)
					go fakeShell(ch, ch, root)
				case "subsystem":
					var payload struct{ Name string }
					if ssh.Unmarshal(req.Payload, &payload) != nil || payload.Name != "sftp" {
						req.Reply(false, nil)
						continue
					}
					req.Reply(true, nil)
					go func() {
						server, err := sftp.NewServer(ch, sftp.WithServerWorkingDirectory(root))
						if err != nil {
							ch.Close()
							return
						}
						defer server.Close()
						server.Serve()
					}()
				case "exec":
					if len(req.Payload) >= 4 {
						n := binary.BigEndian.Uint32(req.Payload)
						if int(4+n) <= len(req.Payload) {
							cmd := string(req.Payload[4 : 4+n])
							req.Reply(true, nil)
							if strings.Contains(cmd, "command -v tmux") {
								ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{127}))
							} else {
								fmt.Fprintf(ch, "exec:%s\n", cmd)
								ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{0}))
							}
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

	}
}

// fakeShell is a minimal command interpreter that emits OSC 7 after every
// cd and at every prompt, so the sshx file panel can follow terminal cwd.
//
// OSC 7 escape: ESC ] 7 ; file://host/path BEL  (BEL = \a)
func fakeShell(in io.Reader, out io.Writer, root string) {
	scanner := bufio.NewScanner(terminalReader{in})
	cwd := filepath.ToSlash(root)
	if !strings.HasPrefix(cwd, "/") {
		cwd = "/" + cwd
	}
	prompt := func() {
		fmt.Fprintf(out, "\x1b]7;file://localhost%s\x07%s $ ", cwd, cwd)
	}
	prompt()
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "__sshx_osc7()") {
			fmt.Fprint(out, "\r\x1b[2K")
			prompt()
			continue
		}
		fmt.Fprintf(out, "%s\r\n", line)
		if line == "" {
			prompt()
			continue
		}
		switch {
		case strings.HasPrefix(line, "cd "):
			target := strings.TrimSpace(strings.TrimPrefix(line, "cd "))
			target = strings.Trim(target, "\"'")
			if !strings.HasPrefix(target, "/") {
				target = cwd + "/" + target
			}
			cwd = cleanPath(target)
			fmt.Fprintf(out, "\x1b]7;file://localhost%s\x07", cwd)
		case line == "pwd":
			fmt.Fprintf(out, "%s\r\n", cwd)
		default:
			fmt.Fprintf(out, "fake-sh: %s: command not found\r\n", strings.Fields(line)[0])
		}
		prompt()
	}
}

func cleanPath(p string) string {
	parts := strings.Split(p, "/")
	out := make([]string, 0, len(parts))
	for _, s := range parts {
		switch s {
		case "", ".":
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, s)
		}
	}
	return "/" + strings.Join(out, "/")
}

// ruler prints a horizontal bar exactly cols wide. If the rendered line
// wraps or falls short of the window, the terminal geometry is wrong.
func ruler(w io.Writer, cols, rows int, why string) {
	if cols <= 0 || cols > 1000 {
		return
	}
	label := fmt.Sprintf(" %s: %dx%d ", why, cols, rows)
	bar := strings.Repeat("=", cols)
	if len(label) < cols {
		start := (cols - len(label)) / 2
		bar = bar[:start] + label + bar[start+len(label):]
	}
	fmt.Fprintf(w, "\r\n\x1b[36m%s\x1b[0m\r\n", bar)
}

// The fixture has no OS PTY line discipline; normalize Enter like a real PTY.
type terminalReader struct{ io.Reader }

func (r terminalReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	for i := 0; i < n; i++ {
		if p[i] == '\r' {
			p[i] = '\n'
		}
	}
	return n, err
}
