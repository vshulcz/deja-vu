package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// importedStore builds a machine on the receiving end of `deja sync import`:
// n sessions, every one of them arrived from somewhere else.
func importedStore(t *testing.T, n int) string {
	tmp := hermeticEnv(t)
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	var batch []byte
	for i := 0; i < n; i++ {
		b, err := json.Marshal(index.SyncRecord{
			Harness: "claude", SessionID: "peer" + string(rune('1'+i)), Project: "tmp/projx",
			Role: "user", Text: "the kafka consumer rebalance keeps flapping, take " + string(rune('1'+i)),
		})
		if err != nil {
			t.Fatal(err)
		}
		batch = append(append(batch, b...), '\n')
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), batch, 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writePolicy(t *testing.T, body string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", p)
}

// Someone who sets `auto: local-only` on a machine where everything arrived by
// sync gets silence from their agent, while search and the listing still
// answer — so the rule is the last thing they suspect. The first screen counted
// the sessions as the memory and said nothing about the rule; doctor had the
// number all along, five screens later (#1067).
func TestBriefSaysWhenThePolicyWithholdsEverything(t *testing.T) {
	dir := importedStore(t, 3)
	writePolicy(t, `{"activations":{"auto":{"local":true,"imported":false}}}`)

	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "all 3 sessions") || !strings.Contains(out, "keeps them out of every agent") {
		t.Errorf("brief does not say the rule withholds everything:\n%s", out)
	}
	// The number has to agree with doctor's, which is where the reader goes next.
	withheld, total := policyWithheldCounts(dir)
	if withheld["auto"] != 3 || total != 3 {
		t.Fatalf("fixture wrong: withheld=%d total=%d", withheld["auto"], total)
	}
}

// The line is about the rule emptying the agent's memory, not about having a
// rule: a store the rule lets through says nothing extra, and neither does one
// with no rule at all.
func TestBriefStaysQuietWhenThePolicyWithholdsNothing(t *testing.T) {
	dir := importedStore(t, 3)

	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "keeps them out of every agent") {
		t.Errorf("no policy file, yet the brief claims memory is withheld:\n%s", buf.String())
	}

	// A rule that names this import group allows it through again.
	writePolicy(t, `{"activations":{"auto":{"local":true,"imported":false,"imported:tmp":true}}}`)
	buf.Reset()
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "keeps them out of every agent") {
		t.Errorf("rule withholds nothing, yet the brief says it does:\n%s", buf.String())
	}

	// Partial withholding stays quiet: the counters above are still broadly
	// true, and a caveat on every line is wallpaper.
	local := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"loc","cwd":"/proj","timestamp":"2026-07-11T10:00:00Z","message":{"role":"user","content":"local work on the ticker window"}}` + "\n"
	if err := os.WriteFile(filepath.Join(local, "loc.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	writePolicy(t, `{"activations":{"auto":{"local":true,"imported":false}}}`)
	if withheld, total := policyWithheldCounts(dir); withheld["auto"] == 0 || withheld["auto"] == total {
		t.Fatalf("fixture is not partial: withheld=%d total=%d", withheld["auto"], total)
	}
	buf.Reset()
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "keeps them out of every agent") {
		t.Errorf("partial withholding reported as total:\n%s", buf.String())
	}

	// A rule on the search path does not empty the agent's memory.
	writePolicy(t, `{"activations":{"search":{"local":true,"imported":false}}}`)
	buf.Reset()
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "keeps them out of every agent") {
		t.Errorf("search-path rule reported as withholding from agents:\n%s", buf.String())
	}
}
