package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// doctorVersionLookup fetches the latest released version. It is injected so
// tests can stub it — the real lookup talks to GitHub with a digest.Short budget.
type doctorVersionLookup func() (latest string, ok bool)

// doctorLookup is the dispatcher's lookup; overridable in tests so they never
// touch the network.
var doctorLookup = defaultDoctorVersionLookup()

func defaultDoctorVersionLookup() doctorVersionLookup {
	return func() (string, bool) {
		download := newHTTPUpdateDownloader(&http.Client{Timeout: 2 * time.Second})
		body, err := download(latestReleaseURL, maxReleaseJSON, "latest release")
		if err != nil {
			return "", false
		}
		var release updateRelease
		if err := json.Unmarshal(body, &release); err != nil {
			return "", false
		}
		latest := normalizeUpdateVersion(release.TagName)
		return latest, latest != ""
	}
}

// runDoctor prints a self-diagnosis report. Diagnosis itself never fails, so
// both human and JSON reports keep exit status 0.
func runDoctor(w io.Writer, args []string, lookup doctorVersionLookup, dir string) error {
	jsonOutput := false
	deep := false
	offline := os.Getenv("DEJA_OFFLINE") == "1"
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOutput = true
		case "--offline":
			offline = true
		case "--deep":
			deep = true
		default:
			return fmt.Errorf("doctor: unknown flag %q", arg)
		}
	}
	if offline {
		lookup = nil
	}
	report := collectDoctorReport(lookup, dir)
	var deepReport *index.DeepReport
	if deep {
		dr, err := index.DeepVerify(dir)
		if err != nil {
			return fmt.Errorf("doctor: deep verify: %w", err)
		}
		deepReport = &dr
		report.Deep = deepReport
	}
	if jsonOutput {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
		return deepDriftErr(deepReport)
	}
	doctorHarnesses(w)
	printDoctorStoreWarnings(w, report.Stores)
	fmt.Fprintln(w)
	doctorTools(w)
	fmt.Fprintln(w)
	doctorPolicy(w)
	fmt.Fprintln(w)
	doctorMCP(w)
	fmt.Fprintln(w)
	doctorHooks(w)
	fmt.Fprintln(w)
	doctorIndex(w, report.Index, dir)
	fmt.Fprintln(w)
	if report.Embed != nil {
		doctorEmbed(w, *report.Embed)
	} else {
		doctorEmbed(w, doctorEmbedReport{State: "unavailable"})
	}
	fmt.Fprintln(w)
	if offline {
		fmt.Fprintln(w, "version: check skipped (offline)")
	} else {
		doctorVersion(w, func() (string, bool) { return report.Version.Latest, report.Version.Latest != "" })
	}
	if deepReport != nil {
		fmt.Fprintln(w)
		doctorDeep(w, *deepReport)
	}
	return deepDriftErr(deepReport)
}

// doctorDeep prints the source-vs-index proof. Everything above it is deja
// trusting its own bookkeeping; this section is the recount.
func doctorDeep(w io.Writer, r index.DeepReport) {
	fmt.Fprintln(w, "Deep verification:")
	fmt.Fprintf(w, "  checked  %s, %s, %s re-parsed, %s resolved\n",
		doctorCount(r.FilesChecked, "source file"),
		doctorCount(r.SessionsIndexed, "indexed session"),
		doctorCount(r.SampledFiles, "sampled file"),
		doctorCount(r.SampledPostings, "posting"))
	if len(r.Stale) > 0 {
		fmt.Fprintf(w, "  stale    %s changed since last pass — `deja index` will absorb them\n", doctorCount(len(r.Stale), "source"))
	}
	if r.Clean() {
		fmt.Fprintln(w, "  status   index matches sources — no memory lost")
		return
	}
	for _, f := range r.Findings {
		fmt.Fprintf(w, "  drift    [%s] %s\n", f.Kind, f.Detail)
	}
	fmt.Fprintf(w, "  status   %s — run `deja index --rebuild`\n", doctorCount(len(r.Findings), "finding"))
}

