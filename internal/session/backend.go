// Package session implements the core session domain: session lifecycle,
// terminal channels with replay buffers, and multi-client attachment.
//
// It is transport-agnostic (no HTTP/WebSocket imports) and backend-agnostic
// (no SSH imports). Concrete backends (SSH, serial, local PTY, ...) implement
// the Backend interface; concrete transports attach via Terminal.Attach.
package session

import (
	"context"
	"io"
)

// TerminalIO is an interactive terminal stream provided by a Backend.
type TerminalIO interface {
	io.ReadWriteCloser
	// Resize updates the remote PTY dimensions.
	Resize(cols, rows int) error
}

// Backend is a connection to some host that can open terminal channels.
// Implementations: sshclient.Client (SSH). Future: serial, local PTY, docker.
type Backend interface {
	// Kind is a short identifier such as "ssh".
	Kind() string
	// Target describes the endpoint for display purposes, e.g. "user@host:22".
	Target() string
	// OpenTerminal starts an interactive shell with a PTY of the given size.
	OpenTerminal(cols, rows int) (TerminalIO, error)
	// Exec runs a command on a separate channel — no PTY, no effect on the
	// interactive terminal. It blocks until the command finishes or ctx is
	// cancelled. exitCode is -1 when the process was killed or did not exit
	// cleanly (e.g. connection lost); err is non-nil only for transport
	// failures, not for a non-zero exit code.
	Exec(ctx context.Context, cmd string) (stdout, stderr []byte, exitCode int, err error)
	// ExecStream starts a command and returns a reader for its stdout as it
	// is produced. Intended for long-running collection loops (monitoring):
	// the caller closes the reader to terminate the process. ctx is used
	// only for the startup phase; once the reader is returned its lifetime
	// is the caller's to manage.
	ExecStream(ctx context.Context, cmd string) (io.ReadCloser, error)
	// Close tears down the underlying connection and all its channels.
	Close() error
}

// FileBackend is an optional Backend capability: browsing and transferring
// files over the same connection. Consumers must type-assert for it, so
// backends without file support (serial consoles) stay valid Backends.
type FileBackend interface {
	// OpenFiles returns a file client bound to this connection. Callers
	// must not close it; the session owns its lifetime.
	OpenFiles() (FileClient, error)
}

// FileEntry describes one directory entry.
type FileEntry struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`    // "-rw-r--r--"
	ModTime int64  `json:"modTime"` // unix millis
	IsDir   bool   `json:"isDir"`
	IsLink  bool   `json:"isLink"`
}

// FileClient is the file-transfer surface used by the HTTP layer and the
// AI tool layer alike.
type FileClient interface {
	List(path string) ([]FileEntry, error)
	Stat(path string) (FileEntry, error)
	// Open returns a reader for download; the caller closes it.
	Open(path string) (io.ReadCloser, error)
	// Create returns a writer for upload, truncating any existing file;
	// the caller closes it.
	Create(path string) (io.WriteCloser, error)
	Mkdir(path string) error
	Remove(path string) error    // file or empty dir
	RemoveAll(path string) error // recursive
	Rename(old, new string) error
	RealPath(path string) (string, error) // resolve "." , "~" , symlinks
}

// AtomicFileClient writes to a temporary sibling and commits by rename. On
// interrupted upload or unsupported replacement the previous file is preserved.
type AtomicFileClient interface {
	WriteAtomic(ctx context.Context, path string, src io.Reader) (int64, error)
}
