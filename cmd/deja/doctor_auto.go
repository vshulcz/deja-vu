package main

import (
	"crypto/sha256"
	"encoding/hex"
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
		// Roo's guidance moved out of the always-on rules file into a skill;
		// checking the old path reported a correctly wired machine as missing.
		{"roo", func() string { return guidancePath("roo") }, ""},
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
	return yamlHasKey(path, "  deja:")
}

func doctorHermesWired(path string) bool {
	return yamlHasKey(path, "mcp_servers:") && yamlHasKey(path, "  deja:")
}

// yamlHasKey looks for a key on its own line. Matching a leading newline
// instead would miss a config whose first line is the key — which is exactly
// how a hand-written file tends to start.
func yamlHasKey(path, key string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimRight(line, " \t\r") == key {
			return true
		}
	}
	return false
}

// codexHookTrusted compares the hook file against the hash codex recorded
// when the user last trusted it. A mismatch means codex is holding the hook
// for review, however enabled it looks in the config.
func codexHookTrusted(hooksPath, stateSection string) bool {
	i := strings.Index(stateSection, "trusted_hash = \"sha256:")
	if i < 0 {
		return true // no pin recorded: nothing to contradict
	}
	rest := stateSection[i+len("trusted_hash = \"sha256:"):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return true
	}
	want := rest[:end]
	b, err := os.ReadFile(hooksPath)
	if err != nil {
		return true
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]) == want
}
