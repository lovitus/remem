package logbuf

import (
	"fmt"
	"sync"
	"time"
)

type Entry struct {
	Time    string `json:"time"`
	Message string `json:"message"`
}

type Buffer struct {
	mu    sync.RWMutex
	max   int
	lines []Entry
}

func New(maxLines int) *Buffer {
	if maxLines <= 0 {
		maxLines = 200
	}
	return &Buffer{max: maxLines, lines: make([]Entry, 0, maxLines)}
}

func (b *Buffer) Addf(format string, args ...any) {
	b.Add(fmt.Sprintf(format, args...))
}

func (b *Buffer) Add(message string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	entry := Entry{
		Time:    time.Now().Format("15:04:05"),
		Message: message,
	}
	b.lines = append(b.lines, entry)
	if len(b.lines) > b.max {
		b.lines = append([]Entry(nil), b.lines[len(b.lines)-b.max:]...)
	}
}

func (b *Buffer) Snapshot() []Entry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]Entry, len(b.lines))
	copy(out, b.lines)
	return out
}
