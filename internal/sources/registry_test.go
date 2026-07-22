package sources

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

type formatRegistry struct {
	SchemaVersion int                   `json:"schema_version"`
	Harnesses     []formatRegistryEntry `json:"harnesses"`
}

type formatRegistryEntry struct {
	ID           string   `json:"id"`
	StorePaths   []string `json:"store_paths"`
	FormatKind   string   `json:"format_kind"`
	FixturePaths []string `json:"fixture_paths"`
	ParserSource string   `json:"parser_source"`
	LastVerified string   `json:"last_verified"`
}

func TestFormatRegistryConformance(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, key := range []string{
		"AIDER_CHAT_HISTORY_FILE", "CLAUDE_CONFIG_DIR", "CODEX_HOME",
		"CURSOR_CONFIG_DIR", "DEJA_AIDER_ROOTS", "DEJA_ANTIGRAVITY_ROOT",
		"DEJA_CLAUDE_ROOT", "DEJA_CODEX_ROOT", "DEJA_CURSOR_CLI_ROOT",
		"DEJA_CURSOR_ROOT", "DEJA_GEMINI_ROOT", "DEJA_GROK_ROOT",
		"DEJA_PI_ROOT", "DEJA_QWEN_ROOT", "DEJA_KIMI_ROOT", "KIMI_CODE_HOME",
		"DEJA_CLINE_ROOT", "DEJA_CLINE_ROOTS", "CLINE_DIR", "CLINE_DATA_DIR",
		"CLINE_SESSION_DATA_DIR", "CLINE_MCP_SETTINGS_PATH", "DEJA_ROO_ROOTS",
		"DEJA_INCLUDE_SUBAGENTS", "DEJA_OPENCODE_DB", "GEMINI_CLI_HOME",
		"GROK_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME",
		"DEJA_NOTES_FILE",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(home, "claude"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(home, "gemini"))

	registry := readFormatRegistry(t, filepath.Join(root, "docs", "registry", "registry.json"))
	if registry.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", registry.SchemaVersion)
	}

	registered := make([]string, 0, len(registry.Harnesses))
	seen := map[string]bool{}
	for _, entry := range registry.Harnesses {
		if entry.ID == "" || seen[entry.ID] {
			t.Fatalf("invalid or duplicate harness id %q", entry.ID)
		}
		seen[entry.ID] = true
		registered = append(registered, entry.ID)
		validateRegistryEntry(t, root, entry)

		entry := entry
		t.Run(entry.ID, func(t *testing.T) {
			for _, fixture := range entry.FixturePaths {
				fixture := filepath.Join(root, filepath.FromSlash(fixture))
				sessions := parseRegistryFixture(t, entry.ID, fixture)
				validateRegistrySessions(t, entry.ID, sessions)
			}
		})
	}

	loaders := registryHarnessIDs()
	sort.Strings(registered)
	if strings.Join(registered, ",") != strings.Join(loaders, ",") {
		t.Fatalf("format registry harnesses = %v, source registry = %v", registered, loaders)
	}
}

// registryHarnessIDs lists the real harnesses in the source registry (excluding
// the notes pseudo-source) to compare against the published format registry.
func registryHarnessIDs() []string {
	var ids []string
	for _, h := range Registry() {
		if h.Name == "deja" {
			continue
		}
		ids = append(ids, h.Name)
	}
	sort.Strings(ids)
	return ids
}

func readFormatRegistry(t *testing.T, path string) formatRegistry {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var registry formatRegistry
	if err := json.Unmarshal(b, &registry); err != nil {
		t.Fatal(err)
	}
	return registry
}

func validateRegistryEntry(t *testing.T, root string, entry formatRegistryEntry) {
	t.Helper()
	if len(entry.StorePaths) == 0 || entry.FormatKind == "" || len(entry.FixturePaths) == 0 || entry.ParserSource == "" {
		t.Fatalf("incomplete registry entry for %q: %#v", entry.ID, entry)
	}
	if _, err := time.Parse("2006-01-02", entry.LastVerified); err != nil {
		t.Fatalf("%s last_verified: %v", entry.ID, err)
	}
	paths := append(append([]string(nil), entry.FixturePaths...), entry.ParserSource)
	for _, path := range paths {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err != nil {
			t.Fatalf("%s path %q: %v", entry.ID, path, err)
		}
	}
}

func parseRegistryFixture(t *testing.T, id, path string) []model.Session {
	t.Helper()
	var (
		sessions []model.Session
		err      error
	)
	switch id {
	case "claude":
		sessions, err = ParseClaudeFile(path)
	case "codex":
		sessions, err = ParseCodexRollout(path)
	case "kimi":
		sessions, err = ParseKimiFile(path)
	case "cline":
		sessions, err = ParseClineFile(path)
	case "roo":
		sessions, err = ParseRooTask(path)
	case "opencode":
		if !SQLite3Available() {
			t.Skip("sqlite3 not installed")
		}
		sql, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		db := filepath.Join(t.TempDir(), "opencode.db")
		if out, runErr := exec.Command("sqlite3", db, string(sql)).CombinedOutput(); runErr != nil {
			t.Fatalf("create sqlite fixture: %v: %s", runErr, out)
		}
		sessions, err = ParseOpencodeDB(db)
	case "aider":
		sessions, err = ParseAiderFile(path)
	case "gemini":
		sessions, err = ParseGeminiFile(path)
	case "cursor":
		sessions, err = ParseCursorTranscript(path)
	case "antigravity":
		sessions, err = ParseAntigravityFile(path)
	case "grok":
		sessions, err = ParseGrokFile(path)
	case "qwen":
		sessions, err = ParseQwenFile(path)
	case "pi":
		sessions, err = ParsePiFile(path)
	case "copilot":
		sessions, err = ParseCopilotFile(path)
	default:
		t.Fatalf("no conformance parser for %q", id)
	}
	if err != nil {
		t.Fatalf("parse %s fixture: %v", id, err)
	}
	return sessions
}

func validateRegistrySessions(t *testing.T, id string, sessions []model.Session) {
	t.Helper()
	if len(sessions) == 0 {
		t.Fatalf("%s fixture produced no sessions", id)
	}
	for _, session := range sessions {
		if session.Harness != id || session.ID == "" || session.Project == "" || session.Path == "" {
			t.Fatalf("%s fixture produced invalid session: %#v", id, session)
		}
		if session.Started.IsZero() || session.Updated.IsZero() || len(session.Messages) == 0 {
			t.Fatalf("%s fixture produced incomplete session: %#v", id, session)
		}
		for _, message := range session.Messages {
			if (message.Role != "user" && message.Role != "assistant") || strings.TrimSpace(message.Text) == "" || message.Time.IsZero() {
				t.Fatalf("%s fixture produced invalid message: %#v", id, message)
			}
		}
	}
}
