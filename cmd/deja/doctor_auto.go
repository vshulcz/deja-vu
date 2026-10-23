package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
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
	ws := []autoWiring{ //nolint:prealloc // a literal table; the loop below adds a row per IDE
		{"opencode", func() string {
			return filepath.Join(opencodeConfigHome(), "opencode", "plugins", "deja.js")
		}, "hook-context", ""},
		// Kilo CLI loads the same plugin from its own config directory (#4398).
		{"kilocode", func() string { return filepath.Join(kilocodeCLIConfigDir(), "plugins", "deja.js") }, "hook-context", ""},
		{"cursor", func() string { return filepath.Join(sources.CursorCLIHome(), "hooks.json") }, "hook-context", ""},
		{"gemini", geminiHooksPath, "hook-context", ""},
		{"qwen", func() string { return filepath.Join(sources.QwenConfigDir(), "settings.json") }, "hook-prompt", ""},
		{"codebuddy", codeBuddySettingsPath, "hook-context", ""},
		{"workbuddy", workBuddySettingsPath, "hook-context", ""},
		{"trae", traeHooksPath, "hook-context", ""},
		{"muse", museSettingsPath, "hook-context", ""},
		// The digest hook, not the prompt one: a config written before kimi had
		// all three blocks still carries hook-prompt, and reading that as wired
		// hides a machine that is missing the session digest and the forget on
		// compaction.
		{"kimi", func() string { return filepath.Join(sources.KimiConfigDir(), "config.toml") }, "hook-context", ""},
		{"antigravity", func() string {
			return filepath.Join(antigravityConfigHome(), "plugins", "deja", "hooks.json")
		}, "hook-antigravity", ""},
		{"pi", func() string { return filepath.Join(sources.PiConfigDir(), "extensions", "deja.ts") }, "hook-context", ""},
		// Senpi loads the same extension from its own agent directory —
		// measured live: the session it starts records what `deja hook-context`
		// returned as a `deja-recall` custom_message (#3670).
		{"senpi", func() string { return filepath.Join(sources.SenpiConfigDir(), "extensions", "deja.ts") }, "hook-context", ""},
		// gajae-code discovers the same extension in its own agent directory —
		// its `customize doctor` calls the file a trusted module discovered for
		// session-start loading, and a print-mode run carried the recall block
		// into the request (#3651).
		{"gjc", func() string { return filepath.Join(sources.GjcConfigDir(), "extensions", "deja.ts") }, "hook-context", ""},
		// Cherry Studio's Claude agents read the settings.json in the
		// CLAUDE_CONFIG_DIR the app gives them.
		{"cherrystudio", func() string {
			return filepath.Join(sources.CherryStudioClaudeConfigDir(), "settings.json")
		}, "hook-context", ""},
		// CodeWhale's TUI hooks, in the config.toml beside its sessions.
		{"codewhale", codewhaleConfigPath, "hook-codewhale", ""},
		// Junie's user hooks; SessionStart's output never reaches its model,
		// so the digest rides the first prompt's hook.
		{"junie", junieConfigPath, "hook-context", ""},
		// Kimchi loads it from its harness directory without a trust prompt.
		{"kimchi", func() string { return filepath.Join(sources.KimchiConfigDir(), "extensions", "deja.ts") }, "hook-context", ""},
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
		{"amp", ampPluginPath, "hook-context", ""},
		{"prime", primeExtensionPath, "hook-context", ""},
		{"deepseek", dshAutoPath, "hook-prompt", ""},
		{"goose", gooseHookPath, "hook-goose", ""},
		// PreToolUse is the only event Crush fires, so the tool hook is the
		// whole of auto-recall here — there is no digest hook to look for.
		{"crush", crushConfigPath, "hook-tool", ""},
		{"grok", grokHooksPath, "hook-context", ""},
		// Devin keeps its hooks in the "hooks" key of the same config.json
		// that holds its other user settings; the file exists whether deja
		// wrote to it or not.
		{"devin", devinConfigPath, "hook-context", ""},
		// The global hook file the IDE and kiro-cli's V3 engine read for every
		// chat; V2 runs the same hooks from deja's agent file (#4304).
		{"kiro", kiroGlobalHooksPath, "hook-context", ""},
		// Copilot CLI and VS Code Copilot Chat read deja's one hook file. The
		// marker is the per-prompt line with its flag: a plain line answers in
		// Claude's envelope, which Copilot CLI runs and drops, and an install
		// from before the file wired no per-prompt hook at all.
		{"copilot", copilotHooksPath, "hook-prompt --copilot", ""},
		{"vscode", copilotHooksPath, "hook-prompt --copilot", ""},
		// ZCode keeps its hooks in the same file as its server map, and the
		// line deja writes ends in `--strict` — its schema discards a whole
		// response over one key it does not know.
		{"zcode", zcodeConfigPath, "hook-context", ""},
		// Command Code keeps its hooks in the same settings.json as its
		// permissions, and its timeout unit is seconds.
		{"commandcode", commandCodeSettings, "hook-context", ""},
		// Reasonix hands the model nothing from a hook but SessionStart, so
		// recall there is the code extension in deja's plugin package.
		{"reasonix", reasonixInstalledManifest, "reasonix-ext", ""},
		{"aider", aiderContextPath, "",
			"context file — refreshed by `deja aider`, not by aider itself"},
		// Roo's guidance moved out of the always-on rules file into a skill;
		// checking the old path reported a correctly wired machine as missing.
		{"roo", func() string { return guidancePath("roo") }, "",
			"guidance — the agent is told to call recall, not handed it"},
		// MiniMax Code loads a plugin package from its own plugin directory,
		// and the hook document inside it is where its hooks live.
		{"mcode", func() string {
			return filepath.Join(sources.McodeConfigHome(), "plugins", "deja", "hooks", "hooks.json")
		}, "hook-context", ""},
	}
	for _, e := range traeIDEPresent() {
		ws = append(ws, autoWiring{"trae-ide", e.hooksPath, "hook-context", ""})
	}
	return ws
}

