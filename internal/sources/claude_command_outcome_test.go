package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A Claude transcript records no exit code, so every command it holds read as
// an outcome nobody knew — and a recall answer could say what a session ran but
// never that it worked. The result record does carry `is_error`, which settles
// the clean case, and the clean case is the one worth stating: a command the
// transcript saw finish is evidence, where prose about it is a claim.
//
// Nothing is invented for a failure. The transcript says a command failed and
// not with what status, so the marker — which carries a number every other
// reader parses — is not written at all.
func TestAClaudeCommandCarriesTheOutcomeItsResultRecorded(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	dir := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "c1.jsonl")
	lines := []string{
		`{"type":"assistant","sessionId":"c1","timestamp":"2026-09-02T10:00:00Z","cwd":"/work/app","message":{"role":"assistant","content":[{"type":"tool_use","id":"call_ok","name":"Bash","input":{"command":"SVC_FIXTURES=./fixtures make test"}}]}}`,
		`{"type":"user","sessionId":"c1","timestamp":"2026-09-02T10:00:20Z","cwd":"/work/app","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_ok","content":"ok  example.com/svc/internal/store  0.6s"}]}}`,
		`{"type":"assistant","sessionId":"c1","timestamp":"2026-09-02T10:01:00Z","cwd":"/work/app","message":{"role":"assistant","content":[{"type":"tool_use","id":"call_bad","name":"Bash","input":{"command":"make test-broken"}}]}}`,
		`{"type":"user","sessionId":"c1","timestamp":"2026-09-02T10:01:20Z","cwd":"/work/app","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_bad","is_error":true,"content":"make: *** [test-broken] Error 2"}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, parse := range []struct {
		name string
		fn   func(string) ([]model.Session, error)
	}{
		{"typed", ParseClaudeFile},
		{"reference", func(p string) ([]model.Session, error) { return parseClaudeGenericFromOffset(p, 0) }},
	} {
		ss, err := parse.fn(path)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%s: sessions = %d, err = %v", parse.name, len(ss), err)
		}
		var passed, failed string
		for _, m := range ss[0].Messages {
			if m.Role != RoleCommand {
				continue
			}
			if strings.Contains(m.Text, "make test-broken") {
				failed = m.Text
			} else if strings.Contains(m.Text, "make test") {
				passed = m.Text
			}
		}
		if !strings.HasSuffix(passed, "  → exit 0") {
			t.Errorf("%s: the command its result reported clean reads %q", parse.name, passed)
		}
		if strings.Contains(failed, "→ exit") {
			t.Errorf("%s: a failure with no recorded status was given one: %q", parse.name, failed)
		}
	}
}
