package sshclient

import (
	"context"
	"errors"
	"github.com/pkg/sftp"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func openSSHSFTP(t *testing.T) *sftpClient {
	t.Helper()
	server := "/usr/lib/openssh/sftp-server"
	if _, err := os.Stat(server); err != nil {
		t.Skip("OpenSSH sftp-server required for integration test")
	}
	cmd := exec.Command(server)
	rd, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	wr, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	raw, err := newSFTPClientPipe(rd, wr)
	if err != nil {
		wr.Close()
		cmd.Wait()
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.close(); wr.Close(); cmd.Wait() })
	return raw
}
func TestSFTPSymlinkMutations(t *testing.T) {
	for _, op := range []string{"delete", "rename", "recursive", "dangling"} {
		t.Run(op, func(t *testing.T) {
			c := openSSHSFTP(t)
			dir := t.TempDir()
			target := filepath.Join(dir, "target")
			link := filepath.Join(dir, "link")
			if op == "recursive" {
				os.Mkdir(target, 0700)
				os.WriteFile(filepath.Join(target, "keep.txt"), []byte("keep"), 0600)
			} else if op != "dangling" {
				os.WriteFile(target, []byte("keep"), 0600)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			var err error
			switch op {
			case "rename":
				err = c.Rename(link, link+"-renamed")
			case "recursive":
				err = c.RemoveAll(link)
			default:
				err = c.Remove(link)
			}
			if err != nil {
				t.Fatal(err)
			}
			if op != "dangling" {
				if _, err := os.Stat(target); err != nil {
					t.Fatal("target was modified:", err)
				}
			}
			if _, err := os.Lstat(link); !os.IsNotExist(err) {
				t.Fatal("old link remains")
			}
			if op == "rename" {
				if fi, err := os.Lstat(link + "-renamed"); err != nil || fi.Mode()&os.ModeSymlink == 0 {
					t.Fatal("renamed entry is not a symlink")
				}
			}
		})
	}
}

type failingUpload struct{ sent bool }

func (r *failingUpload) Read(p []byte) (int, error) {
	if r.sent {
		return 0, errors.New("connection lost")
	}
	r.sent = true
	return copy(p, "partial"), nil
}
func TestAtomicUploadPreservesOriginalOnFailure(t *testing.T) {
	c := openSSHSFTP(t)
	dir := t.TempDir()
	dst := filepath.Join(dir, "config")
	os.WriteFile(dst, []byte("original"), 0640)
	if _, err := c.WriteAtomic(context.Background(), dst, &failingUpload{}); err == nil {
		t.Fatal("expected upload error")
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "original" {
		t.Fatalf("original corrupted: %q", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatal("temporary files leaked")
	}
	if _, err := c.WriteAtomic(context.Background(), dst, strings.NewReader("replacement")); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(dst)
	if string(got) != "replacement" {
		t.Fatal("commit failed")
	}
	fi, _ := os.Stat(dst)
	if fi.Mode().Perm() != 0640 {
		t.Fatal("permissions changed")
	}
}
func TestRecursiveDeleteDoesNotFollowNestedLink(t *testing.T) {
	c := openSSHSFTP(t)
	root := t.TempDir()
	tree := filepath.Join(root, "tree")
	target := filepath.Join(root, "keep")
	os.Mkdir(tree, 0700)
	os.Mkdir(target, 0700)
	os.WriteFile(filepath.Join(target, "keep.txt"), []byte("keep"), 0600)
	os.Symlink(target, filepath.Join(tree, "link"))
	if err := c.RemoveAll(tree); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "keep.txt")); err != nil {
		t.Fatal("followed nested link")
	}
}
func TestUnsafeMutationPathsRejected(t *testing.T) {
	c := openSSHSFTP(t)
	for _, p := range []string{"", "/", ".", "..", "/tmp/..", "/tmp/.", "~"} {
		if _, err := c.resolveMutationPath(p); err == nil {
			t.Errorf("allowed %q", p)
		}
	}
}
func TestBoundedOutput(t *testing.T) {
	b := &boundedOutput{}
	payload := strings.Repeat("x", 2<<20)
	n, err := io.WriteString(b, payload)
	if err != nil || n != len(payload) {
		t.Fatal(n, err)
	}
	out, truncated := b.snapshot()
	if len(out) != 1<<20 || !truncated {
		t.Fatal("output limit not enforced")
	}
}

func newSFTPClientPipe(rd io.Reader, wr io.WriteCloser) (*sftpClient, error) {
	c, err := sftp.NewClientPipe(rd, wr)
	if err != nil {
		return nil, err
	}
	return &sftpClient{client: c}, nil
}
