// Package usage records when deja serves memory to an agent — MCP recalls
// and session-start hook injections — so `deja statusline` can show activity.
// Recording is best-effort by design: a failure to write a usage event must
// never break the recall path itself.
package usage

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Event kinds. Search is tracked too but statusline only counts memory
// served to agents (recall, context, hook).
const (
	KindRecall  = "recall"
	KindContext = "recall_context"
	KindHook    = "hook"
	KindSearch  = "search"
)

type Event struct {
	Time  time.Time `json:"t"`
	Kind  string    `json:"kind"`
	Bytes int       `json:"bytes"`
}

const (
	rotateAt   = 1 << 20 // rewrite the log when it grows past 1MB
	keepWindow = 14 * 24 * time.Hour
)

// Path returns the usage log location for an index dir: a sibling file, like
// the .lock file, so it survives full index rebuilds.
func Path(indexDir string) string {
	return strings.TrimSuffix(indexDir, string(filepath.Separator)) + ".usage.jsonl"
}

// Record appends one event. Errors are swallowed on purpose.
func Record(indexDir, kind string, bytes int) {
	p := Path(indexDir)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	rotate(p)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	b, err := json.Marshal(Event{Time: time.Now().UTC(), Kind: kind, Bytes: bytes})
	if err != nil {
		return
	}
	_, _ = f.Write(append(b, '\n'))
}

// Today sums events since local midnight: agent recalls (recall, context,
// hook) and the context bytes they served.
func Today(indexDir string) (recalls int, bytes int) {
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for _, e := range read(Path(indexDir)) {
		if e.Time.Before(midnight) {
			continue
		}
		switch e.Kind {
		case KindRecall, KindContext, KindHook:
			recalls++
			bytes += e.Bytes
		}
	}
	return recalls, bytes
}

func read(p string) []Event {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Event
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 4096), 1<<20)
	for s.Scan() {
		var e Event
		if json.Unmarshal(s.Bytes(), &e) == nil && !e.Time.IsZero() {
			out = append(out, e)
		}
	}
	return out
}

// rotate rewrites the log keeping only the recent window once it grows past
// rotateAt. Concurrent writers may lose an event during the swap; usage data
// is advisory, so that trade keeps the hot path lock-free.
func rotate(p string) {
	fi, err := os.Stat(p)
	if err != nil || fi.Size() < rotateAt {
		return
	}
	cutoff := time.Now().UTC().Add(-keepWindow)
	var keep []Event
	for _, e := range read(p) {
		if e.Time.After(cutoff) {
			keep = append(keep, e)
		}
	}
	tmp := p + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return
	}
	for _, e := range keep {
		if b, err := json.Marshal(e); err == nil {
			_, _ = f.Write(append(b, '\n'))
		}
	}
	if f.Close() != nil {
		_ = os.Remove(tmp)
		return
	}
	_ = os.Rename(tmp, p)
}
