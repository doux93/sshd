// Package sshclient implements session.Backend over SSH using
// golang.org/x/crypto/ssh. It is the only package that imports the SSH
// library; the session and server layers see it through interfaces.
package sshclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"sshx/internal/session"
)

// HostKeyVerifier decides whether to trust a host key. hostport is
// "host:port". Returning an error aborts the handshake; the connect service
// uses a typed error to drive the TOFU confirmation flow.
type HostKeyVerifier func(hostport string, key ssh.PublicKey) error

// Config describes how to reach and authenticate one SSH endpoint.
type Config struct {
	Host          string
	Port          int
	User          string
	Password      string // password auth (and keyboard-interactive answers)
	KeyPath       string // private key file auth
	KeyPassphrase string // passphrase for KeyPath if encrypted
	UseAgent      bool   // try ssh-agent first
	Timeout       time.Duration

	// VerifyHostKey must be set by callers. Only tests may leave it nil,
	// which accepts any key.
	VerifyHostKey HostKeyVerifier

	// Proxy is an optional SOCKS5 proxy address ("host:port"). When set,
	// the TCP connection is tunneled through the proxy before the SSH
	// handshake begins. No auth (typical for local Shadowsocks/V2Ray).
	Proxy string
}

func (c Config) addr() string {
	port := c.Port
	if port == 0 {
		port = 22
	}
	return net.JoinHostPort(c.Host, fmt.Sprint(port))
}

func (c Config) clientConfig() (*ssh.ClientConfig, func(), error) {
	var agentConn io.Closer
	cleanup := func() {
		if agentConn != nil {
			agentConn.Close()
		}
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 15 * time.Second
	}

	var auths []ssh.AuthMethod
	if c.UseAgent {
		if conn, err := dialAgent(); err == nil {
			agentConn = conn
			auths = append(auths, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
		}
	}
	if c.KeyPath != "" {
		signer, err := loadKey(c.KeyPath, c.KeyPassphrase)
		if err != nil {
			cleanup()
			return nil, func() {}, err
		}
		auths = append(auths, ssh.PublicKeys(signer))
	}
	if c.Password != "" {
		auths = append(auths,
			ssh.Password(c.Password),
			// Some servers only offer keyboard-interactive; answer prompts
			// with the password.
			ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = c.Password
				}
				return answers, nil
			}),
		)
	}
	if len(auths) == 0 {
		cleanup()
		return nil, func() {}, errors.New("no usable auth method (agent unavailable / no key / no password)")
	}

	hostKeyCallback := ssh.InsecureIgnoreHostKey()
	if c.VerifyHostKey != nil {
		verify := c.VerifyHostKey
		hostKeyCallback = func(hostname string, _ net.Addr, key ssh.PublicKey) error {
			return verify(hostname, key)
		}
	}

	return &ssh.ClientConfig{
		User:            c.User,
		Auth:            auths,
		HostKeyCallback: hostKeyCallback,
		// Modern OpenSSH (>= 8.5) defaults ssh-rsa to off, so the server
		// only offers rsa-sha2-512/256, ecdsa-sha2-*, ssh-ed25519. Go's
		// x/crypto/ssh advertises rsa-sha2-* in the host key list, but the
		// default config can still negotiate "ssh-rsa" first depending on
		// the Go version, leading to "unexpected packet in response to
		// channel open" when the server rejects it.
		HostKeyAlgorithms: []string{
			"rsa-sha2-512",
			"rsa-sha2-256",
			"ssh-ed25519",
			"ecdsa-sha2-nistp256",
			"ecdsa-sha2-nistp384",
			"ecdsa-sha2-nistp521",
		},
		Timeout: timeout,
	}, cleanup, nil
}

func loadKey(path, passphrase string) (ssh.Signer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}
	if passphrase != "" {
		signer, err := ssh.ParsePrivateKeyWithPassphrase(data, []byte(passphrase))
		if err != nil {
			return nil, fmt.Errorf("parse private key %s: %w", path, err)
		}
		return signer, nil
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		var missing *ssh.PassphraseMissingError
		if errors.As(err, &missing) {
			return nil, fmt.Errorf("private key %s is encrypted; passphrase required", path)
		}
		return nil, fmt.Errorf("parse private key %s: %w", path, err)
	}
	return signer, nil
}

