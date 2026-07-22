package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
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
	for _, store := range report.Stores {
		if store.State == "parsed-zero" {
			fmt.Fprintf(w, "  warning      %s files found but newest parsed to zero\n", store.Name)
		}
	}
	fmt.Fprintln(w)
	doctorTools(w)
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
			}
		}
	}
	line := fmt.Sprintf("  %-12s %-11s %s", "codex-hook", status, hooksPath)
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

	claudeRoot := sources.ClaudeRoot()
	printRow("claude", claudeRoot, doctorExists(claudeRoot), doctorCount(len(sources.ClaudeFiles()), "file"))

	codexRoot := sources.CodexRoot()
	printRow("codex", codexRoot, doctorExists(codexRoot), doctorCount(len(sources.CodexFiles()), "file"))

	ocDB := sources.OpencodeDB()
	printRow("opencode", ocDB, doctorFilePresent(ocDB), doctorSQLiteDetail(ocDB, sqlite))

	printRow("aider", doctorAiderLocation(), len(sources.AiderFiles()) > 0, doctorCount(len(sources.AiderFiles()), "file"))

	geminiRoot := sources.GeminiRoot()
	printRow("gemini", geminiRoot, doctorExists(geminiRoot), doctorCount(len(sources.GeminiChatFiles()), "file"))

	printRow("cursor", doctorCursorLocation(), doctorCursorPresent(), doctorCursorDetail(sqlite))

	printRow("antigravity", doctorAntigravityLocation(), len(sources.AntigravityRoots()) > 0, doctorCount(len(sources.AntigravityTranscripts()), "file"))

	grokRoot := sources.GrokRoot()
	printRow("grok", grokRoot, doctorExists(grokRoot), doctorCount(len(sources.GrokSessionFiles()), "file"))

	qwenRoot := filepath.Join(sources.QwenRoot(), "projects")
	printRow("qwen", qwenRoot, doctorExists(qwenRoot), doctorCount(len(sources.QwenSessionFiles()), "file"))

	kimiRoot := filepath.Join(sources.KimiRoot(), "sessions")
	printRow("kimi", kimiRoot, doctorExists(kimiRoot), doctorCount(len(sources.KimiSessionFiles()), "file"))

	clineModern := sources.ClineSessionsDir()
	clineFiles := len(sources.ClineSessionFiles())
	clineLoc := clineModern
	if legacy := sources.ClineLegacyRoots(); len(legacy) > 0 {
		clineLoc += ", " + strings.Join(legacy, string(os.PathListSeparator))
	}
	printRow("cline", clineLoc, clineFiles > 0 || doctorExists(clineModern), doctorCount(clineFiles, "file"))

	piRoot := sources.PiRoot()
	printRow("pi", piRoot, doctorExists(piRoot), doctorCount(len(sources.PiSessionFiles()), "file"))
	copilotRoot := sources.CopilotRoot()
	printRow("copilot", copilotRoot, doctorExists(copilotRoot), doctorCount(len(sources.CopilotSessionFiles()), "file"))
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
	}
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
		{"pi", filepath.Join(sources.PiConfigDir(), "mcp.json"), doctorJSONWired("mcpServers")},
		{"copilot", guidancePath("copilot"), doctorFileWired},
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
		_, ok := m["deja"]
		return ok
	}
}

func doctorTOMLWired(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(b), "[mcp_servers.deja]")
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
		fmt.Fprintf(w, "  ingest   %s: %d malformed lines skipped, %d files failed — see `deja doctor --json`\n", h, e.MalformedLines, e.FailedFiles)
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
