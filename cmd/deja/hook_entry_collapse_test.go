package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every build that ever ran an install left its own hook entry behind, because
// "ours" was recognised by the exact command string and by a record of paths
// that forgets. On the machine this was found there were six per event —
// `deja-cont`, `deja-sl`, `deja-prog2`, `deja-shapes`, `deja-arm`, `deja-prog`
// — so every prompt started six deja processes and the injected block appeared
// several times, which is what a reader notices first (#3681).
func TestInstallCollapsesEntriesFromDifferentlyNamedBuilds(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	forgetWrittenExes()
	t.Cleanup(forgetWrittenExes)

	// Six builds under six names, none of them in deja's record.
	var entries []any
	for _, name := range []string{"deja-cont", "deja-sl", "deja-prog2", "deja-shapes", "deja-arm", "deja-prog"} {
		entries = append(entries, map[string]any{
			"hooks": []any{map[string]any{
				"type": "command", "command": "/scratch/" + name + " hook-prompt",
			}},
		})
	}
	// And a hook that is not deja's at all, which must survive untouched.
	entries = append(entries, map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": "/usr/local/bin/other hook-prompt"}},
	})
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]any{"hooks": map[string]any{"UserPromptSubmit": entries}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, b, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := installTarget("claude-auto", filepath.Join(home, "bin", "deja"), false); err != nil {
		t.Fatal(err)
	}

	got := dejaHookCommands(t, settings, "UserPromptSubmit")
	var ours, theirs []string
	for _, c := range got {
		if strings.Contains(c, "other") {
			theirs = append(theirs, c)
			continue
		}
		ours = append(ours, c)
	}
	if len(ours) != 1 {
		t.Errorf("deja hooks after install = %d, want 1:\n  %s", len(ours), strings.Join(ours, "\n  "))
	}
	if len(theirs) != 1 {
		t.Errorf("somebody else's hook was touched: %v", got)
	}
}

// A line the reader built around deja's hook is not deja's entry to collapse,
// even when the binary inside it is a differently-named build.
func TestAWrapperAroundADifferentlyNamedBuildIsLeftAlone(t *testing.T) {
	wrapper := "notify-send start && /scratch/deja-cont hook-prompt | tee /tmp/log"
	if got := hookCommandKindOf(wrapper, "deja hook-prompt"); got != hookWrapsDejas {
		t.Errorf("kind = %v, want hookWrapsDejas", got)
	}
	bare := "/scratch/deja-cont hook-prompt"
	if got := hookCommandKindOf(bare, "deja hook-prompt"); got != hookDejas {
		t.Errorf("kind = %v, want hookDejas", got)
	}
	// And a program that merely takes the same subcommand name is nobody's
	// business but its owner's.
	if got := hookCommandKindOf("/usr/local/bin/other hook-prompt", "deja hook-prompt"); got != hookNotDejas {
		t.Errorf("kind = %v, want hookNotDejas", got)
	}
}

// doctor was silent for the same reason: its repeat counter asks the same
// question, so six entries under six names read as one.
func TestDoctorCountsRepeatsFromDifferentlyNamedBuilds(t *testing.T) {
	hooks := map[string]any{"UserPromptSubmit": []any{
		map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "/scratch/deja-cont hook-prompt"}}},
		map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "/scratch/deja-sl hook-prompt"}}},
	}}
	if n := dejaHookCount(hooks, "UserPromptSubmit", "hook-prompt"); n != 2 {
		t.Errorf("count = %d, want 2", n)
	}
	note := doctorHookRepeats(hooks, []struct{ Event, Sub, Matcher string }{
		{"UserPromptSubmit", "hook-prompt", ""},
	}, "claude-auto")
	if !strings.Contains(note, "UserPromptSubmit ×2") {
		t.Errorf("note = %q", note)
	}
}

func dejaHookCommands(t *testing.T, path, event string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	hooks, _ := root["hooks"].(map[string]any)
	entries, _ := hooks[event].([]any)
	var out []string
	for _, e := range entries {
		m, _ := e.(map[string]any)
		hs, _ := m["hooks"].([]any)
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			if c, ok := hm["command"].(string); ok {
				out = append(out, c)
			}
		}
	}
	return out
}
