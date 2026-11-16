package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"sshx/internal/session"
)

// RegisterDefaults installs the built-in tool set onto a Registry:
//
//	list_sessions   - live session info
//	run_command     - exec a command on the session host (separate channel)
//	read_terminal   - ring buffer snapshot of the interactive terminal
//	list_files      - SFTP directory listing
//	read_file       - SFTP file read
//	write_file      - SFTP file write (writes = true)
//	stat            - SFTP stat
func RegisterDefaults(r *Registry) {
	registerInspection(r)
	r.Register(&Tool{
		Name:   "list_sessions",
		Desc:   "Read the target session ID, endpoint, and attached client count.",
		Writes: false,
		InputSchema: map[string]any{
			"type": "object", "properties": map[string]any{}, "additionalProperties": false,
		},
		Handler: func(ctx context.Context, cc *CallContext, _ json.RawMessage) (Result, error) {
			if cc.Session == nil {
				return Result{Output: nil}, nil
			}
			info := cc.Session.Info()
			return Result{Output: info}, nil
		},
	})

	r.Register(&Tool{
		Name: "run_command",
		Desc: "Run a command on the session's host via a separate exec channel (no PTY, no effect on the interactive terminal). Returns stdout, stderr, and exit code.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "description": "shell command to run"},
				"timeout": map[string]any{"type": "string", "description": "duration string e.g. 30s; default 30s"},
			},
			"required": []string{"command"},
		},
		Handler: func(ctx context.Context, cc *CallContext, args json.RawMessage) (Result, error) {
			if cc.Session == nil {
				return Result{}, fmt.Errorf("no session")
			}
			var a struct {
				Command string `json:"command"`
				Timeout string `json:"timeout,omitempty"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return Result{}, fmt.Errorf("bad args: %w", err)
			}
			if a.Command == "" {
				return Result{}, fmt.Errorf("command is required")
			}

			timeout := 30 * time.Second
			if a.Timeout != "" {
				d, err := time.ParseDuration(a.Timeout)
				if err != nil {
					return Result{}, fmt.Errorf("bad timeout: %w", err)
				}
				if d <= 0 || d > 2*time.Minute {
					return Result{}, fmt.Errorf("timeout must be greater than zero and at most 2m")
				}
				if d > 0 {
					timeout = d
				}
			}

			cctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			stdout, stderr, exitCode, err := cc.Session.Exec(cctx, a.Command)
			if err != nil {
				return Result{Error: err.Error()}, nil
			}
			return Result{
				Output: map[string]any{
					"stdout":   string(stdout),
					"stderr":   string(stderr),
					"exitCode": exitCode,
				},
			}, nil
		},
	})

	r.Register(&Tool{
		Name:   "read_terminal",
		Desc:   "Read recent raw terminal output (bounded ring buffer; not a rendered screen). Refuses while a TUI app (vim/top/htop) is in the alternate screen.",
		Writes: false,
		InputSchema: map[string]any{
			"type": "object", "properties": map[string]any{}, "additionalProperties": false,
		},
		Handler: func(ctx context.Context, cc *CallContext, _ json.RawMessage) (Result, error) {
			if cc.Session == nil {
				return Result{}, fmt.Errorf("no session")
			}
			term := cc.Session.Terminal()
			snapshot, alternate := term.Snapshot()
			if alternate {
				return Result{Error: "terminal is in alternate screen; use inspect_system"}, nil
			}
			return Result{Output: string(snapshot)}, nil
		},
	})

	r.Register(&Tool{
		Name:   "list_files",
		Desc:   "List files in a directory on the session's host via SFTP.",
		Writes: false,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "absolute directory path"},
			},
			"required": []string{"path"},
		},
		Handler: func(ctx context.Context, cc *CallContext, args json.RawMessage) (Result, error) {
			fc, err := fileClient(cc)
			if err != nil {
				return Result{}, err
			}
			var a struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return Result{}, fmt.Errorf("bad args: %w", err)
			}
			entries, err := fc.List(a.Path)
			if err != nil {
				return Result{Error: err.Error()}, nil
			}
			return Result{Output: entries}, nil
		},
	})

	r.Register(&Tool{
		Name:   "read_file",
		Desc:   "Read a file's content via SFTP.",
		Writes: false,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "absolute file path"},
			},
			"required": []string{"path"},
		},
		Handler: func(ctx context.Context, cc *CallContext, args json.RawMessage) (Result, error) {
			fc, err := fileClient(cc)
			if err != nil {
				return Result{}, err
			}
			var a struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return Result{}, fmt.Errorf("bad args: %w", err)
			}
			rc, err := fc.Open(a.Path)
			if err != nil {
				return Result{Error: err.Error()}, nil
			}
			defer rc.Close()
			const maxRead = 1 << 20
			buf, err := io.ReadAll(io.LimitReader(rc, maxRead+1))
			if err != nil {
				return Result{Error: err.Error()}, nil
			}
			if len(buf) > maxRead {
				return Result{Error: "file exceeds 1 MiB read limit"}, nil
			}
			return Result{Output: string(buf)}, nil
		},
	})

	r.Register(&Tool{
		Name:   "write_file",
		Desc:   "Atomically replace a regular file via SFTP. This is a write operation; the Guard will require confirmation under PolicyConfirm and refuse under PolicyReadonly.",
		Writes: true,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "absolute file path"},
				"content": map[string]any{"type": "string", "description": "file content to write"},
			},
			"required": []string{"path", "content"},
		},
		Handler: func(ctx context.Context, cc *CallContext, args json.RawMessage) (Result, error) {
			fc, err := fileClient(cc)
			if err != nil {
				return Result{}, err
			}
			var a struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return Result{}, fmt.Errorf("bad args: %w", err)
			}
			atomic, ok := fc.(session.AtomicFileClient)
			if !ok {
				return Result{Error: "backend does not support atomic writes"}, nil
			}
			if _, err := atomic.WriteAtomic(ctx, a.Path, strings.NewReader(a.Content)); err != nil {
				return Result{Error: err.Error()}, nil
			}

			return Result{Output: "ok"}, nil
		},
	})

	r.Register(&Tool{
		Name:   "stat",
		Desc:   "Stat a file or directory via SFTP.",
		Writes: false,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "absolute path"},
			},
			"required": []string{"path"},
		},
		Handler: func(ctx context.Context, cc *CallContext, args json.RawMessage) (Result, error) {
			fc, err := fileClient(cc)
			if err != nil {
				return Result{}, err
			}
			var a struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return Result{}, fmt.Errorf("bad args: %w", err)
			}
			entry, err := fc.Stat(a.Path)
			if err != nil {
				return Result{Error: err.Error()}, nil
			}
			return Result{Output: entry}, nil
		},
	})
}

// fileClient extracts the SFTP client from the session's backend.
func fileClient(cc *CallContext) (session.FileClient, error) {
	if cc.Session == nil {
		return nil, fmt.Errorf("no session")
	}
	fc, err := cc.Session.Files()
	if err != nil {
		return nil, fmt.Errorf("file transfer unavailable: %w", err)
	}
	return fc, nil
}
