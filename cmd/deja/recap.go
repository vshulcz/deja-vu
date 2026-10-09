package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/jsonout"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/termwidth"
)

// `deja recap` is the week, from the sessions rather than from memory (#544).
//
// The output is a draft with receipts, not prose: every line is a sentence
// somebody already wrote, quoted, under the session it came from. The moment
// this produces a paragraph in a model's voice it becomes the thing people are
// embarrassed to paste, so nothing here writes a sentence of its own.
//
// It also assumes the text is going somewhere public — a PR description, a
// standup message, release notes — so every line goes through the outbound
// redaction pass first, and the screen says what that masked. See
// index.ScanRecap.

// recapDefaultSince is the window `deja recap` takes with no argument: a week,
// which is the unit a standup and a self-assessment are both written in.
const recapDefaultSince = 7 * 24 * time.Hour

// recapDefaultSessions is how many sessions the screen shows, and
// recapPerSession how many lines each of them gets.
const (
	recapDefaultSessions = 12
	recapPerSession      = 3
)

type recapJSON struct {
	Kind       string               `json:"kind"`
	Schema     int                  `json:"schema_version"`
	Since      string               `json:"since"`
	Considered int                  `json:"sessions_in_window"`
	Spoke      int                  `json:"sessions_with_lines"`
	Projects   []string             `json:"projects"`
	Sessions   []index.RecapSession `json:"sessions"`
	Read       int                  `json:"sessions_read,omitempty"`
	Masked     map[string]int       `json:"masked,omitempty"`
	Withheld   int                  `json:"withheld,omitempty"`
}

const recapJSONKind = "deja.recap"

func runRecap(dir string, args []string, stdout io.Writer) error {
	since := recapDefaultSince
	limit := recapDefaultSessions
	asJSON := false
	sinceText := "7d"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			asJSON = true
		case "--since":
			if i+1 >= len(args) {
				return fmt.Errorf("recap: --since needs value")
			}
			i++
			d, err := parseDur(args[i])
			if err != nil {
				return err
			}
			since, sinceText = d, args[i]
		case "--limit":
			if i+1 >= len(args) {
				return fmt.Errorf("recap: --limit needs value")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				return fmt.Errorf("recap: --limit wants a number, got %q", args[i])
			}
			limit = n
		default:
			return fmt.Errorf("recap: unknown flag %q — it takes --since 7d, --limit n and --json", args[i])
		}
	}
	if err := index.Ensure(dir, "", false, os.Stderr); err != nil {
		return ensureError(dir, err)
	}
	r, err := index.ScanRecap(dir, since, recapPerSession)
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		out := recapJSON{
			Kind: recapJSONKind, Schema: jsonout.Version, Since: sinceText,
			Considered: r.Considered, Spoke: r.Spoke, Read: r.Read, Projects: r.Projects,
			Sessions: r.Sessions, Masked: r.Masked, Withheld: r.Withheld,
		}
		if out.Projects == nil {
			out.Projects = []string{}
		}
		if out.Sessions == nil {
			out.Sessions = []index.RecapSession{}
		}
		return enc.Encode(out)
	}
	if r.Considered == 0 {
		if line, ok := emptyStoreLine(dir, "nothing to recap"); ok {
			fmt.Fprintln(stdout, line)
			return nil
		}
	}
	printRecap(stdout, r, sinceText, limit, newestSessionNote(dir, "recap", since))
	return nil
}

