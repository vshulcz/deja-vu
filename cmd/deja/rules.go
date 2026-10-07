package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// A rule someone gives one agent never reaches the others. On the machine this
// was measured on, seven rules had been given again in at least two sessions and
// two different harnesses, and the opencode rules file — the harness with the
// most sessions in the index — held none of what the Claude Code one did
// (#4086). So the reader keeps their rules in one file, and deja copies it as a
// marked block into each agent's own global rules file, the way it already
// shares AGENTS.md with its reader for guidance.

const (
	rulesStart = "<!-- deja rules:start -->"
	rulesEnd   = "<!-- deja rules:end -->"
	// Every agent carries the block in every session, whether or not the
	// question has anything to do with it, so the file is kept to what a person
	// would write as standing rules rather than a manual.
	rulesMaxBytes = 8 << 10
)

// rulesHarnesses are the harnesses whose global rules file deja knows, in the
// order `deja rules` lists them.
var rulesHarnesses = []string{"claude-code", "codex", "opencode", "gemini", "qwen", "kimi", "grok", "goose", "copilot", "vscode", "cline", "kilocode", "zed", "antigravity", "cherrystudio", "hermes", "aider", "continue", "trae", "pi", "omp", "senpi", "gjc", "prime", "kimchi", "codewhale", "crush", "codebuddy", "workbuddy", "commandcode", "kiro", "roo"}

// rulesPath is the file a harness reads its global rules from, or "" when deja
// does not know one. Only paths something already showed reach the model —
// most of them a marker in the file arriving at a stub endpoint, Zed and the
// Cline extension, prime and Senpi from their source. Cursor has no fixed
// global file (its rules walk up from the working directory, and user rules
// live on its server), and Muse is left out until a stand shows one: a block in
// a file the agent never reads looks like a rule that was delivered.
func rulesPath(harness string) string {
	return rulesFileFor(harness).path
}

// rulesFile is where one harness's copy goes. A shared file is the user's own
// and gets a marked block; a file deja owns alone sits in a rules directory the
// harness loads whole, and carries header (frontmatter the harness needs to
// load it in every session) above the block. root is the agent's own directory:
// it has to exist, because a file under a directory the agent never made is one
// nothing reads, but the rules directory under it may be created.
type rulesFile struct {
	path, root, header string
	// existing is a file deja may add to but never create.
	existing bool
}

// ready is whether deja can write f: the agent's directory is there, and so is
// the file when it is one deja must not create.
func (f rulesFile) ready() bool {
	return f.path != "" && dirExists(f.root) && (!f.existing || fileExists(f.path))
}

func sharedRules(path string) rulesFile {
	return rulesFile{path: path, root: filepath.Dir(path)}
}

