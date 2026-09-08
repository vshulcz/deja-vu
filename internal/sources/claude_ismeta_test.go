package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Claude Code marks the user records it writes itself with isMeta: a skill body
// it loaded, a prompt a cron re-fired, the /fork notice. Indexed as the person's
// words they feed terms, titles and "asked by a person" — 779 of them on this
// machine, none carrying a tool call (#3267).
func TestClaudeMetaRecordsAreNotTheirWords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s1.jsonl")
	lines := strings.Join([]string{
		`{"type":"user","sessionId":"s1","timestamp":"2026-09-08T10:00:00Z","message":{"role":"user","content":"why does the index rebuild twice?"}}`,
		`{"type":"user","isMeta":true,"sessionId":"s1","timestamp":"2026-09-08T10:00:01Z","message":{"role":"user","content":"# /loop — schedule a recurring prompt. Parse the input below."}}`,
		`{"type":"user","isMeta":true,"sessionId":"s1","timestamp":"2026-09-08T10:00:02Z","message":{"role":"user","content":"[Image: source: /var/folders/x/Screenshot.png]"}}`,
		`{"type":"assistant","sessionId":"s1","timestamp":"2026-09-08T10:00:03Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./internal/index"}}]}}`,
		// A meta record still gives up the work it names, if it ever carries any.
		`{"type":"user","isMeta":true,"sessionId":"s1","timestamp":"2026-09-08T10:00:04Z","message":{"role":"user","content":[{"type":"tool_use","name":"Read","input":{"file_path":"/repo/internal/index/index.go"}}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	check := func(name string, ss []model.Session, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(ss) != 1 {
			t.Fatalf("%s: sessions = %d", name, len(ss))
		}
		var user, files, commands int
		for _, m := range ss[0].Messages {
			switch m.Role {
			case "user":
				user++
				if !strings.Contains(m.Text, "rebuild twice") {
					t.Errorf("%s: a record the harness wrote is indexed as the person: %q", name, m.Text)
				}
			case RoleFiles:
				files++
			case RoleCommand:
				commands++
			}
		}
		if user != 1 {
			t.Errorf("%s: user turns = %d, want the one the person typed", name, user)
		}
		if commands != 1 {
			t.Errorf("%s: command records = %d, want the one the assistant ran", name, commands)
		}
		if files != 1 {
			t.Errorf("%s: files records = %d — a meta record's work went with its text", name, files)
		}
	}

	typed, err := parseClaudeTypedFromOffset(path, 0)
	check("typed", typed, err)
	generic, err := parseClaudeGenericFromOffset(path, 0)
	check("generic", generic, err)
}
