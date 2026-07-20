package main

import (
	"encoding/json"
	"github.com/vshulcz/deja-vu/internal/index"
	"io"
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

func TestInstallCodexHooksErrorsAndMissingUninstall(t *testing.T) {
	t.Run("malformed json", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		path := filepath.Join(home, ".codex", "hooks.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"hooks":`), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := installCodexHooks("/bin/deja", false); err == nil {
			t.Fatal("expected malformed hooks.json error")
		}
	})

	t.Run("uninstall missing file", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		r, err := installCodexHooks("/bin/deja", true)
		if err != nil {
			t.Fatal(err)
		}
		if r.Path != filepath.Join(home, ".codex", "hooks.json") || r.Action != "created" {
			t.Fatalf("result = %#v", r)
		}
	})
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

func TestInstallOpencodePluginUninstallMissingDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	r, err := installOpencodePlugin("/opt/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if r.Action != "unchanged" || r.Path != filepath.Join(home, ".config", "opencode", "plugins", "deja.js") {
		t.Fatalf("result = %#v", r)
	}
}

func TestInstallAutoWrappers(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   func(string, bool) (installResult, error)
		want string
	}{
		{"codex", installCodexAuto, filepath.Join(".codex", "hooks.json")},
		{"opencode", installOpencodeAuto, filepath.Join(".config", "opencode", "plugins", "deja.js")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			r, err := tc.fn("/bin/deja", false)
			if err != nil {
				t.Fatal(err)
			}
			if r.Action != "created" || r.Path != filepath.Join(home, tc.want) {
				t.Fatalf("install result = %#v", r)
			}
			r, err = tc.fn("/bin/deja", true)
			if err != nil {
				t.Fatal(err)
			}
			if r.Path != filepath.Join(home, tc.want) {
				t.Fatalf("uninstall result = %#v", r)
			}
		})
	}
}

func TestPrintInstallProofListsDistinctProjects(t *testing.T) {
	withStatsStores(t)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	printInstallProof(index.DefaultDir())
	_ = w.Close()
	os.Stderr = old
	b, _ := io.ReadAll(r)
	out := string(b)
	if !strings.Contains(out, "deja already knows this machine:") ||
		!strings.Contains(out, filepath.Join("tmp", "beta")) || !strings.Contains(out, "gamma") {
		t.Fatalf("proof output = %q", out)
	}
	// One line per project, newest first, capped at three.
	if strings.Count(out, "[claude") > 2 {
		t.Fatalf("projects not deduped: %q", out)
	}
}
