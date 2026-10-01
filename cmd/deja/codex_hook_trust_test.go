package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Codex pins each approved hook in config.toml by its place in hooks.json. An
// uninstall left the pins for deja's hooks behind, so the file did not come
// back and a reinstall started trusted without a review (#4183). The reader's
// own hook moves up when deja's goes, and its pin has to move with it.
func TestUninstallTakesCodexTrustPinsWithTheHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("DEJA_CODEX_ROOT", "")
	codex := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codex, 0o755); err != nil {
		t.Fatal(err)
	}
	hooksPath := filepath.Join(codex, "hooks.json")
	cfgPath := filepath.Join(codex, "config.toml")
	cfg := "# mine\nmodel = \"luna\"\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installCodexHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	// The reader adds a SessionStart hook of their own after deja's, and
	// codex pins all of them once the user approves.
	b, _ := os.ReadFile(hooksPath)
	mine := `{"hooks":[{"type":"command","command":"~/bin/mine.sh"}]}`
	withMine := strings.Replace(string(b), `"SessionStart": [`, `"SessionStart": [`+"\n      "+mine+",", 1)
	if withMine == string(b) {
		t.Fatalf("could not place the reader's hook:\n%s", b)
	}
	// Theirs first, deja's second.
	if err := os.WriteFile(hooksPath, []byte(withMine), 0o600); err != nil {
		t.Fatal(err)
	}
	pin := func(event string, g int) string {
		return "\n[hooks.state." + strconv.Quote(hooksPath+":"+event+":"+strconv.Itoa(g)+":0") + "]\ntrusted_hash = \"sha256:" + event + strconv.Itoa(g) + "\"\n"
	}
	approved := cfg + "\n[hooks.state]\n" + pin("session_start", 0) + pin("session_start", 1) + pin("user_prompt_submit", 0) + pin("pre_tool_use", 0) + pin("post_tool_use", 0) + pin("pre_compact", 0)
	if err := os.WriteFile(cfgPath, []byte(approved), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := installCodexHooks("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(cfgPath)
	want := cfg + "\n[hooks.state]\n" + pin("session_start", 0)
	if string(got) != want {
		t.Errorf("after uninstall config.toml is\n%s\nwant\n%s", got, want)
	}

	// A table codex writes after the pins keeps the blank line in front of it.
	if err := os.WriteFile(hooksPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	tail := "\n[tui.model_availability_nux]\n\"gpt-5.6-sol\" = 1\n"
	approved = cfg + "\n[projects.\"/w\"]\ntrust_level = \"trusted\"\n\n[hooks.state]\n" + pin("session_start", 0) + pin("pre_compact", 0) + tail
	if err := os.WriteFile(cfgPath, []byte(approved), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installCodexHooks("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if got, want := readString(t, cfgPath), cfg+"\n[projects.\"/w\"]\ntrust_level = \"trusted\"\n"+tail; got != want {
		t.Errorf("after uninstall config.toml is\n%q\nwant\n%q", got, want)
	}

	// And with nothing of the reader's left, the file comes back as it was.
	if err := os.WriteFile(hooksPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	approved = cfg + "\n[hooks.state]\n" + pin("session_start", 0) + pin("pre_compact", 0)
	if err := os.WriteFile(cfgPath, []byte(approved), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installCodexHooks("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(cfgPath); string(got) != cfg {
		t.Errorf("after uninstall config.toml is\n%q\nwant\n%q", got, cfg)
	}
}

func readString(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
