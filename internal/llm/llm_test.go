package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestComplete_OpenAI(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("path=%s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			t.Errorf("auth header=%q", got)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "gpt-4o-mini" {
			t.Errorf("model=%v", body["model"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "  ls -lhS | head -10  "}},
			},
		})
	}))
	defer ts.Close()
	c := New()
	c.HTTP = ts.Client()
	out, err := c.Complete(context.Background(), Config{Provider: "openai", Model: "gpt-4o-mini", BaseURL: ts.URL}, "sk-test",
		CompleteRequest{System: "be a shell", User: "biggest 10 files"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "ls -lhS | head -10" {
		t.Fatalf("got %q", out)
	}
}

func TestComplete_Anthropic(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path=%s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "sk-ant" {
			t.Errorf("x-api-key=%q", r.Header.Get("x-api-key"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "df -h"},
			},
		})
	}))
	defer ts.Close()
	c := New()
	c.HTTP = ts.Client()
	out, err := c.Complete(context.Background(), Config{Provider: "anthropic", Model: "claude-3-5-haiku-latest", BaseURL: ts.URL}, "sk-ant",
		CompleteRequest{User: "disk usage"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "df -h" {
		t.Fatalf("got %q", out)
	}
}

func TestComplete_MissingKey(t *testing.T) {
	c := New()
	_, err := c.Complete(context.Background(), Config{Provider: "openai", Model: "x"}, "", CompleteRequest{})
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestComplete_UnknownProvider(t *testing.T) {
	c := New()
	_, err := c.Complete(context.Background(), Config{Provider: "nope", Model: "x"}, "k", CompleteRequest{})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}