// Client wraps an established SSH connection and implements session.Backend.
type Client struct {
	target    string
	conn      *ssh.Client
	closers   []io.Closer   // upstream jump clients closed together with this one
	stopKeep  chan struct{} // closes the keepalive loop
	keepOnce  sync.Once
	closeOnce sync.Once
	closeErr  error
	stateMu   sync.Mutex
	closed    bool

	sftpOnce sync.Once // the SFTP subsystem is started at most once per connection
	sftp     *sftpClient
	sftpErr  error
}

// keepaliveInterval keeps NAT/firewall state alive on idle sessions and
// detects dead peers (the design doc's 30s heartbeat).
const keepaliveInterval = 30 * time.Second

// startKeepalive pings the server periodically; two consecutive failures
// close the connection, which ends the terminal stream and lets the
// session layer mark the session closed.
func (c *Client) startKeepalive() {
	c.stopKeep = make(chan struct{})
	go func() {
		ticker := time.NewTicker(keepaliveInterval)
		defer ticker.Stop()
		failures := 0
		for {
			select {
			case <-ticker.C:
				done := make(chan error, 1)
				go func() { _, _, err := c.conn.SendRequest("keepalive@openssh.com", true, nil); done <- err }()
				var err error
				timer := time.NewTimer(10 * time.Second)
				select {
				case err = <-done:
					timer.Stop()
				case <-timer.C:
					c.Close()
					return
				case <-c.stopKeep:
					timer.Stop()
					return
				}
				if err != nil {
					failures++
					if failures >= 2 {
						c.Close()
						return
					}
				} else {
					failures = 0
				}
			case <-c.stopKeep:
				return
			}
		}
	}()
}

var _ session.Backend = (*Client)(nil)

// Dial establishes a direct SSH connection.
// dialMaxRetries is how many times Dial retries the full connect+handshake
// when it hits an EOF or connection reset ? common with flaky SOCKS proxies
// where the tunnel opens but the remote SSH endpoint doesn't respond.
const dialMaxRetries = 3

