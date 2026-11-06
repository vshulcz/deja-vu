package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

func doctorMCP(w io.Writer) {
	fmt.Fprintln(w, "MCP wiring:")
	for _, c := range doctorMCPConfigs() {
		status := "config missing"
		if doctorExists(c.path) {
			if c.wired(c.path) {
				status = "wired"
			} else {
				status = "not wired"
			}
		}
		// The Kimi Code plugin declares the same server, and stands down when
		// this file already has it. Reporting a machine that installed the
		// plugin as "not wired" sends someone to run an install that only
		// pushes the plugin aside.
		if status != "wired" && c.name == "kimi" && kimiPluginInstalled() {
			status = "plugin"
		}
		// And the Grok Build plugin, which declares the same server and stands
		// down when config.toml already has [mcp_servers.deja] (#1828).
		if status != "wired" && c.name == "grok" && grokPluginInstalled() {
			status = "plugin"
		}
		// Same for Codex: `codex plugin add deja-vu@deja-vu` brings the server
		// and the hooks with it.
		if status != "wired" && c.name == "codex" && codexPluginInstalled() {
			status = "plugin"
		}
		if c.name == "cherrystudio" && doctorCherryStudioMCP(w, status, c.path) {
			continue
		}
		// A pi older than 0.99 has no MCP of its own and reads the file only
		// through the adapter package; a newer one can have its built-in MCP
		// switched off. Either way the server never starts (#4583).
		if status == "wired" && c.name == "pi" {
			if note := piMCPUnreadNote(); note != "" {
				fmt.Fprintf(w, "  %-12s %-14s guidance %-11s %s\n", c.name, "no adapter", guidanceStatus(guidanceHarness(c.name)), reportPath(c.path))
				fmt.Fprintf(w, "  %-12s %s\n", "", note)
				continue
			}
		}
		// WorkBuddy AI declares the server and starts it only once the user
		// has clicked Trust for it (#4740).
		label, trust := status, ""
		if status == "wired" && c.name == "workbuddy" {
			if trust = workBuddyUntrustedNote(); trust != "" {
				label = "untrusted"
			}
		}
		fmt.Fprintf(w, "  %-12s %-14s guidance %-11s %s\n", c.name, label, guidanceStatus(guidanceHarness(c.name)), reportPath(c.path))
		if trust != "" {
			fmt.Fprintf(w, "  %-12s %s\n", "", trust)
		}
		if c.name == "codebuddy" {
			if note := codeBuddyPluginNote(); note != "" {
				fmt.Fprintf(w, "  %-12s %s\n", "", note)
			}
		}
		// Cascade's rules file has a cap the user's own rules share, so the
		// block can be missing for a reason install already gave.
		if status == "wired" && c.name == "windsurf" {
			fmt.Fprintf(w, "  %-12s global rules %s: %s\n", "", reportPath(windsurfRulesPath()), windsurfRulesState())
		}
		// One "wired" can be two registrations: a hand add under another name
		// — the project is called deja-vu, after all — plus the `deja` a later
		// install wrote beside it. Each session then starts the server twice
		// and carries the tool schema twice, and the boolean above cannot say
		// so (#2269).
		if status == "wired" && c.dupes != nil {
			if keys := c.dupes(c.path); len(keys) >= 2 {
				fmt.Fprintf(w, "  %-12s %s\n", "", doctorMCPDuplicateNote(keys))
			}
		}
		// "Wired" says the server is declared, not that it can start. A config
		// naming a binary that is gone — a restored backup, a hand edit, a
		// machine where deja moved and one file was fixed by hand — read as
		// healthy while no memory arrived (#2216).
		if status == "wired" {
			if missing := dejaCommandMissing(c.path); missing != "" {
				fix := "`deja install " + c.name + "` rewrites it for this binary"
				if c.name == "cherrystudio" {
					fix += ", then re-import it in Settings → MCP"
				}
				fmt.Fprintf(w, "  %-12s %s\n", "", "points at "+missing+", which is not there — "+fix)
			} else if other := otherBinaryNote(c.path, c.name); other != "" {
				// The quieter half: the binary is there and is neither this one
				// nor the deja on PATH. Two harnesses on the machine this was
				// found on pointed at builds left behind by probe runs, and
				// both rows read `wired` (#3656).
				fmt.Fprintf(w, "  %-12s %s\n", "", other)
			}
		}
		// Declared and switched off is not wired in any sense a session
		// feels: the client never starts the server (#4303).
		if status == "wired" && dejaEntrySwitchedOff(c.name, c.path) {
			fmt.Fprintf(w, "  %-12s %s\n", "",
				"the entry is switched off — "+c.name+" will not start it until you turn it back on")
		}
		// Zed's entry can defer to an extension instead of naming a binary,
		// and then "wired" is a fact about an id rather than about anything
		// runnable (#3660).
		if status == "wired" && c.name == "deepseek" {
			if missing := dshPluginsMissing(c.path); len(missing) > 0 {
				fmt.Fprintf(w, "  %-12s %s\n", "", dshPluginsMissingNote(c.path, missing))
			}
		}
		if status == "wired" && c.name == "zed" {
			if note := zedUnreachableNote(c.path); note != "" {
				fmt.Fprintf(w, "  %-12s %s\n", "", note)
			}
		}
		if note := doctorMCPSwitchedOff(c.name); note != "" && status == "wired" {
			fmt.Fprintf(w, "  %-12s %s\n", "", note)
		}
		if note := doctorWiringNote(c.name); note != "" && status == "wired" {
			fmt.Fprintf(w, "  %-12s %s\n", "", note)
		}
	}
}

