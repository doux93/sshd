package llm

import "testing"

func TestValidateCommandRejectsTerminalControls(t *testing.T) {
	for _, cmd := range []string{"", "echo first\necho second", "echo x\r", "echo \x1b[2J", "echo \x00", "echo \u0085", "echo \u2028"} {
		if err := ValidateCommand(cmd); err == nil {
			t.Errorf("allowed %q", cmd)
		}
	}
	for _, cmd := range []string{"ls -la", "printf '%s' hello", "echo 中文"} {
		if err := ValidateCommand(cmd); err != nil {
			t.Errorf("rejected %q: %v", cmd, err)
		}
	}
}