func deepDriftErr(r *index.DeepReport) error {
	if r == nil || r.Clean() {
		return nil
	}
	return fmt.Errorf("doctor: index drift detected (%s) — run `deja index --rebuild`", doctorCount(len(r.Findings), "finding"))
}

func doctorHooks(w io.Writer) {
	fmt.Fprintln(w, "Hooks:")
	path := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(w, "  %-12s missing      %s\n", "claude-code", path)
		doctorCodexHook(w)
		return
	}
	var root map[string]any
	if json.Unmarshal(b, &root) != nil {
		fmt.Fprintf(w, "  %-12s unreadable   %s\n", "claude-code", path)
		doctorCodexHook(w)
		return
	}
	hooks, _ := root["hooks"].(map[string]any)
	precompact := hookEventWired(hooks, "PreCompact", "hook-precompact")
	status := "missing"
	if precompact {
		status = "wired"
	}
	fmt.Fprintf(w, "  %-12s %-11s %s\n", "precompact", status, path)
	doctorCodexHook(w)
	doctorAutoRecall(w)
}

// doctorCodexHook reports the codex session-start hook state. Codex gates
// hooks behind its own trust store: hooks.json can be perfectly wired while
// codex keeps the hook disabled — memory then silently never arrives.
func doctorCodexHook(w io.Writer) {
	hooksPath := filepath.Join(sources.CodexHome(), "hooks.json")
	if _, err := os.Stat(hooksPath); err != nil {
		fmt.Fprintf(w, "  %-12s missing      %s\n", "codex-hook", hooksPath)
		return
	}
	cfg, err := os.ReadFile(filepath.Join(sources.CodexHome(), "config.toml"))
	status := "wired"
	if err == nil {
		if i := strings.Index(string(cfg), "hooks.json:session_start"); i >= 0 {
			rest := string(cfg[i:])
			off, on := strings.Index(rest, "enabled = false"), strings.Index(rest, "enabled = true")
			if off >= 0 && (on == -1 || on > off) {
				status = "disabled"
			} else if !codexHookTrusted(hooksPath, rest) {
				// Codex pins the hook file's hash when the user trusts it.
				// Any reinstall that rewrites hooks.json invalidates that,
				// and the hook stops running while enabled stays true — so
				// this is the state that looks healthiest and works least.
				status = "untrusted"
			}
		}
	}
	line := fmt.Sprintf("  %-12s %-11s %s", "codex-hook", status, hooksPath)
	if status == "untrusted" {
		line += "  (codex will not run it until you review it — press t in codex, or run /hooks)"
	}
	if status == "disabled" {
		line += "  (codex trusts but disabled it — re-enable in codex settings or hooks.state)"
	}
	fmt.Fprintln(w, line)
}

func hookEventWired(hooks map[string]any, event, command string) bool {
	entries, _ := hooks[event].([]any)
	for _, entryAny := range entries {
		entry, _ := entryAny.(map[string]any)
		if entry == nil {
			continue
		}
		// Substring match: installs write the absolute binary path ahead of
		// the subcommand, so an exact compare would report every real
		// installation as missing.
		hs, _ := entry["hooks"].([]any)
		for _, hAny := range hs {
			h, _ := hAny.(map[string]any)
			if h == nil || h["type"] != "command" {
				continue
			}
			if cmd, _ := h["command"].(string); strings.Contains(cmd, command) {
				return true
			}
		}
	}
	return false
}

func doctorEmbed(w io.Writer, r doctorEmbedReport) {
	fmt.Fprintln(w, "Embedding:")
	if r.Model == "" {
		fmt.Fprintf(w, "  endpoint   %s\n", r.State)
		return
	}
	fmt.Fprintf(w, "  endpoint   %s/model=%s/dim=%d\n", r.State, r.Model, r.Dim)
	fmt.Fprintf(w, "  sidecar    coverage=%.1f%%\n", r.Coverage)
}

