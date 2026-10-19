package main

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
)

// writeQuillmarshSessions writes n Claude transcripts, the newest first, each
// mentioning quillmarsh.
func writeQuillmarshSessions(t *testing.T, from, n int, newest time.Time) {
	t.Helper()
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := from; i < from+n; i++ {
		at := newest.Add(-time.Duration(i) * time.Hour).UTC().Format(time.RFC3339)
		line := fmt.Sprintf(`{"type":"user","message":{"role":"user","content":"quillmarsh rollout step %d, ticket zq%04d"},"timestamp":%q,"sessionId":"q%04d","cwd":"/proj"}`+"\n", i, i, at, i)
		if err := os.WriteFile(filepath.Join(store, fmt.Sprintf("q%04d.jsonl", i)), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// snapshotAt copies the index as it stands when the build says its newest
// slice is searchable — the index a SIGINT right after that line leaves.
type snapshotAt struct {
	t        *testing.T
	from, to string
	took     bool
}

func (s *snapshotAt) Write(p []byte) (int, error) {
	if !s.took && bytes.Contains(p, []byte("searchable now")) {
		s.took = true
		copyIndexTree(s.t, s.from, s.to)
	}
	return len(p), nil
}

func copyIndexTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		dst := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o700)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// After a big first build is cut off past its early slice, the index holds the
// newest sessions and is missing the oldest. The note said "the very newest
// sessions may not appear yet", the opposite of what was missing, and gave no
// count (#5390).
func TestAnInterruptedFirstBuildSaysHowMuchItCovers(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_WARMUP_SENTINEL", "1")
	writeQuillmarshSessions(t, 0, 450, time.Now().Add(-time.Hour))
	dir := filepath.Join(tmp, "index.db")
	cut := filepath.Join(tmp, "cut.db")
	shot := &snapshotAt{t: t, from: dir, to: cut}
	if err := index.Ensure(dir, "", false, shot); err != nil {
		t.Fatal(err)
	}
	if !shot.took {
		t.Fatal("the build published no early slice, so this proves nothing")
	}
	// The build that picks it up again holds the lock while it runs.
	unlock := holdIndexLock(t, cut)
	defer unlock()

	text, _, _, _, err := recallTextResult(cut, "quillmarsh rollout", "", 0, 0, recallMCPBudget)
	if err != nil {
		t.Fatal(err)
	}
	want := "covers only the 200 most recent of 450 sessions found; older ones are missing"
	if !strings.Contains(text, want) {
		t.Errorf("recall does not say what the cut-off index covers, want %q in:\n%s", want, text)
	}
	if strings.Contains(text, "very newest") {
		t.Errorf("recall says the newest are missing when it is the oldest:\n%s", text)
	}

	// A question only an old session answers misses, and the miss says why.
	miss, _, _, _, err := recallTextResult(cut, "zq0449", "", 0, 0, recallMCPBudget)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(miss, want) {
		t.Errorf("a miss on the cut-off index does not say the oldest are missing, want %q in:\n%s", want, miss)
	}

	var cli bytes.Buffer
	if err := ensureForCLISearch(cut, search.Options{Query: "quillmarsh", All: true}, false, &cli); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cli.String(), want) {
		t.Errorf("the CLI does not say what the cut-off index covers, want %q in:\n%s", want, cli.String())
	}
}

// During an ordinary refresh the note names the index's date and how many
// sessions it holds, rather than "the index as it was" (#5390).
func TestARefreshSaysTheIndexAgeAndSize(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_WARMUP_SENTINEL", "1")
	writeQuillmarshSessions(t, 0, 30, time.Now().Add(-time.Hour))
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	writeQuillmarshSessions(t, 30, 10, time.Now().Add(-time.Hour))
	unlock := holdIndexLock(t, dir)
	defer unlock()

	at := index.LastUpdated(dir).Local().Format("2006-01-02 15:04")
	want := "answering from the index of " + at
	text, _, _, _, err := recallTextResult(dir, "quillmarsh rollout", "", 0, 0, recallMCPBudget)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, want) || !strings.Contains(text, "30 sessions") {
		t.Errorf("recall during a refresh gives no date or size, want %q and 30 sessions in:\n%s", want, text)
	}
	var cli bytes.Buffer
	if err := ensureForCLISearch(dir, search.Options{Query: "quillmarsh", All: true}, false, &cli); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cli.String(), want) || !strings.Contains(cli.String(), "30 sessions") {
		t.Errorf("the CLI during a refresh gives no date or size, want %q and 30 sessions in:\n%s", want, cli.String())
	}
}

// The line says what a walk found when there was one, and stays one line.
func TestTheCoverageLineCountsWhatIsPending(t *testing.T) {
	at := time.Now().Add(-3 * time.Minute)
	line := coverageLine(index.Coverage{Stale: true, At: at, Sessions: 7000, Pending: 3000, Found: 10000})
	for _, want := range []string{"the index of " + at.Local().Format("2006-01-02 15:04"), "3 minutes ago", "7000 sessions", "3000 of 10000 transcripts are new or changed since"} {
		if !strings.Contains(line, want) {
			t.Errorf("%q missing from %q", want, line)
		}
	}
	if strings.Contains(line, "\n") {
		t.Errorf("the note is more than one line: %q", line)
	}
}
