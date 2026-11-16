// sshx provides server-side SSH sessions, a web UI, SFTP and AI tools.
//
// Two run modes share one binary:
//
//	local   — desktop loopback, OS-keychain vault, process-lifetime token
//	remote  — VPS long-running, master-password login, encrypted vault
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"sshx/internal/alert"
	"sshx/internal/auth"
	"sshx/internal/connect"
	"sshx/internal/llm"
	"sshx/internal/monitor"
	"sshx/internal/push"
	"sshx/internal/server"
	"sshx/internal/session"
	"sshx/internal/store"
	"sshx/internal/tool"
	"sshx/internal/vault"
	"sshx/web"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8722", "listen address (keep loopback in local mode)")
	dataPath := flag.String("data", "", "database path (default: per-user config dir)")
	sessionTTL := flag.Duration("session-ttl", 24*time.Hour, "close sessions with no attached client after this long")
	printToken := flag.Bool("print-token", false, "print only the access token (for scripting)")

	mode := flag.String("mode", "local", "run mode: local (desktop) or remote (VPS)")
	bindPublic := flag.Bool("bind-public", false, "sugar for --addr 0.0.0.0:8722 (use with care)")
	masterPwFile := flag.String("master-password-file", "", "remote mode: read master password from this file, then unlink")
	tlsCert := flag.String("tls-cert", "", "remote mode: TLS certificate (PEM) — enables HTTPS")
	tlsKey := flag.String("tls-key", "", "remote mode: TLS key (PEM)")
	secureCookies := flag.Bool("secure-cookies", true, "remote mode: require HTTPS cookies (disable only for loopback HTTP testing)")
	flag.Parse()
	*mode = strings.ToLower(strings.TrimSpace(*mode))

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if *mode != "local" && *mode != "remote" {
		log.Error("mode must be local or remote")
		os.Exit(1)
	}
	if (*tlsCert == "") != (*tlsKey == "") {
		log.Error("tls-cert and tls-key must be supplied together")
		os.Exit(1)
	}

	if *bindPublic {
		if !strings.HasPrefix(*addr, "0.0.0.0:") && !strings.HasPrefix(*addr, "[::]:") {
			*addr = "0.0.0.0:8722"
		}
	}
	listenAddr := *addr
	if *mode == "local" {
		host, _, err := net.SplitHostPort(listenAddr)
		ip := net.ParseIP(host)
		if err != nil || ip == nil || !ip.IsLoopback() {
			log.Error("local mode requires a numeric loopback address; use remote mode for network access")
			os.Exit(1)
		}
	}

	dbPath := *dataPath
	if dbPath == "" {
		p, err := store.DefaultPath()
		if err != nil {
			log.Error("resolve data dir", "err", err)
			os.Exit(1)
		}
		dbPath = p
	}
	st, err := store.Open(dbPath)
	if err != nil {
		log.Error("open store", "path", dbPath, "err", err)
		os.Exit(1)
	}
	defer st.Close()

	// Monitor manager: agentless metric collection + alerting. No notifier
	// is wired in local mode; alerts are broadcast to WS clients only.
	// M8 adds webhook/ntfy configuration from settings.
	monMgr := monitor.NewManager(alert.NoopNotifier(), log)

	sessions := session.NewManager()
	janitorCtx, stopJanitor := context.WithCancel(context.Background())
	defer stopJanitor()
	sessions.StartJanitor(janitorCtx, time.Minute, *sessionTTL)

	// Mode-driven wiring: build authn + vault + token handling.
	authn, vaultImpl, passAuth, encVault, bootstrapFn, err := wireMode(parseMode(*mode), st, *masterPwFile, log)
	if err != nil {
		log.Error("init auth", "mode", *mode, "err", err)
		os.Exit(1)
	}
	if passAuth != nil {
		passAuth.SetSecureCookies(*secureCookies)
	}
	if bootstrapFn != nil {
		if err := bootstrapFn(); err != nil {
			log.Error("auth bootstrap", "err", err)
			os.Exit(1)
		}
	}

	// Web Push only exists in remote mode. We construct it after
	// bootstrap so the auth_config row already exists and VAPID keys
	// can be persisted on first delivery. In local mode we hand the
	// monitor a noop notifier, same as before.
	var pushDeps *server.PushDeps
	notifier := alert.NoopNotifier()
	if parseMode(*mode) == modeRemote {
		load, save := server.AttachVAPIDFromStore(st)
		webPush := push.NewWebPushNotifier(st, load, save, log)
		notifier = webPush
		pushDeps = &server.PushDeps{Notifier: webPush, Store: st}
		if pk, err := webPush.PublicKey(); err == nil {
			log.Info("web push ready", "public_key_b64_len", len(pk))
		} else {
			log.Warn("web push init failed; notifications disabled", "err", err)
			notifier = alert.NoopNotifier()
			pushDeps = nil
		}
	}
	monMgr.SetNotifier(notifier)

	connSvc := connect.NewService(st, vaultImpl)

	// Tool layer: Registry + Guard + the default read/write tools. The
	// store is wrapped as a tool.Auditor so every AI/MCP call is recorded
	// in audit_log. registerMonitorTool adds get_monitor_metrics on top
	// of the defaults so it can close over the monitor manager.
	toolReg := tool.NewRegistry(tool.NewGuard(), &storeAuditor{st}, log)
	tool.RegisterDefaults(toolReg)
	// LLM client for Ctrl+K command generation. Thin HTTP wrapper over
	// OpenAI-compatible / Anthropic providers; configured at runtime via
	// /api/ai/config (provider+model in settings, API key in the vault).
	llmClient := llm.New()

	ui, ok := web.Dist()
	if !ok {
		ui = nil
		log.Warn("frontend not built; serving API only (run: cd web && npm run build)")
	}

	srv := server.New(server.Deps{
		Sessions:       sessions,
		Authn:          authn,
		Store:          st,
		Vault:          vaultImpl,
		Connect:        connSvc,
		Monitor:        monMgr,
		UI:             ui,
		Log:            log,
		PassAuth:       passAuth,
		EncryptedVault: encVault,
		Push:           pushDeps,
		Tool:           toolReg,
		LLM:            llmClient,
	})

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Error("listen failed", "addr", listenAddr, "err", err)
		os.Exit(1)
	}

	// In local mode print the launcher URL as before.
	if *mode == "local" {
		token := ""
		if t, ok := authn.(*auth.Token); ok {
			token = t.Secret()
		}
		if *printToken {
			if token != "" {
				fmt.Println(token)
			}
		} else {
			fmt.Printf("\n  sshx ready:  http://%s/?token=%s\n\n", ln.Addr(), token)
		}
	} else {
		// Remote mode: tell the operator where the UI lives and what to do.
		if !*printToken {
			scheme := "http"
			if *tlsCert != "" {
				scheme = "https"
			}
			fmt.Printf("\n  sshx ready (remote mode):  %s://%s\n", scheme, ln.Addr())
			first, _ := passAuth.FirstRun()
			if first {
				fmt.Println("  → first run: open the URL above and set a master password")
			} else {
				fmt.Println("  → open the URL above and log in")
			}
			fmt.Println()
		}
	}

	httpSrv := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 64 << 10}
	// In remote mode behind TLS, install a TLS config so cookies can set Secure.
	if *mode == "remote" && *tlsCert != "" && *tlsKey != "" {
		cert, err := tls.LoadX509KeyPair(*tlsCert, *tlsKey)
		if err != nil {
			log.Error("load TLS cert", "err", err)
			os.Exit(1)
		}
		httpSrv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
		passAuth.SetSecureCookies(true)
		// Re-listen on the TLS-aware server: same listener, TLS handshake
		// handled by ServeTLS below.
	}
	secure := passAuth != nil && passAuth.SecureCookies()
	if !secure && strings.HasPrefix(strings.ToLower(listenAddr), "0.0.0.0") {
		log.Warn("binding on 0.0.0.0 without TLS — session cookies will not carry Secure flag; use a reverse proxy")
	}

	go func() {
		var serveErr error
		if httpSrv.TLSConfig != nil {
			serveErr = httpSrv.ServeTLS(ln, "", "")
		} else {
			serveErr = httpSrv.Serve(ln)
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			log.Error("serve failed", "err", serveErr)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	<-stop
	log.Info("shutting down")
	monMgr.StopAll()
	sessions.CloseAll()
	httpSrv.Close()
}

// modeFlag is the typed mode value used by wireMode.
type modeFlag int

const (
	modeLocal modeFlag = iota
	modeRemote
)

func (m modeFlag) String() string {
	switch m {
	case modeLocal:
		return "local"
	case modeRemote:
		return "remote"
	}
	return "unknown"
}

// parseMode maps the --mode flag string to the typed enum.
func parseMode(s string) modeFlag {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "remote":
		return modeRemote
	default:
		return modeLocal
	}
}

