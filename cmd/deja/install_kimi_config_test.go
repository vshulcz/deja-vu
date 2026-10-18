package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

func kimiDoctorRow(t *testing.T) doctorMCPStatus {
	t.Helper()
	js, err := captureRun(t, "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		MCP []doctorMCPStatus `json:"mcp"`
	}
	if err := json.Unmarshal([]byte(js), &report); err != nil {
		t.Fatalf("doctor --json: %v: %s", err, js)
	}
	for _, r := range report.MCP {
		if r.Name == "kimi" {
			return r
		}
	}
	t.Fatalf("no kimi row:\n%s", js)
	return doctorMCPStatus{}
}

// Kimi Code 2.2 reads `enabled` on an mcp.json entry and drops every other
// key, so an entry with "disabled": true is started and `/mcp` lists it
// connected. Install and doctor called it off.
func TestKimiEntryDisabledKeyIsNotItsSwitch(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	bin := filepath.Join(home, "bin", "deja")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sources.KimiConfigDir(), "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		key string
		off bool
	}{
		{`"disabled": true`, false},
		{`"enabled": false`, true}, // Kimi's own switch: the control
	} {
		body := `{"mcpServers": {"deja": {"command": "` + filepath.ToSlash(bin) + `", "args": ["mcp"], ` + c.key + `}}}` + "\n"
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := captureRun(t, "install", "kimi", "--no-index")
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(out, "switched off"); got != c.off {
			t.Errorf("%s: install says switched off = %v, want %v:\n%s", c.key, got, c.off, out)
		}
		if b, _ := os.ReadFile(path); !strings.Contains(string(b), c.key) {
			t.Errorf("%s: the reader's key was dropped:\n%s", c.key, b)
		}
		if r := kimiDoctorRow(t); r.State != "wired" || r.SwitchedOff != c.off {
			t.Errorf("%s: doctor row = %+v, want wired and switched_off=%v", c.key, r, c.off)
		}
		text, err := captureRun(t, "doctor")
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(text, "the entry is switched off — kimi"); got != c.off {
			t.Errorf("%s: doctor text says switched off = %v, want %v", c.key, got, c.off)
		}
	}
}

// Kimi Code's own login and logout write config.toml back from its parsed
// form, which drops every comment, deja's markers among them. Uninstall then
// left the seven hooks running a deja-hook it had deleted, and the next
// install stacked seven more beside them.
func TestKimiAutoFindsItsHooksAfterKimiRewroteTheConfig(t *testing.T) {
	hermeticEnv(t)
	cfg := filepath.Join(sources.KimiConfigDir(), "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := "default_model = \"luna\"\n\n[[hooks]]\nevent = \"Stop\"\ncommand = \"say done\"\n"
	if err := os.WriteFile(cfg, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "kimi-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(cfg)
	n := strings.Count(string(b), "[[hooks]]")
	// What Kimi 2.2 left after /logout from Kimi Code: the same tables, no
	// comments, a blank line between them.
	var kept []string
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			kept = append(kept, line)
		}
	}
	rewritten := strings.Join(kept, "\n")
	if strings.Contains(rewritten, kimiHookMarker) {
		t.Fatal("the rewrite kept the marker")
	}
	if err := os.WriteFile(cfg, []byte(rewritten), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "kimi-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(cfg)
	if got := strings.Count(string(b), "[[hooks]]"); got != n {
		t.Errorf("reinstall: %d hooks, want %d:\n%s", got, n, b)
	}
	if got := strings.Count(string(b), "hook-prompt"); got != 1 {
		t.Errorf("reinstall: hook-prompt %d times, want 1:\n%s", got, b)
	}
	if err := os.WriteFile(cfg, []byte(rewritten), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "kimi-auto"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(cfg)
	if strings.Contains(string(b), "deja") {
		t.Errorf("uninstall left deja's hooks behind:\n%s", b)
	}
	if !strings.Contains(string(b), `command = "say done"`) {
		t.Errorf("uninstall took the reader's own hook:\n%s", b)
	}

	// The control: a rule the reader wrote around deja's hook is not one deja
	// wrote, and neither install nor uninstall takes it.
	theirs := "[[hooks]]\nevent = \"UserPromptSubmit\"\ncommand = \"deja hook-prompt --plain\"\n"
	if err := os.WriteFile(cfg, []byte(theirs), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "kimi-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "kimi-auto"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(cfg); string(b) != theirs {
		t.Errorf("the reader's own rule changed:\n%q", b)
	}
}

// A read-only config.toml or tui.toml was refused after mcp.json (and
// config.toml) had been written, so the refused target was half wired.
func TestKimiAutoRefusesAReadOnlyFileBeforeWriting(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs unix permissions as a normal user")
	}
	for _, c := range []struct {
		file, seed string
		refused    bool
	}{
		{"config.toml", "default_model = \"luna\"\n", true},
		{"tui.toml", "theme = \"dark\"\n", true},
		// Nothing to change in it, so nothing to refuse: the control.
		{"tui.toml", "[status_line]\ncommand = \"my-status\"\n", false},
	} {
		t.Run(c.file, func(t *testing.T) {
			hermeticEnv(t)
			dir := sources.KimiConfigDir()
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, c.file), []byte(c.seed), 0o444); err != nil {
				t.Fatal(err)
			}
			_, err := captureRun(t, "install", "kimi-auto", "--no-index")
			if !c.refused {
				if err != nil {
					t.Fatalf("install refused a read-only %s it had nothing to write to: %v", c.file, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "read-only") {
				t.Fatalf("install err = %v, want the read-only refusal", err)
			}
			for _, name := range []string{"mcp.json", "config.toml", "tui.toml"} {
				if name == c.file {
					continue
				}
				if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
					t.Errorf("%s was written by a refused target", name)
				}
			}
		})
	}
}

