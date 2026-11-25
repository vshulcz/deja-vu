package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

func isolateGigaCodeCLIEnv(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	idxDir := filepath.Join(tmp, "deja-index")
	t.Setenv("DEJA_DIR", idxDir)
	t.Setenv("DEJA_INDEX_DIR", idxDir)
	t.Setenv("GIGACODE_HOME", "")
	t.Setenv("DEJA_GIGACODE_ROOT", "")
	t.Setenv("QWEN_HOME", "")
	t.Setenv("DEJA_QWEN_ROOT", "")
	t.Setenv("GIGACODE_PACKAGED_LAUNCH", "")
	t.Setenv("GIGACODE_CLI", "")
	t.Setenv("QWEN_CODE", "")
	return tmp
}

func writeGigaCodeJSONL(t *testing.T, path string, lines []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readJSONObject(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var obj map[string]any
	if err = json.Unmarshal(b, &obj); err != nil {
		t.Fatalf("unmarshal %s: %v\n%s", path, err, string(b))
	}
	return obj
}

func TestGigaCodeInstallUninstallAndIsolation(t *testing.T) {
	tmp := isolateGigaCodeCLIEnv(t)
	qwenHome := filepath.Join(tmp, "custom-qwen")
	t.Setenv("QWEN_HOME", qwenHome)
	exe := "/usr/local/bin/deja"

	// With GIGACODE_HOME unset, install must land in ~/.gigacode and ignore QWEN_HOME.
	res, err := installTarget("gigacode", exe, false)
	if err != nil {
		t.Fatalf("install gigacode: %v", err)
	}
	wantSettings := filepath.Join(homeDir(), ".gigacode", "settings.json")
	if res.Path != wantSettings || res.Action == "unchanged" {
		t.Fatalf("install gigacode = %+v, want Path=%q non-unchanged", res, wantSettings)
	}
	cfg := readJSONObject(t, wantSettings)
	servers, _ := cfg["mcpServers"].(map[string]any)
	if _, ok := servers["deja"]; !ok {
		t.Fatalf("mcpServers missing deja in %+v", cfg)
	}
	if _, err = os.Stat(filepath.Join(qwenHome, "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("install gigacode touched QWEN_HOME/settings.json (%v)", err)
	}
	if _, err = installTarget("gigacode", exe, true); err != nil {
		t.Fatalf("uninstall gigacode: %v", err)
	}
	if _, err = os.Stat(wantSettings); !os.IsNotExist(err) {
		after := readJSONObject(t, wantSettings)
		if m, _ := after["mcpServers"].(map[string]any); len(m) != 0 {
			t.Fatalf("uninstall gigacode left mcpServers: %+v", after)
		}
	}

	// Explicit GIGACODE_HOME + gigacode-auto preserves user statusLine and wires hooks/MCP/statusLine.
	gcHome := filepath.Join(tmp, "gc-home")
	t.Setenv("GIGACODE_HOME", gcHome)
	gcSettings := filepath.Join(gcHome, "settings.json")
	customStatus := map[string]any{
		"ui": map[string]any{
			"statusLine": map[string]any{
				"type":    "command",
				"command": "/usr/local/bin/custom-statusline",
			},
		},
	}
	rawCustom, err := json.Marshal(customStatus)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(gcHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(gcSettings, rawCustom, 0o644); err != nil {
		t.Fatal(err)
	}

	autoRes, err := installTarget("gigacode-auto", exe, false)
	if err != nil {
		t.Fatalf("install gigacode-auto: %v", err)
	}
	if autoRes.Path == "" {
		t.Fatalf("install gigacode-auto returned empty path: %+v", autoRes)
	}
	autoCfg := readJSONObject(t, gcSettings)
	if _, ok := autoCfg["mcpServers"].(map[string]any)["deja"]; !ok {
		t.Fatalf("gigacode-auto missing mcpServers.deja: %+v", autoCfg)
	}
	hooks, _ := autoCfg["hooks"].(map[string]any)
	for _, ev := range []string{"SessionStart", "UserPromptSubmit", "PostToolUseFailure", "PostToolUse", "PreCompact", "SessionEnd"} {
		entries, _ := hooks[ev].([]any)
		if len(entries) == 0 {
			t.Fatalf("gigacode-auto missing hook event %s in %+v", ev, hooks)
		}
		rawEv, mErr := json.Marshal(entries)
		if mErr != nil {
			t.Fatalf("marshal hook event %s: %v", ev, mErr)
		}
		if !strings.Contains(string(rawEv), `"timeout":60000`) {
			t.Fatalf("hook event %s missing 60000ms timeout: %s", ev, string(rawEv))
		}
	}
	uiCfg, _ := autoCfg["ui"].(map[string]any)
	slCfg, _ := uiCfg["statusLine"].(map[string]any)
	if slCfg["command"] != "/usr/local/bin/custom-statusline" {
		t.Fatalf("gigacode-auto overwrote user statusLine: %+v", slCfg)
	}
	if _, err = installTarget("gigacode-auto", exe, true); err != nil {
		t.Fatalf("uninstall gigacode-auto: %v", err)
	}
	afterAuto := readJSONObject(t, gcSettings)
	afterUI, _ := afterAuto["ui"].(map[string]any)
	afterSL, _ := afterUI["statusLine"].(map[string]any)
	if afterSL["command"] != "/usr/local/bin/custom-statusline" {
		t.Fatalf("uninstall gigacode-auto lost user statusLine: %+v", afterSL)
	}
	if ah, _ := afterAuto["hooks"].(map[string]any); len(ah) != 0 {
		t.Fatalf("uninstall gigacode-auto left hooks: %+v", ah)
	}

	// Fresh install without pre-existing statusLine writes deja statusLine.
	gcFresh := filepath.Join(tmp, "gc-fresh")
	t.Setenv("GIGACODE_HOME", gcFresh)
	if _, err = installTarget("gigacode-auto", exe, false); err != nil {
		t.Fatalf("fresh install gigacode-auto: %v", err)
	}
	freshCfg := readJSONObject(t, filepath.Join(gcFresh, "settings.json"))
	freshUI, _ := freshCfg["ui"].(map[string]any)
	freshSL, _ := freshUI["statusLine"].(map[string]any)
	if freshSL["type"] != "command" || freshSL["refreshInterval"] != float64(1) {
		t.Fatalf("unexpected fresh ui.statusLine: %+v", freshSL)
	}
	if cmd, _ := freshSL["command"].(string); !strings.Contains(cmd, "statusline") {
		t.Fatalf("fresh ui.statusLine command = %q, want statusline invocation", cmd)
	}
}

func TestGigaCodeGuidanceAndRules(t *testing.T) {
	tmp := isolateGigaCodeCLIEnv(t)
	gcHome := filepath.Join(tmp, "gc-home")
	qwenHome := filepath.Join(tmp, "qwen-home")
	t.Setenv("GIGACODE_HOME", gcHome)
	t.Setenv("QWEN_HOME", qwenHome)
	if err := os.MkdirAll(gcHome, 0o755); err != nil {
		t.Fatal(err)
	}

	gRes, err := guidanceResult("gigacode", false)
	if err != nil {
		t.Fatalf("guidanceResult(gigacode): %v", err)
	}
	if gRes.Path != sharedSkillPath() {
		t.Fatalf("guidanceResult Path = %q, want shared %q", gRes.Path, sharedSkillPath())
	}
	if got := guidanceStatus("gigacode"); got != "written" {
		t.Fatalf("guidanceStatus(gigacode) = %q, want written", got)
	}
	if _, err = os.Stat(filepath.Join(gcHome, "skills", "deja-history", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("gigacode should use ~/.agents/skills, not $GIGACODE_HOME/skills")
	}

	srcRules := rulesSourcePath()
	if err = os.MkdirAll(filepath.Dir(srcRules), 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(srcRules, []byte("- always verify fixtures\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err = runRulesTo(&buf, []string{"sync"}); err != nil {
		t.Fatalf("runRulesTo sync: %v", err)
	}
	gcRules := filepath.Join(gcHome, "GIGACODE.md")
	b, err := os.ReadFile(gcRules)
	if err != nil || !strings.Contains(string(b), "always verify fixtures") {
		t.Fatalf("expected rules synced to %s, got err=%v content=%q", gcRules, err, string(b))
	}
	if _, err = os.Stat(filepath.Join(qwenHome, "QWEN.md")); !os.IsNotExist(err) {
		t.Fatalf("rules sync touched nonexistent QWEN_HOME/QWEN.md")
	}
}

func TestGigaCodeResumeAndHandoff(t *testing.T) {
	tmp := isolateGigaCodeCLIEnv(t)
	gcRoot := filepath.Join(tmp, "gc-root")
	t.Setenv("DEJA_GIGACODE_ROOT", gcRoot)

	cyrillicDir := filepath.Join(tmp, "проекты", "демо-app")
	if err := os.MkdirAll(cyrillicDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cwdJSON, err := json.Marshal(cyrillicDir)
	if err != nil {
		t.Fatal(err)
	}
	chatPath := filepath.Join(gcRoot, "projects", encodeTestProjectDir(cyrillicDir), "chats", "gc-res-1.jsonl")
	writeGigaCodeJSONL(t, chatPath, []string{
		`{"type":"user","sessionId":"gc-res-1","cwd":` + string(cwdJSON) + `,"timestamp":"2026-11-24T10:00:00Z","message":{"role":"user","parts":[{"text":"resume check"}]}}`,
	})
	dir, cmd, err := resumeCommand(model.Session{Harness: "gigacode", ID: "gc-res-1", Path: chatPath})
	if err != nil || dir != cyrillicDir || cmd != "gigacode -r gc-res-1" {
		t.Fatalf("resumeCommand(recorded) = (%q, %q, %v), want (%q, %q, nil)", dir, cmd, err, cyrillicDir, "gigacode -r gc-res-1")
	}

	// Unrecorded transcript falls back to decoding existing project directory name.
	// Resolve symlinks / Windows 8.3 short temp names (RUNNER~1) so the directory
	// listing matches the encoded folder name (#4455).
	fallbackBase, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatal(err)
	}
	fallbackDir := filepath.Join(fallbackBase, "fallback-ws")
	if err = os.MkdirAll(fallbackDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fbEnc := encodeTestProjectDir(fallbackDir)
	fbPath := filepath.Join(gcRoot, "projects", fbEnc, "chats", "gc-res-fb.jsonl")
	writeGigaCodeJSONL(t, fbPath, []string{
		`{"type":"user","sessionId":"gc-res-fb","timestamp":"2026-11-24T10:00:00Z","message":{"role":"user","parts":[{"text":"fallback check"}]}}`,
	})
	fbDir, fbCmd, err := resumeCommand(model.Session{Harness: "gigacode", ID: "gc-res-fb", Path: fbPath})
	if err != nil || fbDir != fallbackDir || fbCmd != "gigacode -r gc-res-fb" {
		t.Fatalf("resumeCommand(fallback) = (%q, %q, %v), want (%q, %q, nil)", fbDir, fbCmd, err, fallbackDir, "gigacode -r gc-res-fb")
	}

	// Gone recorded dir must refuse cleanly with pointer to `deja show`.
	goneDir := filepath.Join(tmp, "gone-ws")
	goneJSON, err := json.Marshal(goneDir)
	if err != nil {
		t.Fatal(err)
	}
	gonePath := filepath.Join(gcRoot, "projects", encodeTestProjectDir(goneDir), "chats", "gc-gone-1.jsonl")
	writeGigaCodeJSONL(t, gonePath, []string{
		`{"type":"user","sessionId":"gc-gone-1","cwd":` + string(goneJSON) + `,"timestamp":"2026-11-24T10:00:00Z","message":{"role":"user","parts":[{"text":"gone check"}]}}`,
	})
	if _, _, err = resumeCommand(model.Session{Harness: "gigacode", ID: "gc-gone-1", Path: gonePath}); err == nil || !strings.Contains(err.Error(), "deja show") {
		t.Fatalf("resumeCommand(gone) = %v, want error mentioning deja show", err)
	}

	// Subagent sessions cannot be reopened standalone.
	if !subagentNotReopened["gigacode"] {
		t.Fatalf("subagentNotReopened missing gigacode")
	}

	// End-to-end runResume through index.
	idxDir := os.Getenv("DEJA_DIR")
	if err = index.Ensure(idxDir, "", true, nil); err != nil {
		t.Fatalf("index.Ensure: %v", err)
	}
	var resBuf bytes.Buffer
	if err = runResume(idxDir, []string{"gc-res-1"}, &resBuf); err != nil {
		t.Fatalf("runResume(gc-res-1): %v", err)
	}
	if !strings.Contains(resBuf.String(), "gigacode -r gc-res-1") {
		t.Fatalf("runResume output = %q, want gigacode -r gc-res-1", resBuf.String())
	}

	// Handoff command + target list.
	argv, ok := handoffCommand("gigacode", "check failing tests")
	if !ok || len(argv) != 3 || argv[0] != "gigacode" || argv[1] != "-i" || argv[2] != "check failing tests" {
		t.Fatalf("handoffCommand = (%v, %v), want ([gigacode -i check failing tests], true)", argv, ok)
	}
	if !slices.Contains(handoffTargets(), "gigacode") {
		t.Fatalf("handoffTargets() missing gigacode: %v", handoffTargets())
	}
}

func TestGigaCodeDeleteAndSources(t *testing.T) {
	tmp := isolateGigaCodeCLIEnv(t)
	gcRoot := filepath.Join(tmp, "gc-root")
	t.Setenv("DEJA_GIGACODE_ROOT", gcRoot)

	proj := filepath.Join(gcRoot, "projects", "-workspace-del")
	chatPath := filepath.Join(proj, "chats", "gc-del-1.jsonl")
	subPath := filepath.Join(proj, "subagents", "gc-del-1", "agent-a.jsonl")
	writeGigaCodeJSONL(t, chatPath, []string{
		`{"type":"user","sessionId":"gc-del-1","cwd":"/workspace/del","timestamp":"2026-11-24T10:00:00Z","message":{"role":"user","parts":[{"text":"delete me"}]}}`,
	})
	writeGigaCodeJSONL(t, subPath, []string{
		`{"type":"user","sessionId":"gc-del-1","agentName":"helper","timestamp":"2026-11-24T10:01:00Z","message":{"role":"user","parts":[{"text":"sub turn"}]}}`,
	})
	old := time.Now().Add(-15 * time.Minute)
	for _, p := range []string{chatPath, subPath, filepath.Dir(subPath)} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}

	idxDir := os.Getenv("DEJA_DIR")
	if err := index.Ensure(idxDir, "", true, nil); err != nil {
		t.Fatalf("index.Ensure: %v", err)
	}
	srcOut, err := captureRun(t, "sources")
	if err != nil {
		t.Fatalf("deja sources: %v", err)
	}
	if !strings.Contains(srcOut, "gigacode") {
		t.Fatalf("deja sources missing gigacode:\n%s", srcOut)
	}

	var delBuf bytes.Buffer
	if err = runDeleteIn(idxDir, []string{"gc-del-1", "--yes"}, nil, &delBuf); err != nil {
		t.Fatalf("runDeleteIn: %v", err)
	}
	if _, err = os.Stat(chatPath); !os.IsNotExist(err) {
		t.Fatalf("delete left main chat %s (%v)", chatPath, err)
	}
	if _, err = os.Stat(filepath.Dir(subPath)); !os.IsNotExist(err) {
		t.Fatalf("delete left subagents dir %s (%v)", filepath.Dir(subPath), err)
	}
}

func TestGigaCodeDoctorAndClientOff(t *testing.T) {
	tmp := isolateGigaCodeCLIEnv(t)
	gcHome := filepath.Join(tmp, "gc-home")
	gcRoot := filepath.Join(tmp, "gc-root")
	t.Setenv("GIGACODE_HOME", gcHome)
	t.Setenv("DEJA_GIGACODE_ROOT", gcRoot)

	if _, err := installTarget("gigacode-auto", "/usr/local/bin/deja", false); err != nil {
		t.Fatalf("install gigacode-auto: %v", err)
	}
	if _, err := guidanceResult("gigacode", false); err != nil {
		t.Fatalf("guidanceResult(gigacode): %v", err)
	}
	writeGigaCodeJSONL(t, filepath.Join(gcRoot, "projects", "-workspace-doc", "chats", "gc-doc-1.jsonl"), []string{
		`{"type":"user","sessionId":"gc-doc-1","timestamp":"2026-11-24T10:00:00Z","message":{"role":"user","parts":[{"text":"doctor check"}]}}`,
	})
	idxDir := os.Getenv("DEJA_DIR")
	if err := index.Ensure(idxDir, "", true, nil); err != nil {
		t.Fatalf("index.Ensure: %v", err)
	}

	var jsonBuf bytes.Buffer
	if err := runDoctor(&jsonBuf, []string{"--json", "--offline"}, nil, idxDir); err != nil {
		t.Fatalf("runDoctor --json: %v", err)
	}
	var rep doctorReport
	if err := json.Unmarshal(jsonBuf.Bytes(), &rep); err != nil {
		t.Fatalf("unmarshal doctor --json: %v", err)
	}
	foundStore := false
	for _, s := range rep.Stores {
		if s.Name == "gigacode" {
			foundStore = true
			if s.Files != 1 {
				t.Fatalf("doctor store gigacode Files = %d, want 1", s.Files)
			}
		}
	}
	foundMCP := false
	for _, m := range rep.MCP {
		if m.Name == "gigacode" && m.State == "wired" {
			foundMCP = true
		}
	}
	foundAuto := false
	for _, a := range rep.AutoRecall {
		if a.Name == "gigacode" && a.State == "wired" {
			foundAuto = true
		}
	}
	foundCmd := false
	for _, c := range rep.Commands {
		if c.Name == "gigacode" && c.State == "skill" {
			foundCmd = true
		}
	}
	if !foundStore || !foundMCP || !foundAuto || !foundCmd {
		t.Fatalf("doctor --json incomplete for gigacode (store=%v mcp=%v auto=%v cmd=%v)", foundStore, foundMCP, foundAuto, foundCmd)
	}

	var textBuf bytes.Buffer
	if err := runDoctor(&textBuf, []string{"--offline"}, nil, idxDir); err != nil {
		t.Fatalf("runDoctor text: %v", err)
	}
	if !strings.Contains(textBuf.String(), "gigacode") {
		t.Fatalf("runDoctor text missing gigacode:\n%s", textBuf.String())
	}

	// Check mcp.excluded glob, empty mcp.allowed, and disableAllHooks + .gigacode/settings.json override.
	settingsPath := filepath.Join(gcHome, "settings.json")
	writeSettings := func(v map[string]any) {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(settingsPath, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeSettings(map[string]any{"mcp": map[string]any{"excluded": []string{"de*"}}})
	if note := clientMCPDenied("gigacode"); note == "" {
		t.Fatalf("clientMCPDenied(gigacode) empty when mcp.excluded matches deja")
	}
	writeSettings(map[string]any{"mcp": map[string]any{"allowed": []string{}}})
	if note := clientMCPDenied("gigacode"); note == "" {
		t.Fatalf("clientMCPDenied(gigacode) empty when mcp.allowed is empty")
	}
	writeSettings(map[string]any{"mcp": map[string]any{"allowed": []string{"deja"}}})
	if note := clientMCPDenied("gigacode"); note != "" {
		t.Fatalf("clientMCPDenied(gigacode) = %q, want empty when allowed", note)
	}

	projDir := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(filepath.Join(projDir, ".gigacode"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(projDir)
	writeSettings(map[string]any{"disableAllHooks": true})
	if note := clientHooksOff("gigacode"); note == "" {
		t.Fatalf("clientHooksOff(gigacode) empty when disableAllHooks=true")
	}
	if err := os.WriteFile(filepath.Join(projDir, ".gigacode", "settings.json"), []byte(`{"disableAllHooks":false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if note := clientHooksOff("gigacode"); note != "" {
		t.Fatalf("clientHooksOff(gigacode) = %q, want cleared by .gigacode/settings.json", note)
	}
}

func TestGigaCodeStatusMCPHooksAndAsker(t *testing.T) {
	tmp := isolateGigaCodeCLIEnv(t)

	// GIGACODE_HOME alone is a config pointer, NOT an active session signal.
	t.Setenv("GIGACODE_HOME", filepath.Join(tmp, "gc-home"))
	if got := statusDetectActiveHarness(); got != "" {
		t.Fatalf("statusDetectActiveHarness() with GIGACODE_HOME only = %q, want empty", got)
	}

	t.Setenv("QWEN_CODE", "1")
	if got := statusDetectActiveHarness(); got != "qwen" {
		t.Fatalf("statusDetectActiveHarness() with QWEN_CODE=1 = %q, want qwen", got)
	}
	t.Setenv("GIGACODE_PACKAGED_LAUNCH", "1")
	if got := statusDetectActiveHarness(); got != "gigacode" {
		t.Fatalf("statusDetectActiveHarness() with GIGACODE_PACKAGED_LAUNCH=1 = %q, want gigacode", got)
	}
	t.Setenv("GIGACODE_PACKAGED_LAUNCH", "")
	t.Setenv("GIGACODE_CLI", "1")
	if got := statusDetectActiveHarness(); got != "gigacode" {
		t.Fatalf("statusDetectActiveHarness() with GIGACODE_CLI=1 = %q, want gigacode", got)
	}
	if got := statusNormalizeHarnessInput("gigacode-cli"); got != "gigacode" {
		t.Fatalf("statusNormalizeHarnessInput(gigacode-cli) = %q, want gigacode", got)
	}

	// mcpPostToolHookReady reads $GIGACODE_HOME/settings.json and ignores $QWEN_HOME.
	qwenHome := filepath.Join(tmp, "qwen-home")
	t.Setenv("QWEN_HOME", qwenHome)
	if _, err := installTarget("qwen-auto", "/usr/local/bin/deja", false); err != nil {
		t.Fatalf("install qwen-auto: %v", err)
	}
	if mcpPostToolHookReady("gigacode") {
		t.Fatalf("mcpPostToolHookReady(gigacode) leaked Qwen hooks")
	}
	if _, err := installTarget("gigacode-auto", "/usr/local/bin/deja", false); err != nil {
		t.Fatalf("install gigacode-auto: %v", err)
	}
	if !mcpPostToolHookReady("gigacode") || !mcpPostToolHookReady("gigacode-cli") {
		t.Fatalf("mcpPostToolHookReady(gigacode) = false after installTarget(gigacode-auto)")
	}
	gcSettings := filepath.Join(sources.GigaCodeConfigDir(), "settings.json")
	if err := os.WriteFile(gcSettings, []byte(`{"disableAllHooks":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if mcpPostToolHookReady("gigacode") {
		t.Fatalf("mcpPostToolHookReady(gigacode) ignored disableAllHooks=true")
	}

	foundAsker := false
	for _, a := range mcpClientPrefixHarnesses {
		if a.prefix == "gigacode" && len(a.harnesses) == 1 && a.harnesses[0] == "gigacode" {
			foundAsker = true
			break
		}
	}
	if !foundAsker {
		t.Fatalf("mcpClientPrefixHarnesses missing gigacode: %+v", mcpClientPrefixHarnesses)
	}
}

func encodeTestProjectDir(p string) string {
	var b strings.Builder
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9':
			b.WriteRune(r)
		case r > 0xFFFF:
			b.WriteString("--")
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}
