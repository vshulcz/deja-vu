package index

import (
	"os"
	"path/filepath"
	"testing"

	search "github.com/vshulcz/deja-vu/internal/query"
)

func TestEnsureHonorsHarnessScope(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"c1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"claude only content"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "c1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	// A second harness's store exists and must NOT be ingested.
	piRoot := filepath.Join(tmp, "pi")
	if err := os.MkdirAll(piRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	piLine := `{"type":"session","id":"p1","timestamp":"2026-01-02T03:04:05Z"}` + "\n" +
		`{"type":"message","timestamp":"2026-01-02T03:04:06Z","message":{"role":"user","content":[{"type":"text","text":"pi session must stay out"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(piRoot, "p1.jsonl"), []byte(piLine), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_PI_ROOT", piRoot)
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "claude", true, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, meta := range m.Sessions {
		if meta.Harness != "claude" {
			t.Fatalf("claude-scoped Ensure ingested harness %q (session %s)", meta.Harness, meta.ID)
		}
	}
	if len(m.Sessions) != 1 {
		t.Fatalf("sessions = %d, want the single claude fixture", len(m.Sessions))
	}
}

func TestDateTokensMakeSessionsFindableByMonth(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	mk := func(id, ts, text string) {
		line := `{"type":"user","sessionId":"` + id + `","timestamp":"` + ts + `","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("jun", "2023-06-10T10:00:00Z", "fixed the camping stove regression")
	mk("sep", "2023-09-03T10:00:00Z", "tuned the winter tent lineup")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	got, err := Search(dir, search.Options{Query: "camping june", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].ID != "jun" {
		t.Fatalf("month token did not surface the June session: %+v", got)
	}
}
