package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

func TestMimoInstallAndUninstall(t *testing.T) {
	hermeticEnv(t)
	dir := filepath.Join(t.TempDir(), "mimocode")
	t.Setenv("DEJA_MIMO_CONFIG_DIR", dir)

	const exe = "/opt/deja/bin/deja"
	if got, want := mimoConfigPath(), filepath.Join(dir, "mimocode.jsonc"); got != want {
		t.Fatalf("mimoConfigPath() = %q, want %q", got, want)
	}
	if !strings.HasSuffix(mimoSkillPath(), filepath.Join("skills", "deja-history", "SKILL.md")) {
		t.Fatalf("unexpected mimoSkillPath: %q", mimoSkillPath())
	}
	if !strings.Contains(mimoPluginJS(exe), "mimo-auto") {
		t.Fatalf("mimoPluginJS does not identify mimo-auto")
	}

	// An existing mimocode.json takes precedence over mimocode.jsonc.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	jsonCfg := filepath.Join(dir, "mimocode.json")
	if err := os.WriteFile(jsonCfg, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := mimoConfigPath(); got != jsonCfg {
		t.Fatalf("mimoConfigPath() with existing .json = %q, want %q", got, jsonCfg)
	}

	if _, err := installMimoAuto(exe, false); err != nil {
		t.Fatalf("installMimoAuto: %v", err)
	}
	if _, err := installMimoAuto(exe, false); err != nil {
		t.Fatalf("idempotent installMimoAuto: %v", err)
	}
	if list := tuiPluginList(t, filepath.Join(dir, "tui.json")); strings.Join(list, ",") != "./plugins/deja-status.tsx" {
		t.Fatalf("tui.json plugins = %v", list)
	}
	if _, err := os.Stat(filepath.Join(dir, "plugins", "deja-status.tsx")); err != nil {
		t.Fatalf("missing MiMo TUI plugin: %v", err)
	}
	if _, err := os.Stat(mimoSkillPath()); err != nil {
		t.Fatalf("missing MiMo skill file: %v", err)
	}

	if _, err := installMimoAuto(exe, true); err != nil {
		t.Fatalf("uninstall installMimoAuto: %v", err)
	}
	for _, p := range []string{
		filepath.Join(dir, "plugins", "deja-status.tsx"),
		mimoSkillPath(),
	} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("uninstall left behind %s", p)
		}
	}

	// Malformed config surfaces an error rather than overwriting it.
	if err := os.WriteFile(jsonCfg, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installMimo(exe, false); err == nil {
		t.Fatal("expected error on malformed mimocode.json")
	}
}