func rulesFileFor(harness string) rulesFile {
	switch harness {
	case "claude-code":
		return sharedRules(filepath.Join(sources.ClaudeConfigDir(), "CLAUDE.md"))
	case "codex":
		return sharedRules(filepath.Join(sources.CodexHome(), "AGENTS.md"))
	case "opencode":
		// Same first-file-wins loader as Kilo (see below).
		dir := filepath.Join(opencodeConfigHome(), "opencode")
		return rulesFile{path: firstExisting(filepath.Join(dir, "AGENTS.md"), filepath.Join(sources.ClaudeConfigDir(), "CLAUDE.md")), root: dir}
	case "gemini":
		return sharedRules(filepath.Join(sources.GeminiHome(), "GEMINI.md"))
	case "qwen":
		return sharedRules(filepath.Join(sources.QwenConfigDir(), "QWEN.md"))
	case "kimi":
		return sharedRules(filepath.Join(sources.KimiConfigDir(), "AGENTS.md"))
	case "grok":
		return sharedRules(filepath.Join(sources.GrokHome(), "AGENTS.md"))
	case "goose":
		return sharedRules(gooseHintsPath())
	case "copilot":
		// Copilot CLI 1.0.92 puts $COPILOT_HOME/copilot-instructions.md in
		// front of every session; a stub endpoint saw it arrive.
		return sharedRules(filepath.Join(sources.CopilotHome(), "copilot-instructions.md"))
	case "vscode", "copilot-chat":
		// Copilot Chat 0.68 attaches ~/.copilot/copilot-instructions.md to
		// every request (useInstructionFiles, on by default) from the real
		// home, whatever COPILOT_HOME says. Without COPILOT_HOME it is the
		// same file Copilot CLI reads, and the second write is a no-op.
		return rulesFile{path: filepath.Join(homeDir(), ".copilot", "copilot-instructions.md"), root: vsCodeFirstRoot()}
	case "cline":
		// $CLINE_DIR/rules, every file a section of the system prompt: the
		// CLI 3.0.69 sent it to a stub, and extension 4.1.23 reads the same
		// folder with new files switched on.
		dir := os.Getenv("CLINE_DIR")
		if dir == "" {
			dir = filepath.Join(homeDir(), ".cline")
		}
		return rulesFile{path: filepath.Join(dir, "rules", "deja-rules.md"), root: dir}
	case "kilocode":
		// Kilo 7.8 (opencode's loader) takes the first global file that exists
		// and stops: its own AGENTS.md, then ~/.claude/CLAUDE.md. Creating the
		// first hides the second, so a reader who keeps rules in CLAUDE.md
		// gets the block there (stand on the CLI bundled with VSIX 7.8.7).
		dir := kilocodeCLIConfigDir()
		if p := os.Getenv("KILO_CONFIG_DIR"); p != "" {
			dir = p
		}
		return rulesFile{path: firstExisting(filepath.Join(dir, "AGENTS.md"), filepath.Join(sources.ClaudeConfigDir(), "CLAUDE.md")), root: dir}
	case "zed":
		// <config dir>/AGENTS.md, the "Personal AGENTS.md" in every request
		// of Zed's own agent (crates/agent_settings/src/user_agents_md.rs on
		// 1.22.0; Zed also writes its Rules Library defaults there, which is
		// why it is a block).
		return sharedRules(filepath.Join(filepath.Dir(sources.ZedSettingsPath()), "AGENTS.md"))
	case "antigravity":
		// agy loads ~/.gemini/GEMINI.md as a user_global rule (stub endpoint),
		// the same file as Gemini CLI, so the two never carry it twice.
		return rulesFile{path: filepath.Join(homeDir(), ".gemini", "GEMINI.md"), root: antigravityConfigHome()}
	case "cherrystudio":
		// Cherry runs its agents with CLAUDE_CONFIG_DIR at Data/Agents/.claude,
		// so its CLAUDE.md is their user memory; a stand on the app's SDK and
		// binary saw it. Sealed and external-CLI agents do not read it.
		for _, r := range sources.CherryStudioRoots() {
			if dir := filepath.Dir(r); filepath.Base(filepath.Dir(dir)) == "Agents" {
				return sharedRules(filepath.Join(dir, "CLAUDE.md"))
			}
		}
		return rulesFile{}
	case "hermes":
		// SOUL.md opens the system prompt; $HERMES_HOME/AGENTS.md is not read
		// (prompt_builder.py load_soul_md, stand on 0.17.0). It is also the
		// agent's identity, which Hermes seeds only when the file is missing,
		// so deja adds to one that exists and never creates it.
		p := filepath.Join(sources.HermesHome(), "SOUL.md")
		return rulesFile{path: p, root: sources.HermesHome(), existing: true}
	case "aider":
		// The read-only file install lists under read:, re-read on every
		// message (stand on 0.86.2); `deja aider` keeps the block when it
		// refreshes the rest.
		return sharedRules(aiderContextPath())
	case "continue":
		// Every .md under <global dir>/rules without globs or invokable is
		// in the system prompt's userRules (cn 1.5.47, stub endpoint).
		return rulesFile{path: filepath.Join(sources.ContinueRoot(), "rules", "deja-rules.md"), root: sources.ContinueRoot()}
	case "trae":
		// codex-rs's global AGENTS.md, at $TRAE_HOME rather than under cli/
		// (traex debug prompt-input on 0.208.1).
		return sharedRules(filepath.Join(sources.TraeHome(), "AGENTS.md"))
	case "pi":
		return piRules(sources.PiConfigDir())
	case "omp", "gjc":
		// omp keeps one user-level context file, the highest of its own
		// AGENTS.md, Claude's, Codex's and Gemini's (context-file.ts), and gjc
		// inherited the loader; creating its own would hide the reader's.
		dir := sources.OmpConfigDir()
		if harness == "gjc" {
			dir = sources.GjcConfigDir()
		}
		return rulesFile{root: dir, path: firstExisting(filepath.Join(dir, "AGENTS.md"),
			filepath.Join(sources.ClaudeConfigDir(), "CLAUDE.md"),
			filepath.Join(sources.CodexHome(), "AGENTS.md"),
			filepath.Join(sources.GeminiHome(), "GEMINI.md"))}
	case "senpi":
		return piRules(sources.SenpiConfigDir(), "AGENTS.override.md")
	case "prime":
		return piRules(sources.PrimeConfigDir())
	case "kimchi":
		// Kimchi kept pi's context file (stand on 0.1.99).
		return piRules(sources.KimchiConfigDir())
	case "codewhale":
		return sharedRules(codeWhaleRulesPath())
	case "crush":
		// <config dir>/CRUSH.md goes into every system prompt as a user
		// preference (internal/config/load.go:575-580 on v0.97.1, and a stub
		// endpoint saw it). CRUSH_GLOBAL_CONFIG moves it with the config.
		return sharedRules(filepath.Join(crushConfigDir(), "CRUSH.md"))
	case "codebuddy":
		// The user memory file, sent with every first message as "user's
		// private global instructions"; AGENTS.md is read in projects only.
		return sharedRules(filepath.Join(sources.CodeBuddyConfigDir(), "CODEBUDDY.md"))
	case "workbuddy":
		// The app runs CodeBuddy's CLI with its own config dir, so the same
		// file name there (measured on the bundled CLI 2.147.0).
		return sharedRules(filepath.Join(sources.WorkBuddyConfigDir(), "CODEBUDDY.md"))
	case "commandcode":
		// getUserMemoryPath in 1.77.0: ~/.commandcode/AGENTS.md, no override.
		return sharedRules(filepath.Join(homeDir(), ".commandcode", "AGENTS.md"))
	case "kiro":
		// Global steering with `inclusion: always` reached the model as a
		// <user-rule> under both engines of kiro-cli 2.28 (see kiroSteeringPath).
		return rulesFile{
			path:   filepath.Join(sources.KiroConfigDir(), "steering", "deja-rules.md"),
			root:   sources.KiroConfigDir(),
			header: "---\ninclusion: always\n---\n\n",
		}
	case "roo":
		// Every file in ~/.roo/rules goes into the system prompt of every task
		// and mode (see rooRulesPath). The extension's own storage is what says
		// Roo is here; the rules directory is ours to create.
		return rulesFile{path: filepath.Join(homeDir(), ".roo", "rules", "deja-rules.md"), root: rooFirstRoot()}
	}
	return rulesFile{}
}

