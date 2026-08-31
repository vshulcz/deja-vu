package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// Every path deja writes hangs off the home directory, and it answers "" when
// there is none — so `filepath.Join("", ".cache", "deja")` is `.cache/deja`,
// and with HOME unset deja built its index in whatever directory it was run
// from. #1690 fixed install; the hooks and `index` still wrote relative, and
// the hook is the path that runs unattended on every prompt (#1692).
func TestWithNoHomeNothingIsWrittenWhereDejaHappensToRun(t *testing.T) {
	for _, c := range []struct {
		args  []string
		stdin string
		// A hook must never cost a turn, so it declines quietly; a command
		// somebody typed says why.
		refuses bool
	}{
		{args: []string{"hook-context"}, stdin: "{}"},
		{args: []string{"hook-prompt"}, stdin: `{"prompt":"anything"}`},
		{args: []string{"hook-tool"}, stdin: `{"tool_name":"Bash","tool_input":{"command":"ls"}}`},
		{args: []string{"hook-precompact"}, stdin: "{}"},
		{args: []string{"index"}, refuses: true},
		{args: []string{"sources"}, refuses: true},
	} {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			wd := t.TempDir()
			t.Chdir(wd)
			t.Setenv("HOME", "")
			t.Setenv("USERPROFILE", "")
			t.Setenv("XDG_CONFIG_HOME", "")
			t.Setenv("XDG_CACHE_HOME", "")
			t.Setenv("DEJA_INDEX_DIR", "")
			if c.stdin != "" {
				withHookStdin(t, c.stdin)
			}

			_, err := captureRun(t, c.args...)
			if c.refuses {
				if err == nil {
					t.Error("a command that cannot find a home directory reported success")
				} else if !strings.Contains(err.Error(), "home directory") {
					t.Errorf("the refusal does not say what is missing: %v", err)
				}
			} else if err != nil {
				t.Errorf("a hook cost the turn instead of declining quietly: %v", err)
			}

			left, rerr := filepath.Glob(filepath.Join(wd, "*"))
			if rerr != nil {
				t.Fatal(rerr)
			}
			if len(left) > 0 {
				t.Errorf("deja wrote into the working directory: %v", left)
			}
		})
	}
}
