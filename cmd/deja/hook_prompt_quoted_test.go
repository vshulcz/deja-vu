package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// `opencode run "<question>"` sends the hook the question wrapped in quotes.
// Read as one quoted clause, the instruction in front of the question spent the
// terms and the block was empty, where the same words on stdin found the
// session. The quotes the host added are not the person's.
func TestAPromptTheHostQuotedIsAnsweredLikeTheBareOne(t *testing.T) {
	ask := "without calling any MCP tool and without reading any project files or running commands: how did we install quokkavllm?"
	answer := func(prompt string) string {
		tmp := t.TempDir()
		home := filepath.Join(tmp, "home")
		claude := filepath.Join(tmp, "claude", "proj")
		for _, d := range []string{home, claude} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
		t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
		t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
		t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
		at := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
		rows := fmt.Sprintf(`{"type":"user","sessionId":"q1","timestamp":%q,`+
			`"message":{"role":"user","content":"the quokkavllm wheel install keeps failing on the build box, fix it"}}`+"\n"+
			`{"type":"assistant","sessionId":"q1","timestamp":%q,`+
			`"message":{"role":"assistant","content":[{"type":"text","text":`+
			`"Decided: install quokkavllm from the internal mirror with pip3 install --index-url https://mirror.example.internal/simple quokkavllm"}]}}`, at, at)
		if err := os.WriteFile(filepath.Join(claude, "q1.jsonl"), []byte(rows+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(tmp, "index.db")
		if err := index.Ensure(dir, "", true, nil); err != nil {
			t.Fatal(err)
		}
		cwd := filepath.Join(tmp, "work", "proj")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(cwd)
		payload, err := json.Marshal(map[string]string{"session_id": "oc-1", "cwd": cwd, "prompt": prompt})
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := runHookPrompt(dir, bytes.NewReader(payload), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if bare := answer(ask); !strings.Contains(bare, "mirror.example.internal") {
		t.Fatalf("the bare question recalled nothing, so this measures nothing: %q", bare)
	}
	if quoted := answer(`"` + ask + `"`); !strings.Contains(quoted, "mirror.example.internal") {
		t.Errorf("the question opencode quoted recalled nothing: %q", quoted)
	}
}
