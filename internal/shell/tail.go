package shell

import (
	"io"
	"strings"
	"sync"
)

// TailWriter forwards writes to an underlying writer and keeps the last bytes in memory,
// so failures can be reported with recent output.
type TailWriter struct {
	mu  sync.Mutex
	w   io.Writer
	buf []byte
	max int
}

// NewTailWriter returns a TailWriter that keeps at most max bytes.
func NewTailWriter(w io.Writer, max int) *TailWriter {
	return &TailWriter{w: w, max: max}
}

// Write implements io.Writer.
func (t *TailWriter) Write(p []byte) (int, error) {
	t.mu.Lock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = t.buf[over:]
	}
	t.mu.Unlock()
	if t.w == nil {
		return len(p), nil
	}
	return t.w.Write(p)
}

// Tail returns up to n trailing lines of what has been written.
func (t *TailWriter) Tail(n int) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return lastLines(string(t.buf), n)
}

// Lines splits s into non-empty trimmed lines.
func Lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}