// unplacedFiles counts transcripts under root that the harness's own filter
// did not pick up. A harness that changes its layout in a new version presents
// exactly this way: quietly fewer sessions, no error, and a directory size that
// still looks right (#701).
func unplacedFiles(root string, seen []string) int {
	have := make(map[string]bool, len(seen))
	for _, p := range seen {
		have[filepath.Clean(p)] = true
	}
	extra := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".jsonl", ".json":
		default:
			return nil
		}
		if !have[filepath.Clean(p)] {
			extra++
		}
		return nil
	})
	return extra
}

// printDoctorStoreWarnings says what deja could not read and why.
func printDoctorStoreWarnings(w io.Writer, stores []doctorStore) {
	for _, store := range stores {
		switch store.State {
		case "parsed-zero":
			fmt.Fprintf(w, "  warning      %s files found but newest parsed to zero\n", store.Name)
		case "unreadable":
			// The store is there and deja cannot read it — usually a harness
			// that changed its format. Silence here reads as "you have no
			// history with that agent".
			fmt.Fprintf(w, "  warning      %s store cannot be read — its format may have changed; please report it\n", store.Name)
		case "denied":
			// Not a format change and not an empty history: deja is not
			// allowed to read the files. On macOS this is usually Full Disk
			// Access rather than the file mode (#802). A store that is only
			// partly unreadable loses sessions from recall while looking whole
			// everywhere else, so it says which half it is (#816).
			what := "store cannot be read"
			if store.Partial {
				what = "store is only partly readable — some sessions are missing from recall"
			}
			fmt.Fprintf(w, "  warning      %s %s — permission denied on %s; check its permissions (on macOS, also Full Disk Access for your terminal)\n", store.Name, what, store.Denied)
		case "needs-sqlite3":
			// Not a format change: the parser could not run at all. Saying so
			// points at installing one package instead of at a bug report
			// against the harness (#792).
			fmt.Fprintf(w, "  warning      %s store needs the sqlite3 CLI — install it, then run `deja index`\n", store.Name)
		}
	}
}

