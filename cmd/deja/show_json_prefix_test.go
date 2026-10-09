package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// `deja show <prefix> --json` refused for want of --harness even when the
// prefix named one session, so a script piping to jq got an error for an id
// the text form resolved. A prefix that names one session is answered; one
// that names more than one is still refused, because a machine reader cannot
// notice it was handed the wrong one.
func TestShowJSONAnswersAUniquePrefixAndRefusesAnAmbiguousOne(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude", "-work-app")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	for _, id := range []string{"abc111", "abc222"} {
		line := fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/app",`+
			`"message":{"role":"user","content":"the retry budget for uploads"}}`, id, at)
		if err := os.WriteFile(filepath.Join(claude, id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	var runErr error
	out := captureStdout(t, func() { runErr = cmdShow(dir, []string{"abc1", "--json"}, "") })
	if runErr != nil {
		t.Fatalf("a unique prefix with --json was refused: %v", runErr)
	}
	var got struct {
		Session struct {
			ID      string `json:"id"`
			Harness string `json:"harness"`
		} `json:"session"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.Session.ID != "abc111" || got.Session.Harness != "claude" {
		t.Fatalf("unique prefix answered %+v (%v):\n%s", got, err, out)
	}

	err := cmdShow(dir, []string{"abc", "--json"}, "")
	if err == nil || !strings.Contains(err.Error(), "2 sessions match") || !strings.Contains(err.Error(), "longer prefix") {
		t.Fatalf("an ambiguous prefix with --json was answered as: %v", err)
	}
}
