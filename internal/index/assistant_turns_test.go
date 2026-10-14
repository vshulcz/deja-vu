package index

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// assistantTurnsStore has a session from this week, one from long ago, one
// the ignore rule keeps out and one in a project the caller does not allow.
func assistantTurnsStore(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	setHome(t, home)
	root := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	policyFile := filepath.Join(home, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", policyFile)
	if err := os.WriteFile(policyFile, []byte(`{"ignore":["*scratch*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	write := func(project, id, early, late string) {
		p := filepath.Join(root, "-w-"+project, id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		line := func(typ, at, content string) string {
			return `{"type":"` + typ + `","sessionId":"` + id + `","cwd":"/w/` + project + `","timestamp":"` + at + `","message":{"role":"` + typ + `","content":` + content + `}}` + "\n"
		}
		body := line("user", early, `"why does the lease expire early"`) +
			line("assistant", early, `[{"type":"text","text":"reply from `+id+` before the week"}]`) +
			line("assistant", late, `[{"type":"text","text":"reply from `+id+`"}]`)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("keep", "week1", "2026-10-01T10:00:00Z", "2026-10-12T10:00:00Z")
	write("keep", "old1", "2026-07-01T10:00:00Z", "2026-07-01T10:05:00Z")
	write("scratch", "ign1", "2026-10-01T10:00:00Z", "2026-10-12T10:00:00Z")
	write("other", "deny1", "2026-10-01T10:00:00Z", "2026-10-12T10:00:00Z")
	dir := filepath.Join(home, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestEachAssistantTurnSinceReadsOnlyThisWeeksAllowedTurns(t *testing.T) {
	dir := assistantTurnsStore(t)
	since := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	allow := func(project string) bool { return filepath.Base(project) != "other" }
	var got []string
	err := EachAssistantTurnSince(dir, since, allow, func(m SessionMeta, text string, at time.Time) {
		if !at.After(since) {
			t.Errorf("%q at %v is before the cut", text, at)
		}
		got = append(got, m.ID+": "+text)
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if want := []string{"week1: reply from week1"}; len(got) != 1 || got[0] != want[0] {
		t.Fatalf("got %q, want %q", got, want)
	}

	// Nothing after the cut reads nothing.
	n := 0
	if err := EachAssistantTurnSince(dir, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), nil, func(SessionMeta, string, time.Time) { n++ }); err != nil || n != 0 {
		t.Fatalf("a cut after every session gave %d turns, err %v", n, err)
	}
}

func TestEachAssistantTurnSinceReportsAMissingStore(t *testing.T) {
	setHome(t, t.TempDir())
	if err := EachAssistantTurnSince(filepath.Join(t.TempDir(), "none.db"), time.Time{}, nil, func(SessionMeta, string, time.Time) {}); err == nil {
		t.Fatal("a store that does not exist must be an error, not an empty week")
	}
}