func doctorHarnesses(w io.Writer) {
	fmt.Fprintln(w, "Harness stores:")
	sqlite := sources.SQLite3Available()

	printRow := func(name, path string, present bool, detail string) {
		status := "missing"
		if present {
			status = "found"
		}
		line := fmt.Sprintf("  %-12s %-8s %s", name, status, path)
		if detail != "" {
			line += "  (" + detail + ")"
		}
		fmt.Fprintln(w, line)
	}

	// printFiles is printRow for the harnesses that answer with a file list.
	// The count comes from the same filter the parser uses, so a store whose
	// layout differs slightly loses those files from every number deja prints
	// — the one thing `doctor` exists to rule out (#701).
	printFiles := func(name, path string, present bool, seen []string) {
		detail := doctorCount(len(seen), "file")
		if extra := unplacedFiles(path, seen); extra > 0 {
			detail += fmt.Sprintf(", %d not recognised here", extra)
		}
		printRow(name, path, present, detail)
	}

	claudeRoot := sources.ClaudeRoot()
	printFiles("claude", claudeRoot, doctorExists(claudeRoot), sources.ClaudeFiles())

	codexRoot := sources.CodexRoot()
	printFiles("codex", codexRoot, doctorExists(codexRoot), sources.CodexFiles())

	ocDB := sources.OpencodeDB()
	printRow("opencode", ocDB, doctorFilePresent(ocDB), doctorSQLiteDetail(ocDB, sqlite))

	printRow("aider", doctorAiderLocation(), len(sources.AiderFiles()) > 0, doctorCount(len(sources.AiderFiles()), "file"))

	geminiRoot := sources.GeminiRoot()
	printFiles("gemini", geminiRoot, doctorExists(geminiRoot), sources.GeminiChatFiles())

	printRow("cursor", doctorCursorLocation(), doctorCursorPresent(), doctorCursorDetail(sqlite))

	printRow("antigravity", doctorAntigravityLocation(), len(sources.AntigravityRoots()) > 0, doctorCount(len(sources.AntigravityTranscripts()), "file"))

	grokRoot := sources.GrokRoot()
	printFiles("grok", grokRoot, doctorExists(grokRoot), sources.GrokSessionFiles())

	qwenRoot := filepath.Join(sources.QwenRoot(), "projects")
	printFiles("qwen", qwenRoot, doctorExists(qwenRoot), sources.QwenSessionFiles())

	kimiRoot := filepath.Join(sources.KimiRoot(), "sessions")
	printFiles("kimi", kimiRoot, doctorExists(kimiRoot), sources.KimiSessionFiles())

	gooseRoot := filepath.Join(sources.GooseRoot(), "sessions")
	printRow("goose", gooseRoot, doctorExists(gooseRoot) || doctorFilePresent(sources.GooseDB()), doctorGooseDetail(sqlite))

	hermesRoot := sources.HermesProfilesRoot()
	printRow("hermes", hermesRoot, doctorExists(hermesRoot), doctorCount(len(sources.HermesSessionFiles()), "store"))

	clineModern := sources.ClineSessionsDir()
	clineFiles := len(sources.ClineSessionFiles())
	clineLoc := clineModern
	if legacy := sources.ClineLegacyRoots(); len(legacy) > 0 {
		clineLoc += ", " + strings.Join(legacy, string(os.PathListSeparator))
	}
	printRow("cline", clineLoc, clineFiles > 0 || doctorExists(clineModern), doctorCount(clineFiles, "file"))

	rooFiles := len(sources.RooTaskFiles())
	rooLoc := "VS Code globalStorage rooveterinaryinc.roo-cline"
	if roots := sources.RooRoots(); len(roots) > 0 {
		rooLoc = strings.Join(roots, string(os.PathListSeparator))
	}
	printRow("roo", rooLoc, rooFiles > 0, doctorCount(rooFiles, "file"))

	piRoot := sources.PiRoot()
	printFiles("pi", piRoot, doctorExists(piRoot), sources.PiSessionFiles())
	openclawRoot := sources.OpenClawRoot()
	printRow("openclaw", openclawRoot, doctorExists(openclawRoot), doctorCount(len(sources.OpenClawSessionFiles()), "file"))
	copilotRoot := sources.CopilotRoot()
	printFiles("copilot", copilotRoot, doctorExists(copilotRoot), sources.CopilotSessionFiles())
	printRow("deja", sources.NotesFile(), doctorFilePresent(sources.NotesFile()), "notes")
}

func doctorSQLiteDetail(db string, sqlite bool) string {
	fi, err := os.Stat(db)
	if err != nil || fi.Size() == 0 {
		return ""
	}
	d := humanBytes(fi.Size())
	if !sqlite {
		d += ", sqlite3 CLI missing — sessions unavailable"
	}
	return d
}

func doctorGooseDetail(sqlite bool) string {
	parts := []string{doctorCount(len(sources.GooseJSONLFiles()), "legacy file")}
	if fi, err := os.Stat(sources.GooseDB()); err == nil && fi.Size() > 0 {
		seg := humanBytes(fi.Size()) + " SQLite"
		if !sqlite {
			seg += ", sqlite3 CLI missing — modern sessions unavailable"
		}
		parts = append(parts, seg)
	}
	return strings.Join(parts, ", ")
}

func doctorCursorDetail(sqlite bool) string {
	parts := []string{doctorCount(len(sources.CursorTranscripts()), "CLI transcript")}
	dbs := sources.CursorDBs()
	if len(dbs) > 0 {
		var size int64
		for _, db := range dbs {
			if fi, err := os.Stat(db); err == nil {
				size += fi.Size()
			}
		}
		seg := fmt.Sprintf("%s IDE %s", doctorCount(len(dbs), "store"), humanBytes(size))
		if !sqlite {
			seg += ", sqlite3 CLI missing — IDE sessions unavailable"
		}
		parts = append(parts, seg)
	}
	return strings.Join(parts, ", ")
}

