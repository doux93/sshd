package session

import (
	"net/url"
	"strings"
)

// osc7Scanner extracts the current working directory from a terminal byte
// stream by watching for OSC 7 sequences, which shells emit as:
//
//	ESC ] 7 ; file://host/path BEL      (or ST = ESC \)
//
// This is how the file panel follows the terminal's `cd` (docs/DESIGN.md
// §3.6). The scanner is fed the same bytes as the ring buffer and tolerates
// sequences split across reads.
type osc7Scanner struct {
	// buf accumulates a partially received sequence between Feed calls.
	buf []byte
}

const (
	esc = 0x1b
	bel = 0x07
	// osc7MaxLen caps how much we buffer for one sequence; a longer
	// "sequence" is malformed (or not really OSC 7) and gets dropped.
	osc7MaxLen = 4096
	osc7Prefix = "\x1b]7;"
)

// Feed consumes terminal output and returns the last complete directory
// reported in it, or "" if none. Paths are percent-decoded.
func (s *osc7Scanner) Feed(p []byte) string {
	// Only buffer what can matter: bytes after the last ESC when no
	// sequence is in progress. This keeps memory flat on high-volume output.
	data := p
	if len(s.buf) > 0 {
		data = append(s.buf, p...)
		s.buf = nil
	}

	found := ""
	for {
		start := indexOSC7(data)
		if start < 0 {
			break
		}
		body := data[start+len(osc7Prefix):]
		end, termLen := indexTerminator(body)
		if end < 0 {
			// Incomplete: keep the tail for the next Feed if it's plausible.
			if len(data)-start <= osc7MaxLen {
				s.buf = append([]byte(nil), data[start:]...)
			}
			return found
		}
		if dir := parseFileURI(string(body[:end])); dir != "" {
			found = dir
		}
		data = body[end+termLen:]
	}

	// Retain a possible partial "\x1b]7;" prefix straddling the boundary.
	if tail := partialPrefixLen(data); tail > 0 {
		s.buf = append([]byte(nil), data[len(data)-tail:]...)
	}
	return found
}

func indexOSC7(data []byte) int {
	return strings.Index(string(data), osc7Prefix)
}

// indexTerminator finds BEL or ST (ESC \) and reports its length.
func indexTerminator(body []byte) (idx, termLen int) {
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case bel:
			return i, 1
		case esc:
			if i+1 < len(body) && body[i+1] == '\\' {
				return i, 2
			}
			// A bare ESC means the sequence was aborted.
			return i, 1
		}
	}
	return -1, 0
}

// parseFileURI turns "file://host/path" (or a bare path) into a path.
func parseFileURI(raw string) string {
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(raw, "file://") {
		// Some shells emit the path alone; accept absolute paths only.
		if strings.HasPrefix(raw, "/") {
			if decoded, err := url.PathUnescape(raw); err == nil {
				return decoded
			}
		}
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Path == "" {
		return ""
	}
	return u.Path
}

// partialPrefixLen reports how many trailing bytes of data could be the
// start of an OSC 7 prefix.
func partialPrefixLen(data []byte) int {
	max := len(osc7Prefix) - 1
	if len(data) < max {
		max = len(data)
	}
	for n := max; n > 0; n-- {
		if string(data[len(data)-n:]) == osc7Prefix[:n] {
			return n
		}
	}
	return 0
}
