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
}

func runRestore(dir string, args []string, stdout io.Writer) error {
	path := ""
	want := 0
	out := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--span":
			if i+1 < len(args) {
				i++
				n, err := strconv.Atoi(args[i])
				if err != nil || n <= 0 {
					return fmt.Errorf("restore: --span wants a positive number, got %q", args[i])
				}
				want = n
			}
		case "-o", "--out":
			if i+1 < len(args) {
				i++
				out = args[i]
			}
		default:
			if !strings.HasPrefix(args[i], "-") && path == "" {
				path = args[i]
			}
		}
	}
	if path == "" {
		return fmt.Errorf("usage: deja restore <path> [--span n] [-o file]")
	}

	spans, err := findRestoreSpans(dir, path)
	if err != nil {
		return err
	}
	if len(spans) == 0 {
		fmt.Fprintf(stdout, "no replaced spans recorded for %q\n", path)
		return nil
	}
	if want == 0 {
		fmt.Fprintf(stdout, "%d replaced spans recorded for %s\n", len(spans), path)
		for i, s := range spans {
			fmt.Fprintf(stdout, "  %d  %s  %s %s  %d B replaced%s\n",
				i+1, s.when.Format("Jan 02 15:04"), s.harness, shortID(s.session),
				len(s.body), redactionNote(s.body))
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
	// Never the original path: this exists because something overwrote work,
	// and writing back over it would repeat the mistake.
	if sameFile(out, path) {
		return fmt.Errorf("restore: refusing to write over %s — pick another -o", path)
	}
	if err := os.WriteFile(out, []byte(span.body), 0o600); err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	fmt.Fprintf(stdout, "wrote %d B to %s — this is what the agent recorded, not the file%s\n",
		len(span.body), out, redactionNote(span.body))
	return nil
}

func findRestoreSpans(dir string, path string) ([]restoreSpan, error) {
	base := filepath.Base(path)
	o := search.Options{Query: base, All: true, Role: "edit"}
	if err := index.EnsureForSearch(dir, o, false, nil); err != nil {
		return nil, ensureError(dir, err)
	}
	hits, err := index.Search(dir, o)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	if len(hits) > restoreMaxSessions {
		hits = hits[:restoreMaxSessions]
	}
	var spans []restoreSpan
	for _, h := range hits {
		full, ok, err := index.FindByIdentity(dir, h.Harness, h.ID)
		if err != nil || !ok {
			continue
		}
		for _, m := range full.Messages {
			if m.Role != "edit" {
				continue
			}
			recorded, body, found := strings.Cut(m.Text, "\n")
			if !found || !pathMatches(recorded, path) {
				continue
			}
			spans = append(spans, restoreSpan{when: m.Time, session: h.ID, harness: h.Harness, body: body})
		}
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