// autoInClientConfig names the rows whose file is the client's own config
// rather than one deja writes whole. Those exist whether deja ever wrote to
// them or not, so the file being there says nothing about deja (#4275).
var autoInClientConfig = map[string]bool{
	"cursor": true, "qwen": true, "codebuddy": true, "workbuddy": true, "trae": true, "trae-ide": true, "muse": true, "kimi": true, "crush": true, "zcode": true, "commandcode": true, "junie": true,
	"copilot": true, "devin": true,
	// CodeWhale's config.toml is the reader's own, and the plain MCP targets
	// of both write the file the row reads, so it is there with no hook of
	// deja's in it on a machine that never asked for auto-recall.
	"codewhale": true, "reasonix": true,
}

// autoUnwired reports whether a row's file holds no deja wiring at all: it is
// not there, or it is the client's own config with no entry of deja's in it.
// Either is a machine that was never wired, not one whose wiring went stale.
func autoUnwired(a autoWiring, b []byte, err error) bool {
	if err != nil {
		return true
	}
	return autoInClientConfig[a.name] && !strings.Contains(string(b), a.marker) && !dejaHookIn(string(b))
}

// dejaHookIn reports whether a config carries an entry of deja's: Kimi's
// marked block, or any string that runs one of deja's hook subcommands — the
// MCP server entry runs `mcp` and is not one. A file that is not JSON is read
// line by line, its escapes undone: TOML spells a quoted Windows path
// `"\"C:/Program Files/deja/deja.exe\" hook-prompt"`.
func dejaHookIn(text string) bool {
	if strings.Contains(text, kimiHookMarker) {
		return true
	}
	runsDeja := func(s string) bool {
		for name := range hookNames {
			if isDejaHookCommand(s, "deja "+name) {
				return true
			}
		}
		return false
	}
	var root any
	if json.Unmarshal([]byte(jsoncToJSON(strings.TrimPrefix(text, string(utf8BOM)))), &root) != nil {
		for l := range strings.SplitSeq(text, "\n") {
			if runsDeja(quotedPathUnescape.Replace(l)) {
				return true
			}
		}
		return false
	}
	var walk func(v any) bool
	walk = func(v any) bool {
		switch v := v.(type) {
		case string:
			return runsDeja(v)
		case []any:
			if slices.ContainsFunc(v, walk) {
				return true
			}
		case map[string]any:
			for _, e := range v {
				if walk(e) {
					return true
				}
			}
		}
		return false
	}
	return walk(root)
}

