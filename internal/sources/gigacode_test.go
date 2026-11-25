package sources

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func isolateGigaCodeTestEnv(t *testing.T) string {
	t.Helper()
	tmp := hermeticSourcesEnv(t)
	t.Setenv("GIGACODE_HOME", "")
	t.Setenv("DEJA_GIGACODE_ROOT", "")
	t.Setenv("QWEN_HOME", "")
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))
	t.Setenv("DEJA_EXCLUDED_HARNESSES", "")
	t.Setenv("DEJA_STORES", "")
	return tmp
}

func TestGigaCodeConfigDirAndRootIsolation(t *testing.T) {
	tmp := isolateGigaCodeTestEnv(t)
	wantDefault := filepath.Join(tmp, "home", ".gigacode")
	if got := GigaCodeConfigDir(); got != wantDefault {
		t.Fatalf("default GigaCodeConfigDir() = %q, want %q", got, wantDefault)
	}
	if got := GigaCodeRoot(); got != wantDefault {
		t.Fatalf("default GigaCodeRoot() = %q, want %q", got, wantDefault)
	}

	// QWEN_HOME must never leak into GigaCode paths.
	t.Setenv("QWEN_HOME", filepath.Join(tmp, "other-qwen"))
	if got := GigaCodeConfigDir(); got != wantDefault {
		t.Fatalf("QWEN_HOME leaked into GigaCodeConfigDir() = %q, want %q", got, wantDefault)
	}
	if got := GigaCodeRoot(); got != wantDefault {
		t.Fatalf("QWEN_HOME leaked into GigaCodeRoot() = %q, want %q", got, wantDefault)
	}

	gcHome := filepath.Join(tmp, "custom-gigacode-home")
	t.Setenv("GIGACODE_HOME", gcHome)
	if got := GigaCodeConfigDir(); got != gcHome {
		t.Fatalf("GigaCodeConfigDir() with GIGACODE_HOME = %q, want %q", got, gcHome)
	}
	if got := GigaCodeRoot(); got != gcHome {
		t.Fatalf("GigaCodeRoot() with GIGACODE_HOME = %q, want %q", got, gcHome)
	}

	gcRoot := filepath.Join(tmp, "custom-gigacode-root")
	t.Setenv("DEJA_GIGACODE_ROOT", gcRoot)
	if got := GigaCodeConfigDir(); got != gcHome {
		t.Fatalf("DEJA_GIGACODE_ROOT changed GigaCodeConfigDir() = %q, want %q", got, gcHome)
	}
	if got := GigaCodeRoot(); got != gcRoot {
		t.Fatalf("GigaCodeRoot() with DEJA_GIGACODE_ROOT = %q, want %q", got, gcRoot)
	}
}

