package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/crypto/ssh"
	"sshx/internal/connect"
	"sshx/internal/llm"
	"sshx/internal/session"
	"sshx/internal/store"
	"sshx/internal/tool"
	"sshx/internal/vault"
)

type inputRecorder struct {
	*stubTerminalIO
	writes chan string
}

func (r *inputRecorder) Write(p []byte) (int, error) {
	r.writes <- string(p)
	return r.stubTerminalIO.Write(p)
}

type recordingBackend struct {
	*stubBackend
	recorder *inputRecorder
}

func (b *recordingBackend) OpenTerminal(int, int) (session.TerminalIO, error) { return b.recorder, nil }

func TestReadOnlyAttachmentRejectsInputOnServer(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		readOnly   bool
	}{{"mobile passive", "passive", false}, {"explicit readonly", "negotiate", true}} {
		t.Run(tc.name, func(t *testing.T) {
			ts, mgr, token := newTestServer(t)
			defer mgr.CloseAll()
			stub := newStubBackend()
			rec := &inputRecorder{stub.tio, make(chan string, 4)}
			sess, err := mgr.Create(&recordingBackend{stub, rec}, session.Options{})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			c := dialWS(t, ctx, ts.URL, token)
			defer c.close()
			data, _ := json.Marshal(map[string]any{"type": "attach", "sessionId": sess.ID(), "channelId": 1, "sizeMode": tc.mode, "readOnly": tc.readOnly})
			if err := c.conn.Write(ctx, websocket.MessageText, data); err != nil {
				t.Fatal(err)
			}
			c.sendInput(1, "blocked")
			c.attach(string(sess.ID()), 2)
			c.sendInput(2, "allowed")
			select {
			case got := <-rec.writes:
				if got != "allowed" {
					t.Fatalf("readonly input reached backend: %q", got)
				}
			case <-ctx.Done():
				t.Fatal("writable channel did not receive input")
			}
		})
	}
}

func TestDraftConnectionAuthenticatesWithoutSavingOrOpeningShell(t *testing.T) {
	s, sid := reviewPasswordServer(t)
	s.vault = vault.Memory{}
	s.connect = connect.NewService(s.store, s.vault)
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{PasswordCallback: func(m ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
		if m.User() == "tester" && string(p) == "draft-secret" {
			return nil, nil
		}
		return nil, fmt.Errorf("bad credentials")
	}}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var channels atomic.Int32
	disconnected := make(chan struct{})
	go func() {
		defer close(disconnected)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
		if err != nil {
			return
		}
		defer sc.Close()
		go ssh.DiscardRequests(reqs)
		for ch := range chans {
			channels.Add(1)
			ch.Reject(ssh.Prohibited, "test must not open channels")
		}
	}()
	host, portText, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portText)
	draft := map[string]any{"name": "draft", "config": map[string]any{"host": host, "port": port, "user": "tester", "authType": "password"}, "password": "draft-secret", "accepts": map[string]string{ln.Addr().String(): ssh.FingerprintSHA256(signer.PublicKey())}}
	body, _ := json.Marshal(draft)
	r := httptest.NewRequest("POST", "/api/connections/test", strings.NewReader(string(body)))
	r.Header.Set("X-Auth-Token", sid)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("test connection: %d %s", w.Code, w.Body.String())
	}
	if len(s.sessions.List()) != 0 {
		t.Fatal("test left an SSH session")
	}
	nodes, err := s.store.ListNodes()
	if err != nil || len(nodes) != 0 {
		t.Fatal("test saved a draft node")
	}
	select {
	case <-disconnected:
	case <-time.After(time.Second):
		t.Fatal("test connection leaked")
	}
	if channels.Load() != 0 {
		t.Fatal("test opened a remote shell or exec channel")
	}
}

func TestAlertHistoryAPIIsAuthenticatedAndAcknowledged(t *testing.T) {
	s, sid := reviewPasswordServer(t)
	id, err := s.store.AppendAlert("node-a", "production", "critical", "disk full")
	if err != nil {
		t.Fatal(err)
	}
	req := func(method, path, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		if token != "" {
			r.Header.Set("X-Auth-Token", token)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if w := req("GET", "/api/alerts", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := req("GET", "/api/alerts", sid); w.Code != 200 || !strings.Contains(w.Body.String(), "production") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := req("POST", fmt.Sprintf("/api/alerts/%d/ack", id), sid); w.Code != 204 {
		t.Fatal(w.Code)
	}
	records, _ := s.store.ListAlerts("node-a", 0, 100)
	if len(records) != 1 || records[0].AcknowledgedAt == 0 {
		t.Fatal(records)
	}
	if w := req("POST", "/api/alerts/999999/ack", sid); w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func TestCommandSuggestionUsesSelectedSessionAndOptInContext(t *testing.T) {
	s, sid := reviewPasswordServer(t)
	defer s.sessions.CloseAll()
	node := &store.Node{Kind: "connection", Name: "host"}
	if err := s.store.CreateNode(node); err != nil {
		t.Fatal(err)
	}
	first, err := s.sessions.Create(newStubBackend(), session.Options{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.sessions.Create(newStubBackend(), session.Options{})
	if err != nil {
		t.Fatal(err)
	}
	s.getMonitorRegistry().Link(first.ID(), node.ID)
	s.getMonitorRegistry().Link(second.ID(), node.ID)
	reads := 0
	var readSession session.ID
	reg := tool.NewRegistry(tool.NewGuard(), nil, nil)
	reg.Register(&tool.Tool{Name: "read_terminal", Handler: func(_ context.Context, cc *tool.CallContext, _ json.RawMessage) (tool.Result, error) {
		reads++
		readSession = cc.Session.ID()
		return tool.Result{Output: "terminal private output"}, nil
	}})
	s.toolReg = reg
	s.llm = llm.New()
	s.vault = vault.Memory{llmAPIKeyID: "test-key"}
	var contextShared bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		contextShared = strings.Contains(req.Messages[0].Content, "terminal private output")
		suggestion := `{"command":"pwd","explanation":"显示当前目录","impact":"只读取目录位置，不修改文件"}`
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": suggestion}}}})
	}))
	defer provider.Close()
	config, _ := json.Marshal(llm.Config{Provider: "openai", Model: "test", BaseURL: provider.URL})
	s.store.SetSetting(llmConfigKey, string(config))
	for _, include := range []bool{false, true} {
		body := fmt.Sprintf(`{"prompt":"current directory","nodeId":%q,"sessionId":%q,"includeContext":%t}`, node.ID, first.ID(), include)
		r := httptest.NewRequest("POST", "/api/ai/ctrlk", strings.NewReader(body))
		r.Header.Set("X-Auth-Token", sid)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var result struct {
			SessionID   string `json:"sessionId"`
			Explanation string `json:"explanation"`
		}
		json.Unmarshal(w.Body.Bytes(), &result)
		if result.SessionID != string(first.ID()) || result.Explanation == "" {
			t.Fatal(result)
		}
		if contextShared != include {
			t.Fatal("terminal context consent ignored")
		}
		if !include && reads != 0 {
			t.Fatal("context read without opt-in")
		}
		if include && (reads != 1 || readSession != first.ID()) {
			t.Fatal("wrong session context")
		}
	}
}
