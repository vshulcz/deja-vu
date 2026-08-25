package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// #1911 pinned the always-present half of the session object; the optional keys
// were pinned by nothing, and the document's own example for `kind` named the
// values Grok passes through while omitting the one both Claude readers set
// (#1936). Three ordinary sessions reach six of those keys.
func TestTheOptionalSessionKeysAreDocumentedToo(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	sessions := map[string][]string{
		// Edits a file, then says it backed out of something.
		"plain": {
			`{"type":"user","sessionId":"plain","cwd":"/proj","timestamp":"2026-08-20T10:00:00Z","message":{"role":"user","content":"fix the retry loop"}}`,
			`{"type":"assistant","sessionId":"plain","cwd":"/proj","timestamp":"2026-08-20T10:00:01Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/proj/parser.go"}}]}}`,
			`{"type":"assistant","sessionId":"plain","cwd":"/proj","timestamp":"2026-08-20T10:00:02Z","message":{"role":"assistant","content":"that did not work, reverted it and tried another way"}}`,
		},
		// No user turn, so the title comes from the assistant.
		"agenty": {
			`{"type":"assistant","sessionId":"agenty","cwd":"/proj","timestamp":"2026-08-20T11:00:00Z","message":{"role":"assistant","content":"looking at the pool exhaustion first"}}`,
		},
		// Spawned by an agent: a sidechain with a parent and an agent name.
		"child": {
			`{"type":"user","sessionId":"parent1","isSidechain":true,"agentId":"child1","attributionAgent":"reviewer","cwd":"/proj","timestamp":"2026-08-20T12:00:00Z","message":{"role":"user","content":"review the parser change"}}`,
			`{"type":"assistant","sessionId":"parent1","isSidechain":true,"agentId":"child1","attributionAgent":"reviewer","cwd":"/proj","timestamp":"2026-08-20T12:00:01Z","message":{"role":"assistant","content":"the retry loop is fine"}}`,
		},
	}
	for name, lines := range sessions {
		if err := os.WriteFile(filepath.Join(store, name+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "last", "10", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Sessions []map[string]any `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	emitted := map[string]any{}
	for _, s := range report.Sessions {
		for k, v := range s {
			emitted[k] = v
		}
	}

	// The fixture is worth nothing if it stops producing these.
	for _, want := range []string{"gave_up", "touched", "agent_title", "kind", "parent", "agent"} {
		if _, ok := emitted[want]; !ok {
			t.Errorf("the fixture no longer emits %q, so nothing here is being checked", want)
		}
	}

	doc, err := os.ReadFile("../../docs/json-output.md")
	if err != nil {
		t.Fatal(err)
	}
	table := docSection(t, string(doc), "### The session object")
	var missing []string
	for k := range emitted {
		// Either form: the table names keys in backticks, the examples quote
		// them as JSON.
		if !strings.Contains(table, "`"+k+"`") && !strings.Contains(string(doc), "`"+k+"`") &&
			!strings.Contains(string(doc), `"`+k+`"`) {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("deja last --json emits %v, absent from docs/json-output.md", missing)
	}

	// The value deja actually sets, not only the ones another harness passes
	// through: a reader meeting "sidechain" must find it in the table.
	if got, _ := emitted["kind"].(string); got != "" && !strings.Contains(table, "`"+got+"`") {
		t.Errorf("the session table does not name kind %q, which is what a Claude store produces", got)
	}
}
