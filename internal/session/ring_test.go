package session

import (
	"bytes"
	"testing"
)

func TestRingBasicWriteAndSnapshot(t *testing.T) {
	r := newRing(8)
	r.Write([]byte("abc"))
	if got := r.Snapshot(); !bytes.Equal(got, []byte("abc")) {
		t.Fatalf("got %q", got)
	}
}

func TestRingWrapAround(t *testing.T) {
	r := newRing(8)
	r.Write([]byte("12345"))
	r.Write([]byte("6789")) // total 9 > 8, oldest byte evicted
	if got := r.Snapshot(); !bytes.Equal(got, []byte("23456789")) {
		t.Fatalf("got %q", got)
	}
}

func TestRingOversizedWrite(t *testing.T) {
	r := newRing(4)
	r.Write([]byte("abcdefgh"))
	if got := r.Snapshot(); !bytes.Equal(got, []byte("efgh")) {
		t.Fatalf("got %q", got)
	}
}

func TestRingManySmallWrites(t *testing.T) {
	r := newRing(10)
	var want []byte
	for i := 0; i < 100; i++ {
		b := []byte{byte('a' + i%26)}
		r.Write(b)
		want = append(want, b...)
	}
	want = want[len(want)-10:]
	if got := r.Snapshot(); !bytes.Equal(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRingExactCapacity(t *testing.T) {
	r := newRing(4)
	r.Write([]byte("abcd"))
	if got := r.Snapshot(); !bytes.Equal(got, []byte("abcd")) {
		t.Fatalf("got %q", got)
	}
	r.Write([]byte("e"))
	if got := r.Snapshot(); !bytes.Equal(got, []byte("bcde")) {
		t.Fatalf("got %q", got)
	}
}