func Dial(ctx context.Context, cfg Config) (*Client, error) {
	sshCfg, cleanup, err := cfg.clientConfig()
	defer cleanup()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, sshCfg.Timeout)
	defer cancel()
	var lastErr error
	for attempt := 1; attempt <= dialMaxRetries; attempt++ {
		var netConn net.Conn
		if cfg.Proxy != "" {
			netConn, err = socksDial(ctx, cfg.Proxy, cfg.addr())
		} else {
			dialer := net.Dialer{Timeout: sshCfg.Timeout}
			netConn, err = dialer.DialContext(ctx, "tcp", cfg.addr())
		}
		if err != nil {
			lastErr = err
			if attempt < dialMaxRetries {
				if err := retryDelay(ctx, time.Duration(attempt)*time.Second); err != nil {
					return nil, err
				}
				continue
			}
			return nil, err
		}

		client, err := handshake(ctx, netConn, cfg, sshCfg)
		if err == nil {
			return client, nil
		}
		lastErr = err
		// Only retry on EOF/reset (flaky proxy); auth failures etc. are final.
		if !isRetryableHandshake(err) || attempt >= dialMaxRetries {
			return nil, err
		}
		if err := retryDelay(ctx, time.Duration(attempt)*time.Second); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

// isRetryableHandshake reports whether a handshake error is worth retrying
// (EOF, connection reset, i/o timeout — typical of an unstable proxy tunnel
// where the TCP connection opens but the remote doesn't respond). Also
// "unexpected packet in response to channel open" is included: the Go SSH
// client emits this when a SOCKS proxy occasionally delivers a stale byte
// stream from a previous connection. A second dial gets a fresh stream.
func isRetryableHandshake(err error) bool {
	s := err.Error()
	if strings.Contains(s, "EOF") {
		return true
	}
	if strings.Contains(s, "reset") {
		return true
	}
	if strings.Contains(s, "i/o timeout") {
		return true
	}
	if strings.Contains(s, "connection refused") {
		return true
	}
	if strings.Contains(s, "unexpected packet") {
		return true
	}
	return false
}

// DialThrough establishes an SSH connection tunneled through an existing
// client (ProxyJump). The jump chain is built by the connect service.
func DialThrough(ctx context.Context, via *Client, cfg Config) (*Client, error) {
	sshCfg, cleanup, err := cfg.clientConfig()
	defer cleanup()
	if err != nil {
		return nil, err
	}
	netConn, err := via.conn.DialContext(ctx, "tcp", cfg.addr())
	if err != nil {
		return nil, fmt.Errorf("dial %s via %s: %w", cfg.addr(), via.target, err)
	}
	return handshake(ctx, netConn, cfg, sshCfg)
}

func handshake(ctx context.Context, netConn net.Conn, cfg Config, sshCfg *ssh.ClientConfig) (*Client, error) {
	finish := guardHandshake(ctx, netConn, sshCfg.Timeout)
	defer finish()
	sshConn, chans, reqs, err := ssh.NewClientConn(netConn, cfg.addr(), sshCfg)
	if err != nil {
		netConn.Close()
		return nil, fmt.Errorf("ssh handshake %s: %w", cfg.addr(), err)
	}
	c := &Client{
		target: fmt.Sprintf("%s@%s", cfg.User, cfg.addr()),
		conn:   ssh.NewClient(sshConn, chans, reqs),
	}
	c.startKeepalive()
	return c, nil
}

func (c *Client) Kind() string   { return "ssh" }
func (c *Client) Target() string { return c.target }

// ChainCloser registers an upstream resource (e.g. the jump-host client)
// to be closed after this connection closes.
func (c *Client) ChainCloser(closer io.Closer) {
	c.stateMu.Lock()
	if c.closed {
		c.stateMu.Unlock()
		closer.Close()
		return
	}
	c.closers = append(c.closers, closer)
	c.stateMu.Unlock()
}

func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.keepOnce.Do(func() { close(c.stopKeep) })
		c.stateMu.Lock()
		c.closed = true
		sf := c.sftp
		upstream := append([]io.Closer(nil), c.closers...)
		c.stateMu.Unlock()
		c.closeErr = c.conn.Close()
		if sf != nil {
			sf.close()
		}
		for i := len(upstream) - 1; i >= 0; i-- {
			upstream[i].Close()
		}
	})
	return c.closeErr
}

// OpenTerminal starts an interactive shell with a PTY.
func (c *Client) OpenTerminal(cols, rows int) (session.TerminalIO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sess, err := c.newSession(ctx)
	if err != nil {
		return nil, fmt.Errorf("new ssh session: %w", err)
	}
	stop := context.AfterFunc(ctx, func() { sess.Close() })
	defer stop()
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := sess.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		sess.Close()
		return nil, fmt.Errorf("request pty: %w", err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		sess.Close()
		return nil, err
	}
	// With a PTY allocated, the remote merges stderr into the PTY stream,
	// so stdout alone carries everything a real terminal would show.
	stdout, err := sess.StdoutPipe()
	if err != nil {
		sess.Close()
		return nil, err
	}
	if err := sess.Shell(); err != nil {
		sess.Close()
		return nil, fmt.Errorf("start shell: %w", err)
	}
	return &terminalIO{sess: sess, stdin: stdin, stdout: stdout}, nil
}

// terminalIO adapts an *ssh.Session to session.TerminalIO.
type terminalIO struct {
	sess   *ssh.Session
	stdin  io.WriteCloser
	stdout io.Reader
}

func (t *terminalIO) Read(p []byte) (int, error)  { return t.stdout.Read(p) }
func (t *terminalIO) Write(p []byte) (int, error) { return t.stdin.Write(p) }

