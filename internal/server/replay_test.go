package server

import (
	"bytes"
	"testing"
)

func TestScreenReplayOmitsOnlyDeviceAttributeRequests(t *testing.T) {
	for _, query := range []string{"\x1b[c", "\x1b[0c", "\x1b[>c", "\x1b[>0c", "\x1b[=c", "\x1b[=00c"} {
		t.Run(query, func(t *testing.T) {
			input := []byte("中文\x1b[31mred\x1b[0m" + query + "\x1b[2Jscreen\x1b[?1;2c\x1b[>1;2;3c")
			before := bytes.Clone(input)
			want := []byte("中文\x1b[31mred\x1b[0m\x1b[2Jscreen\x1b[?1;2c\x1b[>1;2;3c")
			if got := screenReplay(input); !bytes.Equal(got, want) {
				t.Fatalf("replay = %q, want %q", got, want)
			}
			if !bytes.Equal(input, before) {
				t.Fatal("replay filtering changed original history")
			}
		})
	}
	partial := []byte("screen\x1b[>")
	if got := screenReplay(partial); !bytes.Equal(got, partial) {
		t.Fatal("incomplete live sequence was damaged")
	}
}
