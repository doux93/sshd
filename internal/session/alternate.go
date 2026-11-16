package session

import (
	"strings"
)

// Track DEC alternate-screen toggles across read boundaries. This is not a VT emulator.
type alternateScanner struct {
	seq    []byte
	active bool
}

func (s *alternateScanner) Feed(p []byte) {
	for _, b := range p {
		if b == 0x1b {
			s.seq = []byte{b}
			continue
		}
		if len(s.seq) == 0 {
			continue
		}
		s.seq = append(s.seq, b)
		if len(s.seq) == 2 {
			if b == 'c' {
				s.active = false
				s.seq = nil
			} else if b != '[' {
				s.seq = nil
			}
			continue
		}
		if len(s.seq) > 64 {
			s.seq = nil
			continue
		}
		if b >= 0x40 && b <= 0x7e {
			body := string(s.seq[2 : len(s.seq)-1])
			if (b == 'h' || b == 'l') && strings.HasPrefix(body, "?") {
				for _, mode := range strings.Split(body[1:], ";") {
					if mode == "47" || mode == "1047" || mode == "1049" {
						s.active = b == 'h'
					}
				}
			}
			s.seq = nil
		}
	}
}
