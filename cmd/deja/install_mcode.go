package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// MiniMax Code (MCode) is the desktop agent. Two places hold what deja needs
// there: the MCP configuration it reads at startup, and the plugin directory
// it loads packages from.
//
// The `mcode` target writes the first: one server named deja in
// ~/.minimax/mcp.json, the shape MCode's own MCP page writes.
//
// The `mcode-auto` target writes the second as well: a MiniMax-format
// package whose hooks call deja's hook commands at the points the runtime
// fires, so recall arrives with the turn instead of waiting to be asked for.
// The package carries no MCP server of its own — the entry above is the one —
// so a machine that has both does not list the tools twice.

// mcodeConfigPath is the MCP configuration MCode reads.
func mcodeConfigPath() string {
	return filepath.Join(sources.McodeConfigHome(), "mcp.json")
}

// mcodePluginRoot is the package deja writes.
func mcodePluginRoot() string {
	return filepath.Join(sources.McodeConfigHome(), "plugins", "deja")
}

// installMcode writes deja's MCP server into MCode's configuration.
func installMcode(exe string, uninstall bool) (installResult, error) {
	// The command as MiniMax Code's own MCP page writes it: the binary's
	// path and the subcommand beside it, with no shell in between. The
	// helper the other targets use wraps the command in `cmd /c` on Windows
	// for the hosts that need one; this host spawns the command itself.
	return installMCPJSONEntry(mcodeConfigPath(), "mcpServers",
		map[string]any{"type": "stdio", "command": exe, "args": []any{"mcp"}}, uninstall)
}

