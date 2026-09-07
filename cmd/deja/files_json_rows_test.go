package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The envelope test that came with `files --json` runs against the synthetic
// fixture, where the query returns no rows on any machine I have checked — so
// its per-row assertions never execute and the ranked branch is unpinned. This
// seeds a store whose recorded paths sit under a directory with a .git in it,
// which is what `inRepository` looks for, and checks the three counts a
// consumer re-ranks on: near touches, the sessions they came from, and the
// total that makes a file specific to the topic rather than merely busy.
func TestFilesJSONCarriesTheRankedRows(t *testing.T) {
	tmp := hermeticEnv(t)
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	render := filepath.Join(repo, "render.go")
	routes := filepath.Join(repo, "routes.go")
	for _, p := range []string{render, routes} {
		if err := os.WriteFile(p, []byte("package app\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	claude := filepath.Join(tmp, "claude")
	proj := filepath.Join(claude, "-repo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// Marshalled rather than pasted together: a Windows path inside a JSON
	// string is full of backslash escapes — `repo\render.go` carries a \r — so
	// a hand-built line parses as nothing there and the store comes up empty.
	rec := func(sid, role string, content any, at string) []byte {
		b, err := json.Marshal(map[string]any{
			"type": role, "sessionId": sid, "cwd": repo, "timestamp": at,
			"message": map[string]any{"role": role, "content": content},
		})
		if err != nil {
			t.Fatal(err)
		}
		return append(b, '\n')
	}
	edit := func(path string) []any {
		return []any{map[string]any{"type": "tool_use", "name": "Edit",
			"input": map[string]any{"file_path": path, "old_string": "a", "new_string": "b"}}}
	}
	// Three sessions on the topic. render.go is touched in all three, routes.go
	// in one, so the ranking has something to order and `sessions` differs
	// between the two rows.
	for i, sid := range []string{"s1", "s2", "s3"} {
		day := string(rune('1' + i))
		body := rec(sid, "user", "the singbox renderer keeps dropping routes", "2026-10-0"+day+"T09:00:00Z")
		body = append(body, rec(sid, "assistant", edit(render), "2026-10-0"+day+"T09:01:00Z")...)
		if sid == "s1" {
			body = append(body, rec(sid, "assistant", edit(routes), "2026-10-01T09:02:00Z")...)
		}
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), body, 0o600); err != nil {
			t.Fatal(err)
		}
		// A seed the parser cannot read indexes as nothing, and the assertions
		// below then fail for a reason that has nothing to do with ranking —
		// which is how this test went red on Windows and nowhere else.
		for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
			if !json.Valid([]byte(line)) {
				t.Fatalf("the seeded transcript is not JSON: %s", line)
			}
		}
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "files", "--json", "singbox")
	if err != nil {
		t.Fatalf("files --json: %v\n%s", err, out)
	}
	var env struct {
		SessionsScanned int `json:"sessions_scanned"`
		Matched         int `json:"matched"`
		Files           []struct {
			Path     string `json:"path"`
			Near     int    `json:"near"`
			Sessions int    `json:"sessions"`
			Total    int    `json:"total"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(env.Files) == 0 {
		t.Fatalf("the ranked branch returned no rows, so nothing here is pinned:\n%s", out)
	}
	if env.SessionsScanned == 0 || env.Matched == 0 {
		t.Fatalf("rows without the counts behind them: %+v\n%s", env, out)
	}
	byPath := map[string]int{}
	for _, f := range env.Files {
		byPath[f.Path] = f.Sessions
		if f.Near <= 0 {
			t.Fatalf("%s is in the list with no near touch: %+v", f.Path, f)
		}
		if f.Total < f.Near {
			t.Fatalf("%s has total %d below near %d", f.Path, f.Total, f.Near)
		}
	}
	// render.go was touched in three sessions and routes.go in one: the counts
	// a consumer re-ranks on have to carry that difference, not just be
	// present.
	if byPath[render] != 3 {
		t.Fatalf("render.go reports %d sessions, want 3:\n%s", byPath[render], out)
	}
	if got, ok := byPath[routes]; ok && got != 1 {
		t.Fatalf("routes.go reports %d sessions, want 1:\n%s", got, out)
	}
}
