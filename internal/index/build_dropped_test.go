package index

import (
	"os"
	"path/filepath"
	"testing"
)

// The build counts what reached the index; a turn that is only deja's own
// recall reaches nothing, having been stripped before anything counts it. The
// difference between the two numbers an install prints is exactly that, and it
// is now carried rather than left to be worked out (#3386).
func TestTheBuildSaysHowManyOfItsOwnBlocksItDropped(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	proj := filepath.Join(claude, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := `{"type":"user","sessionId":"s1","cwd":"/tmp/app","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"why does the quokkabloom retry"}}
{"type":"assistant","sessionId":"s1","cwd":"/tmp/app","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":"<deja-recall>\nRecalled history from prior sessions.\n</deja-recall>"}}
{"type":"assistant","sessionId":"s1","cwd":"/tmp/app","timestamp":"2026-01-02T03:04:07Z","message":{"role":"assistant","content":"the retry is capped at three"}}
`
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Ensure(filepath.Join(tmp, "index.db"), "", true, nil); err != nil {
		t.Fatal(err)
	}
	if LastBuild.Messages != 2 {
		t.Errorf("indexed %d messages, want the two a person and the agent wrote", LastBuild.Messages)
	}
	if LastBuild.Dropped != 1 {
		t.Errorf("dropped = %d, want deja's own block counted", LastBuild.Dropped)
	}
}
