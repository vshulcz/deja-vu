package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// deepFixture builds a real index over one claude session and returns the
// index dir plus the source file path, fully isolated from the machine.
func deepFixture(t *testing.T) (dir, src string) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir = filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-x")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	src = filepath.Join(proj, "s1.jsonl")
	data := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"fix the flaky auth token test"}}` + "\n" +
		`{"type":"assistant","sessionId":"s1","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"text","text":"expiry check used < instead of <="}]}}` + "\n"
	if err := os.WriteFile(src, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir, src
}

func findingKinds(r DeepReport) map[string]bool {
	kinds := map[string]bool{}
	for _, f := range r.Findings {
		kinds[f.Kind] = true
	}
	return kinds
}

func TestDeepVerifyCleanIndex(t *testing.T) {
	dir, _ := deepFixture(t)
	r, err := DeepVerify(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Clean() {
		t.Fatalf("fresh index must verify clean, got %#v", r.Findings)
	}
	if r.FilesChecked != 1 || r.SessionsIndexed != 1 || r.SampledFiles != 1 {
		t.Fatalf("counts = files=%d sessions=%d sampled=%d, want 1/1/1", r.FilesChecked, r.SessionsIndexed, r.SampledFiles)
	}
	if r.SampledPostings == 0 {
		t.Fatal("expected at least one posting resolved")
	}
}

func TestDeepVerifyTreatsNewAndGrownFilesAsStale(t *testing.T) {
	dir, src := deepFixture(t)
	extra := filepath.Join(filepath.Dir(src), "s2.jsonl")
	line := `{"type":"user","sessionId":"s2","timestamp":"2026-01-03T03:04:05Z","message":{"role":"user","content":"new session never indexed"}}` + "\n"
	if err := os.WriteFile(extra, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(src, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:05:05Z","message":{"role":"user","content":"appended after the index pass"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	r, err := DeepVerify(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Stale) != 2 {
		t.Fatalf("new + appended files are staleness, not drift: stale=%v findings=%#v", r.Stale, r.Findings)
	}
	if !r.Clean() {
		t.Fatalf("staleness must not read as drift, got %#v", r.Findings)
	}
}

func TestDeepVerifyFlagsShrunkSource(t *testing.T) {
	dir, src := deepFixture(t)
	fi, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(src, fi.Size()/2); err != nil {
		t.Fatal(err)
	}
	r, err := DeepVerify(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !findingKinds(r)["shrunk-file"] {
		t.Fatalf("want shrunk-file, got stale=%v findings=%#v", r.Stale, r.Findings)
	}
}

func TestDeepVerifyFlagsDeletedSource(t *testing.T) {
	dir, src := deepFixture(t)
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	r, err := DeepVerify(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !findingKinds(r)["orphan-file"] {
		t.Fatalf("want orphan-file for deleted source, got %#v", r.Findings)
	}
}

func TestDeepVerifyFlagsSessionMissingFromIndex(t *testing.T) {
	dir, _ := deepFixture(t)
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	for k := range m.Sessions {
		delete(m.Sessions, k)
	}
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	r, err := DeepVerify(dir)
	if err != nil {
		t.Fatal(err)
	}
	var hit bool
	for _, f := range r.Findings {
		if f.Kind == "parse-drift" && strings.Contains(f.Detail, "absent from the index") {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("want parse-drift for session missing from index, got %#v", r.Findings)
	}
}

func TestDeepVerifyFlagsTruncatedRecords(t *testing.T) {
	dir, _ := deepFixture(t)
	if err := os.Truncate(recordsPath(dir), 3); err != nil {
		t.Fatal(err)
	}
	r, err := DeepVerify(dir)
	if err != nil {
		t.Fatal(err)
	}
	kinds := findingKinds(r)
	if !kinds["torn-log"] && !kinds["dead-posting"] {
		t.Fatalf("truncated records.bin must surface torn-log or dead-posting, got %#v", r.Findings)
	}
}

func TestDeepVerifyDeterministicSampling(t *testing.T) {
	dir, _ := deepFixture(t)
	a, err := DeepVerify(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := DeepVerify(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a.SampledFiles != b.SampledFiles || a.SampledPostings != b.SampledPostings {
		t.Fatalf("sampling must be deterministic: %d/%d vs %d/%d", a.SampledFiles, a.SampledPostings, b.SampledFiles, b.SampledPostings)
	}
}