// firstExisting is for a harness that loads the first of several files and
// skips the rest: the block goes into the one already winning, or def.
func firstExisting(def string, others ...string) string {
	for _, p := range append([]string{def}, others...) {
		if fileExists(p) {
			return p
		}
	}
	return def
}

// piRules is the global context file of pi and its descendants:
// <agent dir>/AGENTS.md, which every one of them put in the system prompt on a
// stand (prime from source), with the first of the names below winning. Senpi
// lets AGENTS.override.md win over all of them.
func piRules(dir string, overrides ...string) rulesFile {
	def := filepath.Join(dir, "AGENTS.md")
	for _, n := range append(overrides, "AGENTS.md", "AGENTS.MD", "CLAUDE.md", "CLAUDE.MD") {
		if p := filepath.Join(dir, n); fileExists(p) {
			return rulesFile{path: p, root: dir}
		}
	}
	return rulesFile{path: def, root: dir}
}

// codeWhaleRulesPath is the global file CodeWhale 0.10.0 loads, which is the
// first of these that exists (project_context.rs). Writing ~/.codewhale's copy
// beside an existing ~/.agents one would hide the reader's own rules, so the
// block goes into whichever already wins. $CODEWHALE_HOME does not move it: a
// stand with it set loaded nothing from the moved directory.
func codeWhaleRulesPath() string {
	var others []string
	for _, name := range []string{"AGENTS.md", "instructions.md"} {
		for _, dir := range []string{".codewhale", ".agents", ".deepseek"} {
			others = append(others, filepath.Join(homeDir(), dir, name))
		}
	}
	return firstExisting(others[0], others[1:]...)
}

func rulesSourcePath() string {
	return filepath.Join(xdgConfigHome(), "deja", "rules.md")
}

