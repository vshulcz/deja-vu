package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

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
type transcriptSource struct {
	TranscriptPath string `json:"transcript_path"`
}

// statuslineMaxTitle keeps the memory segment from pushing the usage numbers
// off a narrow terminal (#604 is the same concern, one screen over).
const (
	statuslineMaxTitle = 38
	// statuslineMinTitle is the shortest title fragment worth printing. Below
	// it the count form says more than three words and an ellipsis.
	statuslineMinTitle = 12
	// statuslineMaxName bounds the filename for the same reason: a 200-char
	// path put a 226-rune line on a bar that has no horizontal scroll.
	statuslineMaxName = 28
)

// readStatuslineInput consumes the payload without ever blocking. An
// interactive terminal has nothing piped, and a host that pipes something
// deja does not understand must cost nothing to ignore.
//
// "Without blocking" used to mean ReadAll on a pipe, which is only true while
// the host closes it: one that opened stdin and held it hung the status bar
// for as long as the host allowed, on every assistant message (#1074). Same
// bound the hooks have had since #846.
func readStatuslineInput(r io.Reader) transcriptSource {
	var in transcriptSource
	if !pipedStdin(r) {
		return in
	}
	b := readBounded(r, hookStdinWait, false)
	if len(b) == 0 {
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
func statuslineMemory(dir string, in transcriptSource) (fileMemory, bool) {
	if in.TranscriptPath == "" || strings.TrimSpace(in.TranscriptPath) == "" {
		return fileMemory{}, false
	}
	id := strings.TrimSuffix(filepath.Base(in.TranscriptPath), ".jsonl")
	if id == "" || id == "." || id == string(filepath.Separator) {
		return fileMemory{}, false
	}
	metas, err := index.AllMeta(dir)
	if err != nil {
		return fileMemory{}, false
	}
	// The manifest is keyed by harness:id, so an id alone can name two
	// sessions. The payload gives the transcript's own path and SessionMeta
	// carries it, so match on that first and fall back to the id only when no
	// path matches — otherwise which session is "current" depends on Go's
	// randomised map order and can differ between two refreshes 300ms apart.
	var self index.SessionMeta
	var found bool
	for _, m := range metas {
		if m.Path == in.TranscriptPath {
			self, found = m, true
			break
		}
	}
	if !found {
		for _, m := range metas {
			if m.ID == id {
				self, found = m, true
				break
			}
		}
	}
	if !found || len(self.Touched) == 0 {
		return fileMemory{}, false
	}
	// Every file this session worked on, busiest first — not just the busiest
	// one. The file a session spends most of itself in is often the one being
	// written from scratch, which by definition no earlier session touched;
	// stopping there was silent for 41 of 676 sessions on a real store that
	// had memory to report one entry down.
	for _, here := range self.Touched {
		others := sessionsTouching(metas, here, self)
		if len(others) == 0 {
			continue
		}
		// Newest first, then by identity. Timestamp alone is not an order:
		// AllMeta comes out of a map, so two sessions stamped the same second
		// — a shared shutdown, an import, anything below second granularity —
		// swapped places between two refreshes 300ms apart and the bar named a
		// different session each time (#668, measured 40/20 over 60 runs).
		sort.Slice(others, func(i, j int) bool { return newestFirst(others[i], others[j]) })
		return fileMemory{
			Path:     here,
			Sessions: len(others),
			Title:    others[0].Title,
			Last:     others[0].Updated,
		}, true
	}
	// Silence, not a zero: a file nobody else has worked on has no memory to
	// report, and printing "0 sessions" makes the line noise forever.
	return fileMemory{}, false
}

// newestFirst orders the earlier sessions the bar can name.
//
// Timestamp alone is not an order: AllMeta comes out of a map, so two sessions
// stamped the same second — a shared shutdown, an import, anything below second
// granularity — swapped places between two refreshes 300ms apart and the bar
// named a different session each time (#668, measured 40/20 over 60 runs).
// Identity breaks the tie because it is the one thing that does not change.
func newestFirst(a, b index.SessionMeta) bool {
	if !a.Updated.Equal(b.Updated) {
		return a.Updated.After(b.Updated)
	}
	if a.Harness != b.Harness {
		return a.Harness < b.Harness
	}
	return a.ID < b.ID
}

// sessionsTouching returns the sessions other than self that worked on path.
func sessionsTouching(metas []index.SessionMeta, path string, self index.SessionMeta) []index.SessionMeta {
	var out []index.SessionMeta
	for _, m := range metas {
		// Identity is the pair, not the id: two harnesses can carry the same
		// session id, and excluding both would drop a genuinely other session.
		if m.ID == self.ID && m.Harness == self.Harness {
			continue
		}
		for _, p := range m.Touched {
			if p == path {
				out = append(out, m)
				break
			}
		}
	}
	return out
}

// statuslineMemoryLine renders it. A count is a statistic; what the earlier
// session was about is a memory, so the title wins when there is one.
func statuslineMemoryLine(m fileMemory) string {
	return statuslineMemoryLineTo(m, statuslineMaxTitle)
}

// statuslineMemoryLineTo bounds the title to maxTitle runes. maxTitle <= 0
// drops the title and falls through to the count form, which is what a bar too
// narrow to hold any of it gets.
func statuslineMemoryLineTo(m fileMemory, maxTitle int) string {
	// The path comes from a tool call and is recorded verbatim, exactly like
	// the title: a filename can carry a carriage return or an escape just as
	// easily, and it reaches the same bar.
	name := safeForStatusline(filepath.Base(m.Path), statuslineMaxName)
	when := ""
	if !m.Last.IsZero() {
		// A meta with no timestamp would otherwise put "Jan 1 0001" on the bar.
		when = " " + search.RelativeDate(m.Last)
	}
	if title := safeForStatusline(m.Title, maxTitle); maxTitle > 0 && title != "" {
		return fmt.Sprintf("%s · %d earlier: \u201c%s\u201d%s", name, m.Sessions, title, when)
	}
	noun := "sessions"
	if m.Sessions == 1 {
		noun = "session"
	}
	if when != "" {
		when = " ·" + when
	}
	return fmt.Sprintf("%s · %d earlier %s%s", name, m.Sessions, noun, when)
}

// trimStatuslineTitle makes a session title safe for a status bar. The title
// is whatever the user typed first, so it can carry a carriage return that
// rewrites the line, an ANSI escape that recolours the whole bar, or a bell.
func trimStatuslineTitle(t string) string { return safeForStatusline(t, statuslineMaxTitle) }

// safeForStatusline strips what a terminal would act on rather than print, and
// bounds the length in runes.
//
// Control characters (Cc) cover the carriage return and the escape byte.
// Format characters (Cf) are the quieter half of the same problem: U+202E
// reverses the rendering of everything after it, and a zero-width space pads
// the bar invisibly. Both become spaces rather than vanishing, so words on
// either side do not run together.
func safeForStatusline(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > max {
		return strings.TrimSpace(string(r[:max])) + "…"
	}
	return s
}

// withFileMemory puts the memory ahead of the usage numbers when there is
// one. The numbers are about deja; the memory is about the reader's own work,
// and on a line that gets truncated the useful half should survive.
//
// The usage half is shortened to make room rather than appended whole: a
// status line lives in whatever width the terminal has, and a line that runs
// off the edge loses its tail — which would be the numbers if they came
// second, but the memory if the line simply grew.
func withFileMemory(dir string, in transcriptSource, usage string) string {
	m, ok := statuslineMemory(dir, in)
	if !ok {
		return usage
	}
	width := statuslineWidth()
	// The per-component caps never added up to a line anyone measured: 28 for
	// the name and 38 for the title made a memory segment of up to 95 columns
	// on its own, so the half this function deliberately puts first was itself
	// cut mid-word by the terminal — losing the closing quote and the date on
	// top of the numbers (#1076).
	//
	// The memory segment is fitted first, because the comment above is the
	// rule: it is the half that has to survive. The title is its elastic part,
	// with a floor so a long filename cannot cut it to a stub.
	mem := "deja · " + statuslineMemoryLineTo(m, statuslineMaxTitle)
	if over := visibleLen(mem) - width; over > 0 {
		budget := statuslineMaxTitle - over
		if budget < statuslineMinTitle {
			budget = statuslineMinTitle
		}
		mem = "deja · " + statuslineMemoryLineTo(m, budget)
	}
	// Then as much of the usage as still fits. Appending it whole is what put
	// it past the edge; dropping it is the trade this function already
	// documents, and half of it beats a line that runs off.
	for _, tail := range []string{shortenUsage(usage), firstUsageFact(usage)} {
		if tail != "" && visibleLen(mem)+3+visibleLen(tail) <= width {
			return mem + " · " + tail
		}
	}
	return mem
}

// firstUsageFact is the usage segment cut to its leading fact — the recall
// count — for a bar with room for that and no more.
func firstUsageFact(usage string) string {
	parts := strings.Split(strings.TrimPrefix(usage, "deja · "), " · ")
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

// statuslineWidth is the bar deja lays out for. Same reasoning as briefWidth:
// no dependency may be added to read the real size, 80 is wrong least often,
// and COLUMNS is honoured for the readers who export it.
func statuslineWidth() int { return briefWidth() }

// shortenUsage keeps the first two facts — how many recalls and how much
// context — and drops the derived ones.
func shortenUsage(usage string) string {
	parts := strings.Split(strings.TrimPrefix(usage, "deja · "), " · ")
	if len(parts) > 2 {
		parts = parts[:2]
	}
	return strings.Join(parts, " · ")
}

// safeLine is safeForStatusline without the bound, for the listings that print
// harness text on a line of their own (#1090).
func safeLine(s string) string {
	return safeForStatusline(s, math.MaxInt32)
}
