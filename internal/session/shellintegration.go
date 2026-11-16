package session

// ShellIntegration returns a command that installs an OSC 7 hook in the
// remote shell, so every prompt reports the working directory and the file
// panel can follow `cd` (docs/DESIGN.md §3.6).
//
// It is written to be pasted into an interactive shell: it detects bash vs
// zsh at runtime, is a no-op elsewhere, and emits one OSC 7 immediately so
// the panel syncs without waiting for the next prompt. The leading space
// keeps it out of history in shells with HISTCONTROL=ignorespace.
//
// Injection is opt-in per session: writing to the user's terminal is a
// visible side effect, and some environments (restricted shells, non-shell
// commands) must not receive it.
func ShellIntegration() string {
	return " " + shellIntegrationScript + "\n"
}

// The script avoids single quotes inside single quotes and keeps to POSIX
// constructs plus shell-specific hooks guarded by $BASH_VERSION/$ZSH_VERSION.
const shellIntegrationScript = `__sshx_osc7() { printf "\033]7;file://%s%s\007" "${HOSTNAME:-}" "$PWD"; }; ` +
	`if [ -n "${ZSH_VERSION:-}" ]; then ` +
	`autoload -Uz add-zsh-hook 2>/dev/null && add-zsh-hook precmd __sshx_osc7; ` +
	`elif [ -n "${BASH_VERSION:-}" ]; then ` +
	`case "${PROMPT_COMMAND:-}" in *__sshx_osc7*) ;; *) PROMPT_COMMAND="__sshx_osc7${PROMPT_COMMAND:+;$PROMPT_COMMAND}";; esac; ` +
	`fi; __sshx_osc7`
