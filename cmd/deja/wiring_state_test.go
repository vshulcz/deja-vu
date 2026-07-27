package main

import (
	"encoding/json"
	"os"
	"path/filepath"
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

	// Rewind: the wiring an older deja wrote, under an event qwen never
	// consumes, with a timeout it reads as ten milliseconds.
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
	if _, dead := hooks["SessionStart"]; dead {
		t.Fatalf("the abandoned event survived: %s", raw)
	}
	if _, live := hooks["UserPromptSubmit"]; !live {
		t.Fatalf("the working hook was not restored: %s", raw)
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