// readRules returns the rules as they go into the block: without a byte order
// mark, with \n line endings and without trailing blank lines. An absent file
// and an empty one are the same answer — nothing to copy — and exists says
// which it was only so the status can say how to create it.
func readRules() (body string, exists bool, err error) {
	b, err := os.ReadFile(rulesSourcePath())
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	if len(b) > rulesMaxBytes {
		return "", true, fmt.Errorf("%s is %d bytes; the limit is %d, because every agent carries these rules in every session",
			shortHome(rulesSourcePath()), len(b), rulesMaxBytes)
	}
	b = bytes.TrimPrefix(b, utf8BOM)
	body = strings.ReplaceAll(string(b), "\r\n", "\n")
	body = strings.TrimRight(body, " \t\n")
	// A marker inside the rules would end up inside the block, and the next
	// sync would cut the reader's file at it.
	for _, m := range []string{rulesStart, rulesEnd, guidanceStart, guidanceEnd, gooseRecallStart, gooseRecallEnd} {
		if strings.Contains(body, m) {
			return "", true, fmt.Errorf("%s contains %q, one of deja's own block markers — take it out", shortHome(rulesSourcePath()), m)
		}
	}
	return body, true, nil
}

func rulesBlock(body string) string {
	if body == "" {
		return ""
	}
	return rulesStart + "\n" + body + "\n" + rulesEnd + "\n"
}

// rulesTargets splits the harnesses deja is installed for into the ones with a
// known rules file and the ones without. Installed means named in the wiring
// record; a machine with no record — deja wired by hand, or by a version before
// the record — falls back to the harnesses whose config directory exists.
func rulesTargets() (known, unknown []string) {
	installed := map[string]bool{}
	for _, t := range readWiringState().Targets {
		h := guidanceHarness(t)
		// statusline, sync-timer and the like are not agents and have no
		// rules to read.
		if rulesPath(h) != "" || guidancePath(h) != "" {
			installed[h] = true
		}
	}
	if len(installed) == 0 {
		for _, h := range rulesHarnesses {
			if rulesFileFor(h).ready() {
				installed[h] = true
			}
		}
	}
	for _, h := range rulesHarnesses {
		// The directory has to be there: a file created in a directory the
		// agent never made is one nothing reads.
		if installed[h] && rulesFileFor(h).ready() {
			known = append(known, h)
		}
		delete(installed, h)
	}
	for h := range installed {
		unknown = append(unknown, h)
	}
	sort.Strings(unknown)
	return known, unknown
}

// rulesState compares a harness's file with the rules: "in sync", "stale" (a
// block with other text, or one left after the rules were emptied), "missing"
// (rules to copy and no block), "none" (no rules and no block), or
// "unbounded" when a marker has lost its pair and only the reader can say where
// the block ends.
func rulesState(path, body string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	doc := string(bytes.TrimPrefix(b, utf8BOM))
	if checkMarkerPairs(doc, rulesStart, rulesEnd, "rules") != nil {
		return "unbounded", nil
	}
	start, end := markerLines(doc, rulesStart, rulesEnd)
	if start < 0 || end < 0 {
		if body == "" {
			return "none", nil
		}
		return "missing", nil
	}
	if body == "" {
		return "stale", nil
	}
	inner := strings.ReplaceAll(doc[start:end], "\r\n", "\n")
	if inner == rulesBlock(body) {
		return "in sync", nil
	}
	return "stale", nil
}

func runRules(dir string, args []string) error {
	if len(args) > 0 && args[0] == "candidates" {
		return runRulesCandidates(dir, os.Stdout, args[1:])
	}
	return runRulesTo(os.Stdout, args)
}