// nothingWired reports whether no harness on this machine has auto-recall
// wired. It reads one small file per harness and no index, which is what the
// brief can afford — that screen has to feel instant.
//
// Auto-recall alone is the question worth asking here. An MCP server is a tool
// the agent may call; these files are what make memory arrive without anyone
// asking, which is the thing someone thinks they installed. So it reads the
// rows doctor prints: Claude Code's and codex's hooks, which live outside the
// table, and a harness plugin that recalls with nothing in the row's file.
func nothingWired() bool {
	if claudeHookWiringState().state != "missing" {
		return false
	}
	// Codex counts with one of deja's events in hooks.json or the plugin
	// enabled, asked here rather than read off the row: hooks.json also holds
	// the user's own hooks, and the row says plugin only without the file.
	if codexPluginInstalled() {
		return false
	}
	codex := codexHookWiringState()
	for _, h := range codexHookWiring {
		if hookEventWired(codex.hooks, h.Event, h.Sub) {
			return false
		}
	}
	for _, a := range autoWirings() {
		b, err := os.ReadFile(a.path())
		if !autoUnwired(a, b, err) || harnessPluginCarriesRecall(a.name) {
			return false
		}
	}
	return true
}

// harnessPluginCarriesRecall reports whether this harness has a deja plugin of
// its own installed — the copy that recalls when the installer has not written
// to the file this row names. Kimi has said so since #1721; Grok's plugin works
// the same way and went unreported (#1828).
func harnessPluginCarriesRecall(name string) bool {
	switch name {
	case "kimi":
		return kimiPluginInstalled()
	case "grok":
		return grokPluginInstalled()
	}
	return false
}

// harnessPluginNote is what to print beside a "plugin" row: the version note
// when this deja ships a newer plugin than the installed one, and otherwise the
// sentence that says what the plugin is doing there. A version that is current
// is not news; what the plugin does is, because nothing else on screen says it.
func harnessPluginNote(name string) (behind, idle string) {
	switch name {
	case "kimi":
		if note := kimiPluginNote(); note != "" && note != "v"+kimiPluginVersion {
			return note, ""
		}
		return "", "the Kimi Code plugin recalls on every prompt"
	case "grok":
		if note := grokPluginNote(); note != "" && note != "v"+grokPluginVersion {
			return note, ""
		}
		return "", "the Grok Build plugin recalls on every prompt"
	}
	return "", ""
}

