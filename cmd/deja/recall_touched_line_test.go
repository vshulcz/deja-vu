package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// touchedLineStore indexes one session that edited the given paths.
func touchedLineStore(t *testing.T, paths ...string) (string, model.Session) {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-t")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	var b strings.Builder
	b.WriteString(`{"type":"user","sessionId":"t1","cwd":"/w/t","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"the retry queue stalls on staging"}}` + "\n")
	for i, p := range paths {
		q, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		b.WriteString(`{"type":"assistant","sessionId":"t1","cwd":"/w/t","timestamp":"2026-07-20T10:0` +
			fmt.Sprint(i+1) + `:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":` +
			string(q) + `}}]}}` + "\n")
	}
	if err := os.WriteFile(filepath.Join(root, "t1.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir, model.Session{Harness: "claude", ID: "t1"}
}

// Recall names the files under its best hit so an agent that has just learned
// "we solved this before" does not have to search the tree for where. The
// helper underneath is covered next door; the line itself was not.
//
// Measured before this test: removing the line, removing the shortening, or
// removing the overflow count each broke nothing in the package.
func TestRecallNamesTheFilesUnderTheBestHit(t *testing.T) {
	dir, s := touchedLineStore(t,
		"/w/app/internal/queue/retry.go",
		"/w/app/internal/queue/backoff.go",
		"/w/app/internal/queue/jitter.go",
	)
	got := recallTouchedLine(dir, s)
	if got == "" {
		t.Fatal("no line for a session that edited three files")
	}
	// The shared directory is said once, and the names arrive relative to it.
	if strings.Count(got, "/w/app/internal/queue") != 1 {
		t.Errorf("the shared directory is repeated: %q", got)
	}
	for _, name := range []string{"retry.go", "backoff.go", "jitter.go"} {
		if !strings.Contains(got, name) {
			t.Errorf("%s is missing: %q", name, got)
		}
	}
	if !strings.HasSuffix(got, "in /w/app/internal/queue") {
		t.Errorf("the line does not end in the shared directory: %q", got)
	}
	// Shortening has to pay for itself, or it is only rearranging. Both sides
	// go through the same scrubbing, so the comparison is about the shortening
	// and not about what SafeLine does to a line.
	full := search.SafeLine("/w/app/internal/queue/retry.go, /w/app/internal/queue/backoff.go, /w/app/internal/queue/jitter.go")
	if len(got) >= len(full) {
		t.Errorf("shortening saved nothing: %d bytes vs %d\n  %q", len(got), len(full), got)
	}
}

// Past the cap the line says how many it did not name, or a partial list reads
// as the whole of it.
func TestRecallCountsTheFilesItDidNotName(t *testing.T) {
	var paths []string
	for i := 0; i < recallTouchedFiles+3; i++ {
		paths = append(paths, fmt.Sprintf("/w/app/pkg/file%02d.go", i))
	}
	dir, s := touchedLineStore(t, paths...)
	// What the store kept, not what the fixture wrote: the manifest has a cap
	// of its own, and this test is about the line rather than about that cap.
	metas, err := index.AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	held := 0
	for _, m := range metas {
		if m.ID == s.ID {
			held = len(m.Touched)
		}
	}
	// The manifest has a cap of its own, and it is smaller than this fixture:
	// if it ever drops to the line's own cap there is no overflow left to test
	// and this says so rather than passing on a shape it never exercised.
	if held <= recallTouchedFiles {
		t.Fatalf("the store kept %d paths, at or under the line's cap of %d: there is no overflow to check, so the fixture needs more paths or the manifest cap moved",
			held, recallTouchedFiles)
	}
	got := recallTouchedLine(dir, s)
	if got == "" {
		t.Fatal("no line for a session that edited seven files")
	}
	named := 0
	for _, p := range paths {
		if strings.Contains(got, filepath.Base(p)) {
			named++
		}
	}
	// The contract is the sum, not either half: what the line names plus what
	// it says it left out is everything the store held. Checking the number
	// alone would pass while the list was wrong, and checking the list alone
	// would pass while the count was.
	var extra int
	if _, err := fmt.Sscanf(got[strings.Index(got, "(+"):], "(+%d more)", &extra); err != nil {
		t.Fatalf("the line does not say how many of its %d paths it left out: %q", held, got)
	}
	if named != recallTouchedFiles {
		t.Errorf("named %d files, the line's cap is %d: %q", named, recallTouchedFiles, got)
	}
	if named+extra != held {
		t.Errorf("the line accounts for %d of %d paths (%d named, %d more): %q",
			named+extra, held, named, extra, got)
	}
}

// Paths that share nothing are printed whole: there is no prefix to hoist, and
// inventing one would name a directory none of them are in.
func TestRecallLeavesUnrelatedPathsWhole(t *testing.T) {
	dir, s := touchedLineStore(t, "/one/alpha.go", "/two/beta.go")
	got := recallTouchedLine(dir, s)
	if got == "" {
		t.Fatal("no line for a session that edited two files")
	}
	if strings.Contains(got, " in ") {
		t.Errorf("a shared directory was invented: %q", got)
	}
	for _, want := range []string{"/one/alpha.go", "/two/beta.go"} {
		if !strings.Contains(got, want) {
			t.Errorf("%s is missing or was cut: %q", want, got)
		}
	}
}