func TestOmnigentInstallAndStatusLine(t *testing.T) {
	hermeticEnv(t)
	root := filepath.Join(t.TempDir(), "omnigent")
	t.Setenv("DEJA_OMNIGENT_ROOT", root)

	const exe = "/opt/deja/bin/deja"
	if got := omnigentMCPPath(); got != filepath.Join(root, "mcp.json") {
		t.Fatalf("omnigentMCPPath() = %q", got)
	}
	if got := omnigentSettingsPath(); got != filepath.Join(root, "settings.json") {
		t.Fatalf("omnigentSettingsPath() = %q", got)
	}
	if !strings.HasSuffix(omnigentSkillPath(), filepath.Join("skills", "deja-history", "SKILL.md")) {
		t.Fatalf("unexpected omnigentSkillPath: %q", omnigentSkillPath())
	}

	if _, err := installOmnigentAuto(exe, false); err != nil {
		t.Fatalf("installOmnigentAuto: %v", err)
	}
	assertMCPServerEntry(t, omnigentMCPPath())
	if _, err := installOmnigentAuto(exe, false); err != nil {
		t.Fatalf("idempotent installOmnigentAuto: %v", err)
	}
	if settingsBytes, readErr := os.ReadFile(omnigentSettingsPath()); readErr != nil {
		t.Fatal(readErr)
	} else {
		var rootJSON map[string]any
		if umErr := json.Unmarshal(settingsBytes, &rootJSON); umErr != nil {
			t.Fatal(umErr)
		}
		if _, ok := rootJSON["statusLine"]; !ok {
			t.Fatal("settings.json missing statusLine after installOmnigentAuto")
		}
	}

	if _, err := installOmnigentAuto(exe, true); err != nil {
		t.Fatalf("uninstall installOmnigentAuto: %v", err)
	}
	if b, err := os.ReadFile(omnigentMCPPath()); err == nil && strings.Contains(string(b), "deja") {
		t.Errorf("omnigent mcp.json still contains deja after uninstall: %s", b)
	}
	if _, err := os.Stat(omnigentSkillPath()); err == nil {
		t.Errorf("uninstall left behind %s", omnigentSkillPath())
	}

	// Exercise installSettingsStatusLine branches directly.
	slPath := filepath.Join(t.TempDir(), "sl.json")
	if res, err := installSettingsStatusLine(slPath, exe, true); err != nil || res.Action != "unchanged" {
		t.Fatalf("uninstall missing statusline = (%+v, %v)", res, err)
	}
	if err := os.WriteFile(slPath, []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installSettingsStatusLine(slPath, exe, false); err == nil {
		t.Fatal("expected parse error on invalid JSON")
	}
	// Non-deja statusLine produces combinedStatusline guidance.
	if err := os.WriteFile(slPath, []byte(`{"statusLine":{"type":"command","command":"custom-status"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installSettingsStatusLine(slPath, exe, false); err == nil || !strings.Contains(err.Error(), "statusline is already configured") {
		t.Fatalf("expected conflict error, got %v", err)
	}
	// Wrapped statusLine is left unchanged.
	wrapped, wrapErr := json.Marshal(map[string]any{
		"statusLine": map[string]any{
			"type":    "command",
			"command": combinedStatusline("custom-status", exe),
		},
	})
	if wrapErr != nil {
		t.Fatal(wrapErr)
	}
	if err := os.WriteFile(slPath, wrapped, 0o600); err != nil {
		t.Fatal(err)
	}
	if res, err := installSettingsStatusLine(slPath, exe, false); err != nil || res.Action != "unchanged" {
		t.Fatalf("wrapped statusLine install = (%+v, %v)", res, err)
	}
	if res, err := installSettingsStatusLine(slPath, exe, true); err != nil || res.Action != "unchanged" {
		t.Fatalf("wrapped statusLine uninstall = (%+v, %v)", res, err)
	}
	// Previous deja binary path is replaced with an explanatory note.
	stale, staleErr := json.Marshal(map[string]any{
		"statusLine": map[string]any{
			"type":    "command",
			"command": "/usr/local/old/deja statusline",
		},
	})
	if staleErr != nil {
		t.Fatal(staleErr)
	}
	if err := os.WriteFile(slPath, stale, 0o600); err != nil {
		t.Fatal(err)
	}
	if res, err := installSettingsStatusLine(slPath, exe, false); err != nil || !strings.Contains(res.Note, "replaced the deja statusline") {
		t.Fatalf("stale deja replacement = (%+v, %v)", res, err)
	}
	if res, err := installSettingsStatusLine(slPath, exe, true); err != nil || res.Action == "unchanged" {
		t.Fatalf("statusLine uninstall = (%+v, %v)", res, err)
	}

	// Malformed mcp.json returns an error cleanly.
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(omnigentMCPPath(), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installOmnigentAuto(exe, false); err == nil {
		t.Fatal("expected error on malformed omnigent mcp.json")
	}
}

func TestOrcaInstallAndUninstall(t *testing.T) {
	hermeticEnv(t)
	root := filepath.Join(t.TempDir(), "orca")
	t.Setenv("DEJA_ORCA_ROOT", root)

	const exe = "/opt/deja/bin/deja"
	if got := orcaMCPPath(); got != filepath.Join(root, "mcp.json") {
		t.Fatalf("orcaMCPPath() = %q", got)
	}
	if got := orcaSettingsPath(); got != filepath.Join(root, "settings.json") {
		t.Fatalf("orcaSettingsPath() = %q", got)
	}
	if !strings.HasSuffix(orcaSkillPath(), filepath.Join("skills", "deja-history", "SKILL.md")) {
		t.Fatalf("unexpected orcaSkillPath: %q", orcaSkillPath())
	}

	if _, err := installOrcaAuto(exe, false); err != nil {
		t.Fatalf("installOrcaAuto: %v", err)
	}
	assertMCPServerEntry(t, orcaMCPPath())
	if _, err := installOrcaAuto(exe, false); err != nil {
		t.Fatalf("idempotent installOrcaAuto: %v", err)
	}
	for _, p := range []string{orcaMCPPath(), orcaSettingsPath(), orcaSkillPath()} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("missing Orca file %s: %v", p, err)
		}
	}

	if _, err := installOrcaAuto(exe, true); err != nil {
		t.Fatalf("uninstall installOrcaAuto: %v", err)
	}
	if b, err := os.ReadFile(orcaMCPPath()); err == nil && strings.Contains(string(b), "deja") {
		t.Errorf("orca mcp.json still contains deja after uninstall: %s", b)
	}
	if _, err := os.Stat(orcaSkillPath()); err == nil {
		t.Errorf("uninstall left behind %s", orcaSkillPath())
	}

	// Malformed mcp.json propagates error.
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orcaMCPPath(), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installOrcaAuto(exe, false); err == nil {
		t.Fatal("expected error on malformed orca mcp.json")
	}
}

func TestCoverageFloorHelpersAndDoctorProbes(t *testing.T) {
	hermeticEnv(t)

	// bench_ingest reporting + CLI dispatch.
	var buf bytes.Buffer
	printIngestReport(&buf, ingestReport{
		Sessions: 4,
		Seed:     1,
		Classes: []ingestClass{
			{Name: "", Rewritten: false},
			{Name: "append", WallMS: 1.5, RecordsMB: 0.2, AddedKB: 4.0, ClearedKB: 0.5, Rewritten: true},
		},
	})
	if !strings.Contains(buf.String(), "(unnamed)") || !strings.Contains(buf.String(), "YES") {
		t.Fatalf("unexpected printIngestReport output:\n%s", buf.String())
	}
	if err := runBenchIngest([]string{"--bogus"}); err == nil {
		t.Fatal("expected error for bogus ingest bench flag")
	}
	var textErr, jsonErr error
	textOut := captureStdoutCall(t, func() { textErr = runBenchIngest([]string{"--seed", "1"}) })
	jsonOut := captureStdoutCall(t, func() { jsonErr = runBenchIngest([]string{"--json", "--seed", "1"}) })
	if textErr != nil || !strings.Contains(textOut, "deja bench ingest") {
		t.Fatalf("runBenchIngest text = (%v, %q)", textErr, textOut)
	}
	if jsonErr != nil || !strings.Contains(jsonOut, `"classes"`) {
		t.Fatalf("runBenchIngest json = (%v, %q)", jsonErr, jsonOut)
	}

	// doctor_report probe wrappers on missing files return cleanly or error without panicking.
	missing := filepath.Join(t.TempDir(), "missing.db")
	_, _ = doctorProbeKiro(missing)
	_, _ = doctorProbeKiro(sources.KiroDB())
	_, _ = doctorProbeDevin(missing)
	_, _ = doctorProbeMimo(missing)
	_, _ = doctorProbeZCode(missing)
	_, _ = doctorProbeZCode(sources.ZCodeDB())
	_, _ = doctorProbeKilo(missing)
	_, _ = doctorProbeKilo(sources.KiloDB())

	// cli_help_empty grammar & diagnostic helpers.
	if pluralNote(1) != "the promoted note" || pluralNote(2) != "the promoted notes" {
		t.Fatal("unexpected pluralNote output")
	}
	if !strings.Contains(forgetTitleFix(fs.ErrPermission), "permissions") ||
		!strings.Contains(forgetTitleFix(syscall.ENOSPC), "space") ||
		forgetTitleFix(errors.New("other")) != "clear the problem above" {
		t.Fatal("unexpected forgetTitleFix output")
	}
	if got := strings.Join(sortedHarnesses(map[string]index.HarnessIngest{"b": {}, "a": {}}), ","); got != "a,b" {
		t.Fatalf("sortedHarnesses = %q", got)
	}
	if pluralSessionWord(1) != "a session" || pluralSessionWord(3) != "sessions" {
		t.Fatal("unexpected pluralSessionWord output")
	}
	if pluralWhich(1) != "it" || pluralWhich(2) != "them" {
		t.Fatal("unexpected pluralWhich output")
	}

	// install_json_mcp list & entry replacement helpers.
	if naturalList(nil) != "" || naturalList([]string{"a"}) != "a" ||
		naturalList([]string{"a", "b"}) != "a and b" ||
		naturalList([]string{"a", "b", "c"}) != "a, b, and c" {
		t.Fatal("unexpected naturalList output")
	}
	entry := map[string]any{"command": "/new/deja", "args": []any{"mcp"}}
	if replacedEntryNote(nil, entry) != "" || replacedEntryNote(entry, entry) != "" {
		t.Fatal("unexpected replacedEntryNote for nil/matching entry")
	}
	if replacedEntryNote(map[string]any{"x": 1}, entry) != "replaced the deja entry that was already here" {
		t.Fatal("unexpected replacedEntryNote for commandless entry")
	}
	if got := replacedEntryNote(map[string]any{"command": "/old/deja"}, entry); !strings.Contains(got, "/old/deja") {
		t.Fatalf("unexpected replacedEntryNote for old command: %q", got)
	}

	// hook_junie catch-up and failure dedup branches.
	dir := t.TempDir()
	junieCatchUpCompaction(dir, "missing-session", dir, sources.JunieTurnState{})
	junieCatchUpCompaction(dir, "missing-session", dir, sources.JunieTurnState{
		CompactionID: "c1",
		CompactionAt: time.Now(),
	})
	if got := junieFailurePair(dir, "s1", dir, sources.JunieTurnState{}); got != "" {
		t.Fatalf("empty failure state returned %q", got)
	}
	st := sources.JunieTurnState{FailureID: "f1", Failure: "exit 1", FailureCommand: "make test"}
	_ = junieFailurePair(dir, "s1", dir, st)
	if dup := junieFailurePair(dir, "s1", dir, st); dup != "" {
		t.Fatalf("expected deduped second failure call to return empty, got %q", dup)
	}

	// bench_read CLI + printReadReport + readReport skipped/non-skipped branches.
	if err := runBenchRead([]string{"--bogus"}); err == nil {
		t.Fatal("expected error for bogus read bench flag")
	}
	var readTextErr, readJSONErr error
	readTextOut := captureStdoutCall(t, func() { readTextErr = runBenchRead([]string{"--seed", "1"}) })
	readJSONOut := captureStdoutCall(t, func() { readJSONErr = runBenchRead([]string{"--json", "--seed", "1"}) })
	if readTextErr != nil || !strings.Contains(readTextOut, "deja bench read") {
		t.Fatalf("runBenchRead text = (%v, %q)", readTextErr, readTextOut)
	}
	if readJSONErr != nil || !strings.Contains(readJSONOut, `"corpus_hash"`) {
		t.Fatalf("runBenchRead json = (%v, %q)", readJSONErr, readJSONOut)
	}
	buf.Reset()
	printReadReport(&buf, readReport{Skipped: "no sqlite3"})
	if !strings.Contains(buf.String(), "skipped: no sqlite3") {
		t.Fatalf("unexpected skipped read report: %q", buf.String())
	}
	buf.Reset()
	printReadReport(&buf, readReport{
		CorpusHash: "abc", Seed: 1, Ratio: 12.5,
		Classes: []readClass{{Name: "ordinary", WallMS: 1.0, StoreMB: 0.1, Sessions: 2, Messages: 4}},
	})
	if !strings.Contains(buf.String(), "12.5x") {
		t.Fatalf("unexpected read report: %q", buf.String())
	}

	// parseDoctor* wrappers (dispatcher branches for missing/synthetic paths).
	_, _ = parseDoctorClaude(filepath.Join(dir, "history.jsonl"))
	_, _ = parseDoctorClaude(filepath.Join(dir, "session.jsonl"))
	_, _ = parseDoctorTrae(filepath.Join(dir, "history.jsonl"))
	_, _ = parseDoctorTrae(filepath.Join(dir, "rollout.jsonl"))
	_, _ = parseDoctorOpenClaw(filepath.Join(dir, "openclaw-agent.sqlite"))
	_, _ = parseDoctorOpenClaw(filepath.Join(dir, "session.jsonl"))
	_, _ = parseDoctorGoose(filepath.Join(dir, "sessions.db"))
	_, _ = parseDoctorGoose(filepath.Join(dir, "session.jsonl"))
	_, _ = parseDoctorHermes(filepath.Join(dir, "state.db"))

	// printAuditReport all formatting branches (>6 stores, retention modes, claude-mem, wired/unwired).
	buf.Reset()
	printAuditReport(&buf, auditReport{})
	if !strings.Contains(buf.String(), "none detected") || !strings.Contains(buf.String(), "not wired") {
		t.Fatalf("unexpected empty audit report:\n%s", buf.String())
	}
	buf.Reset()
	printAuditReport(&buf, auditReport{
		Stores: []auditStoreRow{
			{Name: "claude", Files: 1}, {Name: "codex", Files: 1}, {Name: "cursor", Files: 1},
			{Name: "gemini", Files: 1}, {Name: "opencode", Files: 1}, {Name: "goose", Files: 1},
			{Name: "kilo", Files: 2},
		},
		TotalFiles: 8, TotalBytes: 2048,
		ClaudeRetention: auditClaudeRetention{
			CleanupPeriodDays: 14, Explicit: true, TotalFiles: 5,
			PastPeriod: 1, ExpiringIn7Days: 1, PreservedDeleted: 2,
		},
		ClaudeMemDBPresent: true, ClaudeMemDBBytes: 4096,
		IndexedSessions: 5, RecoverableFiles: 3,
		WiredTargets: []string{"claude", "codex"},
	})
	if !strings.Contains(buf.String(), "+1 more") || !strings.Contains(buf.String(), "claude-mem.db") {
		t.Fatalf("unexpected populated audit report:\n%s", buf.String())
	}
	buf.Reset()
	printAuditReport(&buf, auditReport{
		TotalFiles: 3,
		ClaudeRetention: auditClaudeRetention{
			CleanupPeriodDays: 0, Explicit: true, TotalFiles: 3,
		},
		IndexedSessions: 2,
		NextCommand:     "deja install --auto",
	})
	if !strings.Contains(buf.String(), "disabled") || !strings.Contains(buf.String(), "deja install --auto") {
		t.Fatalf("unexpected disabled-retention audit report:\n%s", buf.String())
	}

	// changes_block, delete, and doctor_zed_reach pure helpers.
	if clipToFit("abc", 10) != "abc" || !strings.HasSuffix(clipToFit(strings.Repeat("x", 30), 10), "…\n") {
		t.Fatal("unexpected clipToFit result")
	}
	if clipChange("\nabc\n", 10) != "abc" || !strings.HasSuffix(clipChange(strings.Repeat("y", 20), 5), "…") {
		t.Fatal("unexpected clipChange result")
	}
	parent := model.Session{ID: "parent-1"}
	if !childOf(model.Session{Parent: "parent-1"}, parent) ||
		!childOf(model.Session{Path: filepath.Join(dir, "session-parent-1", "a.jsonl")}, parent) ||
		childOf(model.Session{Path: filepath.Join(dir, "unrelated", "a.jsonl")}, parent) {
		t.Fatal("unexpected childOf result")
	}
	if !dirOfSession("session_id1", "id1") || dirOfSession("work-id1", "id1") {
		t.Fatal("unexpected dirOfSession result")
	}
	if dirs := zedExtensionDirs(); len(dirs) == 0 {
		t.Fatal("zedExtensionDirs returned empty slice")
	}
	zedSettings := filepath.Join(dir, "zed-settings.json")
	if err := os.WriteFile(zedSettings, []byte(`{"context_servers":{"deja-context-server":{"command":"/bin/deja","args":["mcp"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !zedEntryHasCommand(zedSettings) || zedEntryHasCommand(filepath.Join(dir, "nope.json")) {
		t.Fatal("unexpected zedEntryHasCommand result")
	}
}
