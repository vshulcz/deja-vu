package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func mcpLine(name, status string) string {
	return fmt.Sprintf("  %-12s %-14s", name, status)
}

func stubLookup(latest string, ok bool) doctorVersionLookup {
	return func() (string, bool) { return latest, ok }
}

func TestDoctorFullReport(t *testing.T) {
	tmp := hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-proj", "sess.jsonl"), "sess", []string{
		`{"type":"user","sessionId":"sess","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"hello doctor"}}`,
	})
	if err := os.WriteFile(os.Getenv("DEJA_OPENCODE_DB"), []byte("sqlite db bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	claudeJSON := filepath.Join(tmp, "home", ".claude.json")
	if err := os.MkdirAll(filepath.Dir(claudeJSON), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudeJSON, []byte(`{"mcpServers":{"deja":{"command":"/bin/deja","args":["mcp"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	old := version
	version = "1.0.0"
	defer func() { version = old }()

	var out bytes.Buffer
	if err := runDoctor(&out, nil, stubLookup("9.9.9", true)); err != nil {
		t.Fatalf("runDoctor: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"Harness stores:", "Tools:", "MCP wiring:", "Index:", "Version:",
		"claude", "opencode", "aider", "gemini", "cursor", "antigravity", "grok",
		"1 file", mcpLine("claude-code", "wired"), "config missing",
		"not built", "current  1.0.0", "latest   v9.9.9", "update available",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("report missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("report contains color codes:\n%s", got)
	}
}

func TestDoctorStoreStates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod permissions are not meaningful on windows")
	}
	tmp := t.TempDir()
	unreadable := filepath.Join(tmp, "unreadable")
	if err := os.Mkdir(unreadable, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(unreadable, 0o700) }()

	cases := []struct {
		name  string
		check doctorStoreCheck
		want  string
	}{
		{"missing", doctorStoreCheck{name: "x", paths: []string{filepath.Join(tmp, "missing")}}, "missing"},
		{"empty", doctorStoreCheck{name: "x", paths: []string{tmp}}, "empty"},
		{"unreadable", doctorStoreCheck{name: "x", paths: []string{unreadable}}, "unreadable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := inspectDoctorStore(tc.check)
			if got.State != tc.want {
				t.Fatalf("state = %q, want %q", got.State, tc.want)
			}
		})
	}

	file := filepath.Join(tmp, "session.jsonl")
	if err := os.WriteFile(file, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		parse func(string) ([]model.Session, error)
		want  string
	}{
		{"ok", func(string) ([]model.Session, error) { return []model.Session{{ID: "1"}}, nil }, "ok"},
		{"parsed-zero", func(string) ([]model.Session, error) { return nil, nil }, "parsed-zero"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := inspectDoctorStore(doctorStoreCheck{name: "x", paths: []string{tmp}, files: []string{file}, parse: tc.parse})
			if got.State != tc.want {
				t.Fatalf("state = %q, want %q", got.State, tc.want)
			}
		})
	}
}

func TestDoctorParsesOnlyNewestFile(t *testing.T) {
	tmp := t.TempDir()
	oldPath := filepath.Join(tmp, "old")
	newPath := filepath.Join(tmp, "new")
	for _, path := range []string{oldPath, newPath} {
		if err := os.WriteFile(path, []byte(path), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	var parsed []string
	check := doctorStoreCheck{
		name: "x", paths: []string{tmp}, files: []string{newPath, oldPath},
		parse: func(path string) ([]model.Session, error) {
			parsed = append(parsed, path)
			return []model.Session{{ID: "1"}}, nil
		},
	}
	got, _ := inspectDoctorStore(check)
	if got.State != "ok" || len(parsed) != 1 || parsed[0] != newPath {
		t.Fatalf("state=%q parsed=%v, want newest only", got.State, parsed)
	}
}

func TestDoctorJSONGolden(t *testing.T) {
	tmp := hermeticEnv(t)
	if err := os.MkdirAll(filepath.Join(tmp, "home"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "project", "session.jsonl"), "session", []string{
		`{"type":"user","sessionId":"session","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"history"}}`,
	})
	t.Setenv("PATH", "")
	oldVersion := version
	version = "1.0.0"
	defer func() { version = oldVersion }()

	var out bytes.Buffer
	if err := runDoctor(&out, []string{"--json"}, stubLookup("2.0.0", true)); err != nil {
		t.Fatal(err)
	}
	// JSON escapes windows separators as \\, which ToSlash would turn
	// into //; collapse them to / before substituting the temp dir.
	got := strings.ReplaceAll(out.String(), `\\`, `/`)
	got = filepath.ToSlash(got)
	got = strings.ReplaceAll(got, filepath.ToSlash(tmp), "<tmp>")
	wantRaw, err := os.ReadFile(filepath.Join("testdata", "doctor.json"))
	if err != nil {
		t.Fatal(err)
	}
	// The golden may be checked out with CRLF on windows.
	want := strings.ReplaceAll(string(wantRaw), "\r\n", "\n")
	if got != want {
		t.Fatalf("doctor JSON mismatch\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestDoctorIndexStates(t *testing.T) {
	hermeticEnv(t)
	if got := inspectDoctorIndex(time.Time{}).State; got != "missing" {
		t.Fatalf("missing index state = %q", got)
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"manifest.gob", "sessions.gob"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manifestTime := time.Now().Add(-time.Minute)
	if err := os.Chtimes(filepath.Join(dir, "manifest.gob"), manifestTime, manifestTime); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		newest time.Time
		want   string
	}{
		{"ok", manifestTime.Add(-time.Minute), "ok"},
		{"stale", manifestTime.Add(time.Minute), "stale"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := inspectDoctorIndex(tc.newest).State; got != tc.want {
				t.Fatalf("state = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDoctorRejectsUnknownFlag(t *testing.T) {
	if err := runDoctor(io.Discard, []string{"--yaml"}, stubLookup("", false)); err == nil {
		t.Fatal("expected unknown flag error")
	}
}

func TestDoctorParserDispatch(t *testing.T) {
	tmp := t.TempDir()
	history := filepath.Join(tmp, "history.jsonl")
	writeFileMkdir(t, history, `{"session_id":"s","text":"hello","ts":1}`+"\n")
	rollout := filepath.Join(tmp, "rollout-s.jsonl")
	writeFileMkdir(t, rollout, `{"type":"response_item","timestamp":"2026-01-01T00:00:00Z","payload":{"role":"user","content":"hello"}}`+"\n")
	transcript := filepath.Join(tmp, "cursor.jsonl")
	writeFileMkdir(t, transcript, "not json\n")
	db := filepath.Join(tmp, "state.vscdb")
	writeFileMkdir(t, db, "")

	for _, tc := range []struct {
		name string
		path string
		fn   func(string) ([]model.Session, error)
	}{
		{"codex history", history, parseDoctorCodex},
		{"codex rollout", rollout, parseDoctorCodex},
		{"cursor transcript", transcript, parseDoctorCursor},
		{"cursor db", db, parseDoctorCursor},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.fn(tc.path); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDoctorVersionReportStates(t *testing.T) {
	cases := []struct {
		current string
		latest  string
		ok      bool
		want    string
	}{
		{"1.0.0", "", false, "unknown"},
		{"dev", "2.0.0", true, "dev"},
		{"1.0.0", "2.0.0", true, "update-available"},
		{"2.0.0", "1.0.0", true, "ahead"},
		{"1.0.0", "1.0.0", true, "ok"},
	}
	oldVersion := version
	defer func() { version = oldVersion }()
	for _, tc := range cases {
		version = tc.current
		got := collectDoctorVersion(stubLookup(tc.latest, tc.ok))
		if got.State != tc.want {
			t.Fatalf("version %q latest %q state = %q, want %q", tc.current, tc.latest, got.State, tc.want)
		}
	}
}

func TestDoctorVersionBranches(t *testing.T) {
	cases := []struct {
		current, latest string
		ok              bool
		want            string
	}{
		{"1.0.0", "9.9.9", true, "update available"},
		{"9.9.9", "9.9.9", true, "up to date"},
		{"9.9.9", "1.0.0", true, "ahead of latest release"},
		{"dev", "9.9.9", true, "dev build"},
		{"1.0.0", "", false, "unable to check"},
	}
	for _, tc := range cases {
		old := version
		version = tc.current
		var out bytes.Buffer
		doctorVersion(&out, stubLookup(tc.latest, tc.ok))
		version = old
		if !strings.Contains(out.String(), tc.want) {
			t.Fatalf("version(%q,%q,%v) = %q want %q", tc.current, tc.latest, tc.ok, out.String(), tc.want)
		}
	}
}

func TestDoctorMCPWiringStates(t *testing.T) {
	tmp := hermeticEnv(t)
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	grokHome := filepath.Join(tmp, "grok-home")
	t.Setenv("GROK_HOME", grokHome)

	// wired via JSON
	writeFileMkdir(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"deja":{}}}`)
	// present but not wired (TOML without our block)
	writeFileMkdir(t, filepath.Join(home, ".codex", "config.toml"), "[cli]\nauto_update = false\n")
	// wired via TOML at the Grok home, separate from the session read root
	writeFileMkdir(t, filepath.Join(grokHome, "config.toml"), "[mcp_servers.deja]\ncommand = \"x\"\n")
	// gemini settings.json left absent -> config missing

	var out bytes.Buffer
	doctorMCP(&out)
	got := out.String()
	for _, want := range []string{
		mcpLine("claude-code", "wired"),
		mcpLine("codex", "not wired"),
		mcpLine("grok", "wired"),
		mcpLine("gemini", "config missing"),
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("MCP wiring missing %q:\n%s", want, got)
		}
	}
}

func TestDoctorJSONCWiringFallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.jsonc")
	if err := os.WriteFile(path, []byte("{\n  // comment\n  \"mcp\": { \"deja\": {} }\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !doctorJSONWired("mcp")(path) {
		t.Fatal("jsonc with deja should read as wired via fallback")
	}
	if doctorJSONWired("mcp")(filepath.Join(dir, "absent.json")) {
		t.Fatal("absent config must not read as wired")
	}
}

func TestDoctorDispatchHermetic(t *testing.T) {
	hermeticEnv(t)
	old := doctorLookup
	doctorLookup = stubLookup("9.9.9", true)
	defer func() { doctorLookup = old }()
	out, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatalf("doctor dispatch err=%v", err)
	}
	if !strings.Contains(out, "Harness stores:") || !strings.Contains(out, "latest   v9.9.9") {
		t.Fatalf("doctor dispatch out=%q", out)
	}
}

func writeFileMkdir(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
