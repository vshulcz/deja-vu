package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// seedFrictionCorpus writes sessions that both fill a recall page and give the
// environment block three walls to name, so one call carries both.
func seedFrictionCorpus(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	// One directory per project: the environment block claims something about
	// the machine, and a wall of one repository no longer qualifies.
	var stores []string
	for i := 0; i < environmentMinProjects; i++ {
		store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj"+string(rune('0'+i)))
		if err := os.MkdirAll(store, 0o755); err != nil {
			t.Fatal(err)
		}
		stores = append(stores, store)
	}
	errs := []string{
		"command not found: quibblectl",
		"undefined: snorblefunc in vendor/blarg/api.go",
		"command not found: frobnicate",
	}
	for s := range 12 {
		var b strings.Builder
		for i := range 16 {
			stamp := "2026-08-0" + string(rune('1'+s%9)) + "T1" + string(rune('0'+i%10)) + ":00:00Z"
			id := "sess" + string(rune('a'+s))
			if i == 3 {
				b.WriteString(`{"type":"user","timestamp":"` + stamp + `","sessionId":"` + id +
					`","cwd":"/proj` + string(rune('0'+(s/3)%environmentMinProjects)) + `","message":{"role":"user","content":[{"type":"tool_result","content":"` +
					errs[s%3] + `"}]},"toolUseResult":{"stdout":"","stderr":"` + errs[s%3] + `"}}` + "\n")
				continue
			}
			role := "user"
			if i%2 == 1 {
				role = "assistant"
			}
			b.WriteString(`{"type":"` + role + `","timestamp":"` + stamp + `","sessionId":"` + id +
				`","cwd":"/proj` + string(rune('0'+(s/3)%environmentMinProjects)) + `","message":{"role":"` + role + `","content":"` +
				strings.Repeat("quibblectl retry budget ", 24) + `"}}` + "\n")
		}
		if err := os.WriteFile(filepath.Join(stores[(s/3)%len(stores)], "sess"+string(rune('a'+s))+".jsonl"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The environment block used to go on after the frame, so the first recall of a
// session — the one an agent plans against — was over its cap by whatever the
// block weighed (#1806).
func TestTheFirstRecallOfASessionFitsItsBudget(t *testing.T) {
	dir := seedFrictionCorpus(t)
	// The block is served once per process and once per 15 minutes per store;
	// another test in this package may have spent either. Both are reset so the
	// call under test is a session's first one.
	resetEnvironmentOnce()
	_ = os.Remove(filepath.Join(filepath.Dir(dir), filepath.Base(dir)+".envblock"))
	out, err := callMCPTool(dir, "recall", []byte(`{"query":"quibblectl retry budget"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "environment facts") {
		t.Fatalf("the fixture did not produce an environment block, so this proves nothing:\n%s", out[max(0, len(out)-300):])
	}
	if len(out) > 4096 {
		t.Errorf("the first recall is %d bytes against a 4096 cap", len(out))
	}
	if len(out) < 3000 {
		t.Errorf("%d bytes is too small to be a full page; the fixture stopped filling", len(out))
	}
	if !strings.Contains(out, "quibblectl retry budget") {
		t.Errorf("the page lost its excerpts to make room for the block:\n%s", out)
	}
}

// The block is spent on delivery, not on being asked for: a recall that fails
// must not cost the session its one block.
func TestAFailedRecallDoesNotSpendTheEnvironmentBlock(t *testing.T) {
	dir := seedFrictionCorpus(t)
	resetEnvironmentOnce()
	_ = os.Remove(filepath.Join(filepath.Dir(dir), filepath.Base(dir)+".envblock"))

	env, _ := environmentOnce(dir)
	if env == "" {
		t.Fatal("the fixture has no block, so this proves nothing")
	}
	// Asked for and not delivered: the next caller still gets it.
	again, deliver := environmentOnce(dir)
	if again == "" {
		t.Error("a block that was never delivered was counted as served")
	}
	deliver()
	if after, _ := environmentOnce(dir); after != "" {
		t.Error("the block was served twice in one process")
	}
}
