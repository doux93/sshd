package sshclient

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
)

func (s *sftpClient) WriteAtomic(ctx context.Context, p string, src io.Reader) (n int64, err error) {
	dst, err := s.resolveMutationPath(p)
	if err != nil {
		return 0, err
	}
	mode := os.FileMode(0600)
	if fi, statErr := s.client.Lstat(dst); statErr == nil {
		if !fi.Mode().IsRegular() {
			return 0, fmt.Errorf("refusing to replace non-regular file %q", p)
		}
		mode = fi.Mode().Perm()
	} else if !os.IsNotExist(statErr) {
		return 0, statErr
	}
	nonce := make([]byte, 16)
	if _, err = rand.Read(nonce); err != nil {
		return 0, err
	}
	tmp := path.Join(path.Dir(dst), ".sshx-upload-"+hex.EncodeToString(nonce))
	f, err := s.client.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return 0, err
	}
	defer s.client.Remove(tmp)
	defer f.Close()
	if err = f.Chmod(0600); err != nil {
		return 0, err
	}
	stop := context.AfterFunc(ctx, func() { f.Close() })
	defer stop()
	n, err = io.Copy(f, src)
	if err != nil {
		return n, err
	}
	if err = f.Close(); err != nil {
		return n, err
	}
	if err = ctx.Err(); err != nil {
		return n, err
	}
	if err = s.client.Chmod(tmp, mode); err != nil {
		return n, err
	}
	if _, ok := s.client.HasExtension("posix-rename@openssh.com"); ok {
		err = s.client.PosixRename(tmp, dst)
	} else {
		err = s.client.Rename(tmp, dst)
	}
	return n, err
}
