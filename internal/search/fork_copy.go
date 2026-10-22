package search

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// CopyOf reports whether c is a copy of src that names no source: c holds
// src's last turn and src holds none of c's after it. A Claude --fork-session
// copy keeps every copied record's time, so it starts on the same record as
// its source and its start tells nothing apart (#5316). Two sessions that both
// went on are siblings, and neither is a copy of the other.
func CopyOf(c, src model.Session) bool {
	return holdsLastTurn(c, src) && !holdsLastTurn(src, c)
}

// holdsLastTurn reports whether holder has other's last timed turn, the same
// role, time and text. A reply the harness wrote on its own is passed over.
func holdsLastTurn(holder, other model.Session) bool {
	m, ok := LastTurn(other)
	if !ok {
		return false
	}
	for _, h := range holder.Messages {
		if h.Role == m.Role && h.Time.UnixNano() == m.Time.UnixNano() && h.Text == m.Text {
			return true
		}
	}
	return false
}

// LastTurn is the turn CopyOf looks for in a copy: s's last timed message,
// passing over empty ones, summaries and replies the harness wrote on its own.
func LastTurn(s model.Session) (model.Message, bool) {
	for i := len(s.Messages) - 1; i >= 0; i-- {
		m := s.Messages[i]
		t := strings.TrimSpace(m.Text)
		if m.Time.IsZero() || t == "" || m.Role == roleSummary || (m.Role == "assistant" && t == "No response requested.") {
			continue
		}
		return m, true
	}
	return model.Message{}, false
}

// SourceOf reports whether src is the session the records c shares with it at
// time at came from, so the work there is src's: c is a fork naming src
// directly or through forks among sessions, or an unnamed copy of it.
func SourceOf(sessions []model.Session, src, c model.Session, at time.Time) bool {
	return forkSource(sessions, src, c) || copiedFrom(at, src, c)
}

// forkSource reports whether src is the session fork was forked from, directly
// or through forks of forks among the candidates. A fork carries its source's
// records at their original times, so the two tie on the record that wrote a
// line, and the newest copy took the credit for an edit it only copied.
func forkSource(sessions []model.Session, src, fork model.Session) bool {
	for range sessions {
		if fork.Kind != "fork" || fork.Parent == "" || fork.Harness != src.Harness {
			return false
		}
		if fork.Parent == src.ID {
			return true
		}
		next, ok := model.Session{}, false
		for _, c := range sessions {
			if c.Harness == fork.Harness && c.ID == fork.Parent {
				next, ok = c, true
				break
			}
		}
		if !ok {
			return false
		}
		fork = next
	}
	return false
}

// copiedFrom reports whether s, tied with best on the record that wrote a
// line, is the session that record was copied from when the copy names no
// source: opencode's --fork carries its source's records at their original
// times and leaves no marker, and the source started before any copy of it.
//
// A Claude --fork-session copy starts on the same record as its source, and is
// told apart by holding the source's last turn (#5316).
//
// A sub-agent copies nothing from its parent: a parent and its child writing
// one line in the same whole second (goose, Hermes) is not a copy, and taking
// the older session handed the child's line to the parent.
func copiedFrom(at time.Time, s, best model.Session) bool {
	if model.SpawnedKind(s.Kind) || model.SpawnedKind(best.Kind) {
		return false
	}
	if at.IsZero() || s.Harness != best.Harness {
		return false
	}
	if !s.Started.IsZero() && s.Started.Before(best.Started) {
		return true
	}
	// claude --fork-session copies with the original times, so the copy
	// starts on its source's first record; what it holds tells them apart.
	return CopyOf(best, s)
}

// RecordNamesFile reports whether a recorded path names the file asked about. The
// recorded one is absolute and may come from a worktree or another machine, so
// the comparison is on the tail.
func RecordNamesFile(recorded string, target BlameTarget) bool {
	recorded = strings.TrimSpace(strings.ReplaceAll(recorded, "\\", "/"))
	if recorded == "" {
		return false
	}
	want := filepath.ToSlash(target.FullPath)
	if recorded == want {
		return true
	}
	// A worktree or a peer's checkout: the repository-relative tail is what
	// both have in common. Two segments is enough to tell `pool.go` in two
	// packages apart and short enough to survive a different checkout root.
	return strings.HasSuffix(recorded, "/"+tailSegments(want, 2)) && strings.HasSuffix(want, "/"+tailSegments(recorded, 2))
}

func tailSegments(p string, n int) string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) <= n {
		return strings.Join(parts, "/")
	}
	return strings.Join(parts[len(parts)-n:], "/")
}
