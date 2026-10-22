package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// codexDejaTimeouts reads the hook timeout under each event of a hooks.json
// that holds only deja's entries.
func codexDejaTimeouts(t *testing.T, path string) map[string]float64 {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Hooks map[string][]struct {
			Hooks []map[string]any `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	out := map[string]float64{}
	for event, groups := range root.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				out[event], _ = h["timeout"].(float64)
			}
		}
	}
	return out
}

// codex-cli 0.149.0 caps SessionEnd hooks at 3 s and prints "clamping
// SessionEnd hook timeout to 3s" on every start when hooks.json asks for more;
// deja wrote 10 there like every other event (#5126).
func TestCodexSessionEndTimeoutFitsCodexCap(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.CodexHome(), "hooks.json")
	if _, err := installCodexHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	got := codexDejaTimeouts(t, path)
	if got["SessionEnd"] != 3 {
		t.Errorf("SessionEnd timeout = %v, want 3 (codex clamps anything above)", got["SessionEnd"])
	}
	for _, h := range codexHookWiring {
		if h.Event != "SessionEnd" && got[h.Event] != 10 {
			t.Errorf("%s timeout = %v, want 10 as before", h.Event, got[h.Event])
		}
	}
}

// A hooks.json written before the fix carries 10; a reinstall rewrites it,
// and a lower value the reader chose stays.
func TestCodexReinstallLowersOldSessionEndTimeout(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.CodexHome(), "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		seed, want float64
	}{{10, 3}, {2, 2}} {
		cmd := hookRun("/usr/local/bin/deja", "hook-session-end")
		seed := mustMarshal(t, map[string]any{"hooks": map[string]any{
			"SessionEnd": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": cmd, "timeout": c.seed}}}},
		}})
		if err := os.WriteFile(path, seed, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := installCodexHooks("/usr/local/bin/deja", false); err != nil {
			t.Fatal(err)
		}
		if got := codexDejaTimeouts(t, path)["SessionEnd"]; got != c.want {
			t.Errorf("seeded %v: SessionEnd timeout after reinstall = %v, want %v", c.seed, got, c.want)
		}
		b, _ := os.ReadFile(path)
		if r, err := installCodexHooks("/usr/local/bin/deja", false); err != nil || r.Action != "unchanged" {
			t.Errorf("seeded %v: a second install = %q, %v; want unchanged", c.seed, r.Action, err)
		}
		if n := dejaHookCount(settingsHooks(t, b), "SessionEnd", "hook-session-end"); n != 1 {
			t.Errorf("seeded %v: SessionEnd runs deja %d times, want once:\n%s", c.seed, n, b)
		}
	}
}