func writeGigaCodeFixtureFile(t *testing.T, p string, lines []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGigaCodeSessionFilesSidecarsAndSubagentBoundary(t *testing.T) {
	tmp := isolateGigaCodeTestEnv(t)
	root := filepath.Join(tmp, "gc-root")
	t.Setenv("DEJA_GIGACODE_ROOT", root)

	chatFile := filepath.Join(root, "projects", "-workspace-demo", "chats", "gc-main.jsonl")
	subFile := filepath.Join(root, "projects", "-workspace-demo", "subagents", "gc-main", "agent-1.jsonl")
	sidecarMeta := filepath.Join(root, "projects", "-workspace-demo", "meta.json")
	sidecarCursor := filepath.Join(root, "projects", "-workspace-demo", "extract-cursor.json")
	sidecarRuntime := filepath.Join(root, "projects", "-workspace-demo", "chats", "gc-main.runtime.json")
	sidecarOrg := filepath.Join(root, "projects", "-workspace-demo", "session-organization.v1.json")
	sidecarWorkflow := filepath.Join(root, "projects", "-workspace-demo", "workflows", "plan.json")
	sidecarSubMeta := filepath.Join(root, "projects", "-workspace-demo", "subagents", "gc-main", "agent-1.meta.json")
	siblingChat := filepath.Join(root, "projects2", "-workspace-demo", "chats", "not-gigacode.jsonl")
	siblingSub := filepath.Join(root, "projects2", "-workspace-demo", "subagents", "gc-main", "agent-sibling.jsonl")

	for _, p := range []string{
		chatFile, subFile, sidecarMeta, sidecarCursor, sidecarRuntime,
		sidecarOrg, sidecarWorkflow, sidecarSubMeta, siblingChat, siblingSub,
	} {
		writeGigaCodeFixtureFile(t, p, []string{`{"ok":true}`})
	}

	if !GigaCodeSubagentFile(subFile) {
		t.Fatalf("GigaCodeSubagentFile(%q) = false, want true", subFile)
	}
	if GigaCodeSubagentFile(chatFile) {
		t.Fatalf("GigaCodeSubagentFile(%q) = true for chat file", chatFile)
	}
	if GigaCodeSubagentFile(siblingSub) {
		t.Fatalf("GigaCodeSubagentFile(%q) = true outside projects/ boundary", siblingSub)
	}

	files := GigaCodeSessionFiles()
	if !slices.Contains(files, chatFile) || !slices.Contains(files, subFile) {
		t.Fatalf("GigaCodeSessionFiles() missing chat/subagent: %v", files)
	}
	for _, bad := range []string{siblingChat, siblingSub, sidecarMeta, sidecarRuntime, sidecarSubMeta} {
		if slices.Contains(files, bad) {
			t.Fatalf("GigaCodeSessionFiles() unexpectedly included %q", bad)
		}
	}

	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "0")
	filesNoSub := GigaCodeSessionFiles()
	if !slices.Contains(filesNoSub, chatFile) || slices.Contains(filesNoSub, subFile) {
		t.Fatalf("DEJA_INCLUDE_SUBAGENTS=0 files = %v, want only %q", filesNoSub, chatFile)
	}
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")

	sidecars := GigaCodeSidecarFiles()
	for _, want := range []string{sidecarMeta, sidecarCursor, sidecarRuntime, sidecarOrg, sidecarWorkflow, sidecarSubMeta} {
		if !slices.Contains(sidecars, want) {
			t.Fatalf("GigaCodeSidecarFiles() missing %q in %v", want, sidecars)
		}
	}
	for _, bad := range []string{chatFile, subFile, siblingChat, siblingSub} {
		if slices.Contains(sidecars, bad) {
			t.Fatalf("GigaCodeSidecarFiles() unexpectedly included %q", bad)
		}
	}
}

