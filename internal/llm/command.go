package llm

import (
	"fmt"
	"unicode"
	"unicode/utf8"
)

// Model output is data. Never pass line breaks or terminal control bytes to a PTY.
func ValidateCommand(cmd string) error {
	if cmd == "" || len(cmd) > 8192 || !utf8.ValidString(cmd) {
		return fmt.Errorf("AI returned an empty or oversized command")
	}
	for _, r := range cmd {
		if unicode.IsControl(r) || r == 0x2028 || r == 0x2029 {
			return fmt.Errorf("AI command contains a line break or control character; regenerate it")
		}
	}
	return nil
}
