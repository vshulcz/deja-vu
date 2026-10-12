package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/query"
)

// A Hermes cron job's run is in the index like any session, and the prompt
// hook offered it to a person asking about the same thing: its prompt is the
// same every fire and often carries someone else's text in the user role.
// It stays searchable; it is not offered unasked (#4948).
func TestPromptRecallLeavesOutAHermesCronRun(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 is not installed")
	}
	ask := func(source string) (hook, search string) {
		t.Helper()
		tmp := hermeticEnv(t)
		claude := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-w-p")
		if err := os.MkdirAll(claude, 0o755); err != nil {
			t.Fatal(err)
		}
		// Other work, so the question's words are rare enough to identify it.
		for i := range 40 {
			id := fmt.Sprintf("other-%d", i)
			line := fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":"/w/p","timestamp":"2026-07-01T09:%02d:00Z","message":{"role":"user","content":"rename table%d and column%d in the billing schema"}}`, id, i, i, i)
			if err := os.WriteFile(filepath.Join(claude, id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		home := filepath.Join(tmp, "hermes")
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("DEJA_HERMES_HOME", home)
		t.Setenv("DEJA_HERMES_PROFILES_ROOT", "")
		script := `CREATE TABLE sessions (id TEXT PRIMARY KEY, source TEXT, parent_session_id TEXT, cwd TEXT);
CREATE TABLE messages (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, role TEXT NOT NULL, content TEXT, tool_call_id TEXT, tool_calls TEXT, tool_name TEXT, timestamp REAL NOT NULL);
INSERT INTO sessions VALUES ('job-zephyr','` + source + `',NULL,'/w/p');
INSERT INTO messages (session_id,role,content,timestamp) VALUES
 ('job-zephyr','user','the zephyr reconciler drops refunds with QX-77',1785000000),
 ('job-zephyr','assistant','The zephyr reconciler skips refunds whose QX-77 ledger id is reused; keyed it on the refund id.',1785000060);`
		cmd := exec.Command("sqlite3", filepath.Join(home, "state.db"))
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("sqlite3: %v\n%s", err, out)
		}
		dir := filepath.Join(tmp, "index.db")
		if err := index.Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
		payload, _ := json.Marshal(map[string]string{"session_id": "asker-" + source, "cwd": "/w/p", "prompt": "why does the zephyr reconciler drop refunds with QX-77"})
		var out strings.Builder
		if err := runHookPromptMode(dir, strings.NewReader(string(payload)), &out, true); err != nil {
			t.Fatal(err)
		}
		hits, err := index.Search(dir, query.Options{Query: "zephyr reconciler QX-77"})
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, h := range hits {
			ids = append(ids, h.ID)
		}
		return out.String(), strings.Join(ids, " ")
	}

	// The control: the same session started by a person is offered, so the
	// silence below is the cron mark's doing and not a fixture too small.
	if hook, _ := ask("cli"); !strings.Contains(hook, "zephyr") {
		t.Fatalf("a person's session was not offered, so this proves nothing:\n%s", hook)
	}
	hook, search := ask("cron")
	if strings.Contains(hook, "zephyr") {
		t.Errorf("the prompt hook offered a cron run:\n%s", hook)
	}
	if !strings.Contains(search, "job-zephyr") {
		t.Errorf("a cron run is no longer searchable: %q", search)
	}
}
