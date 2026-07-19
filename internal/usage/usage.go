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
	Time     time.Time `json:"t"`
	Kind     string    `json:"kind"`
	Bytes    int       `json:"bytes"`
	Sessions int       `json:"sessions,omitempty"`
	Empty    bool      `json:"empty,omitempty"`
}

type Summary struct {
	Recalls          int     `json:"recalls_served"`
	Injections       int     `json:"injections"`
	RecallSessions   int     `json:"recall_sessions"`
	InjectedSessions int     `json:"injected_sessions"`
	Bytes            int     `json:"bytes"`
	InjectedBytes    int     `json:"injected_bytes"`
	EmptyResultRate  float64 `json:"empty_result_rate"`
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
	RecordResult(indexDir, kind, bytes, 0, false)
}

// RecordResult appends an event with result accounting. Errors are swallowed.
func RecordResult(indexDir, kind string, bytes, sessions int, empty bool) {
	p := Path(indexDir)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	rotate(p)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	b, err := json.Marshal(Event{Time: time.Now().UTC(), Kind: kind, Bytes: bytes, Sessions: sessions, Empty: empty})
	if err != nil {
		return
	}
	_, _ = f.Write(append(b, '\n'))
}

// InjectedToday returns session-start context bytes injected since local midnight.
func InjectedToday(indexDir string) int {
	_, _, injected := TodayWithInjections(indexDir)
	return injected
}

// TodayWithInjections returns today's agent-memory events, served bytes, and
// the subset of those bytes injected by session-start hooks.
func TodayWithInjections(indexDir string) (recalls, bytes, injected int) {
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for _, e := range read(Path(indexDir)) {
		if e.Time.Before(midnight) {
			continue
		}
		switch e.Kind {
		case KindRecall, KindContext:
			recalls++
			bytes += e.Bytes
		case KindHook:
			recalls++
			bytes += e.Bytes
			injected += e.Bytes
		}
	}
	return recalls, bytes, injected
}

// Totals summarizes the retained usage log.
func Totals(indexDir string) Summary {
	var out Summary
	empty := 0
	for _, e := range read(Path(indexDir)) {
		switch e.Kind {
		case KindRecall, KindContext:
			out.Recalls++
			out.RecallSessions += e.Sessions
			out.Bytes += e.Bytes
			if e.Empty {
				empty++
			}
		case KindHook:
			out.Recalls++
			out.Injections++
			out.InjectedSessions += e.Sessions
			out.InjectedBytes += e.Bytes
			out.Bytes += e.Bytes
		}
	}
	if served := out.Recalls - out.Injections; served > 0 {
		out.EmptyResultRate = float64(empty) / float64(served)
	}
	return out
}

// Today sums events since local midnight: agent recalls (recall, context,
// hook) and the context bytes they served.
func Today(indexDir string) (recalls int, bytes int) {
	recalls, bytes, _ = TodayWithInjections(indexDir)
	return recalls, bytes
}

func read(p string) []Event {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
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