// [status_line] can hold Kimi's own `items` beside the command. Rewriting
// deja's line rebuilt the table without them, and uninstall took the whole
// table.
func TestKimiStatusLineKeepsTheItemsBesideIt(t *testing.T) {
	hermeticEnv(t)
	tui := filepath.Join(sources.KimiConfigDir(), "tui.toml")
	if err := os.MkdirAll(filepath.Dir(tui), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tui, []byte("theme = \"dark\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "kimi-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(tui)
	withItems := strings.Replace(string(b), "[status_line]\n", "[status_line]\nitems = [\"model\", \"git\"]\n", 1)
	if withItems == string(b) {
		t.Fatalf("no [status_line] written:\n%s", b)
	}
	if err := os.WriteFile(tui, []byte(withItems), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "kimi-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(tui); string(b) != withItems {
		t.Errorf("reinstall changed the table:\n%s\nwant:\n%s", b, withItems)
	}
	if _, err := captureRun(t, "uninstall", "kimi-auto"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(tui)
	if want := "theme = \"dark\"\n\n[status_line]\nitems = [\"model\", \"git\"]\n"; string(b) != want {
		t.Errorf("uninstall left:\n%q\nwant:\n%q", b, want)
	}
}

// Review cases: a comment inside deja's status line table is not the reader's
// key, and a note above the reader's next hook does not hide the unmarked
// deja hook before it.
func TestKimiConfigCommentsAroundDejasTables(t *testing.T) {
	hermeticEnv(t)
	dir := sources.KimiConfigDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := hookExeInConfigs("/bin/deja")
	tui := filepath.Join(dir, "tui.toml")
	seed := "theme = \"dark\"\n\n[status_line]\n# deja's line\ncommand = \"" + exe + " statusline\"\n"
	if err := os.WriteFile(tui, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installKimiStatusline("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(tui); string(b) != "theme = \"dark\"\n" {
		t.Errorf("status line uninstall left %q", b)
	}

	cfg := filepath.Join(dir, "config.toml")
	hooks := "[[hooks]]\nevent = \"SessionEnd\"\ncommand = \"" + exe + " hook-session-end\"\ntimeout = 30\n\n" +
		"[[hooks]]\nevent = \"PreCompact\"\ncommand = \"" + exe + " hook-precompact\"\ntimeout = 30\n\n" +
		"# mine\n[[hooks]]\nevent = \"Stop\"\ncommand = \"say\"\n"
	if err := os.WriteFile(cfg, []byte(hooks), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installKimiAuto("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(cfg); string(b) != "# mine\n[[hooks]]\nevent = \"Stop\"\ncommand = \"say\"\n" {
		t.Errorf("hooks uninstall left %q", b)
	}
}
