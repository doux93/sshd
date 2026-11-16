package sshclient

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sshx/internal/session"
	"strings"
	"time"
)

var ErrTmuxUnavailable = errors.New("远端未找到 tmux，请安装 tmux 后重试，或关闭主机的终端保活选项")
var ErrTmuxMissing = errors.New("原远端终端已不存在；不会自动新建替代会话。可忘记恢复记录后重新连接")
var recoveryIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Each managed terminal has an isolated socket and exact session name. Neither
// host labels nor client-supplied shell fragments ever enter these commands.
func tmuxPrefix(id string) (string, error) {
	if !recoveryIDPattern.MatchString(id) {
		return "", errors.New("invalid recovery id")
	}
	return "TMUX= LC_ALL=C tmux -L sshx-" + id + " -f /dev/null ", nil
}
func (c *Client) runTmux(ctx context.Context, cmd string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, stderr, code, err := c.Exec(ctx, "command -v tmux >/dev/null 2>&1 || exit 127; "+cmd)
	if err != nil {
		return err
	}
	if code == 127 {
		return ErrTmuxUnavailable
	}
	if code != 0 {
		return fmt.Errorf("tmux 操作失败（%d）：%s", code, strings.TrimSpace(string(stderr)))
	}
	return nil
}
func (c *Client) checkPersistent(ctx context.Context, prefix string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, stderr, code, err := c.Exec(ctx, "command -v tmux >/dev/null 2>&1 || exit 127; "+prefix+"has-session -t '=terminal'")
	if err != nil {
		return err
	}
	if code == 127 {
		return ErrTmuxUnavailable
	}
	if code == 0 {
		return nil
	}
	message := string(stderr)
	if strings.Contains(message, "can't find session:") || strings.Contains(message, "no server running on") || (strings.Contains(message, "error connecting to") && strings.Contains(message, "No such file or directory")) {
		return ErrTmuxMissing
	}
	return fmt.Errorf("无法确认远端终端状态（%d）：%s", code, strings.TrimSpace(message))
}

// PreparePersistent creates once or verifies an existing session. Resume NEVER
// uses new-session -A: a missing remote process must not masquerade as recovery.
func (c *Client) PreparePersistent(ctx context.Context, id string, create bool, cols, rows int) (session.Backend, error) {
	prefix, err := tmuxPrefix(id)
	if err != nil {
		return nil, err
	}
	if create {
		if cols < 1 || cols > 1000 {
			cols = 80
		}
		if rows < 1 || rows > 1000 {
			rows = 24
		}
		err = c.runTmux(ctx, fmt.Sprintf("%snew-session -d -s terminal -x %d -y %d", prefix, cols, rows))
	} else {
		err = c.checkPersistent(ctx, prefix)
	}
	if err != nil {
		return nil, err
	}
	return &persistentClient{Client: c, command: prefix + "attach-session -t '=terminal'"}, nil
}
func (c *Client) TerminatePersistent(ctx context.Context, id string) error {
	prefix, err := tmuxPrefix(id)
	if err != nil {
		return err
	}
	if err = c.checkPersistent(ctx, prefix); errors.Is(err, ErrTmuxMissing) {
		return nil
	} else if err != nil {
		return err
	}
	return c.runTmux(ctx, prefix+"kill-session -t '=terminal'")
}

type persistentClient struct {
	*Client
	command string
}

func (c *persistentClient) OpenTerminal(cols, rows int) (session.TerminalIO, error) {
	return c.openTerminal(cols, rows, c.command)
}