// doctorMCPDuplicateNote is the line a duplicated setup never got to read: it
// names every key so the reader can decide which one is theirs. Which one to
// remove is not deja's call — one of them may be a hand add carrying fields
// deja does not know about (#2269).
func doctorMCPDuplicateNote(keys []string) string {
	quoted := make([]string, len(keys))
	for i, key := range keys {
		quoted[i] = "`" + strings.ReplaceAll(key, "`", "'") + "`"
	}
	names := strings.Join(quoted, ", ")
	if len(keys) == 2 {
		return fmt.Sprintf("two entries in this config run deja (%s) — every session starts the server twice", names)
	}
	return fmt.Sprintf("%d entries in this config run deja (%s) — every session starts the server %d times", len(keys), names, len(keys))
}

// dejaEntrySwitchedOff reports whether every deja entry in a JSON config is
// switched off — `disabled: true`, or opencode's `enabled: false`, the test
// install uses. One entry still on is enough for recall to work. A client
// that has no switch on the entry is never off by this test.
func dejaEntrySwitchedOff(client, path string) bool {
	if !entrySwitchHonored(client) {
		return false
	}
	b, err := doctorReadConfig(path)
	if err != nil {
		return false
	}
	var root map[string]any
	if json.Unmarshal([]byte(jsoncToJSON(string(b))), &root) != nil {
		return false
	}
	off := false
	for _, m := range mcpServerMaps(root) {
		for key, v := range m {
			if key != "deja" && !mcpEntryRunsDeja(v) {
				continue
			}
			entry, ok := v.(map[string]any)
			if !ok || !entrySwitchedOffFor(client, entry) {
				return false
			}
			off = true
		}
	}
	return off
}

// dejaCommandMissing returns the deja binary a config names when that file is
// not there, and "" when the config names one that is, names none, or names it
// by bare name for the PATH to resolve. deja writes these commands itself, so
// reading them back is the difference between "declared" and "can start".
func dejaCommandMissing(path string) string {
	cmd := dejaCommandIn(path)
	// A bare name is the PATH's business, and this check would answer for a
	// lookup it does not do. A relative path or a `~` is worse than that: it
	// resolves against wherever the reader is standing, or against nothing,
	// so a stat here would report a working setup broken.
	if cmd == "" || !filepath.IsAbs(cmd) {
		return ""
	}
	if _, err := os.Stat(cmd); err == nil {
		return ""
	}
	return cmd
}

// dejaCommandIn finds the command a config runs deja with. JSON configs are
// parsed the way the wiring check parses them; the rest are read a line at a
// time, because a `command` key with a path whose name is deja means the same
// thing in TOML, YAML and JSONC, and adding a parser per format to answer one
// question is not worth the surface.
func dejaCommandIn(path string) string {
	b, err := doctorReadConfig(path)
	if err != nil {
		return ""
	}
	var root map[string]any
	// JSONC first-class: opencode takes comments and trailing commas in its
	// config, and a strict read sent those to the line scan below, which
	// does not know opencode's list-shaped command (#4197).
	if json.Unmarshal([]byte(jsoncToJSON(string(b))), &root) == nil {
		for _, m := range mcpServerMaps(root) {
			for _, v := range m {
				if cmd := mcpEntryDejaCommand(v); cmd != "" {
					return cmd
				}
			}
			// Nothing recognised by name. An entry under the key deja writes
			// is deja's own whatever binary it happens to run — a build called
			// `deja-probe` or `deja-cont` is what a `go build -o` leaves, and
			// an entry naming one was invisible to every check here while the
			// row still said wired (#3659).
			if v, ok := m["deja"]; ok {
				if cmd := mcpEntryCommand(v); cmd != "" {
					return cmd
				}
			}
			// And the keys deja writes that are not just `deja`: Zed's entry is
			// `deja-context-server`, the id its extension owns. With a build
			// under another name in it — `deja-arm` from a probe run — neither
			// the name test nor the `deja` key matched, so the row said `wired`
			// about a server pointing into a scratch directory (#3683).
			for key, v := range m {
				if !strings.HasPrefix(strings.ToLower(key), "deja") {
					continue
				}
				if cmd := mcpEntryCommand(v); cmd != "" {
					return cmd
				}
			}
		}
		// Parsed, and nothing of deja's under any container this knows. The
		// text reader looks for the key by name instead, which is how a
		// harness with a container nobody has added here still gets an answer
		// — and it can only find what this walk missed, since both require a
		// deja-named key or binary (#3683).
		return dejaKeyedCommand(string(b))
	}
	// The attributed read first. The scan below takes any `command` in the file
	// whose value looks like deja, and goose keeps a `slash_commands` list at
	// the bottom of the same config with `- command: "deja"` in it — so the
	// scan answered with the name of a slash command and the MCP entry three
	// lines from the top, pointing at a build in a scratch directory, was never
	// looked at (#3662).
	if cmd := dejaKeyedCommand(string(b)); cmd != "" {
		return cmd
	}
	for _, m := range commandValue.FindAllStringSubmatch(string(b), -1) {
		// One group per quoting, so a quote inside a value cannot end it: a
		// path under C:\Users\O'Brien is a path, not a delimiter.
		//
		// Only the double-quoted form carries escapes. TOML's literal string
		// and YAML's single-quoted scalar keep their backslashes, which is how
		// a Windows path is normally written there, and reading an escaped one
		// raw named a path that exists nowhere — doctor then called a working
		// install broken (#2216).
		var value string
		switch {
		case m[1] != "":
			value = quotedPathUnescape.Replace(m[1])
		case m[2] != "":
			value = m[2]
		default:
			value = m[3]
		}
		if value = strings.TrimSpace(value); commandIsDeja(value) {
			return value
		}
	}
	return ""
}

