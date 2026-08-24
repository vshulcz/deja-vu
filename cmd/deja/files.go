package main

import (
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/termwidth"
)

// `deja files <topic>` answers which files a piece of work actually touched.
//
// The obvious implementation — aggregate every file a matching session touched
// — does not work, and the failure is instructive: one long session touches two
// hundred files across a dozen subjects, so every topic returned the same five
// files. Measured that way, five queries produced one correct answer.
//
// What works is proximity in time. A file counts when it was opened or edited
// near the place the topic was discussed. On five hand-checked topics that
// gives two exactly right ("sing-box" returns the singbox service and renderer,
// "marzban" the bot's own files), one plausible, one weak, and one honest
// refusal — a topic said in a single session with no file beside it prints that
// rather than guessing.
const (
	// filesWindow is how long either side of a topic mention still counts as
	// the same piece of work. Time, not message count: two thirds of a
	// session's records are tool output, so twenty messages can be a few
	// seconds of one command.
	filesWindow      = 20 * time.Minute
	filesMaxSessions = 250
)

func runFiles(dir string, args []string, stdout io.Writer) error {
	var terms []string
	limit := 10
	project := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--limit":
			// A flag typed with nothing after it used to be dropped in silence,
			// and so did an unknown one and an empty --project: the answer came
			// back looking like an answer to what was asked (#1628).
			if i+1 >= len(args) {
				return fmt.Errorf("files: --limit needs value")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				return fmt.Errorf("files: --limit wants a positive number, got %q", args[i])
			}
			limit = n
		case "--project":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return fmt.Errorf("files: --project needs value")
			}
			i++
			project = args[i]
		case "--":
			// The escape `parseSearch` already offers: everything after it is
			// the topic, dashes and all. Without it, refusing unknown flags
			// left `--feature-flag` with no way to be asked about.
			terms = append(terms, args[i+1:]...)
			i = len(args)
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("files: unknown flag %q", args[i])
			}
			if strings.TrimSpace(args[i]) != "" {
				terms = append(terms, args[i])
			}
		}
	}
	if len(terms) == 0 {
		return fmt.Errorf("usage: deja files <topic> [--project name] [--limit n]")
	}
	q := strings.Join(terms, " ")
	o := search.Options{Query: q, All: true, Project: project}
	if err := index.EnsureForSearch(dir, o, false, os.Stderr); err != nil {
		return ensureError(dir, err)
	}
	hits, err := index.Search(dir, o)
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	// index.Search does not gate; the cmd layer does. Without this, `files`
	// listed a peer's file paths under a rule that withholds imported content —
	// browsing is the search activation, as it is for search, last and blame
	// (#1026).
	hits, hidden := policyFilterSessionsCounted(policy.ActivationSearch, hits)
	if len(hits) == 0 {
		// The topic did match — a rule withheld it. Saying "no sessions
		// mention it" reads as looked-and-absent, the same misread search and
		// last already avoid by naming the rule (#686, #680).
		if note := policyHiddenNote(policy.ActivationSearch, hidden); note != "" {
			fmt.Fprint(stdout, note)
			return nil
		}
		// A filter the caller set is not the topic's fault: three sessions can
		// mention it and still be absent because they are in another project
		// (#727, the same shape as #715 in search).
		if project != "" {
			fmt.Fprintf(stdout, "no sessions mention %q in project %q\n", q, project)
			return nil
		}
		// An empty store is not a miss on the topic: deja has nothing to look
		// in, and saying "no sessions mention it" reads as "looked, not there"
		// (#834).
		if n, err := index.SessionCount(dir); err == nil && n == 0 {
			fmt.Fprintln(stdout, emptyIndexHint(fmt.Sprintf("no sessions mention %q", q)))
			return nil
		}
		fmt.Fprintf(stdout, "no sessions mention %q\n", q)
		return nil
	}
	// Retrieval hands candidates back in map order, so taking the first 250 of
	// 2119 matches counted a different quarter of them on every run and the top
	// file changed each time (#1072). Newest first, the rule blame already
	// uses, with identity to break ties.
	sort.Slice(hits, func(i, j int) bool {
		if !hits[i].Updated.Equal(hits[j].Updated) {
			return hits[i].Updated.After(hits[j].Updated)
		}
		if hits[i].Harness != hits[j].Harness {
			return hits[i].Harness < hits[j].Harness
		}
		return hits[i].ID < hits[j].ID
	})
	// The cap is a read budget, not a fact about the store. Every count this
	// command prints is taken over the sessions it got to, so on a store where
	// 301 sessions said the word the answer read "250 sessions mention
	// "beacon"" — a number the reader has no way to tell from the real one.
	matched := len(hits)
	if len(hits) > filesMaxSessions {
		hits = hits[:filesMaxSessions]
	}

	// The words the person typed, not the expanded variant list: that one runs
	// to eighteen stemmed forms for a two-word query, and requiring most of them
	// in one message matches nothing.
	needles := lowerAll(terms)
	filtered := 0                    // recorded paths dropped by the repository filter
	near := map[string]int{}         // path -> times touched near the topic
	nearSessions := map[string]int{} // path -> how many sessions touched it near the topic
	total := map[string]int{}        // path -> times touched anywhere in these sessions
	sessions := map[string]int{}     // path -> how many sessions touched it
	scanned := 0

	// One pass for all 250, not one pass each: the per-identity lookup streams
	// the whole record log every time it is called (#1069).
	ids := make([]index.Identity, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, index.Identity{Harness: h.Harness, ID: h.ID})
	}
	full250, err := index.FindManyByIdentity(dir, ids)
	if err != nil {
		return fmt.Errorf("read sessions: %w", err)
	}

	for _, full := range full250 {
		// The window is counted over speech and file events only. Tool output is
		// two thirds of the records in a session, so a raw message count would be
		// a few seconds of one command — far too narrow to connect a subject to
		// the files it was about.
		msgs := meaningful(full.Messages)
		hit := topicTimes(msgs, needles)
		if len(hit) == 0 {
			// The search tier that produced this hit can be semantic, so a session
			// can arrive without the words ever being said in it. Counting its
			// files would put a one-off touch from an unrelated project at the top
			// on perfect specificity.
			continue
		}
		scanned++
		seenHere := map[string]bool{}
		nearHere := map[string]bool{}
		for _, m := range msgs {
			if m.Role != "files" {
				continue
			}
			for _, p := range strings.Split(m.Text, "\n") {
				p = strings.TrimSpace(p)
				if p == "" || isScratch(p) {
					continue
				}
				if !inRepository(p) {
					// Recorded, but not under a repository on this disk today.
					// Usually a probe script; sometimes a repo that was
					// archived, renamed, or lives on a volume that is not
					// mounted. Counted rather than forgotten, so the empty
					// answer can say which of the two happened (#664).
					filtered++
					continue
				}
				total[p]++
				if !seenHere[p] {
					seenHere[p] = true
					sessions[p]++
				}
				if withinTime(m.Time, hit, filesWindow) {
					near[p]++
					if !nearHere[p] {
						nearHere[p] = true
						nearSessions[p]++
					}
				}
			}
		}
	}
	if len(near) == 0 {
		// "Recorded nothing" and "recorded files this build will not show" are
		// different answers, and only the first is a fact about the past.
		if filtered > 0 {
			fmt.Fprintf(stdout, "%d session%s mention%s %q%s; %d recorded file%s %s not under a repository on this disk — moved, archived, or an unmounted volume\n",
				scanned, plural(scanned), verbS(scanned), q, filesReadNote(matched), filtered, plural(filtered), verbIs(filtered))
			return nil
		}
		fmt.Fprintf(stdout, "%d session%s mention%s %q%s, none of them recorded a file near it\n", scanned, plural(scanned), verbS(scanned), q, filesReadNote(matched))
		return nil
	}

	type row struct {
		path  string
		score float64
		n     int
	}
	var rows []row
	for p, n := range near {
		// A file touched near the topic and nowhere else is specific to it. One
		// touched in every session — main.go, a test helper — is not, however
		// often it appears next to the words.
		// Sessions, not occurrences. A file opened forty times inside one long
		// session is one piece of evidence; a file opened once in four separate
		// sessions about the subject is four. Counting occurrences let the
		// noisiest session decide the answer — on this corpus, whichever session
		// happened to be *measuring* the topic.
		specificity := float64(n) / float64(total[p])
		// Touches as well as sessions: a file opened once beside the subject has
		// perfect specificity and no evidence behind it, which is how an unrelated
		// repo's dependabot.yml reached the top of the first run.
		rows = append(rows, row{p, float64(nearSessions[p]) * specificity * math.Log(1+float64(n)) * math.Log(1+float64(scanned)/float64(sessions[p])), n})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].score == rows[j].score {
			return rows[i].path < rows[j].path
		}
		return rows[i].score > rows[j].score
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	fmt.Fprintf(stdout, "files touched while working on %q — %d session%s%s\n", q, scanned, plural(scanned), filesReadNote(matched))
	// The path column was a fixed 56, which on a 60-column pane leaves nothing
	// for the count and wraps every row (#604). Budgeted against the window
	// instead, with the same 56 when the window is wide enough or unknown.
	col := 56
	if w := printableWidth(stdout); w > 0 && w-6 < col {
		col = w - 6
		if col < 16 {
			col = 16
		}
	}
	for _, r := range rows {
		// Padded here rather than with %-*s, whose width is runes: a path under
		// a Chinese directory is one rune and two columns per character, so the
		// counts stopped lining up in a column of their own.
		path := filesRowPath(r.path, col)
		fmt.Fprintf(stdout, "  %s%s %d\n", path, strings.Repeat(" ", max(0, col-termwidth.Columns(path))), r.n)
	}
	return nil
}

