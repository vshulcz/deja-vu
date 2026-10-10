package index

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// postingShape is what a bucket says about a token, minus the offsets and
// ordinals two builds are free to assign differently.
func postingShape(t *testing.T, dir string) map[string][2]int {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "buckets", "*.bin"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][2]int{}
	for _, f := range files {
		data, err := readBucket(f)
		if err != nil {
			t.Fatal(err)
		}
		for tok, ps := range data {
			var c [2]int
			for _, p := range ps {
				if p.Tool {
					c[1]++
				} else {
					c[0]++
				}
			}
			out[tok] = c
		}
	}
	return out
}

// An incremental pass that rewrites the record log carries every record it
// keeps into a new one. Those records get the postings a full build gives
// them: the written side's hashes and a replaced span's body are not tokens,
// and a tool record keeps its tool bit.
func TestARewritingPassPostsWhatARebuildPosts(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-Users-me-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(proj, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s1 := `{"type":"user","sessionId":"s1","timestamp":"2026-06-02T03:04:05Z","message":{"role":"user","content":"write the pool config"}}` + "\n" +
		`{"type":"assistant","sessionId":"s1","timestamp":"2026-06-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Write","input":{"file_path":"/Users/me/app/pool.go","content":"func retireConnectionsBeforeTheProxyDoes() {\n\tpool.MaxConnLifetime = 4 * time.Minute\n}\n"}}]}}` + "\n" +
		`{"type":"assistant","sessionId":"s1","timestamp":"2026-06-02T03:04:07Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu2","name":"Bash","input":{"command":"go test ./pool"}}]}}` + "\n" +
		`{"type":"user","sessionId":"s1","timestamp":"2026-06-02T03:04:08Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu2","content":"ok  example.com/app/pool 0.4s"}]}}` + "\n"
	write("s1.jsonl", s1)
	write("s2.jsonl", `{"type":"user","sessionId":"s2","timestamp":"2026-06-03T03:04:05Z","message":{"role":"user","content":"first draft"}}`+"\n")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "no-cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "no-aider"))
	live := filepath.Join(tmp, "live.db")
	if err := Ensure(live, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	// Rewritten rather than appended to, so the pass carries s1 into a new log.
	write("s2.jsonl", `{"type":"user","sessionId":"s2","timestamp":"2026-06-03T03:04:05Z","message":{"role":"user","content":"second draft, rewritten"}}`+"\n")
	if err := Ensure(live, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(tmp, "fresh.db")
	if err := Ensure(fresh, "claude", true, nil); err != nil {
		t.Fatal(err)
	}
	got, want := postingShape(t, live), postingShape(t, fresh)
	var diff []string
	for tok, w := range want {
		if got[tok] != w {
			diff = append(diff, tok)
		}
	}
	for tok := range got {
		if _, ok := want[tok]; !ok {
			diff = append(diff, tok)
		}
	}
	sort.Strings(diff)
	if len(diff) > 0 {
		for _, tok := range diff[:min(10, len(diff))] {
			t.Errorf("%q: incremental %v, rebuild %v (speech, tool)", tok, got[tok], want[tok])
		}
	}
}
