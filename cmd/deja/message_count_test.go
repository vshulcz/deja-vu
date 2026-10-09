package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/stats"
)

// One store, three totals: `deja index` counted every record it wrote, `deja
// sources` every record it parsed and `deja stats` the conversation only, so
// the same index read 1340, 1386 and 1233 messages. A command, file or edit
// record is not a message; all three count the same thing now.
func TestIndexSourcesAndStatsCountTheSameMessages(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	proj := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	ts := func(m int) string { return time.Date(2026, 6, 1, 10, m, 0, 0, time.UTC).Format(time.RFC3339) }
	lines := []string{
		`{"type":"user","sessionId":"s1","cwd":"/work/app","timestamp":"` + ts(1) + `","message":{"role":"user","content":"run the pool tests"}}`,
		`{"type":"assistant","sessionId":"s1","cwd":"/work/app","timestamp":"` + ts(2) + `","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./internal/pool/..."}}]}}`,
		`{"type":"user","sessionId":"s1","cwd":"/work/app","timestamp":"` + ts(3) + `","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok  example.com/app/internal/pool"}]}}`,
		`{"type":"assistant","sessionId":"s1","cwd":"/work/app","timestamp":"` + ts(4) + `","message":{"role":"assistant","content":"the pool tests pass"}}`,
	}
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	built := index.LastBuild.Messages

	parsed := sources.CountMessages(sources.LoadClaude())
	ss, err := index.SearchWithRecovery(dir, search.Options{All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	shown := stats.Build(ss, time.Now()).TotalMessages
	if built == 0 || built != parsed || built != shown {
		t.Fatalf("index %d, sources %d, stats %d — one store, three totals", built, parsed, shown)
	}
}
