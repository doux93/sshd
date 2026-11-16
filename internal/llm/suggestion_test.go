package llm

import "testing"

func TestSuggestionRejectsMissingExplanationAndUnsafeCommandEncoding(t *testing.T) {
	for _, raw := range []string{`pwd`, `{"command":"pwd","explanation":"why"}`, `{"command":"pwd\nrm -r .","explanation":"why","impact":"effect"}`, "```json\n{}\n```"} {
		if _, err := ParseSuggestion(raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	s, err := ParseSuggestion(`{"command":"ls -lh","explanation":"列出文件大小","impact":"读取目录，不改文件"}`)
	if err != nil || s.Command != "ls -lh" {
		t.Fatal(s, err)
	}
}
