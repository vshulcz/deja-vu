package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// ctx takes no flags, and --json/--harness exist on neighbouring commands — so
// folding one into the query answered "no session matches", a false statement
// about the store (#721).
func TestCtxRejectsFlagsInsteadOfSearchingForThem(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	body := `{"type":"user","sessionId":"zz1","cwd":"/w/p","timestamp":"2026-07-22T10:00:00Z","message":{"role":"user","content":"a session about the pool"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "zz1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"ctx", "pool", "--json"},
		{"ctx", "--harness", "qwen", "pool"},
		{"ctx", "--limit", "3", "pool"},
	} {
		_, err := captureRun(t, args...)
		if err == nil {
			t.Errorf("%v succeeded", args)
			continue
		}
		if !strings.Contains(err.Error(), "ctx takes no flags") {
			t.Errorf("%v: %v", args, err)
		}
		if strings.Contains(err.Error(), "no session matches") {
			t.Errorf("%v still blamed the store: %v", args, err)
		}
	}

	// A query is still a query, including one with a dash inside it.
	out, err := captureRun(t, "ctx", "pool")
	if err != nil || !strings.Contains(out, "a session about the pool") {
		t.Fatalf("plain query: %q err=%v", out, err)
	}
	if _, err := captureRun(t, "ctx", "a --dash inside"); err == nil || !strings.Contains(err.Error(), "no session matches") {
		t.Errorf("a phrase containing a dash must stay a query: %v", err)
	}
	// One dash is not a flag here: "-1" and "-v" are things people search for,
	// and ctx has no single-dash options to confuse them with.
	if _, err := captureRun(t, "ctx", "-1 rollback"); err == nil || !strings.Contains(err.Error(), "no session matches") {
		t.Errorf("single-dash argument must stay a query: %v", err)
	}
	// A missing session is still reported as missing.
	if _, err := captureRun(t, "ctx", "nosuchthing"); err == nil || !strings.Contains(err.Error(), "no session matches") {
		t.Errorf("missing session: %v", err)
	}
}
