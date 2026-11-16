package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Explanations are model-authored display text, never an authorization decision.
type Suggestion struct {
	Command     string `json:"command"`
	Explanation string `json:"explanation"`
	Impact      string `json:"impact"`
}

func ParseSuggestion(raw string) (Suggestion, error) {
	var s Suggestion
	if len(raw) > 32768 {
		return s, fmt.Errorf("AI response is too large")
	}
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return s, fmt.Errorf("AI must return command, explanation and impact as JSON; regenerate it")
	}
	if err := ValidateCommand(s.Command); err != nil {
		return s, err
	}
	if strings.TrimSpace(s.Explanation) == "" || strings.TrimSpace(s.Impact) == "" || len(s.Explanation) > 6000 || len(s.Impact) > 6000 {
		return s, fmt.Errorf("AI returned an incomplete or oversized explanation")
	}
	return s, nil
}