func TestGigaCodeParseOffsetLoadAndRegistry(t *testing.T) {
	tmp := isolateGigaCodeTestEnv(t)
	root := filepath.Join(tmp, "gc-root")
	t.Setenv("DEJA_GIGACODE_ROOT", root)

	cyrillicDir := filepath.Join(tmp, "проекты", "демо-app")
	if err := os.MkdirAll(cyrillicDir, 0o755); err != nil {
		t.Fatal(err)
	}
	enc := claudeEncodePath(cyrillicDir)
	chatPath := filepath.Join(root, "projects", enc, "chats", "gc-session-1.jsonl")
	subPath := filepath.Join(root, "projects", enc, "subagents", "gc-session-1", "agent-fork.jsonl")
	emptyPath := filepath.Join(root, "projects", enc, "chats", "gc-empty.jsonl")
	siblingPath := filepath.Join(root, "projects2", enc, "chats", "sibling.jsonl")

	cwdJSON, err := json.Marshal(cyrillicDir)
	if err != nil {
		t.Fatal(err)
	}
	chatLines := []string{
		`{"type":"user","sessionId":"gc-session-1","cwd":` + string(cwdJSON) + `,"forkedFrom":{"sessionId":"gc-parent-0"},"timestamp":"2026-11-24T10:00:00Z","message":{"role":"user","parts":[{"text":"fix tests in demo-app"}]}}`,
		`{"type":"assistant","sessionId":"gc-session-1","cwd":` + string(cwdJSON) + `,"model":"GigaChat-Max","usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5},"timestamp":"2026-11-24T10:00:01Z","message":{"role":"model","parts":[{"functionCall":{"id":"call-1","name":"edit","args":{"file_path":"main.go","old_string":"a","new_string":"b"}}}]}}`,
		`{"type":"tool_result","sessionId":"gc-session-1","timestamp":"2026-11-24T10:00:02Z","message":{"role":"user","parts":[{"functionResponse":{"id":"call-1","name":"edit","response":{"output":"edited"}}}]}}`,
		`{"type":"assistant","sessionId":"gc-session-1","cwd":` + string(cwdJSON) + `,"timestamp":"2026-11-24T10:00:03Z","message":{"role":"model","parts":[{"functionCall":{"id":"call-2","name":"run_shell_command","args":{"command":"go test ./..."}}}]}}`,
		`{"type":"tool_result","sessionId":"gc-session-1","timestamp":"2026-11-24T10:00:04Z","message":{"role":"user","parts":[{"functionResponse":{"id":"call-2","name":"run_shell_command","response":{"output":"Command: go test ./...\nDirectory: (root)\nOutput: FAIL\nError: (none)\nExit Code: 1\nSignal: (none)\nProcess Group PGID: 4242"}}}]}}`,
		`{"type":"system","subtype":"chat_compression","timestamp":"2026-11-24T10:00:05Z","systemPayload":{"compressedHistory":[{"role":"user","parts":[{"text":"summary of demo-app fixes\n` + qwenResumeTrailer + `"}]},{"role":"model","parts":[{"text":"` + qwenSummaryAck + `"}]}]}}`,
	}
	writeGigaCodeFixtureFile(t, chatPath, chatLines)
	writeGigaCodeFixtureFile(t, subPath, []string{
		`{"type":"user","sessionId":"gc-session-1","agentName":"reviewer","cwd":` + string(cwdJSON) + `,"timestamp":"2026-11-24T10:01:00Z","message":{"role":"user","parts":[{"text":"review patch"}]}}`,
		`{"type":"assistant","sessionId":"gc-session-1","agentName":"reviewer","timestamp":"2026-11-24T10:01:01Z","message":{"role":"model","parts":[{"text":"looks solid"}]}}`,
	})
	writeGigaCodeFixtureFile(t, emptyPath, []string{`{"type":"system","subtype":"other"}`})
	writeGigaCodeFixtureFile(t, siblingPath, []string{
		`{"type":"user","sessionId":"sib-1","timestamp":"2026-11-24T10:00:00Z","message":{"role":"user","parts":[{"text":"not gigacode"}]}}`,
	})

	ss, err := ParseGigaCodeFile(chatPath)
	if err != nil || len(ss) != 1 {
		t.Fatalf("ParseGigaCodeFile() = (%+v, %v), want 1 session", ss, err)
	}
	s := ss[0]
	if s.Harness != "gigacode" || s.ID != "gc-session-1" || s.Kind != "fork" || s.Parent != "gc-parent-0" {
		t.Fatalf("unexpected session metadata: %+v", s)
	}
	if s.Project != cwdProjectName(cyrillicDir) {
		t.Fatalf("Project = %q, want %q", s.Project, cwdProjectName(cyrillicDir))
	}
	if s.ToolErrors != 1 {
		t.Fatalf("ToolErrors = %d, want 1", s.ToolErrors)
	}
	hasSummary, hasEdit, hasFailedCmd := false, false, false
	for _, m := range s.Messages {
		if m.Role == RoleSummary && strings.Contains(m.Text, "summary of demo-app fixes") {
			hasSummary = true
		}
		if m.Role == RoleEdit && strings.Contains(m.Text, "main.go") {
			hasEdit = true
		}
		if m.Role == RoleCommand && strings.Contains(m.Text, "$ go test ./...") && strings.Contains(m.Text, "exit 1") {
			hasFailedCmd = true
		}
	}
	if !hasSummary || !hasEdit || !hasFailedCmd {
		t.Fatalf("missing parsed roles (summary=%v edit=%v cmd=%v) in %+v", hasSummary, hasEdit, hasFailedCmd, s.Messages)
	}

	// Verify incremental offset parsing picks up newly appended turns.
	info, err := os.Stat(chatPath)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(chatPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	appended := `{"type":"user","sessionId":"gc-session-1","cwd":` + string(cwdJSON) + `,"timestamp":"2026-11-24T10:02:00Z","message":{"role":"user","parts":[{"text":"follow-up turn"}]}}` + "\n"
	if _, err = f.WriteString(appended); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	_ = f.Close()

	tailSessions, err := ParseGigaCodeFileFromOffset(chatPath, info.Size())
	if err != nil || len(tailSessions) != 1 || len(tailSessions[0].Messages) != 1 {
		t.Fatalf("ParseGigaCodeFileFromOffset() = (%+v, %v), want 1 follow-up message", tailSessions, err)
	}
	if tailSessions[0].Harness != "gigacode" || tailSessions[0].Messages[0].Text != "follow-up turn" {
		t.Fatalf("unexpected offset parse result: %+v", tailSessions[0])
	}

	loaded := LoadGigaCode()
	if len(loaded) != 2 {
		t.Fatalf("LoadGigaCode() count = %d, want 2 (main + subagent); got %+v", len(loaded), loaded)
	}
	var foundSub bool
	for _, ls := range loaded {
		if ls.Harness != "gigacode" {
			t.Fatalf("loaded non-gigacode harness %q", ls.Harness)
		}
		if ls.Kind == "subagent" && ls.Agent == "reviewer" {
			foundSub = true
		}
	}
	if !foundSub {
		t.Fatalf("LoadGigaCode() missing subagent session: %+v", loaded)
	}

	// Verify Cyrillic recorded CWD resolution and unrecorded fallback.
	if dir, recorded := GigaCodeSessionDir(chatPath); !recorded || dir != cyrillicDir {
		t.Fatalf("GigaCodeSessionDir(recorded) = (%q, %v), want (%q, true)", dir, recorded, cyrillicDir)
	}
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
	fallbackPath := filepath.Join(root, "projects", claudeEncodePath(fallbackDir), "chats", "gc-fallback.jsonl")
	writeGigaCodeFixtureFile(t, fallbackPath, []string{
		`{"type":"user","sessionId":"gc-fallback","timestamp":"2026-11-24T10:00:00Z","message":{"role":"user","parts":[{"text":"hi"}]}}`,
	})
	if dir, recorded := GigaCodeSessionDir(fallbackPath); recorded || dir != fallbackDir {
		t.Fatalf("GigaCodeSessionDir(fallback) = (%q, %v), want (%q, false)", dir, recorded, fallbackDir)
	}

	// Verify Registry entry, Match boundary, and DEJA_STORES filtering.
	var regHarness *Harness
	for _, h := range Registry() {
		if h.Name == "gigacode" {
			hCopy := h
			regHarness = &hCopy
			break
		}
	}
	if regHarness == nil || len(regHarness.Kinds) != 1 {
		t.Fatalf("Registry() missing gigacode harness: %+v", regHarness)
	}
	if !regHarness.Kinds[0].Match(chatPath) || !regHarness.Kinds[0].Match(subPath) || regHarness.Kinds[0].Match(siblingPath) {
		t.Fatalf("Registry Kind.Match boundary failed for chat=%v sub=%v sibling=%v",
			regHarness.Kinds[0].Match(chatPath), regHarness.Kinds[0].Match(subPath), regHarness.Kinds[0].Match(siblingPath))
	}
	if h := CompactionTranscriptHarness(chatPath); h != "gigacode" {
		t.Fatalf("CompactionTranscriptHarness(%q) = %q, want gigacode", chatPath, h)
	}
}

func TestGigaCodeWriteBackAndRefusals(t *testing.T) {
	tmp := isolateGigaCodeTestEnv(t)
	root := filepath.Join(tmp, "gc-root")
	t.Setenv("DEJA_GIGACODE_ROOT", root)

	if !CanWriteBack("gigacode") {
		t.Fatal("CanWriteBack(\"gigacode\") = false, want true")
	}

	proj := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "projects", claudeEncodePath(proj), "chats", "gc-wb-1.jsonl")
	orig := model.Session{
		Harness:  "gigacode",
		ID:       "gc-wb-1",
		Project:  "proj",
		Path:     target,
		Messages: writeBackTestTurns(),
	}
	wf := writeBackToDisk(t, orig)
	if wf.Path != target {
		t.Fatalf("WriteBackFile.Path = %q, want %q", wf.Path, target)
	}
	reparsed, err := ParseGigaCodeFile(wf.Path)
	if err != nil || len(reparsed) != 1 {
		t.Fatalf("re-parse written file = (%+v, %v)", reparsed, err)
	}
	if reparsed[0].Harness != "gigacode" {
		t.Fatalf("reparsed Harness = %q, want gigacode", reparsed[0].Harness)
	}
	checkWriteBackTurns(t, reparsed[0].Messages)

	// Subagent sessions cannot be reopened standalone.
	subSession := orig
	subSession.Kind = "subagent"
	if _, err = RenderWriteBack(subSession); err == nil {
		t.Fatal("expected WriteBackRefusal for subagent session")
	} else if !errors.As(err, new(*WriteBackRefusal)) {
		t.Fatalf("expected *WriteBackRefusal, got %T: %v", err, err)
	}

	// Non-chats paths cannot be reopened by `gigacode -r`.
	badPath := orig
	badPath.Path = filepath.Join(root, "projects", "p", "other", "gc-wb-1.jsonl")
	if _, err = RenderWriteBack(badPath); err == nil || !strings.Contains(err.Error(), "gigacode -r") {
		t.Fatalf("expected WriteBackRefusal naming `gigacode -r`, got %v", err)
	}
}