// mcpServerMaps returns every map of servers a config might keep them in. The
// three top-level spellings, and `mcp.servers` one level deeper — which is
// OpenClaw's and ZCode's shape, and was read as an entry rather than as a map
// of them, so no check here could see the binary either of those two runs
// (#3663).
func mcpServerMaps(root map[string]any) []map[string]any {
	var out []map[string]any
	// context_servers is Zed's spelling of the same map (#3683).
	for _, key := range []string{"mcpServers", "mcp", "servers", "context_servers", ampServersKey} {
		m, _ := root[key].(map[string]any)
		if m == nil {
			continue
		}
		out = append(out, m)
		if nested, ok := m["servers"].(map[string]any); ok {
			out = append(out, nested)
		}
	}
	// Muse's legacy `mcp_servers` block is active when `mcpServers` is not also
	// present (#4709).
	if legacy, _ := root["mcp_servers"].(map[string]any); legacy != nil {
		if _, hasCurrent := root["mcpServers"].(map[string]any); !hasCurrent {
			out = append(out, legacy)
		}
	}
	return out
}

// quotedPathUnescape undoes what a quoted string does to a Windows path. Only
// the two escapes a path can carry: anything else in a command line is not
// something this check should be interpreting.
var quotedPathUnescape = strings.NewReplacer(`\\`, `\`, `\"`, `"`)

// commandValue matches a `command` or `cmd` key and the value after it, in the
// three shapes these configs come in: JSON and JSONC quote the key, TOML uses
// `=`, YAML uses `:` and quotes nothing. A file that does not parse even as
// JSONC — JSON5, YAML, TOML — reaches this too, so the whole text is scanned
// rather than a line at a time.
var commandValue = regexp.MustCompile(`"?(?:command|cmd)"?\s*[:=]\s*(?:"([^"\n]*)"|'([^'\n]*)'|([^",\n}]+))`)

// mcpEntryDejaCommand is mcpEntryRunsDeja's answer to "which one": the same
// walk, returning the command or argument that named deja.
func mcpEntryDejaCommand(v any) string {
	m, _ := v.(map[string]any)
	if m == nil {
		return ""
	}
	// The top level first: when an entry carries both, that is the one deja
	// wrote, and naming the nested one sent the reader after a path deja had
	// already replaced (#2716).
	switch c := m["command"].(type) {
	case string:
		if commandIsDeja(c) {
			return strings.TrimSpace(c)
		}
	case []any:
		// opencode writes the command as a list. The recogniser reads that
		// shape since #2713, and a check that says "wired" without being able
		// to name the binary cannot report one that is gone.
		for _, v := range c {
			if s, ok := v.(string); ok && commandIsDeja(s) {
				return strings.TrimSpace(s)
			}
		}
	}
	args, _ := m["args"].([]any)
	for _, a := range args {
		if s, ok := a.(string); ok && commandIsDeja(s) {
			return strings.TrimSpace(s)
		}
	}
	if t, ok := m["transport"].(map[string]any); ok {
		if cmd := mcpEntryDejaCommand(t); cmd != "" {
			return cmd
		}
	}
	return ""
}

// mcpEntryCommand is mcpEntryDejaCommand without the name test: the command an
// entry runs, whatever it is called. Only callers that already know the entry
// is deja's — because it sits under the key deja writes — may use it.
func mcpEntryCommand(v any) string {
	m, _ := v.(map[string]any)
	if m == nil {
		return ""
	}
	switch c := m["command"].(type) {
	case string:
		return strings.TrimSpace(c)
	case []any:
		for _, item := range c {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	if t, ok := m["transport"].(map[string]any); ok {
		return mcpEntryCommand(t)
	}
	return ""
}

// dejaKeyedCommand is the same claim for the formats read as text: the line that
// names deja, then the first `command` inside the block it opens. Hermes keeps
// its servers in YAML and named one `deja-cont`, which nothing here could see
// (#3659).
//
// Two kinds of anchor, because the block is shaped differently under each. A
// mapping key (`deja:`) has its fields indented below it. A TOML table header
// and a `serverName:` field both sit *beside* the command instead — TOML puts
// every key of a table at the header's own indent, and dsh names the server in
// a field of the row it belongs to — so for those the block is the run of
// siblings until the next table header or a line further out.
func dejaKeyedCommand(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		opens, beside := dejaBlockOpens(trimmed)
		if !opens {
			continue
		}
		indent := yamlIndentWidth(line)
		for _, next := range lines[i+1:] {
			nextTrimmed := strings.TrimSpace(next)
			if nextTrimmed == "" {
				continue
			}
			if outOfDejasBlock(next, nextTrimmed, indent, beside) {
				break
			}
			if m := commandValue.FindStringSubmatch(next); m != nil {
				for _, group := range m[1:] {
					if v := strings.TrimSpace(group); v != "" {
						return quotedPathUnescape.Replace(v)
					}
				}
			}
		}
	}
	return ""
}

// outOfDejasBlock reports whether a line has left the block the anchor opened.
//
// For an anchor whose fields sit beside it, only a new table header or a line
// further out ends the block: the old rule stopped at the first sibling that
// was not `command`, which in a codex `[mcp_servers.deja]` written before
// #5590 is `type = "stdio"` on the very next line — so a config whose command
// is not the first key
// read as though it named no binary at all, and the row said `wired` about a
// build in a scratch directory (#3668).
func outOfDejasBlock(line, trimmed string, indent int, beside bool) bool {
	if beside {
		if strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "- ") {
			return true
		}
		return yamlIndentWidth(line) < indent
	}
	if yamlIndentWidth(line) > indent || strings.HasPrefix(trimmed, "-") {
		return false
	}
	if strings.HasPrefix(trimmed, "[") || strings.Contains(trimmed, ":") || strings.Contains(trimmed, "=") {
		return !strings.HasPrefix(trimmed, "command")
	}
	return false
}

// dejaBlockOpens reports whether a line starts the block that belongs to deja,
// and whether that block's keys sit beside the anchor rather than under it. A
// mapping key indents its fields below; a TOML table header does not, and dsh
// and Continue keep the server name in a field of the list row (`serverName:`
// or `- name:`), beside or above the command in the same item.
func dejaBlockOpens(trimmed string) (opens, beside bool) {
	switch trimmed {
	case "deja:":
		return true, false
	case "[mcp_servers.deja]", "[mcp.servers.deja]",
		"serverName: deja", `serverName: "deja"`, "serverName: 'deja'",
		"- name: deja", `- name: "deja"`, "- name: 'deja'":
		return true, true
	}
	// A quoted JSON key, for the files that do not parse even as JSONC, which
	// are read a line at a time; Zed's server key is `deja-context-server`
	// rather than `deja` (#3683).
	if key, ok := jsonKeyOpening(trimmed); ok && strings.HasPrefix(strings.ToLower(key), "deja") {
		return true, false
	}
	return false, false
}

// jsonKeyOpening reads `"name": {` — the line that opens an object under a
// key — and returns the key.
func jsonKeyOpening(trimmed string) (string, bool) {
	if !strings.HasPrefix(trimmed, `"`) {
		return "", false
	}
	end := strings.Index(trimmed[1:], `"`)
	if end < 0 {
		return "", false
	}
	key := trimmed[1 : 1+end]
	rest := strings.TrimSpace(trimmed[1+end+1:])
	if !strings.HasPrefix(rest, ":") {
		return "", false
	}
	if strings.TrimSpace(strings.TrimPrefix(rest, ":")) != "{" {
		return "", false
	}
	return key, true
}

