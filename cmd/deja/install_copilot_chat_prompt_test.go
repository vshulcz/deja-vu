package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Copilot Chat fires no hook, so the prompt file is the only thing that tells
// its model to reach for the tool. It goes in the same `User` directory the
// reader already walks, once per host that exists (#3651).
func TestInstallCopilotChatPromptWritesPerHost(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("DEJA_COPILOT_CHAT_ROOTS", "")

	// Two hosts present, one absent: the absent one must not be created.
	var hosts []string
	for _, name := range []string{"Code", "VSCodium"} {
		dir := vsCodeUserDirForTest(t, home, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		hosts = append(hosts, dir)
	}
	absent := vsCodeUserDirForTest(t, home, "Code - Insiders")

	res, err := installCopilotChatPrompt("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action == "unchanged" {
		t.Fatalf("install changed nothing: %+v", res)
	}
	for _, dir := range hosts {
		path := filepath.Join(dir, "prompts", "deja.prompt.md")
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		body := string(b)
		// The frontmatter is what makes it a prompt file rather than a note,
		// and the description is what the chat box lists beside `/deja`.
		if !strings.HasPrefix(body, "---\n") || !strings.Contains(body, "description:") {
			t.Errorf("%s has no prompt frontmatter:\n%s", path, body[:120])
		}
		if !strings.Contains(body, "$ARGUMENTS") {
			t.Errorf("%s takes no arguments, so /deja <query> would drop the query", path)
		}
		if !strings.Contains(body, "/usr/local/bin/deja search") {
			t.Errorf("%s names no fallback command", path)
		}
	}
	if _, err := os.Stat(absent); err == nil {
		t.Errorf("the installer created a directory for an editor that is not installed: %s", absent)
	}

	if again, err := installCopilotChatPrompt("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	} else if again.Action != "unchanged" {
		t.Errorf("second install = %q, want unchanged", again.Action)
	}

	if _, err := installCopilotChatPrompt("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	for _, dir := range hosts {
		if _, err := os.Stat(filepath.Join(dir, "prompts", "deja.prompt.md")); err == nil {
			t.Errorf("the prompt file survived uninstall in %s", dir)
		}
	}
}

// vsCodeUserDirForTest is where this platform keeps a host's User directory —
// the same three branches CopilotChatRoots walks.
func vsCodeUserDirForTest(t *testing.T, home, host string) string {
	t.Helper()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", host, "User")
	case "windows":
		return filepath.Join(home, "AppData", "Roaming", host, "User")
	default:
		return filepath.Join(home, ".config", host, "User")
	}
}
