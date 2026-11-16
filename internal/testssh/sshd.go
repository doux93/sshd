// Package testssh starts an isolated OpenSSH daemon for integration tests only.
package testssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"golang.org/x/crypto/ssh"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

type Endpoint struct {
	Host, User, KeyPath string
	Port                int
}

func Start(t *testing.T) Endpoint {
	t.Helper()
	require := os.Getenv("SSHX_REQUIRE_TMUX_TEST") == "1"
	unavailable := func(reason string) {
		if require {
			t.Fatal(reason)
		} else {
			t.Skip(reason)
		}
	}
	if runtime.GOOS != "linux" {
		unavailable("real OpenSSH/tmux fixture runs on Linux")
	}
	sshd, err := exec.LookPath("sshd")
	if err != nil {
		sshd = "/usr/sbin/sshd"
		if _, err = os.Stat(sshd); err != nil {
			unavailable("OpenSSH server not installed")
		}
	}
	if _, err = exec.LookPath("tmux"); err != nil {
		unavailable("tmux not installed")
	}
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	key := func(name string) (string, ssh.PublicKey) {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		block, err := ssh.MarshalPrivateKey(priv, "integration")
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name)
		if err = os.WriteFile(path, pem.EncodeToMemory(block), 0600); err != nil {
			t.Fatal(err)
		}
		signer, err := ssh.NewSignerFromKey(priv)
		if err != nil {
			t.Fatal(err)
		}
		return path, signer.PublicKey()
	}
	host, _ := key("host")
	identity, pub := key("identity")
	authorized := filepath.Join(dir, "authorized_keys")
	if err = os.WriteFile(authorized, ssh.MarshalAuthorizedKey(pub), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	config := fmt.Sprintf("ListenAddress 127.0.0.1\nPort %d\nHostKey %s\nPidFile %s\nAuthorizedKeysFile %s\nStrictModes no\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nUsePAM no\nPermitRootLogin prohibit-password\nAllowUsers %s\nLogLevel ERROR\n", port, host, filepath.Join(dir, "pid"), authorized, current.Username)
	configPath := filepath.Join(dir, "sshd_config")
	if err = os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "sshd.log")
	log, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(sshd, "-D", "-e", "-f", configPath)
	cmd.Stdout = log
	cmd.Stderr = log
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
		log.Close()
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return Endpoint{Host: "127.0.0.1", User: current.Username, KeyPath: identity, Port: port}
		}
		select {
		case err := <-done:
			data, _ := os.ReadFile(logPath)
			t.Fatalf("sshd exited: %v: %s", err, data)
		default:
		}
		time.Sleep(50 * time.Millisecond)
	}
	data, _ := os.ReadFile(logPath)
	t.Fatalf("sshd startup timed out: %s", data)
	return Endpoint{}
}
