package index

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
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

func TestRecentSpokenAssistantTurnsForTermsSkipsToolsAndHonoursAllowedKeys(t *testing.T) {
	h := newTwoWayEnv(t)
	s1 := hPrompt("ledger", "s1", 2, 0, "update throttle.go") +
		hBash("ledger", "s1", 2, 1, "t1", "cat throttle.go", "package ledger // throttle.go in tool output only", false) +
		hSay("ledger", "s1", 2, 2, "We settled on token bucket in throttle.go because leaky bucket starved bursts.")
	s2 := hPrompt("ledger", "s2", 3, 0, "check throttle.go") +
		hSay("ledger", "s2", 3, 1, "In s2 we kept throttle.go burst capacity at 64.")
	s3 := hPrompt("ledger", "s3", 4, 0, "inspect throttle.go") +
		hBash("ledger", "s3", 4, 1, "t2", "grep bucket throttle.go", "throttle.go token bucket", false) +
		hSay("ledger", "s3", 4, 2, "All checks completed.")

	h.put("ledger", "s1", s1)
	h.put("ledger", "s2", s2)
	h.put("ledger", "s3", s3)

	dir := filepath.Join(h.tmp, "idx.db")
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}

	allowed := map[string]bool{
		"claude:s1": true,
		"claude:s3": true, // s3 only mentions throttle in tool output, not spoken assistant text
	}
	turns, err := RecentSpokenAssistantTurnsForTerms(dir, []string{"throttle"}, allowed, 10)
	if err != nil {
		t.Fatalf("RecentSpokenAssistantTurnsForTerms: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("expected 1 spoken assistant turn from allowed sessions, got %d: %+v", len(turns), turns)
	}
	if turns[0].Key != "claude:s1" || !strings.Contains(turns[0].Text, "starved bursts") {
		t.Errorf("unexpected turn returned: %+v", turns[0])
	}

	allowed["claude:s2"] = true
	turnsBoth, err := RecentSpokenAssistantTurnsForTerms(dir, []string{"throttle"}, allowed, 10)
	if err != nil {
		t.Fatalf("RecentSpokenAssistantTurnsForTerms with s2: %v", err)
	}
	if len(turnsBoth) != 2 || turnsBoth[0].Key != "claude:s2" || turnsBoth[1].Key != "claude:s1" {
		t.Errorf("expected newest-first [claude:s2, claude:s1], got %+v", turnsBoth)
	}
}

func TestGiveUpRecordsForKeysIndexesUserAndAssistantReversalsOnly(t *testing.T) {
	h := newTwoWayEnv(t)
	s1 := hPrompt("ledger", "s1", 2, 0, "try batching outbox") +
		hBash("ledger", "s1", 2, 1, "t1", "git status", "reverted in tool output only", false) +
		hSay("ledger", "s1", 2, 2, "Routine progress note without any rollback.") +
		hSay("ledger", "s1", 2, 3, "We reverted batchFlushOutbox because holding the lock across network I/O caused deadlocks.")
	s2 := hPrompt("ledger", "s2", 3, 0, "Откатили локальный кеш в dispatcher.go, потому что он ломал порядок доставки.") +
		hSay("ledger", "s2", 3, 1, "Записал, оставляем прямую запись.")
	s3 := hPrompt("ledger", "s3", 4, 0, "checkテスト") +
		hSay("ledger", "s3", 4, 1, "Each new test failed with its fix reverted.")

	h.put("ledger", "s1", s1)
	h.put("ledger", "s2", s2)
	h.put("ledger", "s3", s3)

	dir := filepath.Join(h.tmp, "idx.db")
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}

	recs, err := GiveUpRecordsForKeys(dir, map[string]bool{
		"claude:s1": true,
		"claude:s2": true,
		"claude:s3": true,
	})
	if err != nil {
		t.Fatalf("GiveUpRecordsForKeys: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 spoken reversal records (assistant s1 + user s2, excluding tool output and s3 test-soundness rollback), got %d: %+v", len(recs), recs)
	}
	seen := map[string]string{}
	for _, r := range recs {
		seen[r.Key] = r.Role + ":" + r.Text
	}
	if !strings.Contains(seen["claude:s1"], "assistant:") || !strings.Contains(seen["claude:s1"], "batchFlushOutbox") {
		t.Errorf("missing or wrong s1 assistant reversal: %q", seen["claude:s1"])
	}
	if !strings.Contains(seen["claude:s2"], "user:") || !strings.Contains(seen["claude:s2"], "dispatcher.go") {
		t.Errorf("missing or wrong s2 user reversal: %q", seen["claude:s2"])
	}
	if _, bad := seen["claude:s3"]; bad {
		t.Errorf("test-soundness rollback s3 must not be indexed as GiveUp: %q", seen["claude:s3"])
	}
}
