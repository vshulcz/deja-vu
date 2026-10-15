package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Claude Code deletes transcripts after cleanupPeriodDays, 30 unless set, and
// says nothing; install is where a person hears deja keeps them. The setting
// is read, never written.
func TestInstallSaysClaudeCodeDeletesTranscriptsAndDejaKeepsThem(t *testing.T) {
	cases := []struct {
		name, settings, want string
	}{
		{"no setting", ``, "Claude Code deletes transcripts after 30 days; deja keeps a copy of each one it indexes from now on"},
		{"a short period", `{"cleanupPeriodDays": 7}`, "Claude Code deletes transcripts after 7 days; deja keeps a copy of each one it indexes from now on"},
		{"a long period", `{"cleanupPeriodDays": 3650}`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hermeticEnv(t)
			cfg := filepath.Join(t.TempDir(), "profile")
			t.Setenv("CLAUDE_CONFIG_DIR", cfg)
			settings := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
			if c.settings != "" {
				if err := os.MkdirAll(cfg, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(settings, []byte(c.settings), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			out, err := captureRun(t, "install", "claude-code", "claude-auto", "--no-index")
			if err != nil {
				t.Fatal(err)
			}
			if c.want == "" {
				if strings.Contains(out, "deletes transcripts") {
					t.Errorf("a %s period got the line:\n%s", c.name, out)
				}
			} else if n := strings.Count(out, c.want); n != 1 {
				t.Errorf("the line is said %d times, want once:\n%s", n, out)
			}
			if b, _ := os.ReadFile(settings); strings.Contains(string(b), "cleanupPeriodDays") != strings.Contains(c.settings, "cleanupPeriodDays") {
				t.Errorf("install changed cleanupPeriodDays:\n%s", b)
			}
		})
	}
	// Another agent's install is not about Claude Code.
	hermeticEnv(t)
	out, err := captureRun(t, "install", "gemini", "--no-index")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "deletes transcripts") {
		t.Errorf("a gemini install talked about Claude Code:\n%s", out)
	}
}
