package devprocess

import (
	"strings"
	"sync"
)

type LineTail struct {
	mu    sync.Mutex
	limit int
	lines []string
}

func NewLineTail(limit int) *LineTail { return &LineTail{limit: limit} }

func (t *LineTail) Add(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lines = append(t.lines, line)
	if t.limit > 0 && len(t.lines) > t.limit {
		t.lines = append([]string(nil), t.lines[len(t.lines)-t.limit:]...)
	}
}

func (t *LineTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.lines, "\n")
}
