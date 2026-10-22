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

// grok-dev's MCP modal turns a server off by writing "enabled": false on its
// entry in user-settings.json, and its runtime skips the entry. Install
// rebuilt deja's entry with "enabled": true and said nothing (#5196).
func TestGrokInstallKeepsGrokDevsSwitch(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.GrokHome(), "user-settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{"mcp": {"servers": [{"id": "deja", "label": "deja", "transport": "stdio", "command": "/bin/deja", "args": ["mcp"], "enabled": false}]}}`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "install", "grok", "--no-index")
	if err != nil {
		t.Fatal(err)
	}
	if got := grokDevDejaEnabled(t, path); got != false {
		t.Errorf("deja's grok-dev entry enabled = %v after install, want the reader's false", got)
	}
	if !strings.Contains(out, "switched off") {
		t.Errorf("install did not say it left the entry off:\n%s", out)
	}

	// The entry keyed "deja" decides over another labelled deja.
	both := `{"mcp": {"servers": [{"id": "deja-2", "label": "deja", "command": "/bin/deja", "args": ["mcp"], "enabled": false}, {"id": "deja", "label": "deja", "command": "/bin/deja", "args": ["mcp"], "enabled": true}]}}`
	if werr := os.WriteFile(path, []byte(both), 0o644); werr != nil {
		t.Fatal(werr)
	}
	if _, cerr := captureRun(t, "install", "grok", "--no-index"); cerr != nil {
		t.Fatal(cerr)
	}
	if got := grokDevDejaEnabled(t, path); got != true {
		t.Errorf("enabled = %v, want the id-keyed entry's true", got)
	}

	// Control: an entry that was on stays on, and nothing is said.
	seed = strings.Replace(seed, `"enabled": false`, `"enabled": true`, 1)
	if werr := os.WriteFile(path, []byte(seed), 0o644); werr != nil {
		t.Fatal(werr)
	}
	out, err = captureRun(t, "install", "grok", "--no-index")
	if err != nil {
		t.Fatal(err)
	}
	if got := grokDevDejaEnabled(t, path); got != true {
		t.Errorf("deja's grok-dev entry enabled = %v, want true", got)
	}
	if strings.Contains(out, "switched off") {
		t.Errorf("install called an entry that was on switched off:\n%s", out)
	}
}

func grokDevDejaEnabled(t *testing.T, path string) any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		MCP struct {
			Servers []map[string]any `json:"servers"`
		} `json:"mcp"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	for _, s := range root.MCP.Servers {
		if s["id"] == "deja" {
			return s["enabled"]
		}
	}
	t.Fatalf("no deja entry in\n%s", b)
	return nil
}

// grok writes config.toml first and the other files after it, so a later file
// it could not write was refused once config.toml already held deja: a
// read-only user-settings.json under `deja install grok`, a read-only hooks
// directory under grok-auto (#5197).
func TestGrokRefusesAnUnwritableLaterFileBeforeWriting(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs unix permissions as a normal user")
	}
	t.Run("read-only user-settings.json", func(t *testing.T) {
		hermeticEnv(t)
		dir := sources.GrokHome()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		settings := filepath.Join(dir, "user-settings.json")
		if err := os.WriteFile(settings, []byte("{\"model\": \"grok-4\"}\n"), 0o444); err != nil {
			t.Fatal(err)
		}
		for _, target := range []string{"grok", "grok-auto"} {
			_, err := captureRun(t, "install", target, "--no-index")
			if err == nil || !strings.Contains(err.Error(), "read-only") {
				t.Errorf("%s: install err = %v, want the read-only refusal", target, err)
			}
			if _, err := os.Stat(filepath.Join(dir, "config.toml")); err == nil {
				t.Errorf("%s: config.toml was written by a refused target", target)
				_ = os.Remove(filepath.Join(dir, "config.toml"))
			}
		}

		// A file already holding deja's entry needs no write, and is not refused.
		if err := os.Chmod(settings, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := captureRun(t, "install", "grok", "--no-index"); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(settings, 0o444); err != nil {
			t.Fatal(err)
		}
		if out, err := captureRun(t, "install", "grok", "--no-index"); err != nil {
			t.Errorf("a repeat install refused an unchanged read-only user-settings.json: %v\n%s", err, out)
		}
	})
	t.Run("symlinked user-settings.json in a read-only repo", func(t *testing.T) {
		tmp := hermeticEnv(t)
		repo := filepath.Join(tmp, "dotfiles")
		if err := os.MkdirAll(repo, 0o755); err != nil {
			t.Fatal(err)
		}
		realPath := filepath.Join(repo, "user-settings.json")
		if err := os.WriteFile(realPath, []byte("{\"model\": \"grok-4\"}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(sources.GrokHome(), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(realPath, filepath.Join(sources.GrokHome(), "user-settings.json")); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(repo, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(repo, 0o755) })
		if _, err := captureRun(t, "install", "grok", "--no-index"); err == nil {
			t.Fatal("install wrote through a link into a read-only directory")
		}
		if _, err := os.Stat(filepath.Join(sources.GrokHome(), "config.toml")); err == nil {
			t.Error("config.toml was written by a refused target")
		}
	})
	t.Run("hooks naming the binary when the launcher cannot be written", func(t *testing.T) {
		hermeticEnv(t)
		bin := filepath.Dir(dejaLauncherPath())
		if bin == "." {
			t.Skip("no launcher on this platform")
		}
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		_ = os.Chmod(bin, 0o555)
		t.Cleanup(func() { _ = os.Chmod(bin, 0o755) })
		if _, err := captureRun(t, "install", "grok-auto", "--no-index"); err != nil {
			t.Fatal(err)
		}
		hooks := filepath.Dir(grokHooksPath())
		if err := os.Chmod(hooks, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(hooks, 0o755) })
		if out, err := captureRun(t, "install", "grok-auto", "--no-index"); err != nil {
			t.Errorf("a repeat install refused an unchanged hooks file: %v\n%s", err, out)
		}
	})
	t.Run("read-only hooks directory", func(t *testing.T) {
		hermeticEnv(t)
		hooks := filepath.Dir(grokHooksPath())
		if err := os.MkdirAll(hooks, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(hooks, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(hooks, 0o755) })
		_, err := captureRun(t, "install", "grok-auto", "--no-index")
		if err == nil {
			t.Fatal("install wrote into a read-only hooks directory")
		}
		for _, name := range []string{"config.toml", "user-settings.json"} {
			if _, err := os.Stat(filepath.Join(sources.GrokHome(), name)); err == nil {
				t.Errorf("%s was written by a refused target", name)
			}
		}
	})
}
