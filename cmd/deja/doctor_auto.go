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
	// kind is what the file actually is, when it is not a hook. Every row in
	// this table used to read "wired" under a heading that says Hooks, and two
	// of them are not hooks: aider's is a context file that only a wrapper
	// command refreshes, and roo's is guidance that asks the agent to call
	// recall rather than handing it anything. Reporting all three the same way
	// tells someone their memory arrives on its own when it does not.
	kind string
}

// autoWirings is the single list doctor and the coverage test both read.
// Every harness deja can wire for auto-recall belongs here; a harness with an
// -auto install target and no entry is a hole in the only report a user has
// when memory goes quiet.
func autoWirings() []autoWiring {
	return []autoWiring{
		{"opencode", func() string {
			return filepath.Join(opencodeConfigHome(), "opencode", "plugins", "deja.js")
		}, "hook-context", ""},
		{"cursor", func() string { return filepath.Join(sources.CursorCLIHome(), "hooks.json") }, "hook-context", ""},
		{"gemini", func() string {
			return filepath.Join(sources.GeminiHome(), "extensions", "deja", "hooks", "hooks.json")
		}, "hook-context", ""},
		{"qwen", func() string { return filepath.Join(sources.QwenConfigDir(), "settings.json") }, "hook-prompt", ""},
		{"kimi", func() string { return filepath.Join(sources.KimiConfigDir(), "config.toml") }, "hook-prompt", ""},
		{"antigravity", func() string {
			return filepath.Join(antigravityConfigHome(), "plugins", "deja", "hooks.json")
		}, "hook-antigravity", ""},
		{"pi", func() string { return filepath.Join(sources.PiConfigDir(), "extensions", "deja.ts") }, "hook-context", ""},
		{"hermes", func() string {
			return filepath.Join(sources.HermesHome(), "plugins", "deja", "__init__.py")
		}, "hook-context", ""},
		{"openclaw", func() string {
			return filepath.Join(sources.OpenClawStateDir(), "hooks", openclawHookName, "handler.js")
		}, "hook-context", ""},
		{"cline", func() string { return filepath.Join(sources.ClinePluginsDir(), "deja", "index.js") }, "hook-context", ""},
		{"omp", func() string {
			return filepath.Join(sources.OmpConfigDir(), "extensions", "deja", "index.js")
		}, "hook-prompt", ""},
		{"deepseek", func() string { return dshAutoPath() }, "hook-prompt", ""},
		{"goose", func() string { return gooseHookPath() }, "hook-goose", ""},
		{"grok", func() string { return grokHooksPath() }, "hook-context", ""},
		{"aider", func() string { return aiderContextPath() }, "",
			"context file — refreshed by `deja aider`, not by aider itself"},
		// Roo's guidance moved out of the always-on rules file into a skill;
		// checking the old path reported a correctly wired machine as missing.
		{"roo", func() string { return guidancePath("roo") }, "",
			"guidance — the agent is told to call recall, not handed it"},
	}
}

// nothingWired reports whether no harness on this machine has an auto-recall
// file. It is a stat per harness and no index read, which is what the brief can
// afford — that screen has to feel instant.
//
// Auto-recall alone is the question worth asking here. An MCP server is a tool
// the agent may call; these files are what make memory arrive without anyone
// asking, which is the thing someone thinks they installed.
func nothingWired() bool {
	for _, a := range autoWirings() {
		if _, err := os.Stat(a.path()); err == nil {
			return false
		}
	}
	return true
}

