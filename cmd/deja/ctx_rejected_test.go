package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Search demotes a session the reader rejected and says why; ctx and
// recall_context ranked on their own and handed that same session to the agent
// as the answer (#1099, the shape of #974 on two more surfaces).
func TestCtxDoesNotServeARejectedSession(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(id, when, text string) {
		rec := `{"type":"user","message":{"role":"user","content":"` + text + `"},"timestamp":"` + when + `","sessionId":"` + id + `","cwd":"/proj"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("olddec", "2026-06-01T10:00:00Z", "we cap the retry budget at three attempts")
	write("newdec", "2026-08-01T10:00:00Z", "we raised the retry budget to ten attempts")
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "promote", "newdec", "--state", "rejected", "--note", "rolled back, three stands"); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := cmdCtx(dir, []string{"retry", "budget"}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, "newdec") {
		t.Errorf("ctx served the session the reader rejected:\n%s", out)
	}
	if !strings.Contains(out, "olddec") {
		t.Errorf("ctx did not fall through to the decision that stands:\n%s", out)
	}
}