// filesReadNote names the read budget when it bit, so the session counts above
// it read as "what deja got to" rather than as the size of the match.
func filesReadNote(matched int) string {
	if matched <= filesMaxSessions {
		return ""
	}
	return fmt.Sprintf(" (of %d matching sessions, the %d most recent were read)", matched, filesMaxSessions)
}

// meaningful drops tool output, which carries no subject of its own and would
// otherwise dominate the positions the window is measured in.
func meaningful(ms []model.Message) []model.Message {
	out := make([]model.Message, 0, len(ms))
	for _, m := range ms {
		if m.Role == "tool-output" || m.Role == "command" {
			continue
		}
		out = append(out, m)
	}
	return out
}

// topicTimes returns when the topic was discussed.
func topicTimes(ms []model.Message, needles []string) []time.Time {
	if len(needles) == 0 {
		return nil
	}
	// Half the words, at least one. Requiring every word in a single message is
	// the exact-tier rule, and it is wrong here: a turn that says "the block
	// layout" while the query says "block compression" is still that work.
	want := (len(needles) + 1) / 2
	var out []time.Time
	for _, m := range ms {
		if m.Role == "files" || m.Role == "command" || m.Time.IsZero() {
			continue
		}
		low := strings.ToLower(m.Text)
		n := 0
		for _, t := range needles {
			if strings.Contains(low, t) {
				n++
			}
		}
		if n >= want {
			out = append(out, m.Time)
		}
	}
	return out
}

