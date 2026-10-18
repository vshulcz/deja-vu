package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// openclawIncludeStand is a state dir whose openclaw.json keeps sections in
// included files, each written as given, and a deja binary that exists.
func openclawIncludeStand(t *testing.T, cfg string, included map[string]string) (exe string) {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("OPENCLAW_STATE_DIR", filepath.Join(tmp, "openclaw"))
	dir := filepath.Dir(openclawConfigPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"openclaw.json": cfg}
	for name, body := range included {
		files[name] = body
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	exe = filepath.Join(tmp, "deja")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

// The refusal told the reader to add entries running a launcher, a plugin
// directory and a hook pack that nothing had written, and a rerun refused
// again, so the hand-added entries pointed at nothing (#5331). Every path it
// names is on disk once it is said, and neither openclaw.json nor the file it
// includes is touched.
func TestOpenClawIncludeRefusalWritesWhatItNames(t *testing.T) {
	entry := regexp.MustCompile(`"deja": (\{"args":\[[^\]]*\],"command":"(?:[^"\\]|\\.)*"\})`)
	paths := regexp.MustCompile(`"paths": (\[[^\]]*\])`)
	for name, tc := range map[string]struct {
		target, cfg, inc string
	}{
		"mcp":       {"openclaw", `{"mcp": {"$include": "./inc.json5"}}`, `{}`},
		"mcp, auto": {"openclaw-auto", `{"mcp": {"$include": "./inc.json5"}}`, `{}`},
		"plugins":   {"openclaw-auto", `{"plugins": {"$include": "./inc.json5"}}`, `{"allow": ["x"]}`},
		"hooks":     {"openclaw-auto", `{"hooks": {"$include": "./inc.json5"}}`, `{"internal": {}}`},
	} {
		t.Run(name, func(t *testing.T) {
			exe := openclawIncludeStand(t, tc.cfg, map[string]string{"inc.json5": tc.inc})
			_, err := installTarget(tc.target, exe, false)
			if err == nil {
				t.Fatal("install wrote beside the $include")
			}
			msg := err.Error()
			var named []string
			m := entry.FindStringSubmatch(msg)
			if m == nil {
				t.Fatalf("the refusal names no MCP entry:\n%s", msg)
			}
			var e struct {
				Command string
				Args    []string
			}
			if err := json.Unmarshal([]byte(m[1]), &e); err != nil {
				t.Fatal(err)
			}
			cmd := e.Command
			if cmd == "cmd" && len(e.Args) > 1 { // Windows: cmd /c <deja> mcp
				cmd = e.Args[1]
			}
			named = append(named, cmd)
			if runtime.GOOS != "windows" && cmd != dejaLauncherPath() {
				t.Errorf("the MCP entry runs %s, not the launcher", cmd)
			}
			auto := tc.target == "openclaw-auto"
			if m := paths.FindStringSubmatch(msg); auto {
				if m == nil {
					t.Fatalf("the refusal names no plugin load path:\n%s", msg)
				}
				var ps []string
				if err := json.Unmarshal([]byte(m[1]), &ps); err != nil {
					t.Fatal(err)
				}
				named = append(named, ps...)
				named = append(named, filepath.Join(openclawPluginDir(), "index.mjs"), filepath.Join(openclawHookDir(), "handler.js"))
				for _, want := range []string{openclawAccessKey, `"` + openclawHookName + `"`, `"command":`} {
					if !strings.Contains(msg, want) {
						t.Errorf("the refusal leaves out %s, which the install did not write either:\n%s", want, msg)
					}
				}
			}
			for _, p := range named {
				if _, err := os.Stat(p); err != nil {
					t.Errorf("the refusal names %s, which is not there: %v", p, err)
				}
			}
			if !auto {
				if _, err := os.Stat(openclawPluginDir()); err == nil {
					t.Error("the MCP-only target wrote the plugin")
				}
			}
			dir := filepath.Dir(openclawConfigPath())
			if got := readFile(t, openclawConfigPath()); got != tc.cfg {
				t.Errorf("a refused install changed openclaw.json:\n%s", got)
			}
			if got := readFile(t, filepath.Join(dir, "inc.json5")); got != tc.inc {
				t.Errorf("a refused install changed the included file:\n%s", got)
			}
		})
	}
}

// Doctor reads the included files: the files alone are not a wiring, and the
// entries the refusal asked for, once added by hand, are (#5331).
func TestOpenClawDoctorReadsIncludedWiring(t *testing.T) {
	exe := openclawIncludeStand(t, `{"mcp": {"$include": "./mcp.json5"}, "plugins": {"$include": "./plugins.json5"}}`,
		map[string]string{"mcp.json5": `{}`, "plugins.json5": `{}`})
	if _, err := installTarget("openclaw-auto", exe, false); err == nil {
		t.Fatal("install wrote beside the $include")
	}
	var row autoWiring
	for _, a := range autoWirings() {
		if a.name == "openclaw" {
			row = a
		}
	}
	if st, _ := autoWiringState(row); st != "stale" {
		t.Errorf("deja's files with no entry naming them read as %q, want stale", st)
	}
	if doctorOpenClawWired(openclawConfigPath()) {
		t.Error("the MCP row reads wired with no entry anywhere")
	}

	dir := filepath.Dir(openclawConfigPath())
	hook := hookCommandExe(exe)
	write := func(name string, v any) {
		b, _ := json.Marshal(v)
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("mcp.json5", map[string]any{"servers": map[string]any{"deja": map[string]any{"command": hook, "args": []string{"mcp"}}}})
	write("plugins.json5", map[string]any{
		"entries": map[string]any{openclawPluginID: map[string]any{"enabled": true}},
		"load":    map[string]any{"paths": []string{openclawPluginDir()}},
	})
	write("openclaw.json", map[string]any{
		"mcp":     map[string]any{"$include": "./mcp.json5"},
		"plugins": map[string]any{"$include": "./plugins.json5"},
		"hooks":   map[string]any{"internal": map[string]any{"enabled": true, "entries": map[string]any{openclawHookName: map[string]any{"enabled": true}}}},
	})
	if st, _ := autoWiringState(row); st != "wired" {
		t.Errorf("wired by hand as the refusal said, the row reads %q", st)
	}
	if !doctorOpenClawWired(openclawConfigPath()) {
		t.Error("the MCP entry in the included file reads as not wired")
	}

	// Uninstall takes deja's files and says what it left in the included ones.
	before := readFile(t, filepath.Join(dir, "plugins.json5"))
	res, err := installTarget("openclaw-auto", exe, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{openclawPluginDir(), openclawHookDir()} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("uninstall left %s", p)
		}
	}
	for _, want := range []string{"mcp.servers.deja in", "mcp.json5", "plugins.entries.deja in", "plugins.json5"} {
		if !strings.Contains(res.Note, want) {
			t.Errorf("the uninstall note does not say %q:\n%s", want, res.Note)
		}
	}
	if got := readFile(t, filepath.Join(dir, "plugins.json5")); got != before {
		t.Errorf("uninstall edited the included file:\n%s", got)
	}
}
