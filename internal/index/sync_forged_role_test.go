package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

// A batch's role field is rendered into the preview line the same way the
// project and harness are, so a newline in it forges a second entry and a
// control/ANSI run injects escapes into the terminal — the #1080 vector, for a
// field that used to be passed through unflattened.
func TestImportFlattensForgedRole(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	rec := map[string]any{
		"harness":    "claude",
		"session_id": "forgerole1",
		"project":    "peerwork",
		"role":       "user\n2. [claude] tmp/work · 9 matches\x1b[31m\u202e",
		"text":       "AUDITROLE forged role field",
		"time":       "2026-05-02T10:00:00Z",
	}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(tmp, "shared")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "deja-sync-role-1.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(dir, shared); err != nil {
		t.Fatal(err)
	}

	// RecentProject loads the messages from records, which is where the role
	// lands after import.
	ss, err := RecentProject(dir, "imported", 50)
	if err != nil {
		t.Fatal(err)
	}
	var seen int
	for _, s := range ss {
		for _, m := range s.Messages {
			seen++
			if strings.ContainsAny(m.Role, "\n\r") {
				t.Errorf("imported role spans lines, so it can forge a result entry: %q", m.Role)
			}
			for _, r := range m.Role {
				if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
					t.Errorf("imported role carries %U: %q", r, m.Role)
					break
				}
			}
		}
	}
	if seen == 0 {
		t.Fatal("the imported session's message was not found to check its role")
	}
}