func withinTime(t time.Time, at []time.Time, w time.Duration) bool {
	if t.IsZero() {
		return false
	}
	for _, p := range at {
		if d := t.Sub(p); d < w && d > -w {
			return true
		}
	}
	return false
}

func lowerAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if len(s) > 2 {
			out = append(out, strings.ToLower(s))
		}
	}
	return out
}

// isScratch drops the agent's own working files — a scratchpad note, a task
// output, a probe script written to measure something. They are touched
// constantly while a subject is being worked on, so they outrank the code on
// proximity alone, and they are not what anyone means by "which files matter".
// Same class as the harness plumbing filtered in #551. The markers are
// agent-specific on purpose: excluding all of /tmp also excludes a repository
// someone happens to have checked out there.
func isScratch(p string) bool {
	for _, seg := range []string{
		"/scratchpad/", "/tasks/", "/.claude/", "/.cache/", "/claude-501/",
		"/node_modules/", "/.git/", "/testdata/fixtures/",
	} {
		if strings.Contains(p, seg) {
			return true
		}
	}
	return strings.HasSuffix(p, ".output") || strings.HasSuffix(p, ".log")
}

// inRepository keeps files that belong to a project. A probe script in a
// working directory, a note beside the repo, a downloaded sample — all get
// touched while a subject is being worked on and none of them is what anyone
// means by "which files matter".
func inRepository(p string) bool {
	dir := filepath.Dir(p)
	if v, ok := repoCheck.Load(dir); ok {
		return v.(bool)
	}
	found := inRepositoryUncached(p)
	repoCheck.Store(dir, found)
	return found
}

