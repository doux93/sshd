package sshclient

import (
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/pkg/sftp"

	"sshx/internal/session"
)

// OpenFiles lazily starts one SFTP subsystem per connection and reuses it;
// the sftp client is safe for concurrent use and multiplexes requests.
func (c *Client) OpenFiles() (session.FileClient, error) {
	c.sftpOnce.Do(func() {
		// MaxConcurrentRequestsPerFile raises throughput on fast links.
		client, err := sftp.NewClient(c.conn, sftp.MaxConcurrentRequestsPerFile(64))
		if err != nil {
			c.sftpErr = fmt.Errorf("start sftp subsystem: %w", err)
			return
		}
		c.stateMu.Lock()
		defer c.stateMu.Unlock()
		if c.closed {
			client.Close()
			c.sftpErr = fmt.Errorf("ssh connection closed")
			return
		}
		c.sftp = &sftpClient{client: client}
	})
	if c.sftpErr != nil {
		return nil, c.sftpErr
	}
	return c.sftp, nil
}

var _ session.FileBackend = (*Client)(nil)

// sftpClient adapts *sftp.Client to session.FileClient.
type sftpClient struct {
	client *sftp.Client
	mu     sync.Mutex
	home   string // cached, resolved on first use
}

func toEntry(fi os.FileInfo) session.FileEntry {
	return session.FileEntry{
		Name:    fi.Name(),
		Size:    fi.Size(),
		Mode:    fi.Mode().String(),
		ModTime: fi.ModTime().UnixMilli(),
		IsDir:   fi.IsDir(),
		IsLink:  fi.Mode()&os.ModeSymlink != 0,
	}
}

func (s *sftpClient) List(p string) ([]session.FileEntry, error) {
	resolved, err := s.RealPath(p)
	if err != nil {
		return nil, err
	}
	infos, err := s.client.ReadDir(resolved)
	if err != nil {
		return nil, err
	}
	entries := make([]session.FileEntry, 0, len(infos))
	for _, fi := range infos {
		e := toEntry(fi)
		// Report symlink targets as directories so double-click navigates.
		if e.IsLink {
			if target, err := s.client.Stat(path.Join(resolved, e.Name)); err == nil {
				e.IsDir = target.IsDir()
				e.Size = target.Size()
			}
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func (s *sftpClient) Stat(p string) (session.FileEntry, error) {
	resolved, err := s.RealPath(p)
	if err != nil {
		return session.FileEntry{}, err
	}
	fi, err := s.client.Stat(resolved)
	if err != nil {
		return session.FileEntry{}, err
	}
	return toEntry(fi), nil
}

func (s *sftpClient) Open(p string) (io.ReadCloser, error) {
	resolved, err := s.RealPath(p)
	if err != nil {
		return nil, err
	}
	return s.client.Open(resolved)
}

func (s *sftpClient) Create(p string) (io.WriteCloser, error) {
	resolved, err := s.resolveParent(p)
	if err != nil {
		return nil, err
	}
	return s.client.Create(resolved)
}

func (s *sftpClient) Mkdir(p string) error {
	resolved, err := s.resolveParent(p)
	if err != nil {
		return err
	}
	return s.client.Mkdir(resolved)
}

func (s *sftpClient) Remove(p string) error {
	resolved, err := s.resolveMutationPath(p)
	if err != nil {
		return err
	}
	return s.client.Remove(resolved)
}

func (s *sftpClient) RemoveAll(p string) error {
	resolved, err := s.resolveMutationPath(p)
	if err != nil {
		return err
	}
	return s.removeTree(resolved)
}

func (s *sftpClient) Rename(oldPath, newPath string) error {
	oldResolved, err := s.resolveMutationPath(oldPath)
	if err != nil {
		return err
	}
	newResolved, err := s.resolveMutationPath(newPath)
	if err != nil {
		return err
	}
	return s.client.Rename(oldResolved, newResolved)
}

// RealPath expands "~" and resolves the path server-side.
func (s *sftpClient) RealPath(p string) (string, error) {
	expanded, err := s.expandHome(p)
	if err != nil {
		return "", err
	}
	return s.client.RealPath(expanded)
}

// resolveParent resolves the parent directory of a not-yet-existing path,
// so create/mkdir/rename targets work with "~" and relative paths.
func (s *sftpClient) resolveParent(p string) (string, error) {
	expanded, err := s.expandHome(p)
	if err != nil {
		return "", err
	}
	dir, base := path.Split(expanded)
	if dir == "" {
		dir = "."
	}
	resolvedDir, err := s.client.RealPath(dir)
	if err != nil {
		return "", err
	}
	return path.Join(resolvedDir, base), nil
}

func (s *sftpClient) expandHome(p string) (string, error) {
	if p == "" {
		p = "."
	}
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	s.mu.Lock()
	home := s.home
	s.mu.Unlock()
	if home == "" {
		resolved, err := s.client.RealPath(".")
		if err != nil {
			return "", err
		}
		s.mu.Lock()
		s.home, home = resolved, resolved
		s.mu.Unlock()
	}
	if p == "~" {
		return home, nil
	}
	return path.Join(home, strings.TrimPrefix(p, "~/")), nil
}

func (s *sftpClient) close() error { return s.client.Close() }

// resolveMutationPath preserves a symlink leaf, including dangling links.
// Refuse root/dot entries before cleaning, which would otherwise retarget them.
func (s *sftpClient) resolveMutationPath(p string) (string, error) {
	p = strings.TrimRight(p, "/")
	base := path.Base(p)
	if p == "" || base == "." || base == ".." || p == "~" {
		return "", fmt.Errorf("refusing unsafe mutation path %q", p)
	}
	return s.resolveParent(p)
}

// Recurse using lstat, not stat: directory symlinks are unlinked, never traversed.
// Concurrent hostile changes to the remote filesystem cannot be eliminated by SFTP v3.
func (s *sftpClient) removeTree(p string) error {
	fi, err := s.client.Lstat(p)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return s.client.Remove(p)
	}
	entries, err := s.client.ReadDir(p)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == "." || name == ".." || path.Base(name) != name {
			return fmt.Errorf("invalid directory entry %q", name)
		}
		if err := s.removeTree(path.Join(p, name)); err != nil {
			return err
		}
	}
	return s.client.RemoveDirectory(p)
}
