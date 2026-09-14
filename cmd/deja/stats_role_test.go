package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Three of the six spellings `deja help` documents came back empty on a store
// that holds those records. `--role tool` because stats compared the stored
// name to the documented alias; `--role command`, `--role files` and `--role
// edit` because the index withholds work records from a caller that did not ask
// for them and stats asked for everything, then narrowed afterwards (#3592).
func TestStatsReachesEveryRoleItDocuments(t *testing.T) {
	tmp := hermeticEnv(t)
	at := "2026-08-20T10:00:0%dZ"
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "work", "one.jsonl"), "roles", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"roles","timestamp":%q,"cwd":"/work/app",`+
			`"message":{"role":"user","content":"the api will not start"}}`, fmt.Sprintf(at, 0)),
		fmt.Sprintf(`{"type":"assistant","sessionId":"roles","timestamp":%q,"cwd":"/work/app",`+
			`"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"docker compose up -d db"}}]}}`, fmt.Sprintf(at, 1)),
		fmt.Sprintf(`{"type":"user","sessionId":"roles","timestamp":%q,"cwd":"/work/app",`+
			`"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"Container db Started"}]}}`, fmt.Sprintf(at, 2)),
		fmt.Sprintf(`{"type":"assistant","sessionId":"roles","timestamp":%q,"cwd":"/work/app",`+
			`"message":{"role":"assistant","content":[{"type":"text","text":"the database was down"}]}}`, fmt.Sprintf(at, 3)),
	})
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	messages := regexp.MustCompile(`(?m)^Messages\s+(\d+)`)
	for _, role := range []string{"user", "assistant", "tool", "tool-output", "command"} {
		out, err := captureRun(t, "stats", "--role", role)
		if err != nil {
			t.Fatal(err)
		}
		m := messages.FindStringSubmatch(out)
		if m == nil {
			t.Errorf("stats --role %s found nothing on a store that holds those records:\n%s", role, out)
			continue
		}
		if m[1] == "0" {
			t.Errorf("stats --role %s counted zero:\n%s", role, out)
		}
	}

	// A role the store genuinely holds none of says so, rather than reporting
	// an unbuilt index — the whole point of letting the role reach the query is
	// that it empties the retrieval when it matches nothing.
	out, err := captureRun(t, "stats", "--role", "edit")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "run `deja index`") {
		t.Errorf("a role with no records was reported as an unbuilt index:\n%s", out)
	}
	if !strings.Contains(out, "holds no edit records") {
		t.Errorf("stats does not say the store records no edits:\n%s", out)
	}
}