func doctorCursorPresent() bool {
	return len(sources.CursorTranscripts()) > 0 || len(sources.CursorDBs()) > 0
}

func doctorCursorLocation() string {
	return strings.Join([]string{sources.CursorUserRoot(), sources.CursorCLIRoot()}, ", ")
}

func doctorAiderLocation() string {
	loc := filepath.Join(sources.Home(), ".aider.chat.history.md")
	if roots := os.Getenv("DEJA_AIDER_ROOTS"); roots != "" {
		loc += string(os.PathListSeparator) + roots
	}
	return loc
}

func doctorAntigravityLocation() string {
	if roots := sources.AntigravityRoots(); len(roots) > 0 {
		return strings.Join(roots, string(os.PathListSeparator))
	}
	return filepath.Join(sources.Home(), ".gemini", "antigravity*")
}

func doctorTools(w io.Writer) {
	fmt.Fprintln(w, "Tools:")
	status := "not found"
	if sources.SQLite3Available() {
		status = "found"
	}
	fmt.Fprintf(w, "  %-12s %s (needed for opencode and Cursor IDE stores)\n", "sqlite3", status)
	// git is not optional decoration: without it a hit loses the line saying
	// its files have changed since, project names lose worktree identity, and
	// the session-start hook loses the task signal. All three degrade in
	// silence, which is fine on a hit and not fine with nowhere to ask (#796).
	gitStatus := "not found"
	if _, err := exec.LookPath("git"); err == nil {
		gitStatus = "found"
	}
	fmt.Fprintf(w, "  %-12s %s (needed for changed-file notes, worktree names and the task signal)\n", "git", gitStatus)
}

// doctorPolicy reports the one mechanism that separates local memory from
// imported. Load falls back to the permissive default on any error, so a
// malformed file changed nothing and said nothing, and a working one was
// invisible — leaving no place at all to find out what the rules are (#661).
func doctorPolicy(w io.Writer) {
	fmt.Fprintln(w, "Trust policy:")
	exists, unknown, err := policy.Diagnose()
	if !exists {
		fmt.Fprintf(w, "  %-12s no file at %s — every origin activates everywhere\n", "default", policy.Path())
		return
	}
	if err != nil {
		// The permissive default is what is actually in force, and that is the
		// part worth saying out loud: the file reads like a restriction.
		fmt.Fprintf(w, "  %-12s %s: %v\n", "unreadable", policy.Path(), err)
		fmt.Fprintf(w, "  %-12s every origin activates everywhere until it parses\n", "in force")
		return
	}
	pol := policy.Load()
	for _, activation := range []string{policy.ActivationSearch, policy.ActivationMCP, policy.ActivationAuto} {
		fmt.Fprintf(w, "  %-12s %s\n", activation, pol.Describe(activation))
	}
	for _, u := range unknown {
		fmt.Fprintf(w, "  %-12s %q is not an activation or origin deja consults — this rule does nothing\n", "ignored", u)
	}
}

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
		fmt.Fprintf(w, "  %-12s %-14s guidance %-11s %s\n", c.name, status, guidanceStatus(guidanceHarness(c.name)), c.path)
		if note := doctorWiringNote(c.name); note != "" && status == "wired" {
			fmt.Fprintf(w, "  %-12s %s\n", "", note)
		}
	}
}

// doctorWiringNote adds what "wired" cannot promise for a given harness. Three
// CLIs share ~/.grok and read different files; one of them — @vibe-kit/grok-cli
// — has no user-level MCP config at all, so `grok mcp list` reports nothing no
// matter what an installer writes to the home directory. Saying "wired" without
// that caveat is how someone concludes deja is broken.
func doctorWiringNote(name string) string {
	if name != "grok" {
		return ""
	}
	return "@vibe-kit/grok-cli reads MCP only from <cwd>/.grok/settings.json — run `grok mcp add deja -c deja -a mcp` in a project to wire that one"
}

