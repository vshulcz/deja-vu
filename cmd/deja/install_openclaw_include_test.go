package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A section OpenClaw keeps in its own file is refused, not written beside:
// a key next to the $include turned OpenClaw's own edits of that section into
// overrides in openclaw.json, and they stayed after uninstall (#5258).
func TestOpenClawInstallRefusesASectionKeptInAnInclude(t *testing.T) {
	for name, tc := range map[string]struct {
		target, cfg string
		names       []string // what the refusal has to name
	}{
		"mcp":            {"openclaw", `{"mcp": {"$include": "./mcp.json5"}}`, []string{"mcp comes from", "mcp.json5", "under servers", `"deja": {"args":[`}},
		"mcp, auto":      {"openclaw-auto", `{"mcp": {"$include": "./mcp.json5"}}`, []string{"mcp comes from", "mcp.json5"}},
		"plugins":        {"openclaw-auto", `{"plugins": {"$include": "./plugins.json5"}}`, []string{"plugins comes from", "plugins.json5", "under entries", "under load", `"paths"`}},
		"hooks":          {"openclaw-auto", `{"hooks": {"$include": ["./a.json5", "./b.json5"]}}`, []string{"hooks comes from", "a.json5", "b.json5", "under internal"}},
		"servers nested": {"openclaw", `{"mcp": {"servers": {"$include": "./oc/servers.json5"}}}`, []string{"mcp.servers comes from", "servers.json5", "add there"}},
	} {
		t.Run(name, func(t *testing.T) {
			tmp := hermeticEnv(t)
			t.Setenv("OPENCLAW_STATE_DIR", filepath.Join(tmp, "openclaw"))
			path := openclawConfigPath()
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.cfg), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := installTarget(tc.target, "/bin/deja", false)
			if err == nil {
				t.Fatalf("install wrote beside the $include:\n%s", readFile(t, path))
			}
			for _, want := range append(tc.names, "in place of the $include") {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q:\n%v", want, err)
				}
			}
			if got := readFile(t, path); got != tc.cfg {
				t.Errorf("a refused install changed openclaw.json:\n%s", got)
			}
		})
	}
}

// Controls: a root $include, which OpenClaw never writes through, and a
// section beside one that deja does not write, install as before; and an
// entry an older install left beside a $include still comes out.
func TestOpenClawInstallBesideOtherIncludes(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("OPENCLAW_STATE_DIR", filepath.Join(tmp, "openclaw"))
	path := openclawConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, cfg := range []string{`{"$include": "./base.json5"}`, `{"agents": {"$include": "./agents.json5"}}`} {
		if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := installTarget("openclaw-auto", "/bin/deja", false); err != nil {
			t.Fatalf("%s: %v", cfg, err)
		}
		if !strings.Contains(readFile(t, path), `"deja"`) {
			t.Fatalf("%s: install wrote nothing:\n%s", cfg, readFile(t, path))
		}
		if _, err := installTarget("openclaw-auto", "/bin/deja", true); err != nil {
			t.Fatal(err)
		}
	}
	old := `{"mcp": {"$include": "./mcp.json5", "servers": {"deja": {"command": "/bin/deja", "args": ["mcp"]}}}}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("openclaw", "/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); strings.Contains(got, `"deja"`) || !strings.Contains(got, "mcp.json5") {
		t.Errorf("uninstall did not take out the entry an older install left:\n%s", got)
	}
}