func (t *terminalIO) Resize(cols, rows int) error {
	return t.sess.WindowChange(rows, cols)
}

func (t *terminalIO) Close() error {
	t.stdin.Close()
	return t.sess.Close()
}

// Exec runs cmd on a fresh session channel with no PTY, so it never touches
// the interactive terminal. It blocks until the command exits or ctx is
// cancelled; on cancellation the remote process is signalled and the session
// is torn down.
func (c *Client) Exec(ctx context.Context, cmd string) (stdout, stderr []byte, exitCode int, err error) {
	sess, err := c.newSession(ctx)
	if err != nil {
		return nil, nil, -1, err
	}
	defer sess.Close()
	stop := context.AfterFunc(ctx, func() { sess.Close() })
	defer stop()
	outBuf, errBuf := &boundedOutput{}, &boundedOutput{}
	sess.Stdout, sess.Stderr = outBuf, errBuf
	if err = sess.Start(cmd); err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return nil, nil, -1, err
	}
	err = sess.Wait()
	out, tooMuch := outBuf.snapshot()
	errOut, tooMuchErr := errBuf.snapshot()
	if ctx.Err() != nil {
		return out, errOut, -1, ctx.Err()
	}
	if tooMuch || tooMuchErr {
		return out, errOut, -1, errors.New("command output exceeded 1 MiB per stream")
	}
	if err == nil {
		return out, errOut, 0, nil
	}
	var exitErr *ssh.ExitError
	if errors.As(err, &exitErr) {
		return out, errOut, exitErr.ExitStatus(), nil
	}
	return out, errOut, -1, err
}

// newSession bounds channel-open, which x/crypto/ssh cannot cancel itself.
// A peer that never answers is closed to release the waiting goroutine.
func (c *Client) newSession(ctx context.Context) (*ssh.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	type result struct {
		s   *ssh.Session
		err error
	}
	done := make(chan result, 1)
	go func() { s, err := c.conn.NewSession(); done <- result{s, err} }()
	select {
	case r := <-done:
		return r.s, r.err
	case <-ctx.Done():
		c.Close()
		r := <-done
		if r.s != nil {
			r.s.Close()
		}
		return nil, ctx.Err()
	}
}

type boundedOutput struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	truncated bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	left := (1 << 20) - b.buf.Len()
	if len(p) > left {
		p = p[:left]
		b.truncated = true
	}
	b.buf.Write(p)
	return n, nil
}
func (b *boundedOutput) snapshot() ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...), b.truncated
}

// ExecStream starts cmd and returns a reader for its live stdout. It is
// intended for long-running collection loops (monitoring): the caller closes
// the reader to terminate the process. ctx governs only the startup; once
// the reader is returned, its lifetime is the caller's.
func (c *Client) ExecStream(ctx context.Context, cmd string) (io.ReadCloser, error) {
	sess, err := c.newSession(ctx)
	if err != nil {
		return nil, fmt.Errorf("new ssh session: %w", err)
	}
	stop := context.AfterFunc(ctx, func() { sess.Close() })
	defer stop()
	stdout, err := sess.StdoutPipe()
	if err != nil {
		sess.Close()
		return nil, err
	}
	if err := sess.Start(cmd); err != nil {
		sess.Close()
		return nil, fmt.Errorf("start command: %w", err)
	}
	if err := ctx.Err(); err != nil {
		sess.Close()
		return nil, err
	}
	return &execStreamReader{sess: sess, stdout: stdout}, nil
}

// execStreamReader adapts an ssh.Session running a streaming command into an
// io.ReadCloser. Close kills the remote process and frees the channel.
type execStreamReader struct {
	sess   *ssh.Session
	stdout io.Reader
	once   sync.Once
}

func (e *execStreamReader) Read(p []byte) (int, error) {
	return e.stdout.Read(p)
}

func (e *execStreamReader) Close() error {
	var err error
	e.once.Do(func() {
		// Signal the remote process, then close the session channel.
		// Closing the channel releases blocked reads even if the peer ignores signals.
		err = e.sess.Close()
	})
	return err
}