type doctorMCPConfig struct {
	name  string
	path  string
	wired func(string) bool
}

func doctorMCPConfigs() []doctorMCPConfig {
	return []doctorMCPConfig{
		{"claude-code", sources.ClaudeJSONPath(), doctorJSONWired("mcpServers")},
		{"codex", filepath.Join(sources.CodexHome(), "config.toml"), doctorTOMLWired},
		{"opencode", doctorOpencodeConfigPath(), doctorJSONWired("mcp")},
		{"cursor", filepath.Join(sources.CursorCLIHome(), "mcp.json"), doctorJSONWired("mcpServers")},
		{"gemini", filepath.Join(sources.GeminiHome(), "settings.json"), doctorJSONWired("mcpServers")},
		{"antigravity", filepath.Join(antigravityConfigHome(), "mcp_config.json"), doctorJSONWired("mcpServers")},
		{"grok", filepath.Join(sources.GrokHome(), "config.toml"), doctorTOMLWired},
		{"qwen", filepath.Join(sources.QwenConfigDir(), "settings.json"), doctorJSONWired("mcpServers")},
		{"kimi", filepath.Join(sources.KimiConfigDir(), "mcp.json"), doctorJSONWired("mcpServers")},
		{"cline", sources.ClineMCPSettingsPath(), doctorJSONWired("mcpServers")},
		{"pi", filepath.Join(sources.PiConfigDir(), "mcp.json"), doctorJSONWired("mcpServers")},
		{"openclaw", filepath.Join(sources.OpenClawStateDir(), "openclaw.json"), doctorOpenClawWired},
		{"copilot", guidancePath("copilot"), doctorFileWired},
		{"hermes", filepath.Join(sources.HermesHome(), "config.yaml"), doctorHermesWired},
		{"goose", filepath.Join(gooseConfigDir(), "config.yaml"), doctorGooseWired},
	}
}

