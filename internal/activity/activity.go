// Package activity keeps a short in-memory timeline of each conversation for the dashboard.
package activity

import (
	"sync"
	"time"
)

// Entry kinds besides the agent event kinds (assistant, tool, tool_error).
const (
	KindUser = "user"
	KindBot  = "bot"
)

// Entry is one line of a conversation timeline.
type Entry struct {
	Seq  int64     `json:"seq"`
	Time time.Time `json:"time"`
	Kind string    `json:"kind"`
	Text string    `json:"text"`
}

// Hub stores the last max entries per conversation. The zero value is not usable; use New.
type Hub struct {
	mu    sync.Mutex
	max   int
	seq   int64
	convs map[int][]Entry
}

// New returns a Hub keeping up to max entries per conversation.
func New(max int) *Hub {
	return &Hub{max: max, convs: map[int][]Entry{}}
}

// Add appends an entry to a conversation's timeline. A nil Hub ignores it.
func (h *Hub) Add(conv int, kind, text string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	es := append(h.convs[conv], Entry{Seq: h.seq, Time: time.Now(), Kind: kind, Text: text})
	if over := len(es) - h.max; over > 0 {
		es = es[over:]
	}
	h.convs[conv] = es
}

// Since returns a conversation's entries with Seq greater than seq, oldest first.
func (h *Hub) Since(conv int, seq int64) []Entry {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []Entry{}
	for _, e := range h.convs[conv] {
		if e.Seq > seq {
			out = append(out, e)
		}
	}
	return out
}
