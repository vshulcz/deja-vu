package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

func TestDigestBudgetSpentCountsOnlyFullDigests(t *testing.T) {
	seen := map[string]bool{"sess-a": true, "q:abc": true}
	for i := 0; i < digestsPerSession-1; i++ {
		seen[digestSeenPrefix+strconv.Itoa(i)] = true
	}
	if digestBudgetSpent(seen, "agent") {
		t.Fatal("budget spent before the session had its full digests")
	}
	seen[digestSeenPrefix+"last"] = true
	if !digestBudgetSpent(seen, "agent") {
		t.Fatal("budget not spent after the session had its full digests")
	}
	if digestBudgetSpent(seen, spawnReaderPrefix+"agent:x") {
		t.Error("a spawned agent was cut, but it is a new reader")
	}
	if digestBudgetSpent(seen, "") {
		t.Error("a payload with no session id was cut")
	}
}

// Past the budget a long session gets the pointer line instead of the quotes,
// a spawned agent still gets the quotes, and a compaction resets the count.
func TestHookPromptShrinksToPointerPastTheDigestBudget(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "budgetterm", []string{
		`{"type":"user","sessionId":"budgetterm","timestamp":"` + old +
			`","message":{"role":"user","content":"set up the connection pooler for the api"}}`,
		`{"type":"assistant","sessionId":"budgetterm","timestamp":"` + old +
			`","message":{"role":"assistant","content":[{"type":"text","text":"pgbouncer runs in transaction mode and prepared statements are off"}]}}`,
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "two.jsonl"), "askedterm", []string{
		`{"type":"user","sessionId":"askedterm","timestamp":"` + old +
			`","message":{"role":"user","content":"why does rsync hang in the nightly deploy script"}}`,
		`{"type":"assistant","sessionId":"askedterm","timestamp":"` + old +
			`","message":{"role":"assistant","content":[{"type":"text","text":"rsync waited on a host key prompt, added StrictHostKeyChecking"}]}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	// Each case starts from an empty seen list, so the project cooldown left by
	// the case before does not hide the session.
	spend := func(sid string) {
		t.Helper()
		if err := os.Remove(index.DefaultDir() + ".hookseen"); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		var b strings.Builder
		for i := 0; i < digestsPerSession; i++ {
			b.WriteString(hookseenKey(sid) + " " + digestSeenPrefix + "f" + strconv.Itoa(i) + "\n")
		}
		f, err := os.OpenFile(index.DefaultDir()+".hookseen", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(b.String()); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	const pooling = "pgbouncer prepared statements in transaction pooling"
	askAbout := func(sid, prompt string) string {
		t.Helper()
		payload := `{"prompt":"` + prompt + `","session_id":"` + sid + `"}`
		var out bytes.Buffer
		if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(payload), &out, true); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	ask := func(sid string) string { t.Helper(); return askAbout(sid, pooling) }

	if got := ask("fresh-1"); !strings.Contains(got, "transaction mode") {
		t.Fatalf("a fresh session did not get the full digest:\n%q", got)
	}
	requireDigestRow(t, "fresh-1")

	spend("long-1")
	got := ask("long-1")
	if !strings.Contains(got, "deja: this project has history on") {
		t.Fatalf("a session past its budget did not get the pointer:\n%q", got)
	}
	if strings.Contains(got, "- **") {
		t.Errorf("a session past its budget still got session entries:\n%q", got)
	}

	// A question asked before is the strong claim and keeps its full block.
	spend("long-2")
	if got := askAbout("long-2", "why does rsync hang in the nightly deploy script"); !strings.Contains(got, "StrictHostKeyChecking") {
		t.Errorf("a repeated question past the budget lost its full block:\n%q", got)
	}

	reader := spawnReaderPrefix + "long-1:abc"
	spend(reader)
	if got := ask(reader); !strings.Contains(got, "- **") {
		t.Errorf("a spawned agent was cut to the pointer:\n%q", got)
	}

	spend("compact-1")
	forgetInjected(index.DefaultDir(), "compact-1")
	if got := ask("compact-1"); !strings.Contains(got, "- **") {
		t.Errorf("after compaction the session did not get a full digest again:\n%q", got)
	}
}

// requireDigestRow fails unless the hook recorded a full digest for sid.
func requireDigestRow(t *testing.T, sid string) {
	t.Helper()
	for id := range alreadyInjected(index.DefaultDir(), sid) {
		if strings.HasPrefix(id, digestSeenPrefix) {
			return
		}
	}
	t.Fatalf("no digest row for %s", sid)
}
