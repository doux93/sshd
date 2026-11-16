package sshclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// startTestSFTPServer runs an in-process SSH server that serves the real
// SFTP subsystem rooted at the returned temp directory.
func startTestSFTPServer(t *testing.T) (host string, port int, root string) {
	t.Helper()
	root = t.TempDir()

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
					go func(ch ssh.Channel, reqs <-chan *ssh.Request) {
						for req := range reqs {
							switch {
							case req.Type == "subsystem" && len(req.Payload) > 4 &&
								string(req.Payload[4:]) == "sftp":
								req.Reply(true, nil)
								srv, err := sftp.NewServer(ch)
								if err != nil {
									ch.Close()
									return
								}
								srv.Serve()
								ch.Close()
								return
							case req.Type == "pty-req", req.Type == "shell", req.Type == "window-change":
								req.Reply(true, nil)
							default:
								req.Reply(false, nil)
							}
						}
					}(ch, chReqs)
				}
			}()
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port, root
}

func dialTestClient(t *testing.T, host string, port int) *Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	c, err := Dial(ctx, Config{Host: host, Port: port, User: "test", Password: "secret"})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestSFTPListAndTransfer(t *testing.T) {
	host, port, root := startTestSFTPServer(t)
	// The sftp server serves the whole filesystem; use absolute temp paths.
	rootSlash := filepath.ToSlash(root)

	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hi there"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	client := dialTestClient(t, host, port)
	files, err := client.OpenFiles()
	if err != nil {
		t.Fatalf("open files: %v", err)
	}

	entries, err := files.List(rootSlash)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	byName := map[string]bool{} // name -> isDir
	for _, e := range entries {
		byName[e.Name] = e.IsDir
	}
	if isDir, ok := byName["hello.txt"]; !ok || isDir {
		t.Fatalf("hello.txt missing or misreported: %+v", entries)
	}
	if isDir, ok := byName["sub"]; !ok || !isDir {
		t.Fatalf("sub dir missing or misreported: %+v", entries)
	}

	// Download.
	r, err := files.Open(path.Join(rootSlash, "hello.txt"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	content, _ := io.ReadAll(r)
	r.Close()
	if !bytes.Equal(content, []byte("hi there")) {
		t.Fatalf("download = %q", content)
	}

	// Upload.
	w, err := files.Create(path.Join(rootSlash, "uploaded.bin"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	payload := bytes.Repeat([]byte("x"), 100_000) // exercise chunked writes
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "uploaded.bin"))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("uploaded file mismatch (len %d, err %v)", len(got), err)
	}
}

func TestSFTPMutations(t *testing.T) {
	host, port, root := startTestSFTPServer(t)
	rootSlash := filepath.ToSlash(root)
	client := dialTestClient(t, host, port)
	files, err := client.OpenFiles()
	if err != nil {
		t.Fatal(err)
	}

	newDir := path.Join(rootSlash, "created")
	if err := files.Mkdir(newDir); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(root, "created")); err != nil || !fi.IsDir() {
		t.Fatalf("mkdir did not create a directory: %v", err)
	}

	os.WriteFile(filepath.Join(root, "old.txt"), []byte("data"), 0o644)
	if err := files.Rename(path.Join(rootSlash, "old.txt"), path.Join(rootSlash, "new.txt")); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "new.txt")); err != nil {
		t.Fatalf("rename target missing: %v", err)
	}

	if err := files.Remove(path.Join(rootSlash, "new.txt")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "new.txt")); !os.IsNotExist(err) {
		t.Fatal("file should be gone")
	}

	// Recursive delete of a populated tree.
	os.MkdirAll(filepath.Join(root, "tree", "deep"), 0o755)
	os.WriteFile(filepath.Join(root, "tree", "deep", "f.txt"), []byte("x"), 0o644)
	if err := files.RemoveAll(path.Join(rootSlash, "tree")); err != nil {
		t.Fatalf("removeall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "tree")); !os.IsNotExist(err) {
		t.Fatal("tree should be gone")
	}
}

func TestSFTPClientIsReused(t *testing.T) {
	host, port, _ := startTestSFTPServer(t)
	client := dialTestClient(t, host, port)

	f1, err := client.OpenFiles()
	if err != nil {
		t.Fatal(err)
	}
	f2, err := client.OpenFiles()
	if err != nil {
		t.Fatal(err)
	}
	if f1 != f2 {
		t.Fatal("OpenFiles must reuse one SFTP subsystem per connection")
	}
}
