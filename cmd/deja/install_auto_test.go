package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallCodexHooksMergeAndUninstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// pre-existing foreign hook must survive install and uninstall
	seed := `{"hooks":{"SessionStart":[{"matcher":"startup","hooks":[{"type":"command","command":"other-tool ctx"}]}]}}`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := installCodexHooks("/usr/local/bin/deja", false)
	if err != nil || r.Action != "updated" {
		t.Fatalf("install: %v %v", r, err)
	}
	b, _ := os.ReadFile(path)
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	entries := root["hooks"].(map[string]any)["SessionStart"].([]any)
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want foreign + ours", len(entries))
	}
	if !strings.Contains(string(b), "other-tool ctx") || !strings.Contains(string(b), "deja hook-context") {
		t.Fatalf("merged file wrong: %s", b)
	}
	// idempotent
	r, err = installCodexHooks("/usr/local/bin/deja", false)
	if err != nil || r.Action != "unchanged" {
		t.Fatalf("second install: %v %v", r, err)
	}
	// uninstall removes only ours
	r, err = installCodexHooks("/usr/local/bin/deja", true)
	if err != nil || r.Action != "updated" {
		t.Fatalf("uninstall: %v %v", r, err)
	}
	b, _ = os.ReadFile(path)
	if strings.Contains(string(b), "deja hook-context") || !strings.Contains(string(b), "other-tool ctx") {
		t.Fatalf("uninstall wrong: %s", b)
	}
}

func TestInstallOpencodePlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	r, err := installOpencodePlugin("/opt/deja", false)
	if err != nil || r.Action != "created" {
		t.Fatalf("install: %v %v", r, err)
	}
	b, err := os.ReadFile(r.Path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"experimental.chat.system.transform", "/opt/deja", "hook-context --plain", "cache"} {
		if !strings.Contains(s, want) {
			t.Fatalf("plugin missing %q:\n%s", want, s)
		}
	}
	if r, _ := installOpencodePlugin("/opt/deja", false); r.Action != "unchanged" {
		t.Fatalf("second install action = %s", r.Action)
	}
	if r, _ := installOpencodePlugin("/opt/deja", true); r.Action != "removed" {
		t.Fatalf("uninstall action = %s", r.Action)
	}
	if r, _ := installOpencodePlugin("/opt/deja", true); r.Action != "unchanged" {
		t.Fatalf("second uninstall action = %s", r.Action)
	}
}
