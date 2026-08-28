package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
)

// `deja restore <path>` hands back a span an agent replaced.
//
// The premise people expect is whole-file recovery from what the agent read.
// The corpus says otherwise: reads are partial and line-decorated, while every
// Edit call carries `old_string` — the exact bytes that stopped existing — with
// the path beside it. 3,836 such spans across 862 files here, for 1.09 MB.
//
// Two rules the output never breaks. It is what the agent recorded, not the
// file, and it says so. And it never writes to the path it recovered: restoring
// over live work is the same class of mistake this is meant to undo.
const restoreMaxSessions = 400

type restoreSpan struct {
	when    time.Time
	session string
	harness string
	body    string
	// file is the path the edit recorded, which is what the span was taken
	// from — the -o guard compares against this rather than against whatever
	// spelling the caller typed (#725).
	file string
}

func runRestore(dir string, args []string, stdout io.Writer) error {
	path := ""
	want := 0
	out := ""
	force := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--force":
			force = true
		case "--span":
			// A missing value used to be ignored, which for -o meant the file
			// went to stdout while the reader believed it had been written
			// (#2253).
			if i+1 >= len(args) {
				return fmt.Errorf("restore: --span needs value")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				return fmt.Errorf("restore: --span wants a positive number, got %q", args[i])
			}
			want = n
		case "-o", "--out":
			if i+1 >= len(args) {
				return fmt.Errorf("restore: -o needs value")
			}
			i++
			out = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("restore: unknown flag %q", args[i])
			}
			if path == "" {
				path = args[i]
			}
		}
	}
	if path == "" {
		return fmt.Errorf("usage: deja restore <path> [--span n] [-o file] [--force]")
	}

	spans, err := findRestoreSpans(dir, path)
	if err != nil {
		return err
	}
	if len(spans) == 0 {
		// Same as #834 elsewhere: nothing recorded because nothing is
		// indexed is a different answer from nothing recorded for this path.
		if n, err := index.SessionCount(dir); err == nil && n == 0 {
			fmt.Fprintln(stdout, emptyIndexHint(fmt.Sprintf("no replaced spans recorded for %q", path)))
			return nil
		}
		fmt.Fprintf(stdout, "no replaced spans recorded for %q\n", path)
		return nil
	}
	if want == 0 && out != "" && len(spans) == 1 {
		// `-o` says where the bytes go, and with one span there is nothing to
		// choose: asking for `--span 1` as well is asking someone to name the
		// only door in the room. Without this the listing printed and the file
		// was never written, which is the state #2253 closed one step over.
		want = 1
	}
	if want == 0 {
		fmt.Fprintf(stdout, "%d replaced spans recorded for %s\n", len(spans), path)
		// The row carries a date, a harness, a session id and a size, and on a
		// narrow pane it wrapped between them (#604). The id is what the reader
		// copies into the next command, so the harness gives way first.
		width := printableWidth(stdout)
		for i, s := range spans {
			harness := s.harness
			if width > 0 {
				fixed := len(fmt.Sprintf("  %d  %s   %s  %d B replaced%s", i+1, s.when.Format("Jan 02 15:04"), shortID(s.session), len(s.body), redactionNote(s.body)))
				// Runes, not bytes: a harness name is ASCII today and a
				// byte cut would produce invalid UTF-8 the day one is not.
				if r := []rune(harness); width-fixed < len(r) && width-fixed >= 3 {
					harness = string(r[:width-fixed-1]) + "…"
				}
			}
			fmt.Fprintf(stdout, "  %d  %s  %s %s  %d B replaced%s\n",
				i+1, s.when.Format("Jan 02 15:04"), harness, shortID(s.session),
				len(s.body), redactionNote(s.body))
		}
		if out != "" {
			// The flag was given and could not be honoured. Silence here read
			// as a file written (#2417).
			fmt.Fprintf(stdout, "\nnothing was written to %s — name the span you want:\ndeja restore %s --span 1 -o %s\n",
				out, path, out)
			return nil
		}
		fmt.Fprintf(stdout, "\ndeja restore %s --span 1 -o recovered.txt\n", path)
		return nil
	}
	if want > len(spans) {
		return fmt.Errorf("restore: span %d of %d", want, len(spans))
	}
	span := spans[want-1]
	if out == "" {
		fmt.Fprint(stdout, span.body)
		if !strings.HasSuffix(span.body, "\n") {
			fmt.Fprintln(stdout)
		}
		return nil
	}
	// Never the original: this exists because something overwrote work, and
	// writing back over it would repeat the mistake. Comparing against the
	// argument alone was not enough — `deja restore pool.go -o repo/pool.go`
	// named the same file two ways and went through (#725).
	if sameFile(out, path) || (span.file != "" && sameFile(out, span.file)) {
		return fmt.Errorf("restore: refusing to write over %s — that is the file this span came from; pick another -o", out)
	}
	// Any other existing file is someone's work too, and a recovery command is
	// reached for in exactly the moments when a typo costs the most.
	if _, err := os.Stat(out); err == nil && !force {
		return fmt.Errorf("restore: %s already exists — pick another -o, or pass --force to overwrite it", out)
	}
	if err := os.WriteFile(out, []byte(span.body), 0o600); err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	fmt.Fprintf(stdout, "wrote %d B to %s — this is what the agent recorded, not the file%s\n",
		len(span.body), out, redactionNote(span.body))
	return nil
}