func inRepositoryUncached(p string) bool {
	// Stop when the parent stops changing rather than on "/": on Windows the
	// walk ends at `D:\`, whose parent is itself, and comparing against the
	// unix root spins there forever.
	for d := filepath.Dir(p); ; {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return true
		}
		up := filepath.Dir(d)
		if up == d || up == "." {
			return false
		}
		d = up
	}
}

var repoCheck sync.Map

// filesRowPath is what one row shows: the head removed, what the terminal acts
// on removed, and the rest bounded to the column.
//
// SafeLine comes before the bound, not after. A file name can hold an escape
// or a carriage return — recorded from the tool call verbatim, and #1090
// stripped them from the other reading surfaces while this row was missed —
// and those bytes print as nothing, so measuring the path with them still in
// it spends the budget the file name needs.
func filesRowPath(p string, col int) string {
	return trimPathTo(search.SafeLine(trimPath(p)), col)
}

// trimPathTo bounds a path to a column width, cutting from the left so the
// file name survives: the tail is what identifies it, and a head-first cut
// leaves rows that all read "…/src/".
func trimPathTo(p string, width int) string {
	if width <= 0 || termwidth.Columns(p) <= width {
		return p
	}
	if width < 4 {
		return tailToColumns(p, width)
	}
	return "…" + tailToColumns(p, width-1)
}

// tailToColumns is the last width columns of s, cut on a character boundary.
// A wide character that would only half fit is dropped rather than split.
func tailToColumns(s string, width int) string {
	r := []rune(s)
	n := 0
	for i := len(r) - 1; i >= 0; i-- {
		w := termwidth.RuneColumns(r[i])
		if n+w > width {
			return string(r[i+1:])
		}
		n += w
	}
	return s
}

// trimPath keeps the tail that identifies a file without the home directory in
// front of it.
//
// The cut is marked: two files under /tmp/b7/repo printed as "tmp/b7/repo/x.go"
// and "b7/repo/internal/y.go", which read as relative paths starting in
// different places rather than as one tree with its head removed (#727).
func trimPath(p string) string {
	// Both separators, for the reason CrossBase gives: a store synced from
	// Windows holds paths like C:\src\app\x.go, and on a Unix host those
	// split into one segment, so the head is never removed and a long path
	// runs past the column it is printed in while its Unix twin is trimmed.
	parts := strings.FieldsFunc(filepath.Clean(p), func(r rune) bool {
		return r == '/' || r == '\\'
	})
	// FieldsFunc drops the empty leading field an absolute path produces, which
	// is what the old explicit trim did: losing the root is not a truncation and
	// must not draw an ellipsis.
	if len(parts) > 4 {
		return "…/" + strings.Join(parts[len(parts)-4:], "/")
	}
	return strings.Join(parts, "/")
}

// verbIs keeps "1 file is" from reading as "1 file are".
func verbIs(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}
