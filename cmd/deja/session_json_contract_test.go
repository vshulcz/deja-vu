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

// jsonKeys is every key a value emits, nested ones included, once each.
func jsonKeys(v any, into map[string]bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, inner := range t {
			into[k] = true
			jsonKeys(inner, into)
		}
	case []any:
		for _, inner := range t {
			jsonKeys(inner, into)
		}
	}
}

// `deja show --json` and `deja last --json` are documented in prose and pinned
// by nothing, while `stats --json` has been pinned key by key since #1710 and
// `--impact --json` since #1900. A field added to the session object reaches
// three commands' output at once, so this is the check that a document entry
// comes with it.
//
// Against the whole document rather than the two sections: the keys those
// sections do not name are the ones the shared "session object" table describes
// for search, last and show together, which is where they belong.
//
// It pins what this corpus emits, which is the always-present half. The
// optional keys — agent_title, touched, gave_up, orig_id, from, lifecycle,
// kind, parent, agent — are in that table today and a fixture would have to
// grow a case each to hold them there.
func TestSessionJSONKeysAreDocumented(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for i := 0; i < 4; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		lines = append(lines, `{"type":"`+role+`","sessionId":"sess1","cwd":"/proj","timestamp":"2026-08-20T10:0`+
			string(rune('0'+i))+`:00Z","message":{"role":"`+role+`","content":"the parser retried"}}`)
	}
	if err := os.WriteFile(filepath.Join(store, "sess1.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	_ = tmp

	doc, err := os.ReadFile("../../docs/json-output.md")
	if err != nil {
		t.Fatal(err)
	}
	documented := string(doc)

	for _, c := range []struct {
		name string
		args []string
	}{
		{"show", []string{"show", "sess1", "--harness", "claude", "--json"}},
		{"last", []string{"last", "3", "--json"}},
	} {
		out, err := captureRun(t, c.args...)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var body any
		if err := json.Unmarshal([]byte(out), &body); err != nil {
			t.Fatalf("%s: %v: %s", c.name, err, out)
		}
		keys := map[string]bool{}
		jsonKeys(body, keys)
		if len(keys) < 8 {
			t.Fatalf("%s emitted %d keys, so the corpus is too thin to say anything: %v", c.name, len(keys), keys)
		}
		var missing []string
		for k := range keys {
			if !strings.Contains(documented, "`"+k+"`") && !strings.Contains(documented, `"`+k+`"`) {
				missing = append(missing, k)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("deja %s --json emits %v, absent from docs/json-output.md", c.name, missing)
		}
	}
}
