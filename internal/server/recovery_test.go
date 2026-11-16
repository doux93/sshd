package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"regexp"
	"sshx/internal/auth"
	"sshx/internal/connect"
	"sshx/internal/session"
	"sshx/internal/sshclient"
	"sshx/internal/store"
	"sshx/internal/testssh"
	"sshx/internal/vault"
	"strings"
	"testing"
	"time"
)

func TestRecoveryRoutesRequireAuthAndExplicitConfirmation(t *testing.T) {
	srv, token := reviewPasswordServer(t)
	for _, tc := range []struct{ method, path string }{{"GET", "/api/recovery"}, {"POST", "/api/recovery/unknown/resume"}, {"POST", "/api/recovery/unknown/terminate"}, {"DELETE", "/api/recovery/unknown"}} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal(tc, w.Code)
		}
	}
	for _, method := range []string{"POST", "DELETE"} {
		path := "/api/recovery/unknown"
		if method == "POST" {
			path += "/terminate"
		}
		r := httptest.NewRequest(method, path, strings.NewReader(`{}`))
		r.Header.Set("X-Auth-Token", token)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestTmuxRecoveryAcrossWorkbenchRestart(t *testing.T) {
	endpoint := testssh.Start(t)
	database := filepath.Join(t.TempDir(), "recovery.db")
	var srv *Server
	var token *auth.Token
	var st *store.Store
	var manager *session.Manager
	start := func() {
		var err error
		st, err = store.Open(database)
		if err != nil {
			t.Fatal(err)
		}
		token = auth.NewToken()
		manager = session.NewManager()
		v := vault.Memory{}
		srv = New(Deps{Sessions: manager, Authn: token, Store: st, Vault: v, Connect: connect.NewService(st, v)})
	}
	start()
	defer func() { manager.CloseAll(); st.Close() }()
	node := &store.Node{Kind: "connection", Name: "persistent test", Config: store.ConnConfig{Host: endpoint.Host, Port: endpoint.Port, User: endpoint.User, KeyPath: endpoint.KeyPath, AuthType: "key", PersistentTerminal: true}}
	if err := st.CreateNode(node); err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(data))
		req.Header.Set("X-Auth-Token", token.Secret())
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		return w
	}
	connectPath := "/api/nodes/" + node.ID + "/connect"
	reply := request("POST", connectPath, map[string]any{})
	if reply.Code != 409 {
		t.Fatalf("expected first-use host key prompt: %d %s", reply.Code, reply.Body.String())
	}
	var prompt struct {
		HostKey connect.HostKeyPromptError `json:"hostKey"`
	}
	json.Unmarshal(reply.Body.Bytes(), &prompt)
	reply = request("POST", connectPath, map[string]any{"accepts": map[string]string{prompt.HostKey.Host: prompt.HostKey.Fingerprint}})
	if reply.Code != 201 {
		t.Fatalf("create: %d %s", reply.Code, reply.Body.String())
	}
	var first session.Info
	json.Unmarshal(reply.Body.Bytes(), &first)
	if first.RecoveryID == "" {
		t.Fatal("missing recovery identity")
	}
	// Only clean up this test's isolated tmux socket, even on assertion failure.
	defer exec.Command("tmux", "-L", "sshx-"+first.RecoveryID, "kill-server").Run()
	measure := func(info session.Info, initial bool) string {
		sess, ok := manager.Get(info.ID)
		if !ok {
			t.Fatal("session disappeared")
		}
		att := sess.Terminal().Attach(session.SizeNegotiate, 140, 30)
		defer att.Detach()
		tag := "SSHX_RESUMED"
		cmd := ""
		if initial {
			tag = "SSHX_INITIAL"
			cmd = "export SSHX_KEEP=persisted; cd /tmp; "
		}
		cmd += "printf '\\n" + tag + ":%s:%s:%s\\n' \"$$\" \"$SSHX_KEEP\" \"$PWD\"\r"
		if _, err := sess.Terminal().Write([]byte(cmd)); err != nil {
			t.Fatal(err)
		}
		pattern := regexp.MustCompile(tag + `:([0-9]+):persisted:/tmp`)
		var output strings.Builder
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		for {
			select {
			case data := <-att.Out:
				output.Write(data)
				if m := pattern.FindStringSubmatch(output.String()); m != nil {
					return m[1]
				}
			case <-att.Closed:
				t.Fatalf("terminal closed: %q", output.String())
			case <-timer.C:
				t.Fatalf("terminal marker timed out: %q", output.String())
			}
		}
	}
	pid := measure(first, true)
	// Same-host double-connect reuses the active terminal instead of attaching a
	// second writer or silently creating another remote shell.
	reply = request("POST", connectPath, map[string]any{})
	var reused session.Info
	json.Unmarshal(reply.Body.Bytes(), &reused)
	if reply.Code != 200 || reused.ID != first.ID {
		t.Fatal(reply.Code, reply.Body.String())
	}
	manager.CloseAll()
	st.Close()
	start()
	records, err := st.ListRecovery()
	if err != nil || len(records) != 1 {
		t.Fatal(records, err)
	}
	resumePath := "/api/recovery/" + first.RecoveryID + "/resume"
	// Changed host identity fails locally, before dialing an unrelated machine.
	original := node.Config.Host
	node.Config.Host = "wrong.invalid"
	st.UpdateNode(node)
	reply = request("POST", resumePath, map[string]any{})
	if reply.Code != 409 {
		t.Fatal("changed target accepted", reply.Code)
	}
	node.Config.Host = original
	st.UpdateNode(node)
	reply = request("POST", resumePath, map[string]any{})
	if reply.Code != 201 {
		t.Fatalf("resume: %d %s", reply.Code, reply.Body.String())
	}
	var resumed session.Info
	json.Unmarshal(reply.Body.Bytes(), &resumed)
	if got := measure(resumed, false); got != pid {
		t.Fatalf("shell replaced: PID %s -> %s", pid, got)
	}
	// Explicit disconnect leaves the durable identity and remote process intact.
	if w := request("DELETE", "/api/sessions/"+string(resumed.ID), nil); w.Code != 204 {
		t.Fatal(w.Code)
	}
	records, _ = st.ListRecovery()
	if len(records) != 1 {
		t.Fatal("disconnect lost recovery")
	}
	reply = request("POST", resumePath, map[string]any{})
	json.Unmarshal(reply.Body.Bytes(), &resumed)
	if reply.Code != 201 || measure(resumed, false) != pid {
		t.Fatal("disconnect did not preserve shell", reply.Body.String())
	}
	// Missing remote sessions must not be recreated on resume.
	if out, err := exec.Command("tmux", "-L", "sshx-"+first.RecoveryID, "kill-session", "-t", "=terminal").CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	manager.CloseAll()
	reply = request("POST", resumePath, map[string]any{})
	if reply.Code != 410 {
		t.Fatalf("missing was not explicit: %d %s", reply.Code, reply.Body.String())
	}
	records, _ = st.ListRecovery()
	if len(records) != 1 {
		t.Fatal("missing identity silently removed")
	}
	// Termination of an already-missing terminal is safe and removes the record.
	reply = request("POST", "/api/recovery/"+first.RecoveryID+"/terminate", map[string]any{"confirmed": true})
	if reply.Code != 204 {
		t.Fatal(reply.Code, reply.Body.String())
	}
	records, _ = st.ListRecovery()
	if len(records) != 0 {
		t.Fatal("termination retained record")
	}
	// Start a fresh terminal and explicitly terminate it through the API.
	reply = request("POST", connectPath, map[string]any{})
	json.Unmarshal(reply.Body.Bytes(), &resumed)
	if reply.Code != 201 {
		t.Fatal(reply.Code, reply.Body.String())
	}
	defer exec.Command("tmux", "-L", "sshx-"+resumed.RecoveryID, "kill-server").Run()
	reply = request("POST", "/api/recovery/"+resumed.RecoveryID+"/terminate", map[string]any{"confirmed": true})
	if reply.Code != 204 {
		t.Fatal(reply.Code, reply.Body.String())
	}
	client, _, err := srv.connect.DialNode(context.Background(), node.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err = client.PreparePersistent(context.Background(), resumed.RecoveryID, false, 80, 24); !errors.Is(err, sshclient.ErrTmuxMissing) {
		t.Fatalf("remote terminal survived explicit termination: %v", err)
	}
	t.Log("Verified original shell PID, environment and directory across restart; missing-session and termination semantics")
}