func doctorFileWired(path string) bool {
	_, err := os.Stat(path)
	return err == nil
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

// doctorOpenClawWired checks openclaw.json's nested mcp.servers map.
func doctorOpenClawWired(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var root map[string]any
	if json.Unmarshal(b, &root) != nil {
		return strings.Contains(string(b), `"deja"`)
	}
	mcp, _ := root["mcp"].(map[string]any)
	servers, _ := mcp["servers"].(map[string]any)
	_, ok := servers["deja"]
	return ok
}

func doctorJSONWired(key string) func(string) bool {
	return func(path string) bool {
		b, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		var root map[string]any
		if json.Unmarshal(b, &root) != nil {
			// jsonc or otherwise unparseable — fall back to a substring probe.
			return strings.Contains(string(b), `"deja"`)
		}
		m, _ := root[key].(map[string]any)
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

// mcpEntryRunsDeja reports whether an MCP server entry launches deja, in any
// of the shapes clients accept: a bare command, a command plus args, or a
// nested transport object.
func mcpEntryRunsDeja(v any) bool {
	m, _ := v.(map[string]any)
	if m == nil {
		return false
	}
	if t, ok := m["transport"].(map[string]any); ok {
		if mcpEntryRunsDeja(t) {
			return true
		}
	}
	cmd, _ := m["command"].(string)
	if commandIsDeja(cmd) {
		return true
	}
	// Windows and npx-style wiring puts the binary in args instead.
	args, _ := m["args"].([]any)
	for _, a := range args {
		if s, ok := a.(string); ok && commandIsDeja(s) {
			return true
		}
	}
	return false
}

func commandIsDeja(cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return false
	}
	// A windows path read on a unix build and vice versa: filepath.Base only
	// knows the separator it was compiled for, and these configs travel.
	cmd = strings.ReplaceAll(cmd, `\`, "/")
	base := strings.ToLower(path.Base(cmd))
	base = strings.TrimSuffix(base, ".exe")
	return base == "deja"
}

func doctorTOMLWired(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	if strings.Contains(string(b), "[mcp_servers.deja]") {
		return true
	}
	// Same reasoning as the JSON probe: a hand-wired server under another
	// name still runs deja. The TOML here is small and flat enough that
	// finding a command line naming the binary is enough.
	for _, line := range strings.Split(string(b), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "command" {
			continue
		}
		if commandIsDeja(strings.Trim(strings.TrimSpace(value), `"`)) {
			return true
		}
	}
	return false
}

func doctorIndex(w io.Writer, idx doctorComponent, dir string) {
	fmt.Fprintln(w, "Index:")
	loc := idx.Path
	if loc == "" {
		loc = dir
	}
	fmt.Fprintf(w, "  location %s\n", loc)
	fmt.Fprintf(w, "  exclusions %d active patterns\n", len(sources.ExclusionPatterns()))
	// A precise non-claim: users deciding what to trust deserve to read the
	// boundary in the tool itself, not only in the security docs.
	fmt.Fprintln(w, "  security plaintext on disk — protected by file permissions only, no encryption or access control")
	if idx.State == "missing" {
		fmt.Fprintln(w, "  status   not built (run `deja warmup`)")
		return
	}
	updated := "unknown"
	if fi, err := os.Stat(filepath.Join(dir, "manifest.gob")); err == nil {
		updated = fi.ModTime().Format("2006-01-02 15:04")
	}
	fmt.Fprintf(w, "  status   built (size=%s, updated=%s)\n", humanBytes(pathSize(dir)), updated)
	// A store whose postings vanished or whose record log was truncated cannot
	// answer anything, and said "up to date" until #735. The next search
	// rebuilds it, which is worth saying too — the reader has not lost memory,
	// only this build of the index.
	if index.Damaged(dir) {
		fmt.Fprintln(w, "  integrity damaged — records or postings are missing; the next search rebuilds the index")
		return
	}
	switch idx.State {
	case "stale":
		if idx.StaleStores == 1 {
			fmt.Fprintln(w, "  freshness 1 store changed since last build — run `deja index`")
		} else {
			fmt.Fprintf(w, "  freshness %d stores changed since last build — run `deja index`\n", idx.StaleStores)
		}
	default:
		fmt.Fprintln(w, "  freshness up to date")
	}
	health := index.IngestHealth(dir)
	names := make([]string, 0, len(health))
	for h, e := range health {
		if e.MalformedLines > 0 || e.FailedFiles > 0 {
			names = append(names, h)
		}
	}
	sort.Strings(names)
	for _, h := range names {
		e := health[h]
		// "malformed" covered only unparseable lines; valid JSON deja cannot
		// use is skipped just as invisibly, and the reader needs the same
		// warning either way (#814).
		fmt.Fprintf(w, "  ingest   %s: %d unusable lines skipped, %d files failed — see `deja doctor --json`\n", h, e.MalformedLines, e.FailedFiles)
	}
}

func doctorVersion(w io.Writer, lookup doctorVersionLookup) {
	fmt.Fprintln(w, "Version:")
	fmt.Fprintf(w, "  current  %s\n", version)
	latest, ok := lookup()
	if !ok {
		fmt.Fprintln(w, "  latest   unable to check")
		return
	}
	fmt.Fprintf(w, "  latest   v%s\n", latest)
	current := normalizeUpdateVersion(version)
	if order, ok := compareUpdateVersions(current, latest); ok {
		switch {
		case order < 0:
			fmt.Fprintln(w, "  status   update available (run `deja update`)")
		case order == 0:
			fmt.Fprintln(w, "  status   up to date")
		default:
			fmt.Fprintln(w, "  status   ahead of latest release")
		}
		return
	}
	if current == "dev" || current == "" {
		fmt.Fprintln(w, "  status   dev build")
	}
}

func doctorCount(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func doctorExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func doctorFilePresent(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Size() > 0
}