// findRestoreSpans scans rather than searches.
//
// Ranked retrieval was the wrong instrument here: it samples postings per
// session before ranking, so in a session with hundreds of edit records the
// one being looked for never reached the candidate set and the session did not
// surface at all. Measured on a 1153-session store: 41 of 1,621 recorded paths
// were unreachable that way, 76 spans in total, and both traced cases sat in
// the two largest sessions — which is the worst place to lose them, since a
// session where an agent replaced a lot of code is exactly the large one
// (#647).
//
// Everywhere else in deja ranking is right, because the reader wants the most
// relevant session. Restore is the opposite: the file name is an exact key,
// the person knows what they lost, and a near-miss is worth nothing. The scan
// costs ~180 ms on an 82 MB log, which for a command run once, in a panic, is
// not a cost.
func findRestoreSpans(dir string, path string) ([]restoreSpan, error) {
	o := search.Options{Query: filepath.Base(path), All: true, Role: "edit"}
	if err := index.EnsureForSearch(dir, o, false, os.Stderr); err != nil {
		return nil, ensureError(dir, err)
	}
	var spans []restoreSpan
	seen := map[string]bool{}
	// restore hands back the exact bytes a session recorded, so a trust rule
	// that withholds a peer's content has to hold here too — otherwise
	// `restore` was the one direct-access command that read an imported edit
	// span out loud while show, share, handoff and ctx all refused (#1026).
	pol := policy.Load()
	err := index.EachRecordOfRole(dir, index.RoleEdit, func(meta index.SessionMeta, r index.Record) {
		if !pol.Allows(policy.ActivationSearch, meta.Project) {
			return
		}
		if len(seen) >= restoreMaxSessions && !seen[meta.Harness+":"+meta.ID] {
			return
		}
		recorded, body, found := strings.Cut(r.Text, "\n")
		if !found || !pathMatches(recorded, path) {
			return
		}
		seen[meta.Harness+":"+meta.ID] = true
		spans = append(spans, restoreSpan{when: r.Time, session: meta.ID, harness: meta.Harness, body: body, file: recorded})
	})
	if err != nil {
		return nil, fmt.Errorf("restore: %w", err)
	}
	// Newest first: the span someone wants back is almost always the last one
	// that was replaced.
	sort.Slice(spans, func(i, j int) bool { return spans[i].when.After(spans[j].when) })
	return spans, nil
}

// pathMatches accepts what someone would actually type — a bare file name, a
// suffix of the path, or the whole thing.
func pathMatches(recorded, want string) bool {
	recorded = filepath.ToSlash(recorded)
	want = filepath.ToSlash(want)
	if recorded == want {
		return true
	}
	if strings.HasSuffix(recorded, "/"+strings.TrimPrefix(want, "/")) {
		return true
	}
	return filepath.Base(recorded) == want
}

// redactionNote flags a span that passed through redaction, because restoring
// it byte for byte would put a placeholder where a credential was.
func redactionNote(body string) string {
	if strings.Contains(body, "[redacted:") {
		return "  (contains a redaction placeholder — not byte-exact)"
	}
	return ""
}

func sameFile(a, b string) bool {
	ap, err1 := filepath.Abs(a)
	bp, err2 := filepath.Abs(b)
	return err1 == nil && err2 == nil && ap == bp
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
