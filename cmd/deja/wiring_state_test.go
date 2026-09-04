package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The scenario this exists for: a user installed months ago, the generator
// has been fixed since, and their config still holds the broken version.
// Nobody re-runs an installer that already succeeded, so the repair has to
// happen without being asked.
func TestWiringRefreshesAfterUpgrade(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("DEJA_QWEN_ROOT", "")
	if err := os.MkdirAll(filepath.Join(home, ".qwen"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := installQwenAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	recordWiring([]string{"qwen-auto"}, false)

	// Rewind: the wiring an older deja wrote — the old binary path, and a
	// timeout qwen reads as ten milliseconds.
	settings := filepath.Join(home, ".qwen", "settings.json")
	stale := map[string]any{"hooks": map[string]any{
		"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{
			"type": "command", "command": "/old/deja hook-context", "timeout": 10,
		}}}},
	}}
	b, _ := json.MarshalIndent(stale, "", "  ")
	if err := os.WriteFile(settings, b, 0o644); err != nil {
		t.Fatal(err)
	}
	statePath := wiringStatePath()
	if err := os.WriteFile(statePath, []byte(`{"version":"0.14.0","targets":["qwen-auto"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	changed := refreshWiringAfterUpgrade()
	if len(changed) == 0 {
		t.Fatal("upgrade left the stale wiring in place")
	}
	var got map[string]any
	raw, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	hooks, _ := got["hooks"].(map[string]any)
	if _, live := hooks["UserPromptSubmit"]; !live {
		t.Fatalf("the working hook was not restored: %s", raw)
	}
	// The stale entry is taken over rather than left as it was: same event,
	// this binary, and a timeout in the units qwen actually reads.
	start, _ := hooks["SessionStart"].([]any)
	if len(start) != 1 {
		t.Fatalf("SessionStart entries = %d, want the stale one adopted: %s", len(start), raw)
	}
	entry, _ := start[0].(map[string]any)
	inner, _ := entry["hooks"].([]any)
	h, _ := inner[0].(map[string]any)
	if cmd, _ := h["command"].(string); strings.HasPrefix(cmd, "/old/deja") {
		t.Fatalf("the old binary path survived the upgrade: %s", raw)
	}
	if to, _ := h["timeout"].(float64); to < 1000 {
		t.Fatalf("timeout = %v, still the value qwen reads as ten milliseconds: %s", to, raw)
	}
	// And the record now says this binary owns it, so the repair does not
	// repeat on every session start.
	st := readWiringState()
	if st.Version != version {
		t.Fatalf("state still says %q", st.Version)
	}
	if second := refreshWiringAfterUpgrade(); second != nil {
		t.Fatalf("repair ran twice: %v", second)
	}
}

// Repair, not spread: a harness the user never wired must stay untouched.
func TestWiringRefreshTouchesOnlyRecordedTargets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	if err := os.MkdirAll(filepath.Join(home, ".qwen"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(wiringStatePath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wiringStatePath(), []byte(`{"version":"0.1.0","targets":["qwen-auto"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	refreshWiringAfterUpgrade()
	if _, err := os.Stat(filepath.Join(home, ".kimi-code", "config.toml")); !os.IsNotExist(err) {
		t.Fatal("refresh wired a harness the user never asked for")
	}
}