// wireMode assembles the auth + vault pieces for the requested mode.
// In remote mode it also handles the optional first-run bootstrap read
// from --master-password-file. Returns the bootstrap closure (or nil
// if not needed) — the caller invokes it once after the store is open
// but before constructing the server.
func wireMode(m modeFlag, st *store.Store, masterPwFile string, log *slog.Logger) (
	authn auth.Authenticator,
	vaultImpl vault.Vault,
	passAuth *auth.Password,
	encVault *vault.Encrypted,
	bootstrap func() error,
	err error,
) {
	switch m {
	case modeLocal:
		return localMode(st)
	case modeRemote:
		return remoteMode(st, masterPwFile, log)
	default:
		return nil, nil, nil, nil, nil, fmt.Errorf("unknown --mode %q", m)
	}
}

func localMode(st *store.Store) (
	auth.Authenticator, vault.Vault, *auth.Password, *vault.Encrypted, func() error, error,
) {
	return auth.NewToken(), vault.Keyring{}, nil, nil, nil, nil
}

func remoteMode(st *store.Store, masterPwFile string, log *slog.Logger) (
	auth.Authenticator, vault.Vault, *auth.Password, *vault.Encrypted, func() error, error,
) {
	passAuth := auth.NewPassword(st, false, log)
	encVault := vault.NewEncrypted(st, nil)
	bootstrap := func() error {
		first, err := passAuth.FirstRun()
		if err != nil {
			return err
		}
		if !first {
			return nil
		}
		if masterPwFile == "" {
			// Not bootstrapped and no password file: refuse to start in
			// remote mode. The first-run UI path only works once a
			// password has been sealed via /api/auth/setup, which the
			// systemd unit can't drive unattended.
			return errors.New("--master-password-file is required for first-run bootstrap")
		}
		data, err := os.ReadFile(masterPwFile)
		pw := strings.TrimRight(string(data), "\r\n")
		if err != nil {
			return fmt.Errorf("read master password file: %w", err)
		}
		if err := passAuth.Bootstrap(pw); err != nil {
			return fmt.Errorf("seal master password: %w", err)
		}
		if err := os.Remove(masterPwFile); err != nil {
			log.Warn("auth configured but password file cleanup failed; remove it manually", "file", masterPwFile, "err", err)
		}
		log.Info("auth bootstrap complete", "file", masterPwFile)
		return nil
	}
	return passAuth, encVault, passAuth, encVault, bootstrap, nil
}

// storeAuditor adapts *store.Store to the tool.Auditor
// interface (Store exposes InsertAudit; the tool layer calls Audit).
type storeAuditor struct{ st *store.Store }

func (a *storeAuditor) Audit(actor, action string, detail any) error {
	return a.st.InsertAudit(actor, action, detail)
}
