package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/stats"
)

// `deja stats --project payments` narrowed the sessions and printed the
// machine's spans and recall figures beside them. The spans follow the filter;
// the recall figures cannot, and the screen says so.
func TestStatsProjectFilterNarrowsSpansAndLabelsRecall(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	edit := func(id, cwd, file string) {
		dir := filepath.Join(root, strings.ReplaceAll(cwd, "/", "-"))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		lines := []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"user","content":"shorten the pool lifetime"}}`, id, cwd, at),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":%q,"old_string":"cfg.MaxConnLifetime = 30 * time.Minute","new_string":"cfg.MaxConnLifetime = 4 * time.Minute"}}]}}`, id, cwd, at, file),
		}
		if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	edit("pay1", "/work/payments", "/work/payments/pool.go")
	edit("bill1", "/work/billing", "/work/billing/pool.go")
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	all, _, err := index.SpanInventory(dir)
	if err != nil || all != 2 {
		t.Fatalf("premise: the store holds %d spans (%v), want 2", all, err)
	}
	some, _, err := index.SpanInventoryWhere(dir, func(m index.SessionMeta) bool { return m.ID == "pay1" })
	if err != nil || some != 1 {
		t.Fatalf("narrowed to one session: %d spans (%v), want 1", some, err)
	}

	var out bytes.Buffer
	printStats(&out, stats.Report{TotalSessions: 1, TotalMessages: 1, RecallMachineWide: true})
	if !strings.Contains(out.String(), "whole machine") {
		t.Fatalf("filtered stats printed machine-wide recall unlabelled:\n%s", out.String())
	}
}
