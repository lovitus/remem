package logbuf

import (
	"fmt"
	"sync"
	"time"
)

type Kind string

const (
	KindRoutine Kind = "routine"
	KindAction  Kind = "action"
	KindError   Kind = "error"
	KindKill    Kind = "kill"
)

type Entry struct {
	Time    string `json:"time"`
	Kind    Kind   `json:"kind"`
	Message string `json:"message"`
}

type Snapshot struct {
	Routine   []Entry `json:"routine"`
	Important []Entry `json:"important"`
}

type Buffer struct {
	mu sync.RWMutex

	routineMax   int
	importantMax int
	routine      []Entry
	important    []Entry
}

func New(routineMaxLines, importantMaxLines int) *Buffer {
	if routineMaxLines <= 0 {
		routineMaxLines = 10
	}
	if importantMaxLines <= 0 {
		importantMaxLines = 100
	}
	return &Buffer{
		routineMax:   routineMaxLines,
		importantMax: importantMaxLines,
		routine:      make([]Entry, 0, routineMaxLines),
		important:    make([]Entry, 0, importantMaxLines),
	}
}

// Add/Addf are backward-compatible and treated as routine logs.
func (b *Buffer) Addf(format string, args ...any) {
	b.Add(fmt.Sprintf(format, args...))
}

func (b *Buffer) Add(message string) {
	b.AddRoutine(message)
}

func (b *Buffer) AddRoutinef(format string, args ...any) {
	b.AddRoutine(fmt.Sprintf(format, args...))
}

func (b *Buffer) AddActionf(format string, args ...any) {
	b.addImportant(KindAction, fmt.Sprintf(format, args...))
}

func (b *Buffer) AddErrorf(format string, args ...any) {
	b.addImportant(KindError, fmt.Sprintf(format, args...))
}

func (b *Buffer) AddKillf(format string, args ...any) {
	b.addImportant(KindKill, fmt.Sprintf(format, args...))
}

func (b *Buffer) AddRoutine(message string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	entry := Entry{Time: time.Now().Format("15:04:05"), Kind: KindRoutine, Message: message}
	b.routine = append(b.routine, entry)
	if len(b.routine) > b.routineMax {
		b.routine = append([]Entry(nil), b.routine[len(b.routine)-b.routineMax:]...)
	}
}

func (b *Buffer) addImportant(kind Kind, message string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	entry := Entry{Time: time.Now().Format("2006-01-02 15:04:05"), Kind: kind, Message: message}
	b.important = append(b.important, entry)
	if len(b.important) > b.importantMax {
		b.important = append([]Entry(nil), b.important[len(b.important)-b.importantMax:]...)
	}
}

func (b *Buffer) SnapshotByCategory() Snapshot {
	b.mu.RLock()
	defer b.mu.RUnlock()

	r := make([]Entry, len(b.routine))
	copy(r, b.routine)
	i := make([]Entry, len(b.important))
	copy(i, b.important)
	return Snapshot{Routine: r, Important: i}
}
