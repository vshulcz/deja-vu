package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// autoWiring is one harness's session-start recall: the file deja writes and
// the marker that proves the file is still ours rather than a leftover the
// user or the harness rewrote.
type autoWiring struct {
	name   string
	path   func() string
	marker string
}

// autoWirings is the single list doctor and the coverage test both read.
// Every harness deja can wire for auto-recall belongs here; a harness with an
// -auto install target and no entry is a hole in the only report a user has
// when memory goes quiet.
func autoWirings() []autoWiring {
	return []autoWiring{
		{"opencode", func() string {
			return filepath.Join(opencodeConfigHome(), "opencode", "plugins", "deja.js")
		}, "hook-context"},
		{"cursor", func() string { return filepath.Join(sources.CursorCLIHome(), "hooks.json") }, "hook-context"},
		{"gemini", func() string {
			return filepath.Join(sources.GeminiHome(), "extensions", "deja", "hooks", "hooks.json")
		}, "hook-context"},
		{"qwen", func() string { return filepath.Join(sources.QwenConfigDir(), "settings.json") }, "hook-prompt"},
		{"kimi", func() string { return filepath.Join(sources.KimiConfigDir(), "config.toml") }, "hook-prompt"},
		{"antigravity", func() string {
			return filepath.Join(antigravityConfigHome(), "plugins", "deja", "hooks.json")
		}, "hook-antigravity"},
		{"pi", func() string { return filepath.Join(sources.PiConfigDir(), "extensions", "deja.ts") }, "hook-context"},
		{"hermes", func() string {
			return filepath.Join(sources.HermesHome(), "plugins", "deja", "__init__.py")
		}, "hook-context"},
		{"openclaw", func() string {
			return filepath.Join(sources.OpenClawStateDir(), "hooks", openclawHookName, "handler.js")
		}, "hook-context"},
		{"cline", func() string { return filepath.Join(sources.ClinePluginsDir(), "deja", "index.js") }, "hook-context"},
		{"goose", func() string { return gooseHookPath() }, "hook-goose"},
		{"aider", func() string { return aiderContextPath() }, ""},
		{"roo", func() string { return rooRulesPath() }, ""},
	}
}

// doctorAutoRecall prints one line per harness. "stale" is the interesting
// state: the file is there, so an install looks done, but nothing in it calls
// deja any more — which is exactly how a silently dead integration looks.
func doctorAutoRecall(w io.Writer) {
	for _, a := range autoWirings() {
		path := a.path()
		b, err := os.ReadFile(path)
		switch {
		case err != nil:
			fmt.Fprintf(w, "  %-12s %-11s %s\n", a.name, "missing", path)
		case a.marker != "" && !strings.Contains(string(b), a.marker):
			fmt.Fprintf(w, "  %-12s %-11s %s  (no %s call — reinstall)\n", a.name, "stale", path, a.marker)
		default:
			fmt.Fprintf(w, "  %-12s %-11s %s\n", a.name, "wired", path)
		}
	}
}

// Goose and Hermes keep MCP servers in YAML rather than JSON or TOML, and
// both nest ours under a key: a plain "deja appears in the file" check would
// pass on a disabled entry.
func doctorGooseWired(path string) bool {
	b, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(b), "\n  deja:\n")
}

func doctorHermesWired(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	s := string(b)
	return strings.Contains(s, "\nmcp_servers:\n") && strings.Contains(s, "\n  deja:\n")
}
