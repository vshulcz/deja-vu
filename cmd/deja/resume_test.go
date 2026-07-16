package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestResumeCommandPerHarness(t *testing.T) {
	tmp := t.TempDir()
	real := filepath.Join(tmp, "projects", "my-app")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	encoded := strings.ReplaceAll(real, string(filepath.Separator), "-")
	claudePath := filepath.Join("/claude/projects", encoded, "abc.jsonl")

	cases := []struct {
		name    string
		s       model.Session
		wantDir string
		wantCmd string
		wantErr string
	}{
		{"claude with resolvable dir", model.Session{Harness: "claude", ID: "abc-123", Project: "projects/my-app", Path: claudePath}, real, "claude --resume abc-123", ""},
		{"codex rollout", model.Session{Harness: "codex", ID: "uuid-1", Project: "my-app"}, "", "codex resume uuid-1", ""},
		{"codex history entry", model.Session{Harness: "codex", ID: "uuid-2", Project: "history"}, "", "", "nothing to resume"},
		{"opencode with dir", model.Session{Harness: "opencode", ID: "ses_1", Project: "my-app", Path: real}, real, "opencode -s ses_1", ""},
		{"imported", model.Session{Harness: "claude", ID: "imported-9f5", Project: "imported:my-app"}, "", "", "another machine"},
		{"unknown harness", model.Session{Harness: "mystery", ID: "x"}, "", "", "don't know how"},
	}
	for _, c := range cases {
		if runtime.GOOS == "windows" && c.name == "claude with resolvable dir" {
			continue // claude encodes unix-style absolute paths; resolution is a no-op on windows
		}
		dir, cmd, err := resumeCommand(c.s)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("%s: err = %v, want %q", c.name, err, c.wantErr)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if dir != c.wantDir || cmd != c.wantCmd {
			t.Fatalf("%s: got (%q, %q), want (%q, %q)", c.name, dir, cmd, c.wantDir, c.wantCmd)
		}
	}
}
