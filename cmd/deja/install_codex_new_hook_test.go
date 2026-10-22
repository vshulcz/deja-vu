package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// A machine that approved deja's older hooks gets SessionEnd from an install
// or an upgrade, and codex runs it only once approved in /hooks. Install
// said so only when no hook was approved, so here it said nothing and only
// doctor counted 5 of 6 (#4572).
func TestCodexInstallNamesTheHooksLeftToApprove(t *testing.T) {
	for _, c := range []struct {
		name   string
		events []string
		want   string
	}{
		{"older five approved", []string{"session_start", "user_prompt_submit", "pre_tool_use", "post_tool_use", "pre_compact"}, "1 of 6 hooks is new or changed"},
		{"none approved", nil, "open codex once and approve the hook (/hooks)"},
		{"all approved", []string{"session_start", "user_prompt_submit", "pre_tool_use", "post_tool_use", "pre_compact", "session_end"}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			hermeticEnv(t)
			home := sources.CodexHome()
			if err := os.MkdirAll(home, 0o755); err != nil {
				t.Fatal(err)
			}
			hooks := filepath.Join(home, "hooks.json")
			cfg := "model = \"gpt-5\"\n"
			var cfgSb35 strings.Builder
			for _, e := range c.events {
				cfgSb35.WriteString("\n[hooks.state." + strconv.Quote(hooks+":"+e+":0:0") + "]\ntrusted_hash = \"sha256:00\"\n")
			}
			cfg += cfgSb35.String()
			if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o644); err != nil {
				t.Fatal(err)
			}
			out := captureStdout(t, func() {
				if _, err := installCodexAuto("/usr/local/bin/deja", false); err != nil {
					t.Fatalf("install codex-auto: %v", err)
				}
			})
			said := ""
			var saidSb47 strings.Builder
			for l := range strings.SplitSeq(out, "\n") {
				if strings.HasPrefix(l, "codex: ") {
					saidSb47.WriteString(l)
				}
			}
			said += saidSb47.String()
			if c.want == "" && said != "" || c.want != "" && !strings.Contains(said, c.want) {
				t.Errorf("install said %q, want %q", said, c.want)
			}
		})
	}
}