// installMcodeAuto writes the plugin and the MCP entry.
func installMcodeAuto(exe string, uninstall bool) (installResult, error) {
	plugin, err := installMcodePlugin(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	mcp, err := installMcode(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	return wroteAll(plugin, mcp), nil
}

// mcodeHookEvent is one event the package answers, the hook command it runs
// and the tool matcher that decides when it fires.
type mcodeHookEvent struct {
	event, sub, matcher string
	// args are the flags beside the subcommand.
	args []string
	// shaped marks the hooks whose answer carries text to the model: they
	// are asked for MiniMax Code's shape rather than Claude's.
	shaped bool
}

// mcodeHookEvents are the events the package answers. The commands name the
// hook they run, so the wiring a reader sees in the file is the hook that
// fires, and the --mcode flag is what turns the answer into the shape
// MiniMax Code reads.
var mcodeHookEvents = []mcodeHookEvent{
	{event: "SessionStart", sub: "hook-context", shaped: true},
	{event: "UserPromptSubmit", sub: "hook-prompt", shaped: true},
	{event: "PreToolUse", sub: "hook-tool", matcher: "bash|edit|write|read", shaped: true},
	{event: "PostToolUse", sub: "hook-tool-after", matcher: "bash", shaped: true},
	// The session just ended: the hook records the end so the next session's
	// recall can answer with it, and says nothing to the model.
	{event: "SessionEnd", sub: "hook-session-end"},
	// The compaction itself: the hook captures the transcript before the
	// context is cut. The harness is named because the capture reads the
	// transcript through that harness's own reader, and MiniMax Code's
	// payload does not say which one it is. The packet rides the next
	// prompt, which is where MiniMax Code can carry text.
	{event: "PreCompact", sub: "hook-precompact", args: []string{"--harness", "mcode"}},
}

// mcodeHooksDoc is the hook document the package carries.
func mcodeHooksDoc(exe string) []byte {
	doc := map[string]any{}
	for _, h := range mcodeHookEvents {
		args := h.args
		if h.shaped {
			args = append(append([]string{}, args...), "--mcode")
		}
		cmd := hookRun(exe, append([]string{h.sub}, args...)...)
		group := map[string]any{
			"hooks": []any{map[string]any{
				"type":    "command",
				"command": cmd,
				// Seconds. MiniMax Code takes 1 to 10 and defaults to 5;
				// this hook answers in milliseconds.
				"timeout": 5,
			}},
		}
		if h.matcher != "" {
			group["matcher"] = h.matcher
		}
		doc[h.event] = []any{group}
	}
	b, err := json.MarshalIndent(map[string]any{"hooks": doc}, "", "  ")
	if err != nil {
		return []byte("{}")
	}
	return append(b, '\n')
}

// mcodePluginManifest is the package's manifest. The name is the server name
// every other manifest deja ships uses, so a machine that has both this
// package and the MCP entry above collapses to one server.
func mcodePluginManifest() []byte {
	m := map[string]any{
		"schemaVersion": 1,
		"name":          "deja",
		"displayName":   "déjà vu",
		"version":       "1.0.0",
		"description":   "Gives the agent this machine's own past sessions: what was decided before, what was already tried, and what failed.",
		"author":        "deja",
		"icon":          "icon.png",
		"category":      "Code",
		"exampleQueries": []any{
			"What did we decide about this file before?",
			"What broke here last week, and what fixed it?",
		},
		"apps":       []any{},
		"mcpServers": []any{},
		// The skill rides inside the package: MiniMax Code has no user-level
		// skill directory of its own, and the plugin contract is where a skill
		// can live (skills/<name>/SKILL.md, named by the manifest).
		"skills": []any{"skills/deja-history/SKILL.md"},
		"hooks":  []any{"hooks/hooks.json"},
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return []byte("{}")
	}
	return append(b, '\n')
}

// mcodePluginIcon is the package's logo: a dark tile with a lighter disc,
// drawn rather than shipped as a blob so the package carries no binary
// nobody can regenerate.
func mcodePluginIcon() []byte {
	const size = 128
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{0x1b, 0x1f, 0x2a, 0xff}}, image.Point{}, draw.Src)
	disc := color.RGBA{0x7c, 0x9c, 0xf5, 0xff}
	for y := range size {
		for x := range size {
			dx, dy := x-size/2, y-size/2
			if dx*dx+dy*dy <= (size/3)*(size/3) {
				img.Set(x, y, disc)
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}

// mcodePluginIsOurs reports whether the package at root is the one deja
// wrote: a package someone edited by hand is theirs.
func mcodePluginIsOurs(root string) bool {
	b, err := os.ReadFile(filepath.Join(root, ".minimax-plugin", "plugin.json"))
	if err != nil {
		return false
	}
	var m struct {
		Name    string `json:"name"`
		Author  string `json:"author"`
		Version string `json:"version"`
	}
	if json.Unmarshal(b, &m) != nil {
		return false
	}
	return m.Name == "deja" && m.Author == "deja"
}

// installMcodePlugin writes the package, or takes it back out.
func installMcodePlugin(exe string, uninstall bool) (installResult, error) {
	root := mcodePluginRoot()
	if uninstall {
		if !mcodePluginIsOurs(root) {
			return installResult{Path: root, Action: "unchanged"}, nil
		}
		if err := os.RemoveAll(root); err != nil {
			return installResult{}, err
		}
		return installResult{Path: root, Action: "removed"}, nil
	}
	// The launcher, not this binary: a package is as much a config as a
	// hook file, and one that names the build it was installed from stops
	// working the day that build moves.
	exe = hookExeFor(exe, uninstall)
	files := []struct {
		path string
		body []byte
	}{
		{filepath.Join(root, ".minimax-plugin", "plugin.json"), mcodePluginManifest()},
		{filepath.Join(root, "hooks", "hooks.json"), mcodeHooksDoc(exe)},
		{filepath.Join(root, "icon.png"), mcodePluginIcon()},
		// The skill, inside the package: the runtime loads a package's skills
		// from the manifest, and the directory name has to match the name in
		// the frontmatter.
		{filepath.Join(root, "skills", "deja-history", "SKILL.md"), []byte(skillFile(guidanceBody))},
	}
	// Whether the package was there before this run: the report says
	// "created" for a package that is not there yet and "updated" for one
	// whose files changed under it.
	existed := isRealDir(root)
	// The result is the package itself, and the files inside it are named one
	// by one under it. A report that named only the first file left the
	// others unnamed, and a reader of the report could not tell what had
	// been written (#3254).
	res := installResult{Path: root, Action: "unchanged"}
	var lines []string
	for _, f := range files {
		if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
			return installResult{}, err
		}
		old, err := readConfig(f.path)
		if err != nil {
			return installResult{}, err
		}
		action, err := writeIfChanged(f.path, old, f.body)
		if err != nil {
			return installResult{}, err
		}
		res.also = append(res.also, f.path)
		if action == "unchanged" {
			continue
		}
		if res.Action == "unchanged" {
			if existed {
				res.Action = "updated"
			} else {
				res.Action = "created"
			}
		}
		lines = append(lines, fmt.Sprintf("also %s %s", action, shortHome(f.path)))
	}
	res.Note = strings.Join(lines, "; ")
	return res, nil
}
