package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// `deja log` is the record of what an agent was handed, and the two tools that
// serve commands were the two it never mentioned (#2858). The kind list already
// argues the case twice — blame and the resource read were added because
// leaving them out "understated what the agent was given" (#682), and remember
// because a write "showed up nowhere in the journal the user reads".
//
// Recorded, and deliberately not counted as memory served: `servedKind` decides
// the ratios `deja stats` publishes, and a one-line command is not the store's
// text handed over. That half is asserted below too, because the last time
// these drifted a blame was a recall on one screen and nothing on the other
// (#1907).
func TestHowAndFixReachTheJournal(t *testing.T) {
	tmp := hermeticEnv(t)
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	const errLine = "ld: symbol(s) not found for architecture arm64"
	for i, sid := range []string{"s1", "s2"} {
		day := string(rune('1' + i))
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/app","timestamp":"2026-08-0` + day + `T09:00:00Z","message":{"role":"user","content":"deploying the zonkomatic"}}
{"type":"user","sessionId":"` + sid + `","cwd":"/app","timestamp":"2026-08-0` + day + `T09:01:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"` + errLine + `"}]}}
{"type":"assistant","sessionId":"` + sid + `","cwd":"/app","timestamp":"2026-08-0` + day + `T09:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"helm upgrade zonkomatic --set replicas=9"}}]}}
`
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	kinds := func() map[string]int {
		t.Helper()
		out := map[string]int{}
		b, err := os.ReadFile(usage.Path(dir))
		if err != nil {
			return out
		}
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			var ev struct {
				Kind string `json:"kind"`
			}
			if json.Unmarshal([]byte(line), &ev) == nil {
				out[ev.Kind]++
			}
		}
		return out
	}
	// The number `deja stats` publishes: recalls counted as memory served.
	served := func() int {
		t.Helper()
		n, _, _ := usage.TodayWithInjections(dir)
		return n
	}

	before := served()
	text, err := callMCPTool(dir, "how", json.RawMessage(`{"what":"zonkomatic"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "helm upgrade") {
		t.Fatalf("how served nothing, so there is nothing to journal:\n%s", text)
	}
	text, err = callMCPTool(dir, "fix", json.RawMessage(`{"error":"`+errLine+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "helm upgrade") {
		t.Fatalf("fix served nothing, so there is nothing to journal:\n%s", text)
	}

	got := kinds()
	if got[usage.KindHow] != 1 {
		t.Errorf("how is not in the journal: %v", got)
	}
	if got[usage.KindFix] != 1 {
		t.Errorf("fix is not in the journal: %v", got)
	}
	// And the number deja publishes has not moved: a command offered is not
	// the store's text served.
	if after := served(); after != before {
		t.Errorf("the served count moved from %d to %d — the ratios in `deja stats` changed with it", before, after)
	}
}
