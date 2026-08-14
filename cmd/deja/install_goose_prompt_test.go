package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGooseHookCoversTheUserPrompt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if err := writeGooseHook(); err != nil {
		t.Fatalf("write: %v", err)
	}
	b, err := os.ReadFile(gooseHookPath())
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("hooks.json is not valid JSON: %v", err)
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit"} {
		entries, ok := doc.Hooks[event]
		if !ok || len(entries) == 0 || len(entries[0].Hooks) == 0 {
			t.Fatalf("no %s hook:\n%s", event, b)
		}
		if !strings.Contains(entries[0].Hooks[0].Command, "hook-goose") {
			t.Errorf("%s runs %q", event, entries[0].Hooks[0].Command)
		}
	}
	if cmd := doc.Hooks["UserPromptSubmit"][0].Hooks[0].Command; !strings.Contains(cmd, "hook-goose-prompt") {
		t.Errorf("the prompt hook runs the session-start path: %q", cmd)
	}
}

// Only the MOIM file is re-read per turn; .goosehints is read once, when the
// session opens. Rewriting the hints file mid-session would replace recall the
// model has already been given with text it will never see.
func TestGoosePromptHookLeavesTheHintsFileAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", "")

	hints := gooseHintsPath()
	if err := os.MkdirAll(filepath.Dir(hints), 0o755); err != nil {
		t.Fatal(err)
	}
	const opening = "what the session opened with\n"
	if err := os.WriteFile(hints, []byte(opening), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := refreshGooseForPrompt(t.TempDir(), []byte(`{"message":"why is the pool exhausted?"}`)); err != nil {
		t.Fatalf("prompt hook: %v", err)
	}
	after, err := os.ReadFile(hints)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != opening {
		t.Fatalf("the hints file was rewritten mid-session:\n%s", after)
	}
}

// A prompt the history has nothing for must leave whatever is already in front
// of the model in place, rather than blanking it.
func TestGoosePromptHookKeepsTheDigestWhenItHasNothingToSay(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	moim := filepath.Join(home, "recall.md")
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", moim)

	const digest = "the digest from session start\n"
	if err := os.WriteFile(moim, []byte(digest), 0o644); err != nil {
		t.Fatal(err)
	}
	// An empty index has nothing to say about anything.
	if err := refreshGooseForPrompt(t.TempDir(), []byte(`{"message":"pgbouncer pool sizing"}`)); err != nil {
		t.Fatalf("prompt hook: %v", err)
	}
	after, err := os.ReadFile(moim)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != digest {
		t.Fatalf("a silent recall blanked the file:\n%s", after)
	}
}