// doctorCherryStudioMCP prints the cherrystudio row from the app's own server
// table when it can be read, and reports false when it cannot, leaving the
// import-file row and its caveat to the caller. The import file existing says
// nothing about the app having the server (#4344).
func doctorCherryStudioMCP(w io.Writer, fileStatus, importPath string) bool {
	known, wired, off, db, missing := cherryStudioAppWiring()
	if !known {
		return false
	}
	guidance := guidanceStatus(guidanceHarness("cherrystudio"))
	if off {
		fmt.Fprintf(w, "  %-12s %-14s guidance %-11s %s\n", "cherrystudio", "disabled", guidance, reportPath(db))
		fmt.Fprintf(w, "  %-12s %s\n", "", "Cherry Studio has deja's server switched off, so it never starts — turn it on in Settings → MCP")
		return true
	}
	if !wired {
		status := "not imported"
		if fileStatus == "config missing" {
			status = fileStatus
		}
		fmt.Fprintf(w, "  %-12s %-14s guidance %-11s %s\n", "cherrystudio", status, guidance, reportPath(importPath))
		fix := "`deja install cherrystudio`, then import " + reportPath(importPath)
		if status == "not imported" {
			fix = "import this file"
		}
		fmt.Fprintf(w, "  %-12s %s\n", "", "Cherry Studio has no deja server — "+fix+" in Settings → MCP → Import from JSON")
		return true
	}
	fmt.Fprintf(w, "  %-12s %-14s guidance %-11s %s\n", "cherrystudio", "wired", guidance, reportPath(db))
	if missing != "" {
		// Rewriting the import file does not reach the app: its copy keeps
		// the dead path until the server is re-imported or edited.
		fmt.Fprintf(w, "  %-12s %s\n", "", "points at "+missing+", which is not there — `deja install cherrystudio`, then re-import "+
			reportPath(importPath)+" in Settings → MCP (or fix the command there)")
	}
	return true
}

// doctorWiringNote adds what "wired" cannot promise for a given harness. Three
// CLIs share ~/.grok and read different files; one of them — @vibe-kit/grok-cli
// — has no user-level MCP config at all, so `grok mcp list` reports nothing no
// matter what an installer writes to the home directory. Saying "wired" without
// that caveat is how someone concludes deja is broken.
func doctorWiringNote(name string) string {
	switch name {
	case "grok":
		// Grok Build rejects that command ("unexpected argument '-c'") and
		// reads the server from config.toml already, so the line is for a
		// machine whose grok is the vibe-kit CLI, or that has none (#4587).
		if !grokOnPathIsVibeKit() {
			return ""
		}
		return "@vibe-kit/grok-cli reads MCP only from <cwd>/.grok/settings.json — run `grok mcp add deja -c deja -a mcp` in a project to wire that one"
	case "cherrystudio":
		// The only target whose "wired" is about a file the app has not read
		// yet: Cherry Studio keeps its servers in an app database with no
		// config file to write, so the install writes the JSON its importer
		// takes and the row must not be read as "the app has it".
		return "Cherry Studio has no config file to write — this is the JSON to import in Settings → MCP → Import from JSON"
	case "roo", "kilocode":
		// One settings file per VS Code-compatible host, and the path above is
		// whichever one exists. `deja install` writes every host that has the
		// extension; the row can only speak for one.
		return "one settings file per editor — `deja install " + name + "` writes every host that has the extension"
	}
	return ""
}