func runRulesTo(w io.Writer, args []string) error {
	sync := false
	for _, a := range args {
		switch a {
		case "sync":
			sync = true
		case "status":
		case "candidates":
			return fmt.Errorf("rules: candidates goes first and takes no other subcommand — `deja rules candidates [--json] [--limit n] [--since 90d]`")
		default:
			return unknownFlag("rules", a, []string{"sync", "status", "candidates"})
		}
	}
	body, exists, err := readRules()
	if err != nil {
		return err
	}
	known, unknown := rulesTargets()
	if sync {
		return syncRules(w, body, known)
	}
	switch {
	case !exists:
		fmt.Fprintf(w, "rules: %s is not there — write your standing rules in it, then `deja rules sync` copies them to every agent below\n", shortHome(rulesSourcePath()))
	case body == "":
		fmt.Fprintf(w, "rules: %s is empty — `deja rules sync` removes the copies below\n", shortHome(rulesSourcePath()))
	default:
		fmt.Fprintf(w, "rules: %s, %d lines, %d bytes\n", shortHome(rulesSourcePath()), strings.Count(body, "\n")+1, len(body))
	}
	behind := false
	for _, h := range known {
		path := rulesPath(h)
		state, err := rulesState(path, body)
		if err != nil {
			return err
		}
		if state == "missing" || state == "stale" {
			behind = true
		}
		if state == "unbounded" {
			state = "marker without its pair — fix by hand"
		}
		fmt.Fprintf(w, "  %-12s %-9s %s\n", h, state, shortHome(path))
	}
	for _, h := range unknown {
		fmt.Fprintf(w, "  %-12s no global rules file known\n", h)
	}
	if len(known) == 0 && len(unknown) == 0 {
		fmt.Fprintln(w, "  no agents found — `deja install` first")
	}
	if behind {
		fmt.Fprintln(w, "run `deja rules sync` to bring them in line")
	}
	return nil
}

// syncRules writes the block into every known file, or takes it out when there
// are no rules. Every file is attempted: one the reader has to fix by hand is
// named at the end rather than stopping the rest.
func syncRules(w io.Writer, body string, known []string) error {
	release, _, _ := holdInstallLock(true)
	defer release()
	var refused []string
	for _, h := range known {
		path := rulesPath(h)
		action, err := writeRulesFile(rulesFileFor(h), body)
		if err != nil {
			refused = append(refused, err.Error())
			continue
		}
		fmt.Fprintf(w, "%s: rules %s %s\n", h, action, shortHome(path))
	}
	if len(refused) > 0 {
		return fmt.Errorf("left as they are:\n  %s", strings.Join(refused, "\n  "))
	}
	return nil
}

// writeRulesFile puts body into f as deja's rules block, or removes the block
// when body is empty. A file that held nothing but the block goes with it: it
// was deja's to begin with, and an empty CLAUDE.md is litter. A file deja owns
// starts from its header and goes once nothing but the header is left.
func writeRulesFile(f rulesFile, body string) (string, error) {
	path := f.path
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if len(old) == 0 && body == "" {
		return "unchanged", nil
	}
	base := string(old)
	if len(old) == 0 {
		base = f.header
	}
	next, err := replaceMarkedBlock(base, rulesStart, rulesEnd, "rules", rulesBlock(body))
	if err != nil {
		return "", markerErrorFor(path, err)
	}
	if body == "" && strings.TrimSpace(next) == strings.TrimSpace(f.header) {
		if err := os.Remove(path); err != nil {
			return "", err
		}
		return "removed", nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return writeIfChanged(path, old, []byte(next))
}

// dropRulesBlock is uninstall's half: the harness is leaving, so the copy of
// the rules goes with it, the same way its guidance does.
func dropRulesBlock(target string) (path, action string, err error) {
	h := guidanceHarness(target)
	f := rulesFileFor(h)
	path = f.path
	if path == "" {
		return "", "", nil
	}
	// Copilot CLI and Copilot Chat can share one file; the copy stays while
	// the other one is still installed.
	for _, t := range readWiringState().Targets {
		if other := guidanceHarness(t); other != h && rulesPath(other) == path {
			return path, "unchanged", nil
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return path, "unchanged", nil
		}
		return path, "", err
	}
	if start, end := markerLines(string(b), rulesStart, rulesEnd); start < 0 || end < 0 {
		return path, "unchanged", nil
	}
	action, err = writeRulesFile(f, "")
	return path, action, err
}

// rulesDoctorNote is the one line doctor prints when rules exist and some
// agent's copy is behind them. Silence otherwise: most machines have no rules
// file, and a line about a feature nobody uses is noise on every run.
func rulesDoctorNote() string {
	body, _, err := readRules()
	if err != nil {
		return err.Error()
	}
	if body == "" {
		return ""
	}
	known, _ := rulesTargets()
	var behind []string
	for _, h := range known {
		if state, err := rulesState(rulesPath(h), body); err == nil && state != "in sync" {
			behind = append(behind, h)
		}
	}
	if len(behind) == 0 {
		return ""
	}
	return fmt.Sprintf("%s behind %s — `deja rules sync`", strings.Join(behind, ", "), shortHome(rulesSourcePath()))
}
