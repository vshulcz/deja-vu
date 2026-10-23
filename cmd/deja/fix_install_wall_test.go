package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// An index that refuses a package, then the same install pointed at a mirror:
// `deja fix` on the refusal names the mirror install. It said "no session on
// this machine ran a command after that error" because pip's lines were not
// read as errors at all (#5599).
func TestFixNamesTheMirrorInstallAfterAPipRefusal(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(tmp, "claude", "-work-img")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))

	refusal := "ERROR: Could not find a version that satisfies the requirement vllm==0.6.3 (from versions: none)\nERROR: No matching distribution found for vllm==0.6.3"
	mirror := "pip3 install --index-url https://mirror.example.internal/simple vllm==0.6.3"
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	rows := []string{
		fmt.Sprintf(`{"type":"user","sessionId":"m1","timestamp":%q,"cwd":"/work/img","message":{"role":"user","content":"build the vllm image"}}`, at),
		fmt.Sprintf(`{"type":"assistant","sessionId":"m1","timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"pip3 install vllm==0.6.3"}}]}}`, at),
		fmt.Sprintf(`{"type":"user","sessionId":"m1","timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":%q}]}}`, at, refusal),
		fmt.Sprintf(`{"type":"assistant","sessionId":"m1","timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":%q}}]}}`, at, mirror),
		fmt.Sprintf(`{"type":"user","sessionId":"m1","timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"Successfully installed vllm-0.6.3"}]}}`, at),
	}
	if err := os.WriteFile(filepath.Join(claude, "m1.jsonl"), []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(filepath.Join(tmp, "index.db"), "", true, nil); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		"ERROR: Could not find a version that satisfies the requirement vllm==0.6.3 (from versions: none)",
		"ERROR: No matching distribution found for vllm==0.6.3",
	} {
		out, err := captureRun(t, "fix", line)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, mirror) {
			t.Errorf("fix %q does not name the mirror install:\n%s", line, out)
		}
	}
}