// grokOnPathIsVibeKit reports whether the `grok` on PATH is
// @vibe-kit/grok-cli, or whether there is no grok to ask. Read off where the
// command resolves to, or off a small launcher script that names the package,
// so doctor runs nothing to find out.
func grokOnPathIsVibeKit() bool {
	p, err := exec.LookPath("grok")
	if err != nil {
		return true
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	if strings.Contains(filepath.ToSlash(p), "@vibe-kit/") {
		return true
	}
	if st, err := os.Stat(p); err == nil && st.Size() < 64<<10 {
		b, _ := os.ReadFile(p)
		return bytes.Contains(b, []byte("@vibe-kit/grok-cli"))
	}
	return false
}

type doctorMCPConfig struct {
	name  string
	path  string
	wired func(string) bool
	// dupes lists every key in the config that runs deja, so doctorMCP can say
	// when "wired" is really two registrations (#2269). Nil where the format
	// has no counting probe — the boolean is then all doctor can promise.
	dupes func(string) []string
}

func doctorMCPConfigs() []doctorMCPConfig {
	configs := []doctorMCPConfig{ //nolint:prealloc // a literal table; the loop below adds a row per IDE
		{"claude-code", sources.ClaudeJSONPath(), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"codex", filepath.Join(sources.CodexHome(), "config.toml"), doctorTOMLWired, doctorTOMLDejaKeys},
		{"opencode", doctorOpencodeConfigPath(), doctorJSONWiredIn(doctorOpencodeServers), doctorJSONDejaKeysIn(doctorOpencodeServers)},
		{"cursor", filepath.Join(sources.CursorCLIHome(), "mcp.json"), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"gemini", filepath.Join(sources.GeminiHome(), "settings.json"), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"antigravity", filepath.Join(antigravityConfigHome(), "mcp_config.json"), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"grok", filepath.Join(sources.GrokHome(), "config.toml"), doctorTOMLWired, doctorTOMLDejaKeys},
		{"qwen", filepath.Join(sources.QwenConfigDir(), "settings.json"), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"codebuddy", codeBuddyMCPPath(), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"workbuddy", workBuddyMCPPath(), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"trae", traeConfigPath(), doctorTOMLWired, doctorTOMLDejaKeys},
		{"muse", museSettingsPath(), doctorJSONWiredIn(doctorMuseServers), doctorJSONDejaKeysIn(doctorMuseServers)},
		{"kimi", filepath.Join(sources.KimiConfigDir(), "mcp.json"), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"cline", sources.ClineMCPSettingsPath(), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"pi", filepath.Join(sources.PiConfigDir(), "mcp.json"), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"omp", filepath.Join(sources.OmpConfigDir(), "mcp.json"), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"amp", sources.AmpSettingsFile(), doctorJSONWired(ampServersKey), doctorJSONDejaKeys(ampServersKey)},
		{"prime", primeSettingsPath(), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"openclaw", filepath.Join(sources.OpenClawStateDir(), "openclaw.json"), doctorOpenClawWired, doctorJSONDejaKeysIn(doctorMCPServersBlock)},
		{"copilot", copilotMCPConfigPath(), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"vscode", doctorVSCodeMCPPath(), doctorJSONWired("servers"), doctorJSONDejaKeys("servers")},
		{"hermes", filepath.Join(sources.HermesHome(), "config.yaml"), doctorHermesWired, nil},
		{"goose", filepath.Join(gooseConfigDir(), "config.yaml"), doctorGooseWired, nil},
		{"continue", continueConfigPath(), doctorContinueWired, nil},
		{"crush", crushConfigPath(), doctorJSONWired("mcp"), doctorJSONDejaKeys("mcp")},
		{"zed", sources.ZedSettingsPath(), doctorZedWired, nil},
		{"devin", devinMCPConfigPath(), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		// The nine targets `deja install` has always had and this table never
		// named. A row here is the only place a machine says whether the
		// server is declared and which binary it runs, so for these the report
		// said nothing at all — deepseek's entry on the author's machine still
		// pointed at a throwaway build with every other row repaired.
		{"deepseek", dshPatchPath(), doctorDSHWired, nil},
		{"roo", doctorFirstExisting(rooMCPSettingsPaths(), vsCodeExtensionMCPPath(sources.RooExtensionID)), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"kilocode", kilocodeDoctorPath(), doctorKilocodeWired, doctorJSONDejaKeysIn(doctorKilocodeServers)},
		{"kiro", kiroMCPSettingsPath(), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"senpi", senpiMCPPath(), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"kimchi", kimchiMCPPath(), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"codewhale", codewhaleMCPPath(), doctorJSONWired("servers"), doctorJSONDejaKeys("servers")},
		{"junie", junieMCPPath(), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"jetbrains", jetBrainsMCPPath(), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"windsurf", windsurfMCPPath(), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"mcode", mcodeConfigPath(), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"gjc", gjcMCPPath(), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"zcode", zcodeDoctorPath(), doctorZCodeWired, doctorJSONDejaKeysIn(doctorMCPServersBlock)},
		{"commandcode", commandCodeMCPPath(), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"cherrystudio", cherryStudioImportPath(), doctorFileWired, nil},
		// The server rides in deja's plugin package; the manifest Reasonix
		// loaded it from is the file that says so.
		{"reasonix", reasonixInstalledManifest(), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
	}
	// One row per TRAE IDE build on this machine; each reads its own file.
	for _, e := range traeIDEPresent() {
		configs = append(configs, doctorMCPConfig{"trae-ide", e.mcpPath(), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")})
	}
	return configs
}

// dshPatchPath is the home-level patch layer installDeepSeek writes.
func dshPatchPath() string {
	return filepath.Join(sources.DSHHome(), "cordis.patch.yml")
}

// doctorDSHWired reads the layer for deja's own block rather than for a server
// key: dsh has no MCP config of its own, it has an ordered list of patch
// entries, and deja's is `mcp-deja`.
func doctorDSHWired(path string) bool {
	b, err := readConfig(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(b), "id: mcp-deja")
}

// dshPluginsMissing lists the plugin files deja's block in the layer names by
// path and that are not on disk. dsh imports every row when it builds a
// profile, and one file that is gone fails the whole load: no agent at all, not
// just no recall, while the server row above still reads wired (#4292).
func dshPluginsMissing(path string) []string {
	b, err := readConfig(path)
	if err != nil {
		return nil
	}
	var missing []string
	inBlock := false
	for line := range strings.SplitSeq(lfText(b), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, dshBlockStart):
			inBlock = true
			continue
		case t == dshBlockEnd:
			inBlock = false
			continue
		}
		name, ok := strings.CutPrefix(t, "name:")
		if !inBlock || !ok {
			continue
		}
		for _, m := range dshNameMissing(yamlScalar(strings.TrimSpace(name))) {
			if !slices.Contains(missing, m) {
				missing = append(missing, m)
			}
		}
	}
	return missing
}

// dshNameMissing is what one plugin name resolves to that dsh cannot load.
// The rules are dsh 0.1.1-rc.2's, measured rather than assumed: a name
// starting with "." is resolved against the directory of the profile being
// built, not against the layer, so it is checked in every profile there is; a
// file:// URL is imported as the file; `~/` is never expanded and loads
// nothing even when the file is there. A package name such as
// '@deepseek-ai/dsh-mcp-client' resolves inside dsh's own bundle and is not a
// file deja can check.
func dshNameMissing(name string) []string {
	switch {
	case strings.HasPrefix(name, "~/"):
		return []string{name}
	case strings.HasPrefix(name, "file://"):
		u, err := url.Parse(name)
		if err != nil {
			return nil
		}
		// dsh's URL parser keeps a drive letter as part of the path, written
		// file://C:/x or file:///C:/x; Go's reads the first as host "C:" and
		// the second as /C:/x, and either way doctor named a drive-less path
		// that is not the one in the layer (#4438).
		// Any other host but localhost names a share, \\host\path.
		p := u.Path
		switch {
		case len(u.Host) == 2 && u.Host[1] == ':':
			p = u.Host + p
		case u.Host != "" && u.Host != "localhost":
			p = "//" + u.Host + p
		case len(p) > 2 && p[0] == '/' && p[2] == ':':
			p = p[1:]
		}
		p = filepath.FromSlash(p)
		if !doctorExists(p) {
			return []string{p}
		}
	case strings.HasPrefix(name, "."):
		var out []string
		for _, dir := range dshProfileDirs() {
			if p := filepath.Join(dir, filepath.FromSlash(name)); !doctorExists(p) {
				out = append(out, p)
			}
		}
		return out
	case filepath.IsAbs(name) && !doctorExists(name):
		return []string{name}
	}
	return nil
}

// dshProfileDirs are the profiles dsh has generated, one directory each beside
// the node_modules their plugins load from.
func dshProfileDirs() []string {
	root := filepath.Join(sources.DSHHome(), "profiles")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && e.Name() != "node_modules" && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, filepath.Join(root, e.Name()))
		}
	}
	return out
}

