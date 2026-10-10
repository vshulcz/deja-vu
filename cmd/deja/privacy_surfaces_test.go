package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Every value below is synthetic.

func privacyLine(sid, role, ts, text string) string {
	b, _ := json.Marshal(map[string]any{
		"type": role, "sessionId": sid, "timestamp": ts, "cwd": "/Users/alicehunt/src/app",
		"message": map[string]any{"role": role, "content": text},
	})
	return string(b)
}

// Colored tool output (grep --color, bat, a colored logger) puts escape
// sequences between a key and its value. Redaction saw no `NAME=value`, and
// every reader stripped the escapes and printed the value whole.
func TestColoredSecretIsMaskedOnEverySurface(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-Users-alicehunt-src-app")
	const val = "Qx7vR2mK9pL4sT8wZ3nB6yH1jF5dG0cA"
	const gh = "ghp_FAKEaaaaBBBBccccDDDDeeeeFFFFgggg1234"
	writeClaudeFixture(t, filepath.Join(store, "ansi1.jsonl"), "ansi1", []string{
		privacyLine("ansi1", "user", "2026-05-01T10:00:00Z", "here is the env:\nDEPLOY_API_TOKEN\x1b[0m=\x1b[33m"+val+"\x1b[0m\nok"),
		privacyLine("ansi1", "assistant", "2026-05-01T10:01:00Z", "the token ghp_\x1b[1m"+gh[4:]+"\x1b[0m is loaded"),
	})
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"show", "ansi1"}, {"recall", "DEPLOY_API_TOKEN"}, {"recall", "token loaded"}} {
		out, err := captureRun(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if strings.Contains(out, val) || strings.Contains(out, gh) {
			t.Errorf("%v printed a secret in the clear:\n%s", args, out)
		}
	}
	exp := filepath.Join(tmp, "exp")
	if _, err := index.ExportFull(dir, exp); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(exp, "*.jsonl"))
	for _, f := range files {
		b, _ := os.ReadFile(f)
		if strings.Contains(string(b), val) {
			t.Errorf("sync export carries the value: %s", b)
		}
	}
}

// share is for pasting to someone else, so it runs the second pass recap
// runs: the account name in a home path and an email do not go out.
func TestShareMasksHomePathAndEmail(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-Users-alicehunt-src-app")
	writeClaudeFixture(t, filepath.Join(store, "home1.jsonl"), "home1", []string{
		privacyLine("home1", "user", "2026-05-01T10:00:00Z", "the config is at /Users/alicehunt/src/app/config.yaml, ping alicehunt@example.org"),
		privacyLine("home1", "assistant", "2026-05-01T10:01:00Z", "fixed /Users/alicehunt/src/app/config.yaml, it now loads"),
	})
	if err := index.Ensure(filepath.Join(tmp, "index.db"), "", true, nil); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "share", "home1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "alicehunt") {
		t.Errorf("share carries the account name / email:\n%s", out)
	}
}