// autoWiringState is the state one auto-recall row is in, decided once for the
// report and the JSON both: two surfaces reading one file and disagreeing is
// the shape this repository keeps paying for. binaryMissing is the state an
// upgrade leaves — the entry is wired and names a path that no longer exists,
// so every hook exits 127.
func autoWiringState(a autoWiring) (state string, binaryMissing bool) {
	path := a.path()
	b, err := os.ReadFile(path)
	switch {
	case harnessPluginCarriesRecall(a.name) && (err != nil || !strings.Contains(string(b), a.marker)):
		state = "plugin"
	case a.name == "aider" && aiderWiring(err == nil) != "":
		state = aiderWiring(err == nil)
	case autoUnwired(a, b, err):
		state = "missing"
	// Roo's guidance is the skill most targets share, so the file says
	// nothing about Roo: without deja in Roo's own server list, Roo was never
	// wired.
	case a.name == "roo" && !doctorJSONWired("mcpServers")(doctorFirstExisting(rooMCPSettingsPaths(), vsCodeExtensionMCPPath(sources.RooExtensionID))):
		state = "missing"
	case a.marker != "" && !strings.Contains(string(b), a.marker):
		state = "stale"
	// An opencode that moved between majors refuses the plugin written for the
	// other one — 1.x wants a named export, 2.0 only reads the default — and
	// the file keeps naming the hook either way. Reported as wired, an upgrade
	// looks like a machine with memory and runs without it.
	case a.name == "opencode" && opencodePluginShapeStale(string(b)):
		state = "stale"
	case a.name == "deepseek" && dshNoticeSourceRetired(string(b)):
		state = "stale"
	// Reasonix loads a package only while plugin-packages.json holds an
	// enabled record of it; the directory alone is inert.
	case a.name == "reasonix" && !reasonixPackageEnabled():
		state = "stale"
	case a.name == "zcode" && !zcodeHooksRun(b):
		state = "stale"
	case a.name == "openclaw" && openclawPluginMissing():
		state = "stale"
	case a.name == "openclaw" && openclawIncludedWiringMissing() != "":
		state = "stale"
	// Written, and the CLI's default V2 engine does not start it: that runs
	// kiro_default unless the default agent is deja's or a chat names it
	// (#4304). The IDE and --v3 run the global file regardless.
	case a.name == "kiro" && !kiroV2RunsDeja():
		state = "installed"
	default:
		state = "wired"
	}
	if a.name == "reasonix" && reasonixRuntimeMissing() != "" {
		binaryMissing = true
	}
	// Only the files that run the binary: aider's is a digest of past sessions
	// and roo's is guidance, and either may quote a path for reasons of its own.
	if a.marker != "" && dejaHookCommandMissing(path) != "" {
		binaryMissing = true
	}
	return state, binaryMissing
}

// openclawPluginDir is where openclaw-auto puts deja's plugin.
func openclawPluginDir() string {
	return filepath.Join(sources.OpenClawStateDir(), "extensions", openclawPluginID)
}

// openclawPluginMissing reports whether the plugin half of openclaw-auto is
// gone. The row's file is the hook pack, which fires only in gateway mode; the
// plugin carries the digest and per-prompt recall under `openclaw agent
// --local`, and OpenClaw says nothing when the entry in openclaw.json names a
// plugin that is not there (#4580). `openclaw chat` and `openclaw tui --local`
// run neither (#5271).
func openclawPluginMissing() bool {
	_, err := os.Stat(filepath.Join(openclawPluginDir(), "index.mjs"))
	return err != nil
}

// aiderWiring is the aider row's state when the context file and the read:
// entry that makes aider load it disagree, and "" when they agree. The file
// alone said wired while aider loaded nothing, and the entry alone said missing
// — the word for never installed — while every start printed an error (#4327).
func aiderWiring(fileThere bool) string {
	// readConfig, for the byte order mark install writes back.
	b, _ := readConfig(aiderConfPath())
	named := aiderConfReadsContext(string(b))
	switch {
	case named && !fileThere:
		return "broken"
	case !named && fileThere:
		return "stale"
	}
	return ""
}

// autoWiringSwitchedOff is the line under a wired row whose harness has turned
// the wiring off, and "" when it has not. The file is still ours and still
// right, so the row stays wired, the way a switched-off MCP entry does.
func autoWiringSwitchedOff(name string) string {
	if name == "antigravity" && antigravityPluginSwitchedOff() {
		return "the plugin is switched off — antigravity will not run it until `agy plugin enable deja`"
	}
	return clientHooksOff(name)
}

