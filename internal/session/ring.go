package session

// ring is a fixed-capacity byte ring buffer used as the terminal replay
// buffer: it retains the most recent output so a client attaching later can
// reconstruct the screen. Not safe for concurrent use; callers synchronize.
type ring struct {
	buf   []byte
	start int // index of oldest byte
	size  int // number of valid bytes
}

func newRing(capacity int) *ring {
	return &ring{buf: make([]byte, capacity)}
}

func (r *ring) Write(p []byte) {
	if len(p) >= len(r.buf) {
		// Payload alone fills the ring: keep only its tail.
		copy(r.buf, p[len(p)-len(r.buf):])
		r.start = 0
		r.size = len(r.buf)
		return
	}
	end := (r.start + r.size) % len(r.buf)
	n := copy(r.buf[end:], p)
	if n < len(p) {
		copy(r.buf, p[n:])
	}
	r.size += len(p)
	if r.size > len(r.buf) {
		r.start = (r.start + r.size - len(r.buf)) % len(r.buf)
		r.size = len(r.buf)
	}
}

// Snapshot returns the buffered bytes oldest-first as a fresh slice.
func (r *ring) Snapshot() []byte {
	out := make([]byte, r.size)
	n := copy(out, r.buf[r.start:min(r.start+r.size, len(r.buf))])
	copy(out[n:], r.buf[:r.size-n])
	return out
}
