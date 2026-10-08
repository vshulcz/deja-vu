package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tuiPluginList(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var root struct {
		Plugin []string `json:"plugin"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return root.Plugin
}

// opencode 1.x and Kilo load a TUI plugin only when tui.json lists it; the
// plugin runs deja statusline and shows its line in the sidebar, the prompt
// row and the home screen. Uninstall takes the file and the entry back and
// leaves the reader's own plugins.
func TestV1TUIPluginIsListedAndTakenBack(t *testing.T) {
	hermeticEnv(t)
	t.Cleanup(func() { opencodeVersionMajor = opencodeVersionMajorReal })
	opencodeVersionMajor = func() int { return 1 }
	for _, c := range []struct{ target, dir string }{
		{"opencode-auto", filepath.Join(opencodeConfigHome(), "opencode")},
		{"kilocode-auto", kilocodeCLIConfigDir()},
	} {
		if err := os.MkdirAll(c.dir, 0o755); err != nil {
			t.Fatal(err)
		}
		list := filepath.Join(c.dir, "tui.json")
		if err := os.WriteFile(list, []byte(`{"plugin":["./plugins/mine.tsx"]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := installTarget(c.target, "/opt/deja/bin/deja", false); err != nil {
			t.Fatalf("%s: %v", c.target, err)
		}
		got := tuiPluginList(t, list)
		if strings.Join(got, ",") != "./plugins/mine.tsx,./plugins/deja-status.tsx" {
			t.Errorf("%s: tui.json plugin = %v", c.target, got)
		}
		b, err := os.ReadFile(filepath.Join(c.dir, "plugins", "deja-status.tsx"))
		if err != nil {
			t.Fatalf("%s: no plugin file: %v", c.target, err)
		}
		src := string(b)
		for _, want := range []string{"@jsxImportSource @opentui/solid", `["statusline"]`, "sidebar_content", "session_prompt_right", "export default { id: \"deja.status\", tui }"} {
			if !strings.Contains(src, want) {
				t.Errorf("%s: plugin lacks %q", c.target, want)
			}
		}
		if !strings.HasPrefix(src, "/** @jsxImportSource") {
			t.Errorf("%s: the JSX pragma is not the first line", c.target)
		}
		if _, err := installTarget(c.target, "/opt/deja/bin/deja", true); err != nil {
			t.Fatal(err)
		}
		if got := tuiPluginList(t, list); strings.Join(got, ",") != "./plugins/mine.tsx" {
			t.Errorf("%s: uninstall left tui.json plugin = %v", c.target, got)
		}
		if _, err := os.Stat(filepath.Join(c.dir, "plugins", "deja-status.tsx")); err == nil {
			t.Errorf("%s: uninstall left the plugin file", c.target)
		}
	}
}

// opencode 2.x discovers <config>/plugins/<name>/tui.tsx itself and has
// footer slots; nothing is listed anywhere.
func TestV2TUIPluginNeedsNoList(t *testing.T) {
	hermeticEnv(t)
	t.Cleanup(func() { opencodeVersionMajor = opencodeVersionMajorReal })
	opencodeVersionMajor = func() int { return 2 }
	dir := filepath.Join(opencodeConfigHome(), "opencode")
	if _, err := installTarget("opencode-auto", "/opt/deja/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "plugins", "deja-status", "tui.tsx"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"setup(ctx)", `append: "prompt.footer.status"`, `["statusline"]`, "input.sessionID", "ctx.theme.text.muted"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("plugin lacks %q", want)
		}
	}
	// 2.0.x has no theme.textMuted: the line came out in the default colour.
	if strings.Contains(string(b), "textMuted") {
		t.Error("plugin reads theme.textMuted, which 2.x does not have")
	}
	if _, err := os.Stat(filepath.Join(dir, "tui.json")); err == nil {
		t.Error("a 2.x install wrote tui.json")
	}
	if _, err := installTarget("opencode-auto", "/opt/deja/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "plugins", "deja-status")); err == nil {
		t.Error("uninstall left the plugin directory")
	}
}