// antigravityPluginSwitchedOff reads the switch `agy plugin disable` writes:
// plugins.<dir>.enabled in config.json, which wins wherever it has an entry,
// and otherwise a `"disabled": true` in the plugin's own plugin.json (#4359).
func antigravityPluginSwitchedOff() bool {
	var config struct {
		Plugins map[string]struct {
			Enabled *bool `json:"enabled"`
		} `json:"plugins"`
	}
	if b, err := readConfig(filepath.Join(antigravityConfigHome(), "config.json")); err == nil &&
		json.Unmarshal([]byte(jsoncToJSON(string(b))), &config) == nil {
		if p, ok := config.Plugins[antigravityPluginName]; ok && p.Enabled != nil {
			return !*p.Enabled
		}
	}
	var manifest struct {
		Disabled bool `json:"disabled"`
	}
	b, err := readConfig(filepath.Join(antigravityConfigHome(), "plugins", antigravityPluginName, "plugin.json"))
	return err == nil && json.Unmarshal([]byte(jsoncToJSON(string(b))), &manifest) == nil && manifest.Disabled
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
		if harnessPluginCarriesRecall(a.name) && (err != nil || !strings.Contains(string(b), a.marker)) {
			// Behind is the line worth the width: what it does is in the
			// README, what to run is not obvious from anywhere.
			pluginNote, idle := harnessPluginNote(a.name)
			if pluginNote == "" {
				pluginNote = idle
			}
			fmt.Fprintf(w, "  %-12s %-11s %s  (%s)\n", a.name, "plugin", reportPath(path), pluginNote)
			continue
		}
		switch {
		case a.name == "aider" && aiderWiring(err == nil) == "broken":
			fmt.Fprintf(w, "  %-12s %-11s %s  (%s reads it and it is not there — aider prints an error on every start; `deja install aider` writes it)\n", a.name, "broken", reportPath(path), reportPath(aiderConfPath()))
		case a.name == "aider" && aiderWiring(err == nil) == "stale":
			fmt.Fprintf(w, "  %-12s %-11s %s  (no read: entry for it in %s, so aider never loads it — `deja install aider`)\n", a.name, "stale", reportPath(path), reportPath(aiderConfPath()))
		case err != nil:
			fmt.Fprintf(w, "  %-12s %-11s %s%s\n", a.name, "missing", reportPath(path), note)
			// Missing here is not "never installed" when the layer still names
			// the file: dsh then fails the whole profile load (#4292).
			if a.name == "deepseek" && dshLayerNamesMissing(path) {
				fmt.Fprintf(w, "  %-12s %s\n", "", reportPath(dshPatchPath())+" still names it — dsh will not start; `deja install deepseek-auto` writes it again, or `deja uninstall deepseek` takes deja out of the layer")
			}
		case autoUnwired(a, b, err):
			// The client's config is there and deja never wrote its hook into
			// it: the MCP install writes this same file, and only the -auto
			// target writes the hook (#3313, #4275).
			fmt.Fprintf(w, "  %-12s %-11s %s  (no deja hook — `deja install %s-auto`)\n", a.name, "missing", reportPath(path), a.name)
		case a.marker != "" && !strings.Contains(string(b), a.marker):
			// "reinstall" was the advice, and for the common way to get here
			// it cannot work: the MCP install writes this same file, and only
			// the -auto target writes the hook (#3313). Name that target.
			fmt.Fprintf(w, "  %-12s %-11s %s  (no %s call — `deja install %s-auto`)\n", a.name, "stale", reportPath(path), a.marker, a.name)
		case a.name == "deepseek" && dshNoticeSourceRetired(string(b)):
			fmt.Fprintf(w, "  %-12s %-11s %s  (written for dsh's v3 sessions — dsh 0.1.7 and later refuse its notices and stop the session; `deja install deepseek-auto`)\n", a.name, "stale", reportPath(path))
		case a.name == "reasonix" && !reasonixPackageEnabled():
			fmt.Fprintf(w, "  %-12s %-11s %s  (no enabled record in %s — `deja install reasonix-auto`)\n", a.name, "stale", reportPath(path), reportPath(reasonixStatePath()))
		case a.name == "zcode" && !zcodeHooksRun(b):
			fmt.Fprintf(w, "  %-12s %-11s %s  (deja's hook is not under hooks.events with hooks.enabled on — `deja install zcode-auto`)\n", a.name, "stale", reportPath(path))
		case a.name == "openclaw" && openclawPluginMissing():
			fmt.Fprintf(w, "  %-12s %-11s %s  (deja's plugin is not in %s — `openclaw agent --local` gets no recall; `deja install openclaw-auto`)\n", a.name, "stale", reportPath(path), reportPath(openclawPluginDir()))
		case a.name == "openclaw" && openclawIncludedWiringMissing() != "":
			fmt.Fprintf(w, "  %-12s %-11s %s  (openclaw.json keeps a section deja writes in an $include, and %s is not in it or the file it includes yet — `deja install openclaw-auto` says what to add where)\n", a.name, "stale", reportPath(path), openclawIncludedWiringMissing())
		case a.name == "kiro" && !kiroV2RunsDeja():
			fmt.Fprintf(w, "  %-12s %-11s %s  (%s)\n", a.name, "installed", reportPath(path), kiroV2Note())
		default:
			fmt.Fprintf(w, "  %-12s %-11s %s%s\n", a.name, "wired", reportPath(path), note)
			if off := autoWiringSwitchedOff(a.name); off != "" {
				fmt.Fprintf(w, "  %-12s %s\n", "", off)
			}
			// TRAE keeps Codex's per-hook trust, and a hook nobody trusted
			// does not run. How it keys that trust is not read here, so the
			// row says where to look rather than guessing either way.
			if a.name == "trae" {
				fmt.Fprintf(w, "  %-12s %s\n", "", "traex runs these only once trusted — it asks at start-up, and /hooks shows which")
			}
			if a.name == "openclaw" {
				fmt.Fprintf(w, "  %-12s %s\n", "", openclawLocalTUINote)
			}
			// TRAE IDE keeps its hooks switch with the agent's settings, off
			// by default and not in a file deja can read.
			if a.name == "trae-ide" {
				fmt.Fprintf(w, "  %-12s %s\n", "", traeIDEHooksOffNote)
			}
		}
		// Under the row whatever the row said. A machine that upgraded is most
		// often stale rather than wired — the entries were written by the
		// version before — and saying this only under a healthy row left the
		// rows most likely to be pointing at a binary that is gone saying
		// nothing about it (#3502).
		//
		// Only the rows that run the binary. aider's file is a digest of past
		// sessions and roo's is guidance — neither executes anything, and both
		// can quote a path for reasons of their own.
		if a.marker == "" {
			continue
		}
		launcher := doctorLauncherNote(path, a.name+"-auto")
		if missing := reasonixRuntimeMissingFor(a.name); missing != "" {
			fmt.Fprintf(w, "  %-12s runs %s, which is not there — `deja install reasonix-auto` rewrites it for this binary\n", "", missing)
		} else if exe := hookExeNote(path, a.name+"-auto"); exe != "" {
			// When the binary that is gone is the launcher, the note below
			// says so; printing both named one file twice (#4245).
			if launcher == "" || !hookExeIsLauncher(path) {
				fmt.Fprintf(w, "  %-12s %s\n", "", exe)
			}
		} else if other := otherBinaryNote(path, a.name+"-auto"); other != "" {
			// The quieter half of the same question: the binary is there and is
			// not this one, which works until that file goes (#3656).
			fmt.Fprintf(w, "  %-12s %s\n", "", other)
		}
		if launcher != "" {
			fmt.Fprintf(w, "  %-12s %s\n", "", launcher)
		}
	}
}

