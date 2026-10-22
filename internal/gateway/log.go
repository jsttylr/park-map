package gateway

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Entry is one line of the gateway log.
type Entry struct {
	N      int       `json:"n"`
	Time   time.Time `json:"time"`
	Level  string    `json:"level"` // info, warn, error
	Event  string    `json:"event"`
	Detail string    `json:"detail,omitempty"`
}

// Log keeps recent entries in memory for the log viewer and appends them to
// a file in the config folder.
type Log struct {
	mu      sync.Mutex
	entries []Entry
	n       int
	path    string
}

const (
	logKeep    = 1000
	logMaxFile = 5 << 20
)

func NewLog(path string) *Log { return &Log{path: path} }

func (l *Log) add(level, event, detail string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.n++
	e := Entry{N: l.n, Time: time.Now().UTC(), Level: level, Event: event, Detail: detail}
	l.entries = append(l.entries, e)
	if len(l.entries) > logKeep {
		l.entries = l.entries[len(l.entries)-logKeep:]
	}
	if l.path == "" {
		return
	}
	if fi, err := os.Stat(l.path); err == nil && fi.Size() > logMaxFile {
		os.Rename(l.path, l.path+".1")
	}
	if f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		b, _ := json.Marshal(e)
		f.Write(append(b, '\n'))
		f.Close()
	}
}

func (l *Log) Info(event string, detail ...any)  { l.add("info", event, join(detail)) }
func (l *Log) Warn(event string, detail ...any)  { l.add("warn", event, join(detail)) }
func (l *Log) Error(event string, detail ...any) { l.add("error", event, join(detail)) }

func join(d []any) string {
	if len(d) == 0 {
		return ""
	}
	if f, ok := d[0].(string); ok && len(d) > 1 {
		return fmt.Sprintf(f, d[1:]...)
	}
	return fmt.Sprint(d...)
}

// Since returns the entries after number n, oldest first.
func (l *Log) Since(n int) []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []Entry{}
	for _, e := range l.entries {
		if e.N > n {
			out = append(out, e)
		}
	}
	return out
}
