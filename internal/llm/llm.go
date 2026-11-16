package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Config is the non-secret part of the LLM provider config. The API key
// is kept out of here on purpose — it is supplied separately to Complete
// so it never round-trips through JSON-encoded settings.
type Config struct {
	Provider string `json:"provider"`          // "openai" | "anthropic"
	Model    string `json:"model"`             // e.g. "gpt-4o-mini", "claude-3-5-haiku-latest"
	BaseURL  string `json:"baseURL,omitempty"` // optional override; provider default if empty
}

// CompleteRequest is one non-streaming chat turn.
type CompleteRequest struct {
	System string // system prompt (may be empty)
	User   string // the user message
}

// Client calls an LLM provider over HTTP. One struct so tests can swap
// the base URL and HTTP client; production wires the default http.Client.
type Client struct {
	HTTP *http.Client
}

func New() *Client {
	return &Client{HTTP: &http.Client{Timeout: 60 * time.Second}}
}

// Complete sends one turn and returns the assistant text. provider is
// derived from cfg.Provider; an unknown provider is an error rather
// than a silent fallback, so misconfiguration is obvious.
func (c *Client) Complete(ctx context.Context, cfg Config, apiKey string, req CompleteRequest) (string, error) {
	if apiKey == "" {
		return "", fmt.Errorf("llm: api key not set")
	}
	if cfg.Model == "" {
		return "", fmt.Errorf("llm: model not set")
	}
	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: 60 * time.Second}
	}
	switch strings.ToLower(cfg.Provider) {
	case "openai", "openai-compatible", "":
		return c.completeOpenAI(ctx, cfg, apiKey, req)
	case "anthropic", "claude":
		return c.completeAnthropic(ctx, cfg, apiKey, req)
	default:
		return "", fmt.Errorf("llm: unknown provider %q", cfg.Provider)
	}
}

// ---- OpenAI-compatible (covers OpenAI, OpenRouter, DeepSeek, local) ----

func (c *Client) completeOpenAI(ctx context.Context, cfg Config, apiKey string, req CompleteRequest) (string, error) {
	base := cfg.BaseURL
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	base = strings.TrimRight(base, "/")
	msgs := []map[string]string{{"role": "user", "content": req.User}}
	if req.System != "" {
		msgs = append([]map[string]string{{"role": "system", "content": req.System}}, msgs...)
	}
	body, _ := json.Marshal(map[string]any{
		"model":       cfg.Model,
		"messages":    msgs,
		"temperature": 0,
		"stream":      false,
	})
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("llm: request: %w", err)
	}
	defer resp.Body.Close()
	rbody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm: openai %d: %s", resp.StatusCode, truncate(string(rbody), 400))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rbody, &out); err != nil {
		return "", fmt.Errorf("llm: decode: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("llm: empty completion")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

// ---- Anthropic ----

func (c *Client) completeAnthropic(ctx context.Context, cfg Config, apiKey string, req CompleteRequest) (string, error) {
	base := cfg.BaseURL
	if base == "" {
		base = "https://api.anthropic.com"
	}
	base = strings.TrimRight(base, "/")
	payload := map[string]any{
		"model":      cfg.Model,
		"max_tokens": 1024,
		"messages":   []map[string]string{{"role": "user", "content": req.User}},
	}
	if req.System != "" {
		payload["system"] = req.System
	}
	body, _ := json.Marshal(payload)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("llm: request: %w", err)
	}
	defer resp.Body.Close()
	rbody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm: anthropic %d: %s", resp.StatusCode, truncate(string(rbody), 400))
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(rbody, &out); err != nil {
		return "", fmt.Errorf("llm: decode: %w", err)
	}
	for _, b := range out.Content {
		if b.Type == "text" {
			return strings.TrimSpace(b.Text), nil
		}
	}
	return "", fmt.Errorf("llm: empty completion")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
