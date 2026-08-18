package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// excludeStore indexes one ordinary project and one the user later decides is
// private. Nothing is excluded at build time: the point of #1307 is what
// happens when the pattern is set afterwards.
func excludeStore(t *testing.T) string {
	t.Helper()
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for name, text := range map[string]string{
		"-work":    "why does the retry queue stall on staging",
		"-private": "the quibblesnatch acquisition price is confidential",
	} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		line := `{"type":"user","message":{"role":"user","content":"` + text + `"},"timestamp":"2026-08-02T10:00:00Z","sessionId":"s` + name + `","cwd":"/` + strings.TrimPrefix(name, "-") + `"}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, "a.jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The sequence a careful person performs — realise a project should not be
// shared, set the exclusion, sync — was the one that shipped it. The export is
// where data leaves the machine, so the pattern holds there whether or not the
// index has been rebuilt.
func TestExportHonoursAnExclusionSetAfterTheBuild(t *testing.T) {
	dir := excludeStore(t)
	out := filepath.Join(t.TempDir(), "batch")
	t.Setenv("DEJA_EXCLUDE_PROJECTS", "private")
	if _, err := index.ExportFull(dir, out); err != nil {
		t.Fatal(err)
	}
	shipped := readBatch(t, out)
	if strings.Contains(shipped, "quibblesnatch") {
		t.Errorf("the excluded project was exported:\n%s", shipped)
	}
	if !strings.Contains(shipped, "retry queue") {
		t.Errorf("the export dropped work nothing excluded:\n%s", shipped)
	}
}

// And with no pattern set, everything still goes.
func TestExportWithoutExclusionsShipsEverything(t *testing.T) {
	dir := excludeStore(t)
	out := filepath.Join(t.TempDir(), "batch")
	shipped := ""
	if _, err := index.ExportFull(dir, out); err != nil {
		t.Fatal(err)
	}
	shipped = readBatch(t, out)
	for _, want := range []string{"quibblesnatch", "retry queue"} {
		if !strings.Contains(shipped, want) {
			t.Errorf("export dropped %q with no exclusion set:\n%s", want, shipped)
		}
	}
}

// The other half: `deja index` used to return silently having applied nothing,
// which reads exactly like success. This is the up-to-date path — nothing
// changed on disk, which is the most misleading moment to stay quiet.
func TestIndexSaysAnExclusionNeedsARebuildWhenNothingChanged(t *testing.T) {
	excludeStore(t)
	t.Setenv("DEJA_EXCLUDE_PROJECTS", "private")
	out, err := captureRunStderr(t, "index")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "up to date") {
		t.Fatalf("this test means to exercise the up-to-date path:\n%s", out)
	}
	if !strings.Contains(out, "exclude list changed") {
		t.Errorf("index said nothing about the exclusion it did not apply:\n%s", out)
	}
}

// And after an incremental build, which writes a fresh manifest and could
// otherwise stamp the new patterns as applied without having applied them to
// anything already indexed.
func TestIndexSaysAnExclusionNeedsARebuildAfterAnIncrementalBuild(t *testing.T) {
	excludeStore(t)
	t.Setenv("DEJA_EXCLUDE_PROJECTS", "private")
	newWork := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work", "b.jsonl")
	line := `{"type":"user","message":{"role":"user","content":"the retry queue stalled again today"},"timestamp":"2026-08-03T10:00:00Z","sessionId":"sb","cwd":"/work"}` + "\n"
	if err := os.WriteFile(newWork, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRunStderr(t, "index")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "up to date") {
		t.Fatalf("this test means to exercise a real build:\n%s", out)
	}
	if !strings.Contains(out, "exclude list changed") {
		t.Errorf("an incremental build claimed the new exclusion set without applying it:\n%s", out)
	}
	// The rebuild does apply it, and then the line has to stop.
	if _, err := captureRunStderr(t, "index", "--rebuild"); err != nil {
		t.Fatal(err)
	}
	out, err = captureRunStderr(t, "index")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "exclude list changed") {
		t.Errorf("index still asks for a rebuild it already got:\n%s", out)
	}
	// And the excluded project is gone from search, which is what the rebuild
	// was for.
	hits, err := captureRun(t, "quibblesnatch")
	if err == nil && strings.Contains(hits, "quibblesnatch") {
		t.Errorf("the rebuild kept the excluded project searchable:\n%s", hits)
	}
}

// A machine with no exclusions must never see the line.
func TestIndexStaysQuietWithoutExclusions(t *testing.T) {
	excludeStore(t)
	out, err := captureRunStderr(t, "index")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "exclude list") {
		t.Errorf("a machine with no exclusions was told to rebuild:\n%s", out)
	}
}

func readBatch(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		all.Write(b)
	}
	return all.String()
}

// An exclusion is a decision, not a deletion. The incremental export resumes
// from a per-source watermark, so letting it run past a record it held back
// would settle that work forever: remove the pattern later and it never syncs
// again.
//
// Notes are the case where this is reachable — one store file carries every
// note, and `--project` puts them in different projects, so an included record
// can advance a watermark past an excluded one in the same source. Measured on
// a real 227-source index: two sources carry several projects each, the worst
// of them five.
func TestAnExcludedRecordIsNotSettledByTheWatermark(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if _, err := captureRun(t, "remember", "the quibblesnatch acquisition price", "--project", "private"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "remember", "the retry queue stalls on staging", "--project", "work"); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "batch")
	t.Setenv("DEJA_EXCLUDE_PROJECTS", "private")
	if _, err := index.Export(dir, out); err != nil {
		t.Fatal(err)
	}
	shipped := readBatch(t, out)
	if strings.Contains(shipped, "quibblesnatch") {
		t.Fatalf("the excluded note was exported:\n%s", shipped)
	}
	if !strings.Contains(shipped, "retry queue") {
		t.Fatalf("this test needs the other note in the same source to be sent:\n%s", shipped)
	}
	// The user changes their mind.
	t.Setenv("DEJA_EXCLUDE_PROJECTS", "")
	again := filepath.Join(t.TempDir(), "batch2")
	if _, err := index.Export(dir, again); err != nil {
		t.Fatal(err)
	}
	if shipped := readBatch(t, again); !strings.Contains(shipped, "quibblesnatch") {
		t.Errorf("work held back by an exclusion never synced after the pattern was removed:\n%s", shipped)
	}
}

// share hands a session to another person, which is the same boundary as an
// export.
func TestShareRefusesAnExcludedProject(t *testing.T) {
	excludeStore(t)
	t.Setenv("DEJA_EXCLUDE_PROJECTS", "private")
	id := sessionIDFor(t, "quibblesnatch")
	if id == "" {
		t.Skip("no session to share")
	}
	out, err := captureRun(t, "share", id)
	if err == nil {
		t.Errorf("share handed over a project the exclude list covers:\n%s", out)
	}
	if err != nil && !strings.Contains(err.Error(), "exclude list") {
		t.Errorf("share refused for the wrong reason: %v", err)
	}
}

// sessionIDFor finds the id of the session holding a phrase, before any
// exclusion is set.
func sessionIDFor(t *testing.T, phrase string) string {
	t.Helper()
	metas, err := index.AllMeta(index.DefaultDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range metas {
		if strings.Contains(m.Project, "private") {
			return m.ID
		}
	}
	return ""
}