// doctorAutoRecall prints one line per harness. "stale" is the interesting
// state: the file is there, so an install looks done, but nothing in it calls
// deja any more — which is exactly how a silently dead integration looks.
func doctorAutoRecall(w io.Writer) {
	for _, a := range autoWirings() {
		path := a.path()
		b, err := os.ReadFile(path)
		note := ""
		if a.kind != "" {
			note = "  (" + a.kind + ")"
		}
		// Same as the MCP line: the plugin carries this harness's recall, and
		// its own hook is the one that runs when the installer has not written
		// here at all.
		if a.name == "kimi" && kimiPluginInstalled() && (err != nil || !strings.Contains(string(b), a.marker)) {
			// Behind is the line worth the width: what it does is in the
			// README, what to run is not obvious from anywhere.
			note := kimiPluginNote()
			if note == "" || note == "v"+kimiPluginVersion {
				note = "the Kimi Code plugin recalls on every prompt"
			}
			fmt.Fprintf(w, "  %-12s %-11s %s  (%s)\n", a.name, "plugin", reportPath(path), note)
			continue
		}
		switch {
		case err != nil:
			fmt.Fprintf(w, "  %-12s %-11s %s%s\n", a.name, "missing", reportPath(path), note)
		case a.marker != "" && !strings.Contains(string(b), a.marker):
			fmt.Fprintf(w, "  %-12s %-11s %s  (no %s call — reinstall)\n", a.name, "stale", reportPath(path), a.marker)
		default:
			fmt.Fprintf(w, "  %-12s %-11s %s%s\n", a.name, "wired", reportPath(path), note)
			// Only the rows that run the binary. aider's file is a digest of
			// past sessions and roo's is guidance — neither executes anything,
			// and both can quote a path for reasons of their own.
			if exe := hookExeNote(path, a.name+"-auto"); a.marker != "" && exe != "" {
				fmt.Fprintf(w, "  %-12s %s\n", "", exe)
			}
		}
	}
}

// Goose and Hermes keep MCP servers in YAML rather than JSON or TOML, and
// both nest ours under a key: a plain "deja appears in the file" check would
// pass on a disabled entry.
func doctorGooseWired(path string) bool {
	return yamlHasChildKey(path, "extensions:", "deja:")
}

func doctorHermesWired(path string) bool {
	return yamlHasChildKey(path, "mcp_servers:", "deja:")
}

// yamlHasChildKey reports whether a key sits directly under a top-level parent.
//
// The indent is whatever the reader wrote the block at, and asking for exactly
// two called a goose deja had just wired at four unwired — while the writer
// itself follows the block (#2614, #2727). "Anywhere below the top level" was
// the other end of the same mistake: `deja:` in another server's env, or in a
// comment-shaped example under `notes:`, then read as a wired server (#2730).
func yamlHasChildKey(path, parent, key string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	lines := strings.Split(string(b), "\n")
	inBlock := false
	child := -1
	for _, raw := range lines {
		line := strings.TrimRight(raw, " \t\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.TrimSpace(line) == parent && yamlIndentWidth(line) == 0 {
			inBlock, child = true, -1
			continue
		}
		if !inBlock {
			continue
		}
		if yamlIndentWidth(line) == 0 {
			inBlock = false
			continue
		}
		if child < 0 {
			child = yamlIndentWidth(line)
		}
		if strings.TrimSpace(line) == key && yamlIndentWidth(line) == child {
			return true
		}
	}
	return false
}

// codexHasSeenItsHook reports whether codex has recorded any opinion about the
// session-start hook. Until it has, codex runs nothing — and in `codex exec`
// there is no interface in which to approve it, which is how scripted runs end
// up with no memory while every file on disk looks correctly installed.
func codexHasSeenItsHook() bool {
	cfg, err := os.ReadFile(filepath.Join(sources.CodexHome(), "config.toml"))
	if err != nil {
		return true // nothing to read: do not raise an alarm we cannot support
	}
	return codexHookTrustSection(string(cfg)) != ""
}

// codexHookTrustSection returns the block of codex's config that records what
// it thinks of our session-start hook, or "" when there is none.
//
// Codex keys its trust store per hook rather than per file —
// `[hooks.state."<path>/hooks.json:session_start:0:0"]` — and the block ends at
// the next table header. Reading to the end of the file instead, which is what
// this did, lets an `enabled = false` belonging to some unrelated table decide
// what deja reports about ours.
//
// It deliberately does not check the recorded hash. deja cannot reproduce it:
// on codex 0.142.4 the pin for a hook whose command is one line long is not the
// sha256 of the hook file, of the command, of the handler object in any
// serialisation, or of any combination of the two with the matcher, the event
// or the key — checked. Comparing the file's own sha256 against it, which is
// what this did, therefore called every working install untrusted. Presence of
// a pin is what deja can honestly read: it means codex has been shown this hook
// and kept an opinion about it.
func codexHookTrustSection(cfg string) string {
	i := strings.Index(cfg, "hooks.json:session_start")
	if i < 0 {
		return ""
	}
	rest := cfg[i:]
	// Past the key's own line, so the header we stop at is the next one.
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		if end := strings.Index(rest[nl:], "\n["); end >= 0 {
			return rest[:nl+end]
		}
	}
	return rest
}
