package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/search"
)

// `deja secrets` reports; this is the line that makes it a tool. It rewrites the
// transcripts that still hold a credential, putting the marker the index already
// uses where the value was, so a re-index drops the finding on its own.
//
// What it will not do, because getting this wrong destroys someone's history:
//
//   - Touch anything the report did not name. A store with no per-session file —
//     opencode, Zed, Cursor IDE, Copilot Chat's database — cannot be rewritten at
//     all, and the count of what was left is printed rather than quietly dropped.
//     Measured on one real machine: 52 of 88 findings sat in a per-session file,
//     36 were in a database (#3823).
//   - Widen the rules. Only the kinds `deja secrets` lists by name are replaced,
//     and only the ones found in that file: `entropy`, `credential` and
//     `quoted-secret` fire on the value side of an assignment as often as on a
//     secret, and rewriting a digest in someone's history is worse than leaving
//     it. redact.TextKinds is what enforces that.
//   - Write under a live session. A transcript is an append-only log the client
//     holds open, and rewriting it mid-session is how a conversation gets
//     truncated. A session an agent is inside — or a file written seconds ago — is
//     skipped with a line saying why.
//   - Write without a copy beside it. The original goes to
//     `<file>.deja-backup-<stamp>` first and stays there; the rewrite itself is a
//     rename over the original, and a file whose size or mtime moved between the
//     read and the write is left alone.
const (
	// scrubBusyWindow is how recently a transcript can have been written to and
	// still be treated as finished. A live session restamps `.live` on every
	// prompt and action, so this only has to cover the harness that has no
	// hooks wired at all.
	scrubBusyWindow  = 2 * time.Minute
	scrubBackupExt   = ".deja-backup-"
	scrubStampLayout = "20060102-150405"
)

// scrubTarget is one transcript and what the report found in it.
type scrubTarget struct {
	path  string
	id    string
	harn  string
	kinds map[string]bool
	// found is how many markers the report counted here, for the line that says
	// what this pass reached and what it did not.
	found int
}

// scrubOutcome is what happened to one target.
type scrubOutcome struct {
	target  scrubTarget
	skipped string
	written map[string]int
	backup  string
}

func runSecretsScrub(dir string, dryRun bool, stdout io.Writer) error {
	if err := index.Ensure(dir, "", false, os.Stderr); err != nil {
		return ensureError(dir, err)
	}
	scan, err := index.ScanSecrets(dir)
	if err != nil {
		return err
	}
	targets, unreachable := scrubTargets(scan.Findings)
	if len(targets) == 0 {
		fmt.Fprintln(stdout, "deja: nothing to scrub — no finding names a transcript file deja can rewrite")
		printScrubUnreachable(stdout, unreachable)
		return nil
	}
	live := scrubLiveSessions(dir)
	var outcomes []scrubOutcome
	for _, t := range targets {
		outcomes = append(outcomes, scrubOne(t, live, dryRun))
	}
	printScrub(stdout, outcomes, unreachable, dryRun)
	return nil
}

// scrubTargets groups the findings this can act on by file, and counts the rest
// by why it cannot.
func scrubTargets(findings []index.SecretFinding) ([]scrubTarget, map[string]int) {
	unreachable := map[string]int{}
	byPath := map[string]*scrubTarget{}
	for _, f := range findings {
		if !index.SecretKindNamed(f.Kind) {
			// Counted rather than listed: the report does not name it and this
			// does not rewrite it.
			unreachable["not a named rule"]++
			continue
		}
		if f.Path == "" {
			unreachable["no transcript file — the store is a database"]++
			continue
		}
		st, err := os.Stat(f.Path)
		if err != nil || !st.Mode().IsRegular() {
			unreachable["the transcript is gone or is not a file"]++
			continue
		}
		t := byPath[f.Path]
		if t == nil {
			t = &scrubTarget{path: f.Path, id: f.ID, harn: f.Harness, kinds: map[string]bool{}}
			byPath[f.Path] = t
		}
		t.kinds[f.Kind] = true
		t.found += max(f.Count, 1)
	}
	out := make([]scrubTarget, 0, len(byPath))
	for _, t := range byPath {
		out = append(out, *t)
	}
	// Stable order, so two runs read the same and a test can name a line.
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, unreachable
}