func TestGigaCodeCompactionHeadAndLiveTailIsolation(t *testing.T) {
	tmp := isolateLiveTailEnv(t)
	t.Setenv("GIGACODE_HOME", "")
	root := filepath.Join(tmp, "gigacode")
	t.Setenv("DEJA_GIGACODE_ROOT", root)

	work := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	cwdJSON, err := json.Marshal(work)
	if err != nil {
		t.Fatal(err)
	}
	sid := "gc-compact-1"
	chatPath := filepath.Join(root, "projects", claudeEncodePath(work), "chats", sid+".jsonl")
	largePreamble := strings.Repeat("a", 500)
	lines := []string{
		`{"type":"user","sessionId":"` + sid + `","cwd":` + string(cwdJSON) + `,"timestamp":"2026-11-24T12:00:00Z","message":{"role":"user","parts":[{"text":"first prompt ` + largePreamble + `"}]}}`,
		`{"type":"assistant","sessionId":"` + sid + `","cwd":` + string(cwdJSON) + `,"timestamp":"2026-11-24T12:00:01Z","message":{"role":"model","parts":[{"functionCall":{"id":"sh-1","name":"run_shell_command","args":{"command":"go test ./pkg"}}}]}}`,
		`{"type":"tool_result","sessionId":"` + sid + `","timestamp":"2026-11-24T12:00:02Z","message":{"role":"user","parts":[{"functionResponse":{"id":"sh-1","name":"run_shell_command","response":{"output":"Command: go test ./pkg\nOutput: ok pkg\nError: (none)\nExit Code: 0"}}}]}}`,
		`{"type":"assistant","sessionId":"` + sid + `","cwd":` + string(cwdJSON) + `,"timestamp":"2026-11-24T12:00:03Z","message":{"role":"model","parts":[{"text":"done fixing pkg"}]}}`,
		`{"type":"system","subtype":"chat_compression","timestamp":"2026-11-24T12:00:04Z","systemPayload":{"compressedHistory":[{"role":"user","parts":[{"text":"GC-SUMMARY\n` + qwenResumeTrailer + `"}]},{"role":"model","parts":[{"text":"` + qwenSummaryAck + `"}]}]}}`,
	}
	writeGigaCodeFixtureFile(t, chatPath, lines)

	head, err := TranscriptHead(chatPath, sid)
	if err != nil {
		t.Fatalf("TranscriptHead() err = %v", err)
	}
	if head.Harness != "gigacode" || len(head.Messages) == 0 {
		t.Fatalf("TranscriptHead() = %+v, want harness gigacode with messages", head)
	}

	// Head with large non-compaction opening (>1 line / qwenShapedHead fallback) still refines to gigacode.
	headOnlyPath := filepath.Join(root, "projects", claudeEncodePath(work), "chats", "gc-head-only.jsonl")
	writeGigaCodeFixtureFile(t, headOnlyPath, []string{
		`{"type":"user","sessionId":"gc-head-only","cwd":` + string(cwdJSON) + `,"timestamp":"2026-11-24T12:05:00Z","message":{"role":"user","parts":[{"text":"opening prompt"}]}}`,
	})
	head2, err := TranscriptHead(headOnlyPath, "gc-head-only")
	if err != nil || head2.Harness != "gigacode" {
		t.Fatalf("TranscriptHead(single user turn) = (%+v, %v), want gigacode", head2, err)
	}

	tr, err := ReadCompactionSession("", sid, chatPath, "")
	if err != nil {
		t.Fatalf("ReadCompactionSession(auto) err = %v", err)
	}
	if tr.Harness != "gigacode" || tr.Workspace != work {
		t.Fatalf("ReadCompactionSession = harness %q workspace %q, want gigacode %q", tr.Harness, tr.Workspace, work)
	}
	trByID, err := ReadCompactionSession("gigacode", sid, "", "")
	if err != nil || trByID.Harness != "gigacode" || trByID.Path != chatPath {
		t.Fatalf("ReadCompactionSession(gigacode by id) = (%+v, %v)", trByID, err)
	}

	// Strictly separated from qwen: requesting as qwen must fail.
	if _, err = ReadCompactionSession("qwen", sid, chatPath, ""); err == nil {
		t.Fatal("ReadCompactionSession(\"qwen\", gigacodePath) succeeded; want harness mismatch error")
	}

	msgs, ok := ReadLiveTranscriptTailMessages("gigacode", chatPath, sid)
	if !ok || len(msgs) == 0 {
		t.Fatalf("ReadLiveTranscriptTailMessages(gigacode, path) = (%+v, %v)", msgs, ok)
	}
	msgsByID, ok := ReadLiveTranscriptTailMessages("gigacode", "", sid)
	if !ok || len(msgsByID) == 0 {
		t.Fatalf("ReadLiveTranscriptTailMessages(gigacode, by id) = (%+v, %v)", msgsByID, ok)
	}
	if _, ok = ReadLiveTranscriptTailMessages("qwen", chatPath, sid); ok {
		t.Fatal("ReadLiveTranscriptTailMessages(\"qwen\", gigacodePath) = true; want false")
	}
}
