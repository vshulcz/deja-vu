package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A full page is where offset exists to be used, and it was the page that lost
// the line naming the offset — the excerpts have to give way instead (#1726).
func TestFullRecallPageKeepsThePagingLine(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "zqxwv widget pipeline stalled. " + strings.Repeat("detail ", 400)
	for i := range 12 {
		line := func(role, text string) string {
			if role == "user" {
				return fmt.Sprintf(`{"type":"user","sessionId":"sess-%02d","cwd":"/work/zq","timestamp":"2026-08-%02dT03:04:05Z","message":{"role":"user","content":%q}}`, i, i+1, text)
			}
			return fmt.Sprintf(`{"type":"assistant","sessionId":"sess-%02d","cwd":"/work/zq","timestamp":"2026-08-%02dT03:05:05Z","message":{"role":"assistant","content":[{"type":"text","text":%q}]}}`, i, i+1, text)
		}
		content := line("user", "why does zqxwv fail? "+body) + "\n" + line("assistant", "zqxwv resolved by resharding. "+body) + "\n"
		if err := os.WriteFile(filepath.Join(proj, fmt.Sprintf("s%02d.jsonl", i)), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(t.TempDir(), "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(t.TempDir(), "opencode.db"))
	dir := filepath.Join(t.TempDir(), "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)

	const budget = 4096
	out, served, _, _, err := recallTextResult(dir, "zqxwv", "", 0, 0, budget)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > budget {
		t.Fatalf("page is %d bytes, over the %d budget", len(out), budget)
	}
	if served == 0 || served >= 12 {
		t.Fatalf("fixture served %d of 12 — it does not fill a page", served)
	}
	want := fmt.Sprintf("offset=%d", served)
	if !strings.Contains(out, "more match(es)") || !strings.Contains(out, want) {
		t.Errorf("a full page dropped the paging line (want %q), tail was:\n%q", want, out[max(0, len(out)-200):])
	}
}

// A budget smaller than the paging line itself must not trim to a negative
// length: the page is what there is room for, the instruction is what goes.
func TestTinyBudgetDropsTheLineRatherThanPanicking(t *testing.T) {
	if got := trimUTF8("hello world", -5); got != "" {
		t.Errorf("trimUTF8 with a negative budget = %q", got)
	}
	if got := trimUTF8("hello", 0); got != "" {
		t.Errorf("trimUTF8 with a zero budget = %q", got)
	}
}