// printRecap writes the recap screen. older is what newestSessionNote says
// when the window holds nothing.
func printRecap(w io.Writer, r index.Recap, since string, limit int, older string) {
	if r.Considered == 0 {
		fmt.Fprintln(w, fitLine(w, fmt.Sprintf("nothing in the last %s%s", since, older)))
		printRecapTail(w, r)
		return
	}
	if len(r.Sessions) == 0 {
		fmt.Fprintf(w, "%d session%s in the last %s, and none of them settled anything this can quote\n",
			r.Considered, pluralS(r.Considered), since)
		printRecapTail(w, r)
		return
	}
	fmt.Fprintf(w, "last %s: %d session%s, %d of them concluded something",
		since, r.Considered, pluralS(r.Considered), r.Spoke)
	if n := len(r.Projects); n > 1 {
		fmt.Fprintf(w, ", across %d projects", n)
	}
	fmt.Fprintln(w)
	if r.Read > 0 {
		fmt.Fprintf(w, "(the newest %d of them were read — ask for a shorter window for the rest)\n", r.Read)
	}
	shown := r.Sessions
	if limit > 0 && len(shown) > limit {
		shown = shown[:limit]
	}
	// Grouped by project, in the order the newest session of each appeared:
	// a multi-project week is unreadable interleaved, and the grouping is what
	// makes it a draft rather than a log. Grouped on the name as printed, so
	// the two spellings of a home directory are one heading and not two.
	var order []string
	seen := map[string]bool{}
	for _, s := range shown {
		name := recapProjectName(s.Project)
		if !seen[name] {
			seen[name] = true
			order = append(order, name)
		}
	}
	// On a terminal: project headings bold, the receipts dimmed with the
	// harness in its colour, and every line wrapped at spaces. A pipe gets the
	// draft unwrapped, since it is written to be pasted elsewhere.
	color, width := search.ColorOK(w), printableWidth(w)
	for _, name := range order {
		if color {
			fmt.Fprintf(w, "\n%s%s%s\n", statBold, name, statReset)
		} else {
			fmt.Fprintf(w, "\n%s\n", name)
		}
		for _, s := range shown {
			if recapProjectName(s.Project) != name {
				continue
			}
			for _, line := range s.Lines {
				fmt.Fprintln(w, termwidth.Indent(line, width, "  · ", "    "))
			}
			// The receipt: which session said it, so the draft can be checked
			// rather than trusted.
			if color {
				fmt.Fprintf(w, "    %s%s\n", search.HarnessTag(s.Harness, true), statDim+strings.TrimPrefix(recapSource(s), s.Harness)+statReset)
			} else {
				fmt.Fprintf(w, "    %s\n", recapSource(s))
			}
		}
	}
	if n := len(r.Sessions) - len(shown); n > 0 {
		fmt.Fprintf(w, "\n  %d more session%s — `deja recap --limit 0`, or `--json` for all of it\n",
			n, pluralS(n))
	}
	printRecapTail(w, r)
}

func recapProjectName(p string) string {
	if p == "" || p == "-" {
		return "(no project)"
	}
	// A project named after the home directory carries the account name, and
	// this text is written to be pasted somewhere else.
	if index.RecapProjectIsHome(p) {
		return "(home directory)"
	}
	return p
}

func recapSource(s index.RecapSession) string {
	out := s.Harness
	if !s.When.IsZero() {
		out += " · " + search.DisplayDate(s.When)
	}
	if s.ID != "" {
		out += " · " + search.SafeLine(search.ShortID(s.ID))
	}
	return out
}

func printRecapTail(w io.Writer, r index.Recap) {
	if n := recapMaskedTotal(r.Masked); n > 0 {
		// Said out loud, because the reader is about to paste this somewhere
		// public and the masking is the reason they can.
		line := fmt.Sprintf("masked for outbound use: %s — this text is written to be pasted, so addresses, internal hostnames, emails and home paths are removed",
			recapMaskedSummary(r.Masked))
		fmt.Fprintf(w, "\n%s\n", termwidth.Indent(line, printableWidth(w), "", ""))
	}
	if r.Withheld > 0 {
		fmt.Fprintf(w, "the ignore rule kept %d session%s out of this recap\n", r.Withheld, pluralS(r.Withheld))
	}
}

func recapMaskedTotal(c map[string]int) int {
	n := 0
	for _, v := range c {
		n += v
	}
	return n
}

// recapMaskedSummary names the classes behind the number, biggest first, so a
// reader can tell one address from eleven home paths.
func recapMaskedSummary(c map[string]int) string {
	kinds := make([]string, 0, len(c))
	for k := range c {
		kinds = append(kinds, k)
	}
	sort.Slice(kinds, func(i, j int) bool {
		if c[kinds[i]] != c[kinds[j]] {
			return c[kinds[i]] > c[kinds[j]]
		}
		return kinds[i] < kinds[j]
	})
	parts := make([]string, 0, len(kinds))
	for _, k := range kinds {
		parts = append(parts, fmt.Sprintf("%d %s", c[k], k))
	}
	return strings.Join(parts, ", ")
}
