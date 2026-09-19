package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const opencodeDiffFixture = `[
  {"file": "internal/pool/pool.go", "status": "modified", "additions": 2, "deletions": 1,
   "patch": "--- a/internal/pool/pool.go\n+++ b/internal/pool/pool.go\n@@ -40,1 +40,2 @@\n-\tcfg.MaxConnLifetime = 30 * time.Minute\n+\tcfg.MaxConnLifetime = 4 * time.Minute\n+\tcfg.HealthCheckPeriod = 30 * time.Second\n"},
  {"file": "internal/queue/worker.go", "status": "added", "additions": 3, "deletions": 0,
   "patch": "--- /dev/null\n+++ b/internal/queue/worker.go\n@@ -0,0 +1,3 @@\n+package queue\n+\n+func Work() {}\n"}
]`

// The diff store is the only account of what most opencode sessions changed —
// 17 of 400 sampled sessions held an edit record from the database, and 384 of
// them are in the index (#3791). So what it parses has to arrive as records on
// the session the database already gave, keyed on the same id.
func TestOpencodeDiffStoreYieldsTheSessionsOwnChanges(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_OPENCODE_DIFFS", dir)
	path := filepath.Join(dir, "ses_1a3dd1e75ffeOKtQ9luutwjr7S.json")
	if err := os.WriteFile(path, []byte(opencodeDiffFixture), 0o644); err != nil {
		t.Fatal(err)
	}

	ss, err := ParseOpencodeDiff(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("parsed %d sessions, want 1", len(ss))
	}
	s := ss[0]
	if s.Harness != "opencode" {
		t.Errorf("harness %q", s.Harness)
	}
	if s.ID != "ses_1a3dd1e75ffeOKtQ9luutwjr7S" {
		t.Errorf("id %q — it has to be the id the database uses, or this arrives as a second session", s.ID)
	}

	var edits, files int
	for _, m := range s.Messages {
		switch m.Role {
		case RoleEdit:
			edits++
			// "path\nreplaced bytes", the shape every other harness's edits
			// take, so restore and blame need not know where it came from.
			path, span, ok := strings.Cut(m.Text, "\n")
			if !ok {
				t.Errorf("edit record has no path line: %q", m.Text)
			}
			if path != "internal/pool/pool.go" {
				t.Errorf("edit record names %q", path)
			}
			if !strings.Contains(span, "30 * time.Minute") {
				t.Errorf("the replaced span is %q, and the removed line is what a diff holds", span)
			}
			if strings.Contains(span, "+") || strings.Contains(span, "4 * time.Minute") {
				t.Errorf("the added side leaked into the replaced span: %q", span)
			}
		case RoleFiles:
			files++
			for _, want := range []string{"internal/pool/pool.go", "internal/queue/worker.go"} {
				if !strings.Contains(m.Text, want) {
					t.Errorf("the files record does not name %s: %q", want, m.Text)
				}
			}
		}
	}
	// One patch removes a line and one is a new file, so exactly one edit.
	if edits != 1 {
		t.Errorf("%d edit records, want 1: a new file removes nothing", edits)
	}
	if files != 1 {
		t.Errorf("%d files records, want 1", files)
	}
}

// Most of those files are an empty list — the session changed nothing — and an
// empty session would add a row to every screen that counts sessions.
func TestAnEmptyDiffFileIsNotASession(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_OPENCODE_DIFFS", dir)
	for name, body := range map[string]string{
		"ses_empty.json":   "[]",
		"ses_broken.json":  "{not json",
		"notasession.json": opencodeDiffFixture,
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		ss, err := ParseOpencodeDiff(path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(ss) != 0 {
			t.Errorf("%s produced %d sessions, want none", name, len(ss))
		}
	}
}

// The store is found from the database path, so every override carries over:
// a second variable to keep in step is how two paths drift apart.
func TestTheDiffStoreFollowsTheDatabasePath(t *testing.T) {
	t.Setenv("DEJA_OPENCODE_DIFFS", "")
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join("/tmp", "stand", "opencode.db"))
	want := filepath.Join("/tmp", "stand", "storage", "session_diff")
	if got := OpencodeDiffDir(); got != want {
		t.Errorf("OpencodeDiffDir() = %q, want %q", got, want)
	}

	dir := t.TempDir()
	t.Setenv("DEJA_OPENCODE_DIFFS", dir)
	if err := os.WriteFile(filepath.Join(dir, "ses_a.json"), []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := OpencodeDiffFiles()
	if len(got) != 1 || filepath.Base(got[0]) != "ses_a.json" {
		t.Errorf("OpencodeDiffFiles() = %v, want the one session file", got)
	}
}
