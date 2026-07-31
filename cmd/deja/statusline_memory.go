package main

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
)

// The status line is the only surface deja has that is present *during* the
// work rather than at the start of it, which is what #581 is about: say what
// memory exists for the file being worked on, while it is being worked on.
//
// The obvious version cannot be built. Claude Code's status-line payload has
// no field naming the file under edit — no current_file, no active_file, none
// — so "3 sessions touched store_io.go" cannot be tied to what is open. What
// the payload does carry is transcript_path, which identifies the *current
// session*, and a session's busiest files have been in the manifest since
// #571. So the claim this makes is the one the data supports: the file this
// session has worked on most, and what deja remembers about it.
type statuslineInput struct {
	TranscriptPath string `json:"transcript_path"`
	Workspace      struct {
		CurrentDir string `json:"current_dir"`
	} `json:"workspace"`
}

// statuslineMaxTitle keeps the memory segment from pushing the usage numbers
// off a narrow terminal (#604 is the same concern, one screen over).
const statuslineMaxTitle = 38

// readStatuslineInput consumes the payload without ever blocking. An
// interactive terminal has nothing piped, and a host that pipes something
// deja does not understand must cost nothing to ignore.
func readStatuslineInput(r io.Reader) statuslineInput {
	var in statuslineInput
	if !pipedStdin(r) {
		return in
	}
	b, err := io.ReadAll(io.LimitReader(r, 1<<20))
	if err != nil || len(b) == 0 {
		return in
	}
	_ = json.Unmarshal(b, &in)
	return in
}

// fileMemory is what deja remembers about the file the current session is
// working on: how many earlier sessions touched it, and what the most recent
// of those was about.
type fileMemory struct {
	Path     string
	Sessions int
	Title    string
	Last     time.Time
}

// statuslineMemory answers from the manifest alone — no record read, no lock,
// no git. A status line re-runs on every assistant message, so anything that
// forks or scans the log is disqualified.
func statuslineMemory(dir string, in statuslineInput) (fileMemory, bool) {
	id := strings.TrimSuffix(filepath.Base(in.TranscriptPath), ".jsonl")
	if id == "" || id == "." {
		return fileMemory{}, false
	}
	metas, err := index.AllMeta(dir)
	if err != nil {
		return fileMemory{}, false
	}
	var here string
	for _, m := range metas {
		if m.ID == id && len(m.Touched) > 0 {
			here = m.Touched[0]
			break
		}
	}
	if here == "" {
		return fileMemory{}, false
	}
	var others []index.SessionMeta
	for _, m := range metas {
		if m.ID == id {
			continue
		}
		for _, p := range m.Touched {
			if p == here {
				others = append(others, m)
				break
			}
		}
	}
	if len(others) == 0 {
		// Silence, not a zero: a file nobody else has worked on has no memory
		// to report, and printing "0 sessions" makes the line noise forever.
		return fileMemory{}, false
	}
	sort.Slice(others, func(i, j int) bool { return others[i].Updated.After(others[j].Updated) })
	return fileMemory{
		Path:     here,
		Sessions: len(others),
		Title:    others[0].Title,
		Last:     others[0].Updated,
	}, true
}

// statuslineMemoryLine renders it. A count is a statistic; what the earlier
// session was about is a memory, so the title wins when there is one.
func statuslineMemoryLine(m fileMemory) string {
	name := filepath.Base(m.Path)
	if title := trimStatuslineTitle(m.Title); title != "" {
		return fmt.Sprintf("%s · %d earlier: \"%s\" %s", name, m.Sessions, title, search.RelativeDate(m.Last))
	}
	noun := "sessions"
	if m.Sessions == 1 {
		noun = "session"
	}
	return fmt.Sprintf("%s · %d earlier %s · %s", name, m.Sessions, noun, search.RelativeDate(m.Last))
}

func trimStatuslineTitle(t string) string {
	t = strings.TrimSpace(strings.ReplaceAll(t, "\n", " "))
	r := []rune(t)
	if len(r) > statuslineMaxTitle {
		return strings.TrimSpace(string(r[:statuslineMaxTitle])) + "…"
	}
	return t
}

// withFileMemory puts the memory ahead of the usage numbers when there is
// one. The numbers are about deja; the memory is about the reader's own work,
// and on a line that gets truncated the useful half should survive.
//
// The usage half is shortened to make room rather than appended whole: a
// status line lives in whatever width the terminal has, and a line that runs
// off the edge loses its tail — which would be the numbers if they came
// second, but the memory if the line simply grew.
func withFileMemory(dir string, in statuslineInput, usage string) string {
	m, ok := statuslineMemory(dir, in)
	if !ok {
		return usage
	}
	return "deja · " + statuslineMemoryLine(m) + " · " + shortenUsage(usage)
}

// shortenUsage keeps the first two facts — how many recalls and how much
// context — and drops the derived ones.
func shortenUsage(usage string) string {
	parts := strings.Split(strings.TrimPrefix(usage, "deja · "), " · ")
	if len(parts) > 2 {
		parts = parts[:2]
	}
	return strings.Join(parts, " · ")
}