// dshLayerNamesMissing reports whether the layer names a plugin file that is gone.
func dshLayerNamesMissing(file string) bool {
	return slices.Contains(dshPluginsMissing(dshPatchPath()), file)
}

// dshPluginsMissingNote is the line under the deepseek row when the layer names
// a plugin file that is gone. The -auto target is named when the layer carries
// the auto row, since the plain one would take that row out.
func dshPluginsMissingNote(path string, missing []string) string {
	b, _ := readConfig(path)
	target := "deepseek"
	if strings.Contains(string(b), "id: deja-auto") {
		target = "deepseek-auto"
	}
	names := make([]string, len(missing))
	for i, m := range missing {
		names[i] = reportPath(m)
	}
	list, it := names[0], "it"
	if n := len(names); n > 1 {
		list, it = strings.Join(names[:n-1], ", ")+" and "+names[n-1], "them"
	}
	return "names " + list + ", which dsh cannot find — dsh will not start; `deja install " + target + "` writes " + it + " again, or `deja uninstall deepseek` takes deja out of the layer"
}

// yamlScalar reads one plain, single- or double-quoted YAML scalar, the three
// ways a hand edit or deja's own yamlQuote can spell a path. A comment after
// it is not part of it: a plain scalar ends at " #", a quoted one at its
// closing quote.
func yamlScalar(v string) string {
	if len(v) >= 2 && v[0] == '\'' {
		for i := 1; i < len(v); i++ {
			if v[i] != '\'' {
				continue
			}
			if i+1 < len(v) && v[i+1] == '\'' {
				i++
				continue
			}
			return strings.ReplaceAll(v[1:i], "''", "'")
		}
	}
	if len(v) >= 2 && v[0] == '"' {
		for i := 1; i < len(v); i++ {
			if v[i] == '\\' {
				i++
				continue
			}
			if v[i] == '"' {
				r := strings.NewReplacer(`\\`, `\`, `\"`, `"`)
				return r.Replace(v[1:i])
			}
		}
	}
	for i := 1; i < len(v); i++ {
		if v[i] == '#' && (v[i-1] == ' ' || v[i-1] == '\t') {
			return strings.TrimSpace(v[:i])
		}
	}
	return v
}

// doctorZCodeWired reads `mcp.servers`, one level deeper than the `mcpServers`
// the rest of this table uses.
func doctorZCodeWired(path string) bool {
	return doctorJSONWiredIn(doctorMCPServersBlock)(path)
}

// doctorFirstExisting names the host a report should talk about when a harness
// has one settings file per VS Code-compatible editor: the one that is there.
// A row with no path at all says less than nothing, so fallback takes the first
// candidate, and then the one passed in — Kilo Code's reader lists only
// directories that exist, which on a machine without the extension is none.
func doctorFirstExisting(paths []string, fallback string) string {
	for _, p := range paths {
		if doctorExists(p) {
			return p
		}
	}
	if len(paths) > 0 {
		return paths[0]
	}
	return fallback
}

// kilocodeDoctorPath names the config that is actually there. Kilo has two: the
// extension's settings under a VS Code host, and the CLI's own
// `<config>/kilo/kilo.jsonc`, which is OpenCode-shaped because the CLI is
// OpenCode vendored. A machine with only the CLI had its row pointing at an
// editor path that does not exist (#3672).
func kilocodeDoctorPath() string {
	for _, p := range kilocodeMCPSettingsPaths() {
		if doctorExists(p) {
			return p
		}
	}
	if cli := kilocodeCLIConfigPath(); doctorExists(cli) {
		return cli
	}
	if paths := kilocodeMCPSettingsPaths(); len(paths) > 0 {
		return paths[0]
	}
	return vsCodeExtensionMCPPath(sources.KiloExtensionID)
}

// doctorKilocodeServers picks the server map out of whichever of Kilo's shapes
// the file is in: `mcpServers` in the extension's settings, or OpenCode-shaped
// `mcp` / `mcp.servers` in the CLI's kilo.jsonc.
func doctorKilocodeServers(root map[string]any) map[string]any {
	if m, _ := root["mcpServers"].(map[string]any); m != nil {
		return m
	}
	return doctorOpencodeServers(root)
}

// doctorKilocodeWired reads whichever of the two shapes the named file is in:
// `mcpServers` in the extension's settings, `mcp` / `mcp.servers` in the CLI's config.
func doctorKilocodeWired(path string) bool {
	return doctorJSONWiredIn(doctorKilocodeServers)(path)
}

// vsCodeExtensionMCPPath is where an extension would keep its MCP settings in
// plain VS Code, for a report on a machine that has neither the editor nor the
// extension: both readers list only directories that exist, so on such a
// machine the row had no path to print at all.
func vsCodeExtensionMCPPath(extension string) string {
	return filepath.Join(vsCodeDefaultUserDir(), "globalStorage", extension, "settings", "mcp_settings.json")
}

// doctorZedWired reads the same JSONC the installer writes, with the same
// scanner. The generic probe falls back to looking for "deja" anywhere in an
// unparseable file, which in a settings file full of comments answers a
// different question than "is the server wired".
//
// Either id counts as wired: the current one, and the one deja wrote before
// the two halves were given the same id. A machine that has not reinstalled
// since is still wired, and should not be told otherwise.
func doctorZedWired(path string) bool {
	b, err := readConfig(path)
	if err != nil {
		return false
	}
	text := string(b)
	return zedLocate(text, zedServerID) != nil || zedLocate(text, zedLegacyServerID) != nil
}

func doctorFileWired(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// doctorVSCodeMCPPath is the mcp.json in the first VS Code User folder present.
// Copilot Chat's config key is `servers`, not the common `mcpServers`.
func doctorVSCodeMCPPath() string {
	dirs := vsCodeUserDirs()
	if len(dirs) == 0 {
		return filepath.Join(vsCodeDefaultUserDir(), "mcp.json")
	}
	return filepath.Join(dirs[0], "mcp.json")
}

func doctorOpencodeConfigPath() string {
	dir := filepath.Join(opencodeConfigHome(), "opencode")
	path := filepath.Join(dir, "opencode.json")
	if !doctorExists(path) {
		if jsonc := filepath.Join(dir, "opencode.jsonc"); doctorExists(jsonc) {
			return jsonc
		}
	}
	return path
}

// doctorOpenClawWired checks openclaw.json's nested mcp.servers map, with
// any $include read in: an entry added by hand to the included file is the
// wiring `deja install openclaw` asks for there (#5331).
func doctorOpenClawWired(path string) bool {
	return doctorJSONWiredIn(doctorMCPServersBlock)(path)
}

// doctorJSONBlock picks the server map out of a config that keeps it under one
// top-level key.
func doctorJSONBlock(key string) func(map[string]any) map[string]any {
	return func(root map[string]any) map[string]any {
		m, _ := root[key].(map[string]any)
		return m
	}
}

// doctorMCPServersBlock picks the nested `mcp.servers` map used by OpenClaw
// and ZCode.
func doctorMCPServersBlock(root map[string]any) map[string]any {
	m, _ := root["mcp"].(map[string]any)
	if m == nil {
		return nil
	}
	sub, _ := m["servers"].(map[string]any)
	return sub
}

// doctorOpencodeServers is the same for opencode, which has two shapes: 1.x
// files the servers directly under `mcp`, 2.x one level down under
// `mcp.servers`, and the installer writes into whichever the config already
// uses. Doctor has to read both, or it reports a wired 2.x config as unwired
// and the user re-runs an install that had nothing left to do.
func doctorOpencodeServers(root map[string]any) map[string]any {
	m, _ := root["mcp"].(map[string]any)
	if m == nil {
		return nil
	}
	if nested, ok := opencodeNestedServers(m); ok {
		return nested
	}
	return m
}

// doctorMuseServers is the block Muse loads: either key on its own, and
// neither when both are present, since Muse then drops every server (#4709).
func doctorMuseServers(root map[string]any) map[string]any {
	legacy, hasLegacy := root["mcp_servers"].(map[string]any)
	current, hasCurrent := root["mcpServers"].(map[string]any)
	if hasLegacy && hasCurrent {
		return nil
	}
	if hasLegacy {
		return legacy
	}
	return current
}

func doctorJSONWired(key string) func(string) bool {
	return doctorJSONWiredIn(doctorJSONBlock(key))
}

func doctorJSONWiredIn(pick func(map[string]any) map[string]any) func(string) bool {
	return func(path string) bool {
		b, err := doctorReadConfig(path)
		if err != nil {
			return false
		}
		var root map[string]any
		if json.Unmarshal([]byte(jsoncToJSON(string(b))), &root) != nil {
			// Unparseable even as JSONC — fall back to a substring probe.
			return strings.Contains(string(b), `"deja"`)
		}
		m := pick(root)
		if _, ok := m["deja"]; ok {
			return true
		}
		// Someone who wired the server by hand may have called it anything —
		// "deja-vu" is the obvious other choice. What identifies it is the
		// command it runs, not the key it was filed under, and telling a
		// working setup it is not wired sends the debugging the wrong way.
		for _, v := range m {
			if mcpEntryRunsDeja(v) {
				return true
			}
		}
		return false
	}
}

// doctorJSONDejaKeys lists every key in the config's server block that runs
// deja: the literal `deja` first, then the hand-named rest in sorted order, so
// the duplicate line reads the same on every run (#2269). Nil on a file that
// will not parse — the substring fallback above can say "wired", but it
// cannot count.
func doctorJSONDejaKeys(key string) func(string) []string {
	return doctorJSONDejaKeysIn(doctorJSONBlock(key))
}

func doctorJSONDejaKeysIn(pick func(map[string]any) map[string]any) func(string) []string {
	return func(path string) []string {
		b, err := doctorReadConfig(path)
		if err != nil {
			return nil
		}
		var root map[string]any
		// The file may be JSONC — the install keeps a reader's comments and
		// trailing commas (#3243) — so it is read the way install reads it.
		if json.Unmarshal([]byte(jsoncToJSON(string(b))), &root) != nil {
			return nil
		}
		m := pick(root)
		if m == nil {
			return nil
		}
		var out []string
		if _, ok := m["deja"]; ok {
			out = append(out, "deja")
		}
		var rest []string
		for name, entry := range m {
			if name != "deja" && mcpEntryRunsDeja(entry) {
				rest = append(rest, name)
			}
		}
		sort.Strings(rest)
		return append(out, rest...)
	}
}

// mcpEntryRunsDeja reports whether an MCP server entry launches deja, in any
// of the shapes clients accept: a bare command, a command plus args, a command
// written as a list, or a nested transport object.
//
// One function rather than two. install had grown its own answer, and they
// disagreed in both directions — a nested transport and a capitalised path
// were deja here and not there, a list command the other way round (#2713).
func mcpEntryRunsDeja(v any) bool {
	m, _ := v.(map[string]any)
	if m == nil {
		return false
	}
	return entryRunsDeja(m)
}

// commandIsDeja is the token test, under the name doctor's readers use. One
// implementation, because two answers to "is this the binary" disagreed on a
// quoted Windows path: the entry read as wired while nothing could name the
// command in it, so the missing-binary check had nothing to report (#2716).
func commandIsDeja(cmd string) bool {
	return isDejaBinaryToken(cmd)
}

func doctorTOMLWired(path string) bool {
	return len(doctorTOMLDejaKeys(path)) > 0
}

// doctorTOMLDejaKeys is the TOML side of the same count: every
// `[mcp_servers.X]` block that runs deja, the literal `deja` first (its header
// alone is enough — deja wrote it), then the hand-named rest sorted (#2269).
// Same reasoning as the JSON probe for the names: a hand-wired server under
// another name still runs deja. Attribution is per block rather than the old
// whole-file scan, because a `command` line elsewhere in this config — a hook,
// say — is not MCP wiring; and args count as well as command, since Windows
// wiring runs deja behind a `cmd /c` shim.
func doctorTOMLDejaKeys(path string) []string {
	b, err := readConfig(path)
	if err != nil {
		return nil
	}
	found := map[string]bool{}
	current := ""
	for _, line := range tomlCodeLines(string(b)) {
		if key, ok := tomlMCPHeader(line); ok {
			current = key
			if key == "deja" {
				found[key] = true
			}
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "[") {
			current = ""
			continue
		}
		if current == "" || found[current] {
			continue
		}
		key, value, ok := tomlLineKeyValue(line)
		if !ok || (key != "command" && key != "args") {
			continue
		}
		if slices.ContainsFunc(tomlStringValues(value), commandIsDeja) {
			found[current] = true
		}
	}
	var out []string
	if found["deja"] {
		out = append(out, "deja")
		delete(found, "deja")
	}
	rest := make([]string, 0, len(found))
	for key := range found {
		rest = append(rest, key)
	}
	sort.Strings(rest)
	return append(out, rest...)
}
