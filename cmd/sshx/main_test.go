package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sshx/internal/store"
	"testing"
)

func TestBootstrapKeepsPasswordFileWhenValidationFails(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	file := filepath.Join(dir, "password")
	os.WriteFile(file, []byte("short"), 0600)
	_, _, _, _, bootstrap, err := remoteMode(st, file, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := bootstrap(); err == nil {
		t.Fatal("weak bootstrap accepted")
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("password file removed before config committed")
	}
	os.WriteFile(file, []byte("a-longer-password"), 0600)
	if err := bootstrap(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("password file not removed after successful bootstrap")
	}
}