// Goose and Hermes keep MCP servers in YAML rather than JSON or TOML, and
// both nest ours under a key: a plain "deja appears in the file" check would
// pass on a disabled entry.
func doctorGooseWired(path string) bool {
	b, err := readConfig(path)
	if err != nil {
		return false
	}
	return len(gooseDejaEntries(strings.Split(lfText(b), "\n"))) > 0
}

func doctorHermesWired(path string) bool {
	b, err := readConfig(path)
	if err != nil {
		return false
	}
	return len(hermesDejaEntries(strings.Split(lfText(b), "\n"))) > 0
}

// Continue keys its servers by a name field inside a list item rather than by a
// key, so the child-key check above has nothing to look for: the row asks
// whether an item under mcpServers names deja.
func doctorContinueWired(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return removeContinueItem(string(b), "mcpServers", "deja") != string(b)
}

// yamlKeyLine reports whether line is the mapping key `key` (with its colon)
// and nothing else — a trailing comment or blanks allowed, as YAML allows
// them. An exact match missed `mcp_servers:  # mine` (#4289).
func yamlKeyLine(line, key string) bool {
	line = strings.TrimRight(line, "\r\n")
	if i := strings.Index(line, "#"); i > 0 && (line[i-1] == ' ' || line[i-1] == '\t') {
		line = line[:i]
	}
	return strings.TrimSpace(line) == key
}

