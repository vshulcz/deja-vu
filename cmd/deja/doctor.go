package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// doctorVersionLookup fetches the latest released version. It is injected so
// tests can stub it — the real lookup talks to GitHub with a short budget.
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

// runDoctor prints a self-diagnosis report. It never fails: every section
// degrades to a plain status line so the exit code stays 0.
func runDoctor(w io.Writer, lookup doctorVersionLookup) error {
	doctorHarnesses(w)
	fmt.Fprintln(w)
	doctorTools(w)
	fmt.Fprintln(w)
	doctorMCP(w)
	fmt.Fprintln(w)
	doctorIndex(w)
	fmt.Fprintln(w)
	doctorVersion(w, lookup)
	return nil
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
	return strings.Join([]string{sources.CursorUserRoot(), sources.CursorCLIRoot()}, string(os.PathListSeparator))
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
	h := homeDir()
	configs := []struct {
		name  string
		path  string
		wired func(string) bool
	}{
		{"claude-code", sources.ClaudeJSONPath(), doctorJSONWired("mcpServers")},
		{"codex", filepath.Join(sources.CodexHome(), "config.toml"), doctorTOMLWired},
		{"opencode", doctorOpencodeConfigPath(), doctorJSONWired("mcp")},
		{"cursor", filepath.Join(sources.CursorCLIHome(), "mcp.json"), doctorJSONWired("mcpServers")},
		{"gemini", filepath.Join(sources.GeminiHome(), "settings.json"), doctorJSONWired("mcpServers")},
		{"antigravity", filepath.Join(h, ".gemini", "config", "mcp_config.json"), doctorJSONWired("mcpServers")},
		{"grok", filepath.Join(sources.GrokRoot(), "config.toml"), doctorTOMLWired},
	}
	for _, c := range configs {
		status := "config missing"
		if doctorExists(c.path) {
			if c.wired(c.path) {
				status = "wired"
			} else {
				status = "not wired"
			}
		}
		fmt.Fprintf(w, "  %-12s %-14s %s\n", c.name, status, c.path)
	}
}

func doctorOpencodeConfigPath() string {
	dir := filepath.Join(homeDir(), ".config", "opencode")
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

func doctorIndex(w io.Writer) {
	fmt.Fprintln(w, "Index:")
	dir := index.DefaultDir()
	fmt.Fprintf(w, "  location %s\n", dir)
	if !index.HasManifest(dir) {
		fmt.Fprintln(w, "  status   not built (run `deja warmup`)")
		return
	}
	updated := "unknown"
	if fi, err := os.Stat(filepath.Join(dir, "manifest.gob")); err == nil {
		updated = fi.ModTime().Format("2006-01-02 15:04")
	}
	fmt.Fprintf(w, "  status   built (size=%s, updated=%s)\n", humanBytes(pathSize(dir)), updated)
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
