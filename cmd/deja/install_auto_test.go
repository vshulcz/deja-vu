package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
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

// Codex sends the same UserPromptSubmit payload Claude does, and it is the only
// event that carries the user's own words. Without it a codex session recalls
// nothing between the session digest and a command it is about to run: measured
// on codex 0.149.0, the event fires on every `codex exec` turn.
func TestInstallCodexWiresThePromptItself(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installCodexHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".codex", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Hooks map[string][]struct {
			Matcher *string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	entries := root.Hooks["UserPromptSubmit"]
	if len(entries) != 1 {
		t.Fatalf("UserPromptSubmit entries = %d, want 1: %s", len(entries), b)
	}
	if got := entries[0].Hooks[0].Command; !strings.HasSuffix(got, "deja hook-prompt") {
		t.Fatalf("command = %q, want deja hook-prompt", got)
	}
	// Codex's own examples carry no matcher on an every-turn event, and an
	// empty pattern is not the same thing as an absent one.
	if entries[0].Matcher != nil {
		t.Fatalf("matcher = %q, want the key left out", *entries[0].Matcher)
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
	// The session id has to travel with the per-prompt payload: recall skips
	// what it already showed a session, and without an id there is nothing to
	// skip by. Measured on a real store, half of all injections were a
	// word-for-word repeat, nearly all within a minute of each other.
	for _, want := range []string{"session_id", "input?.sessionID", "last?.info?.sessionID"} {
		if !strings.Contains(s, want) {
			t.Errorf("opencode plugin does not pass %q, so recall cannot dedupe:\n%s", want, s)
		}
	}
	for _, want := range []string{"experimental.chat.system.transform", "/opt/deja", "hook-context", "cache"} {
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
	// An -auto target writes several files, and what it reports is the first
	// one it changed — reporting the last said "unchanged" about a run that
	// had rewired something else (#2396). The rest ride along in the note.
	for _, tc := range []struct {
		name string
		fn   func(string, bool) (installResult, error)
		want string
		also string
	}{
		{"codex", installCodexAuto, filepath.Join(".codex", "config.toml"), "hooks.json"},
		{"opencode", installOpencodeAuto, filepath.Join(".config", "opencode", "opencode.json"), "deja.js"},
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
			if !strings.Contains(r.Note, tc.also) {
				t.Errorf("the other file it wrote is unmentioned: %#v", r)
			}
			r, err = tc.fn("/bin/deja", true)
			if err != nil {
				t.Fatal(err)
			}
			if r.Path == "" || r.Action == "" {
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
		!strings.Contains(out, "tmp/beta") || !strings.Contains(out, "gamma") {
		t.Fatalf("proof output = %q", out)
	}
	// One line per project, newest first, capped at three.
	if strings.Count(out, "[claude") > 2 {
		t.Fatalf("projects not deduped: %q", out)
	}
}

// TestInstallCodexHooksHonoursCodexHome pins #850: install must write to
// CODEX_HOME (via sources.CodexHome()), not a raw ~/.codex, so a sandboxed
// install cannot edit the operator's real config. HOME and CODEX_HOME point at
// distinct dirs; the hooks file must land under CODEX_HOME and not under HOME.
func TestInstallCodexHooksHonoursCodexHome(t *testing.T) {
	home := t.TempDir()
	codexHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", codexHome)

	r, err := installCodexHooks("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	want := filepath.Join(codexHome, "hooks.json")
	if r.Path != want {
		t.Errorf("install wrote to %q, want %q (CODEX_HOME must be honoured)", r.Path, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("no hooks.json under CODEX_HOME: %v", err)
	}
	// The real (HOME-based) ~/.codex must be left untouched.
	if _, err := os.Stat(filepath.Join(home, ".codex", "hooks.json")); !os.IsNotExist(err) {
		t.Errorf("install wrote to HOME/.codex despite CODEX_HOME being set (err=%v)", err)
	}
}
