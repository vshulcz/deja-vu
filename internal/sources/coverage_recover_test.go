package sources

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// hermeticEnv sets up an isolated HOME/USERPROFILE plus the DEJA_* overrides
// this package reads, mirroring hermeticSourcesEnv in coverage_extra_test.go
// but returning the temp root for callers that need to build fixtures inside it.
func hermeticEnv(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_AIDER_ROOTS", "")
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "cursor-user"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "antigravity"))
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")
	t.Setenv("DEJA_GROK_ROOT", filepath.Join(tmp, "grok"))
	return tmp
}

// --- codex.go: CodexFiles (0%) ---

func TestCodexFilesListsRolloutsAndHistory(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "codex")
	rollDir := filepath.Join(root, "sessions", "2026", "01", "02")
	if err := os.MkdirAll(rollDir, 0o755); err != nil {
		t.Fatal(err)
	}
	roll := filepath.Join(rollDir, "rollout-2026-01-02T03-04-05-r.jsonl")
	if err := os.WriteFile(roll, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(rollDir, "notes.jsonl")
	if err := os.WriteFile(other, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CODEX_ROOT", root)

	// Without history.jsonl present, only the rollout should be listed.
	files := CodexFiles()
	if len(files) != 1 || files[0] != roll {
		t.Fatalf("CodexFiles without history = %v, want [%s]", files, roll)
	}

	hist := filepath.Join(root, "history.jsonl")
	if err := os.WriteFile(hist, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files = CodexFiles()
	if len(files) != 2 {
		t.Fatalf("CodexFiles with history = %v, want 2 entries", files)
	}
	foundRoll, foundHist := false, false
	for _, f := range files {
		if f == roll {
			foundRoll = true
		}
		if f == hist {
			foundHist = true
		}
	}
	if !foundRoll || !foundHist {
		t.Fatalf("CodexFiles missing entries = %v", files)
	}
}

// --- grok.go: LoadGrok and assorted branches (0%/86%/85%/90%/86%/60%/80%) ---

func grokFixtureTree(t *testing.T) (root, updates string) {
	t.Helper()
	tmp := hermeticEnv(t)
	root = filepath.Join(tmp, "grok")
	t.Setenv("DEJA_GROK_ROOT", root)
	group := url.PathEscape("/work/needle-project")
	dir := filepath.Join(root, "sessions", group, "load-grok-session")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, filepath.Join(dir, "updates.jsonl")
}

func TestLoadGrokDiscoversAndParses(t *testing.T) {
	_, updates := grokFixtureTree(t)
	lines := `{"timestamp":1,"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"hi"}}}}` + "\n"
	if err := os.WriteFile(updates, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss := LoadGrok()
	if len(ss) != 1 || len(ss[0].Messages) != 1 {
		t.Fatalf("LoadGrok = %#v", ss)
	}
}

func TestParseGrokFileFallbacksAndDefaultKind(t *testing.T) {
	_, updates := grokFixtureTree(t)
	// No summary.json at all: id falls back to the session dir name, cwd
	// falls back to grokCWDFromPath (encoded group segment), title falls
	// back from empty generated_title to session_summary.
	lines := `{"timestamp":1,"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"hi"},"_meta":{"promptIndex":0}}}}` + "\n" +
		// contains the literal user_message_chunk marker in an unrelated field
		// so the byte-level prefilter matches, but the real sessionUpdate kind
		// does not - exercising the callback's default: return branch.
		`{"timestamp":2,"note":"user_message_chunk","params":{"update":{"sessionUpdate":"tool_call","content":{"type":"text","text":"tool noise"}}}}` + "\n" +
		// matching kind but empty rendered text - exercises the text=="" return.
		`{"timestamp":3,"params":{"update":{"sessionUpdate":"agent_message_chunk","content":{}}}}` + "\n" +
		// assistant chunk without a promptId - grokMessageKey returns "".
		`{"timestamp":4,"params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"answer"}}}}` + "\n"
	if err := os.WriteFile(updates, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGrokFile(updates)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("ParseGrokFile = %#v", ss)
	}
	s := ss[0]
	if s.ID != "load-grok-session" {
		t.Fatalf("fallback id = %q", s.ID)
	}
	if s.Project != "needle-project" {
		t.Fatalf("fallback cwd/project = %q", s.Project)
	}
	if len(s.Messages) != 2 {
		t.Fatalf("messages = %#v, want 2 (tool_call and empty-text filtered)", s.Messages)
	}
}

func TestParseGrokFileTitleFallsBackToSummary(t *testing.T) {
	_, updates := grokFixtureTree(t)
	summary := `{"info":{"id":"x","cwd":"/work/needle-project"},"session_summary":"fallback summary"}`
	if err := os.WriteFile(filepath.Join(filepath.Dir(updates), "summary.json"), []byte(summary), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(updates, []byte(`{"timestamp":1,"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"hi"}}}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGrokFile(updates)
	if err != nil || len(ss) != 1 {
		t.Fatalf("ParseGrokFile = %#v err=%v", ss, err)
	}
	if ss[0].Title != "fallback summary" {
		t.Fatalf("title = %q, want fallback summary", ss[0].Title)
	}
}

func TestGrokContentTextTypeMismatchReturnsEmpty(t *testing.T) {
	// block.Text != "" but block.Type is neither "" nor "text": grokContentText
	// must reject it rather than returning the text of an unknown block kind.
	got := grokContentText([]byte(`{"type":"image","text":"ignored"}`))
	if got != "" {
		t.Fatalf("grokContentText type mismatch = %q, want empty", got)
	}
}

func TestScanGrokUpdatesOpenError(t *testing.T) {
	if err := scanGrokUpdates(filepath.Join(t.TempDir(), "missing.jsonl"), func(grokUpdateEvent) {}); err == nil {
		t.Fatal("scanGrokUpdates on missing file returned nil error")
	}
}

func TestScanGrokUpdatesReadError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("reading a directory as a file is unix-only")
	}
	dir := t.TempDir()
	if err := scanGrokUpdates(dir, func(grokUpdateEvent) {}); err == nil {
		t.Fatal("scanGrokUpdates on a directory returned nil error")
	}
}

func TestGrokCWDForSessionEmptyPath(t *testing.T) {
	if GrokCWDForSession("") != "" {
		t.Fatal("GrokCWDForSession(\"\") should be empty")
	}
}

func TestGrokCWDForSessionPrefersSummary(t *testing.T) {
	_, updates := grokFixtureTree(t)
	summary := `{"info":{"cwd":"/work/from-summary"}}`
	if err := os.WriteFile(filepath.Join(filepath.Dir(updates), "summary.json"), []byte(summary), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := GrokCWDForSession(updates); got != "/work/from-summary" {
		t.Fatalf("GrokCWDForSession = %q, want /work/from-summary", got)
	}
}

func TestGrokCWDFromPathEdgeCases(t *testing.T) {
	if grokCWDFromPath("") != "" {
		t.Fatal("grokCWDFromPath(\"\") should be empty")
	}
	// A group segment that is not valid percent-encoding must not panic and
	// must resolve to "".
	bad := filepath.Join(t.TempDir(), "sessions", "%zz-bad-escape", "s", "updates.jsonl")
	if err := os.MkdirAll(filepath.Dir(bad), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := grokCWDFromPath(bad); got != "" {
		t.Fatalf("grokCWDFromPath bad escape = %q, want empty", got)
	}
}

// --- claude.go: decodeProjectBase (87%), ClaudeProjectDirBase (80%) ---

func TestDecodeProjectBaseSingleResolvedSegment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on unix-style single-segment absolute path resolution")
	}
	if _, err := os.Stat(string(filepath.Separator) + "tmp"); err != nil {
		t.Skip("/tmp not present on this system")
	}
	if got := decodeProjectBase("-tmp"); got != "tmp" {
		t.Fatalf("decodeProjectBase(-tmp) = %q, want tmp", got)
	}
}

func TestClaudeProjectDirBaseReturnsBase(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	path := filepath.Join(root, "-tmp-demo-project", "session.jsonl")
	if got := ClaudeProjectDirBase(path); got != "-tmp-demo-project" {
		t.Fatalf("ClaudeProjectDirBase = %q, want -tmp-demo-project", got)
	}
}

// --- antigravity.go: AntigravityRoots (90%) ---

func TestAntigravityRootsBadGlobPattern(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", "")
	// An unmatched '[' in HOME makes the antigravity glob pattern invalid,
	// so filepath.Glob returns ErrBadPattern and AntigravityRoots must
	// swallow it as nil rather than propagating.
	badHome := filepath.Join(tmp, "wei[rd-home")
	t.Setenv("HOME", badHome)
	t.Setenv("USERPROFILE", badHome)
	if got := AntigravityRoots(); got != nil {
		t.Fatalf("AntigravityRoots with bad glob = %v, want nil", got)
	}
}

// --- gemini.go: GeminiChatFiles (92%), geminiProjectFromRegistry (90%) ---

func TestGeminiChatFilesSkipsNonDirEntries(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "gemini")
	t.Setenv("DEJA_GEMINI_ROOT", root)
	tmpDir := filepath.Join(root, "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A plain file sitting next to the per-session directories must be
	// skipped instead of treated as a project id.
	if err := os.WriteFile(filepath.Join(tmpDir, "not-a-dir.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	chats := filepath.Join(tmpDir, "pid", "chats")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionFile := filepath.Join(chats, "s.json")
	if err := os.WriteFile(sessionFile, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := GeminiChatFiles()
	if len(got) != 1 || got[0] != sessionFile {
		t.Fatalf("GeminiChatFiles = %v, want [%s]", got, sessionFile)
	}
}

func TestGeminiProjectFromRegistryNoMatch(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "gemini")
	t.Setenv("DEJA_GEMINI_ROOT", root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	registry := `{"projects":{"/work/other":"other-id"}}`
	if err := os.WriteFile(filepath.Join(root, "projects.json"), []byte(registry), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := geminiProjectFromRegistry("does-not-exist"); got != "" {
		t.Fatalf("geminiProjectFromRegistry no match = %q, want empty", got)
	}
}

// --- cursor.go: numberVal (80%), CursorDBs/CursorTranscripts (91%/92%),
// ParseCursorTranscript (94%), cursorQuery (91%) ---

func TestNumberValFloat64Case(t *testing.T) {
	n, ok := numberVal(float64(42))
	if !ok || n != 42 {
		t.Fatalf("numberVal(float64(42)) = %d,%v", n, ok)
	}
	if n, ok := numberVal("not a number"); ok || n != 0 {
		t.Fatalf("numberVal(unsupported) = %d,%v, want 0,false", n, ok)
	}
}

func TestCursorDBsSkipsNonDirWorkspaceEntries(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "cursor-user")
	t.Setenv("DEJA_CURSOR_ROOT", root)
	ws := filepath.Join(root, "workspaceStorage")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	// A plain file among the workspace directories must be skipped.
	if err := os.WriteFile(filepath.Join(ws, "not-a-dir"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(ws, "w1", "state.vscdb")
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := CursorDBs()
	if len(got) != 1 || got[0] != real {
		t.Fatalf("CursorDBs = %v, want [%s]", got, real)
	}
}

func TestCursorTranscriptsSkipsNonJSONLFiles(t *testing.T) {
	tmp := hermeticEnv(t)
	cliRoot := filepath.Join(tmp, "cursor-cli")
	t.Setenv("DEJA_CURSOR_CLI_ROOT", cliRoot)
	dir := filepath.Join(cliRoot, "projects", "p", "agent-transcripts", "s")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := CursorTranscripts()
	if len(got) != 1 || got[0] != transcript {
		t.Fatalf("CursorTranscripts = %v, want [%s]", got, transcript)
	}
}

func TestParseCursorTranscriptSkipsNonMapMessage(t *testing.T) {
	tmp := hermeticEnv(t)
	path := filepath.Join(tmp, "s.jsonl")
	lines := `{"role":"user","message":"not an object"}` + "\n" +
		`{"role":"user","message":{"content":"hi"}}` + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCursorTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 1 || ss[0].Messages[0].Text != "hi" {
		t.Fatalf("ParseCursorTranscript = %#v", ss)
	}
}

func TestParseCursorDBNoComposers(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "state.vscdb")
	// A DB file that exists and is non-empty but has no composerData rows:
	// cursorQuery succeeds with zero rows, exercising the len(composers)==0
	// early return.
	schema := `create table cursorDiskKV (key text primary key, value text);`
	cmd := exec.Command("sqlite3", db, schema)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
	ss, err := ParseCursorDB(db)
	if err != nil || ss != nil {
		t.Fatalf("ParseCursorDB empty composers = %#v err=%v", ss, err)
	}
}

func TestCursorQueryBadJSONOutput(t *testing.T) {
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(bin, "sqlite3")
	// Emits syntactically-invalid JSON so the decoder fails after the
	// initial (non-error) invocation, exercising cursorQuery's decode error.
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '[not valid json'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := cursorQuery(filepath.Join(tmp, "x.db"), "select 1"); err == nil {
		t.Fatal("cursorQuery with malformed JSON returned nil error")
	}
}