// yamlTopKeyEnd is where the first child of the top-level `key` (colon
// included) goes, just past the key's line, or -1 when the document has none.
// doc must end in a newline. A second top-level key, or the key under another
// spelling (`"key":`, `key :`, an inline value), is refused: the client reads
// the last one, so joining the first left deja unloaded while doctor said
// wired, and appending another hid the reader's own entries (#4289).
func yamlTopKeyEnd(doc, key string) (int, error) {
	name := strings.TrimSuffix(key, ":")
	at, found := 0, -1
	for _, line := range strings.SplitAfter(doc, "\n") {
		start := at
		at += len(line)
		if yamlIndentWidth(line) != 0 || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if yamlKeyLine(line, key) {
			if found >= 0 {
				return -1, fmt.Errorf("%s is written twice at the top level, and only the last one is read — keep one and run this again", key)
			}
			found = start + len(line)
			continue
		}
		head, rest, ok := strings.Cut(line, ":")
		if !ok || strings.Trim(strings.TrimSpace(head), `"'`) != name {
			continue
		}
		v := strings.TrimSpace(rest)
		if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		switch strings.ToLower(v) {
		case "{}", "[]", "~", "null", "''", `""`:
			// Nothing of the reader's under it: a block written after it
			// shadows nothing, and uninstall takes that block back.
			continue
		}
		return -1, fmt.Errorf("%s is written as %q, and deja edits only the block form `%s` on a line of its own — rewrite it that way and run this again", key, strings.TrimSpace(line), key)
	}
	if found < 0 {
		return -1, nil
	}
	// Past any comment or blank line under the key: a comment there heads the
	// reader's entries, and an entry written above it would carry it away
	// when uninstall takes that entry out (#4289).
	// A blank line is skipped only when what follows it still belongs to the
	// key; the one that separates an empty key from the next one is the
	// file's, and an entry written below it moved on every install.
	for at := found; at < len(doc); {
		end := strings.IndexByte(doc[at:], '\n')
		if end < 0 {
			break
		}
		line := doc[at : at+end]
		t := strings.TrimSpace(line)
		if t != "" && !strings.HasPrefix(t, "#") {
			if yamlIndentWidth(line) > 0 {
				found = at
			}
			break
		}
		at += end + 1
		if t != "" {
			found = at
		}
	}
	return found, nil
}

// codexHasSeenItsHook reports whether codex has recorded any opinion about
// deja's session-start hook. Until it has, codex runs nothing — and in `codex
// exec` there is no interface in which to approve it, which is how scripted
// runs end up with no memory while every file on disk looks correctly
// installed.
func codexHasSeenItsHook() bool {
	cfg, err := os.ReadFile(filepath.Join(sources.CodexHome(), "config.toml"))
	if err != nil {
		return true // nothing to read: do not raise an alarm we cannot support
	}
	path := filepath.Join(sources.CodexHome(), "hooks.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	var root map[string]any
	if json.Unmarshal(b, &root) != nil {
		return true
	}
	hooks, _ := root["hooks"].(map[string]any)
	return codexPrimaryPin(codexDejaPins(string(cfg), path, hooks)) != ""
}

// dshNoticeSourceRetired reports whether the installed plugin still tags its
// notices with the v3 "plugin" source kind, which dsh's v4 sessions refuse on
// append (#4937).
func dshNoticeSourceRetired(js string) bool {
	return strings.Contains(js, `source: { kind: "plugin",`)
}

// opencodePluginShapeStale reports whether the installed plugin is written for
// the other opencode major, or is a 2.x plugin from before the hook deadline,
// which lets a stuck hook hold the turn (#4951). Silent when the version
// cannot be read: a guess here would report a working machine as broken.
func opencodePluginShapeStale(js string) bool {
	v2 := strings.Contains(js, "export default")
	if v2 && !strings.Contains(js, "HOOK_DEADLINE") {
		return true
	}
	switch major := opencodeVersionMajor(); {
	case major == 1:
		return v2
	case major >= 2:
		return !v2
	}
	return false
}
