package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// writeDeadHookFixture wires Claude Code's settings at a binary that is not
// there and returns the path it names.
func writeDeadHookFixture(t *testing.T, tmp string) string {
	t.Helper()
	gone := filepath.Join(tmp, "Cellar", "deja-vu", "0.19.3", "bin", "deja")
	path := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{"hooks": map[string]any{
		"SessionStart": []any{map[string]any{"hooks": []any{
			map[string]any{"type": "command", "command": gone + " hook-context"},
		}}},
	}}
	b, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return gone
}

// The state a package upgrade leaves: every hook entry names the binary the
// install ran from, that path is gone, and the surfaces that would report it
// are the hooks themselves. So the screen a person looks at has to say it
// (#3502).
func TestTheBriefSaysWhenHooksRunABinaryThatIsGone(t *testing.T) {
	tmp := hermeticEnv(t)
	gone := writeDeadHookFixture(t, tmp)
	dir := filepath.Join(tmp, "idx")

	note := deadHookNoticeAt(dir, time.Now())
	if !strings.Contains(note, "which is not there") || !strings.Contains(note, "deja install --auto") {
		t.Fatalf("the notice does not name the state or the repair: %q", note)
	}
	if !strings.Contains(note, filepath.Base(filepath.Dir(filepath.Dir(gone)))) {
		t.Errorf("the notice does not name the version the binary came from: %q", note)
	}
}

// Said once a day, not on every command: the check reads every wiring file, and
// a line repeated on a screen someone runs all day is a line they stop reading.
func TestTheDeadHookNoticeIsSaidOnceADay(t *testing.T) {
	tmp := hermeticEnv(t)
	writeDeadHookFixture(t, tmp)
	dir := filepath.Join(tmp, "idx")

	now := time.Now()
	if first := deadHookNoticeAt(dir, now); first == "" {
		t.Fatal("the first run said nothing, so the fixture is wrong")
	}
	if again := deadHookNoticeAt(dir, now.Add(23*time.Hour)); again != "" {
		t.Errorf("said again after 23 hours: %q", again)
	}
	if tomorrow := deadHookNoticeAt(dir, now.Add(25*time.Hour)); tomorrow == "" {
		t.Error("stopped saying it while the hooks are still dead")
	}
}

// A machine whose hooks run a binary that is there hears nothing, including the
// machine with no hooks at all — which is most of them.
func TestTheDeadHookNoticeIsSilentWhenTheBinaryIsThere(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "idx")
	if note := deadHookNoticeAt(dir, time.Now()); note != "" {
		t.Errorf("a machine with no hooks was told its hooks are dead: %q", note)
	}

	here := filepath.Join(tmp, "bin", "deja")
	if err := os.MkdirAll(filepath.Dir(here), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(here, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{"hooks": map[string]any{
		"SessionStart": []any{map[string]any{"hooks": []any{
			map[string]any{"type": "command", "command": here + " hook-context"},
		}}},
	}}
	b, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if note := deadHookNoticeAt(dir, time.Now().Add(48*time.Hour)); note != "" {
		t.Errorf("a live hook was called dead: %q", note)
	}
}
