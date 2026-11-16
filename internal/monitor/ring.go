package monitor

import (
	"sync"
	"time"
)

// ringLen is the number of samples retained in memory. At 5s intervals this
// holds ~1 hour of data (720 points). The design doc deliberately keeps this
// out of SQLite ? the value window is hours, not months.
const ringLen = 720

// TimeSeries is a fixed-capacity ring buffer of Samples. It is safe for
// concurrent use. Push overwrites the oldest sample when full.
type TimeSeries struct {
	mu   sync.Mutex
	buf  [ringLen]Sample
	head int // index of next write
	n    int // number of valid samples (<= ringLen)
}

func NewTimeSeries() *TimeSeries {
	return &TimeSeries{}
}

// Push appends a sample, evicting the oldest if the buffer is full.
func (t *TimeSeries) Push(s Sample) {
	t.mu.Lock()
	t.buf[t.head] = s
	t.head = (t.head + 1) % ringLen
	if t.n < ringLen {
		t.n++
	}
	t.mu.Unlock()
}

// Latest returns the most recent sample, or false if none.
func (t *TimeSeries) Latest() (Sample, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.n == 0 {
		return Sample{}, false
	}
	idx := (t.head - 1 + ringLen) % ringLen
	return t.buf[idx], true
}

// All returns all valid samples in chronological order (oldest first).
// The slice is a copy; the caller may mutate it freely.
func (t *TimeSeries) All() []Sample {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Sample, t.n)
	if t.n < ringLen {
		copy(out, t.buf[:t.n])
		return out
	}
	// Buffer is full: head is the oldest sample.
	copy(out, t.buf[t.head:])
	copy(out[ringLen-t.head:], t.buf[:t.head])
	return out
}

// Recent returns up to the last n samples (chronological order).
func (t *TimeSeries) Recent(n int) []Sample {
	if n <= 0 {
		return nil
	}
	all := t.All()
	if n >= len(all) {
		return all
	}
	return all[len(all)-n:]
}

// Since returns samples with TS >= from, in chronological order.
func (t *TimeSeries) Since(from time.Time) []Sample {
	all := t.All()
	start := len(all)
	for i, s := range all {
		if !s.TS.Before(from) {
			start = i
			break
		}
	}
	if start >= len(all) {
		return nil
	}
	return all[start:]
}