// scrubOne rewrites one transcript, or says why it did not.
func scrubOne(t scrubTarget, live map[string]bool, dryRun bool) scrubOutcome {
	if live[t.id] {
		return scrubOutcome{target: t, skipped: "an agent is in that session now"}
	}
	st, err := os.Stat(t.path)
	if err != nil {
		return scrubOutcome{target: t, skipped: "the transcript is gone"}
	}
	if time.Since(st.ModTime()) < scrubBusyWindow {
		return scrubOutcome{target: t, skipped: "still being written"}
	}
	b, err := os.ReadFile(t.path)
	if err != nil {
		return scrubOutcome{target: t, skipped: "cannot read it: " + err.Error()}
	}
	clean, counts := redact.TextKinds(string(b), t.kinds)
	if clean == string(b) {
		// The report reads the index, which redacts decoded message text; a
		// value the raw file spells differently — escaped inside JSON, split
		// across a line — is not reachable from here, and saying so is better
		// than a rewrite that missed it.
		return scrubOutcome{target: t, skipped: "no value in the file itself matched — nothing rewritten"}
	}
	if dryRun {
		return scrubOutcome{target: t, written: counts}
	}
	backup := t.path + scrubBackupExt + time.Now().UTC().Format(scrubStampLayout)
	if err := os.WriteFile(backup, b, 0o600); err != nil {
		return scrubOutcome{target: t, skipped: "could not write the copy beside it: " + err.Error()}
	}
	// Between the read and the write the client may have appended a turn. The
	// rewrite would drop it, so the file is left alone and the copy goes.
	if now, err := os.Stat(t.path); err != nil || now.Size() != st.Size() || !now.ModTime().Equal(st.ModTime()) {
		_ = os.Remove(backup)
		return scrubOutcome{target: t, skipped: "it changed while deja was reading it"}
	}
	if err := atomicfile.Write(t.path, []byte(clean), st.Mode().Perm()); err != nil {
		return scrubOutcome{target: t, skipped: "could not replace it: " + err.Error()}
	}
	return scrubOutcome{target: t, written: counts, backup: backup}
}

func printScrub(w io.Writer, outcomes []scrubOutcome, unreachable map[string]int, dryRun bool) {
	wrote, files := 0, 0
	for _, o := range outcomes {
		name := search.SafePath(reportPath(o.target.path))
		switch {
		case o.skipped != "":
			fmt.Fprintf(w, "  skipped %s — %s\n", name, o.skipped)
		default:
			files++
			for _, kind := range sortedKindCounts(o.written) {
				wrote += o.written[kind]
			}
			verb := "would replace"
			if !dryRun {
				verb = "replaced"
			}
			fmt.Fprintf(w, "  %s %s in %s\n", verb, scrubKindSummary(o.written), name)
			if o.backup != "" {
				fmt.Fprintf(w, "    the original is %s\n", search.SafePath(reportPath(o.backup)))
			}
		}
	}
	switch {
	case dryRun:
		fmt.Fprintf(w, "\ndeja: %d value%s in %d file%s — `deja secrets --scrub` to rewrite them\n",
			wrote, plural(wrote), files, plural(files))
	case files == 0:
		fmt.Fprintln(w, "\ndeja: nothing was rewritten")
	default:
		fmt.Fprintf(w, "\ndeja: rewrote %d value%s in %d file%s — `deja index` to drop them from the index\n",
			wrote, plural(wrote), files, plural(files))
	}
	printScrubUnreachable(w, unreachable)
}

func printScrubUnreachable(w io.Writer, unreachable map[string]int) {
	if len(unreachable) == 0 {
		return
	}
	reasons := make([]string, 0, len(unreachable))
	for r := range unreachable {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		fmt.Fprintf(w, "deja: %d finding%s out of reach — %s\n", unreachable[r], plural(unreachable[r]), r)
	}
}

func scrubKindSummary(counts map[string]int) string {
	var parts []string
	for _, kind := range sortedKindCounts(counts) {
		parts = append(parts, fmt.Sprintf("%d %s", counts[kind], kind))
	}
	return strings.Join(parts, ", ")
}

func sortedKindCounts(counts map[string]int) []string {
	out := make([]string, 0, len(counts))
	for k := range counts {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// scrubLiveSessions is the sessions an agent is inside, when this build knows —
// the marks the hooks leave (#3945). Without them the mtime window below is the
// only signal, which is why it exists as well.
func scrubLiveSessions(dir string) map[string]bool { return liveSessionIDs(dir) }

// scrubBackupsIn is every copy this command left in one directory, for the tests
// and for the line that tells a reader where to look.
func scrubBackupsIn(dir string) []string {
	matches, err := filepath.Glob(filepath.Join(dir, "*"+scrubBackupExt+"*"))
	if err != nil {
		return nil
	}
	sort.Strings(matches)
	return matches
}
