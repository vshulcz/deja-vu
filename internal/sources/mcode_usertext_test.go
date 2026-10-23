package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// parseMcodeLines writes the lines as one session transcript and reads it.
func parseMcodeLines(t *testing.T, lines ...string) []string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, mcodeTranscriptName)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseMcodeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, s := range ss {
		for _, m := range s.Messages {
			out = append(out, m.Role+": "+m.Text)
		}
	}
	return out
}

func mcodeUserLine(msg string) string {
	return `{"message_id":"m1","turn_id":"t1","message":{"role":"user",` + msg + `,"timestamp":1792465960900}}`
}

// The runtime writes an empty range at the end of the text plus
// genuineUserQueryText whenever the typed text is not the tail of what it
// assembled (bK in @minimax-ai/code 0.6.12). The range alone cuts to "", and
// the turn went missing from the index (#5593).
func TestMcodeGenuineUserQueryTextIsTheTypedTurn(t *testing.T) {
	got := parseMcodeLines(t, mcodeUserLine(
		`"content":[{"type":"text","text":"<system-reminder>\nagent banner\n</system-reminder>\n\nfix the flaky login test (edited by queue)"}],`+
			`"canonicalTextRange":{"startOffset":93,"endOffset":93},"genuineUserQueryText":"fix the flaky login test"`))
	if len(got) != 1 || got[0] != "user: fix the flaky login test" {
		t.Fatalf("messages = %q, want the typed text alone", got)
	}
}

// genuineUserQueryText wins over a range that would cut something else.
func TestMcodeGenuineUserQueryTextWinsOverTheRange(t *testing.T) {
	got := parseMcodeLines(t, mcodeUserLine(
		`"content":[{"type":"text","text":"abcdef"}],`+
			`"canonicalTextRange":{"startOffset":0,"endOffset":3},"genuineUserQueryText":"typed words"`))
	if len(got) != 1 || got[0] != "user: typed words" {
		t.Fatalf("messages = %q, want genuineUserQueryText", got)
	}
}

// A turn with no range is the assembled text, and the runtime strips its own
// blocks from it before reading it back (AUt). Kept whole, the banner was
// indexed as the person's words.
func TestMcodeATurnWithNoRangeLosesTheRuntimeBlocks(t *testing.T) {
	got := parseMcodeLines(t, mcodeUserLine(
		`"content":[{"type":"text","text":"<system-reminder>\nbanner\n</system-reminder>\n\nsecond question"},`+
			`{"type":"text","text":"<background-task-finished id=\"x\">task done</background-task-finished><SYSTEM-REMINDER/>"}]`))
	if len(got) != 1 || got[0] != "user: second question" {
		t.Fatalf("messages = %q, want the question alone", got)
	}
}

// A scheduled task's prompt is not a person's turn.
func TestMcodeACronPromptIsNotATurn(t *testing.T) {
	for _, head := range []string{"[System CronTask nightly]", "[cron report 3]"} {
		got := parseMcodeLines(t, mcodeUserLine(`"content":[{"type":"text","text":"`+head+` run the checks"}]`))
		if len(got) != 0 {
			t.Fatalf("%s: messages = %q, want none", head, got)
		}
	}
	// The control: the same words without the head are a turn.
	if got := parseMcodeLines(t, mcodeUserLine(`"content":[{"type":"text","text":"run the checks"}]`)); len(got) != 1 {
		t.Fatalf("messages = %q, want the turn", got)
	}
}

// The range counts into every text block joined with a newline (YIe), not
// into the first block: here it runs past the first one.
func TestMcodeTheRangeSpansTheJoinedBlocks(t *testing.T) {
	// "<r/>" + "\n" = 5 units, then "second block typed" (18 units).
	got := parseMcodeLines(t, mcodeUserLine(
		`"content":[{"type":"text","text":"<r/>"},{"type":"image","data":"x"},{"type":"text","text":"second block typed"}],`+
			`"canonicalTextRange":{"startOffset":5,"endOffset":23}`))
	if len(got) != 1 || got[0] != "user: second block typed" {
		t.Fatalf("messages = %q, want the second block", got)
	}
}

// The offsets are UTF-16 units, as a JavaScript string counts them.
func TestMcodeTheRangeCountsUTF16Units(t *testing.T) {
	// "前缀\n" is 3 units, "修复登录" 4 more, "😀" 2.
	got := parseMcodeLines(t, mcodeUserLine(
		`"content":[{"type":"text","text":"前缀\n修复登录😀"}],"canonicalTextRange":{"startOffset":3,"endOffset":9}`))
	if len(got) != 1 || got[0] != "user: 修复登录😀" {
		t.Fatalf("messages = %q", got)
	}
}

// A range that does not fit the text, or that is negative, falls back to the
// text with the runtime's blocks taken out rather than to a panic or the
// banner.
func TestMcodeABrokenRangeFallsBackToTheStrippedText(t *testing.T) {
	for _, r := range []string{
		`{"startOffset":-1,"endOffset":4}`,
		`{"startOffset":2,"endOffset":999}`,
		`{"startOffset":4,"endOffset":2}`,
		`{"startOffset":1.5,"endOffset":4}`,
		`{"startOffset":"0","endOffset":4}`,
	} {
		got := parseMcodeLines(t, mcodeUserLine(
			`"content":[{"type":"text","text":"<system-reminder>x</system-reminder>asked"}],"canonicalTextRange":`+r))
		if len(got) != 1 || got[0] != "user: asked" {
			t.Fatalf("range %s: messages = %q", r, got)
		}
	}
}

// A non-string genuineUserQueryText is a turn the runtime cannot read; it is
// not indexed as the assembled text either.
func TestMcodeANonStringGenuineTextIsNoTurn(t *testing.T) {
	got := parseMcodeLines(t, mcodeUserLine(
		`"content":[{"type":"text","text":"<system-reminder>x</system-reminder>words"}],"genuineUserQueryText":7`))
	if len(got) != 0 {
		t.Fatalf("messages = %q, want none", got)
	}
	// An empty one is a turn with nothing typed, as the runtime reads it.
	got = parseMcodeLines(t, mcodeUserLine(
		`"content":[{"type":"text","text":"words"}],"genuineUserQueryText":""`))
	if len(got) != 0 {
		t.Fatalf("empty genuineUserQueryText: messages = %q, want none", got)
	}
}

// A content that is a plain string is the text itself (YIe), and the range
// counts into it.
func TestMcodeAStringContentIsTheText(t *testing.T) {
	got := parseMcodeLines(t, mcodeUserLine(`"content":"<system-reminder>r</system-reminder>asked plainly"`))
	if len(got) != 1 || got[0] != "user: asked plainly" {
		t.Fatalf("messages = %q", got)
	}
	got = parseMcodeLines(t, mcodeUserLine(`"content":"xxtyped","canonicalTextRange":{"startOffset":2,"endOffset":7}`))
	if len(got) != 1 || got[0] != "user: typed" {
		t.Fatalf("with a range: messages = %q", got)
	}
}

// The marker a migration leaves when it could not recover older history is
// a user-role line, but nobody typed it; it became the session's title.
func TestMcodeTheLegacyBoundaryMarkerIsNotATurn(t *testing.T) {
	marker := mcodeUserLine(`"content":[{"type":"text","text":"<system-reminder>\nEarlier legacy conversation context could not be safely recovered during migration.\n</system-reminder>"}],` +
		`"archonCompaction":{"schemaVersion":1,"summary":"Earlier legacy conversation context could not be safely recovered during migration."},` +
		`"archonLegacyContextBoundary":{"schemaVersion":1,"reason":"legacy_history_unavailable"}`)
	dir := t.TempDir()
	path := filepath.Join(dir, mcodeTranscriptName)
	body := marker + "\n" + mcodeUserLine(`"content":[{"type":"text","text":"the real question"}]`) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseMcodeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].Title != "the real question" || len(ss[0].Messages) != 1 {
		t.Fatalf("session = %#v, want one turn titled by the real question", ss)
	}
}

// A compaction marker with a summary of its own is the record of the turns
// it cut, indexed under the summary role and never as speech.
func TestMcodeACompactionMarkerSummaryIsIndexedAsASummary(t *testing.T) {
	got := parseMcodeLines(t,
		mcodeUserLine(`"content":[{"type":"text","text":"<system-reminder>ARCHON-SUMMARY</system-reminder>"}],`+
			`"archonCompaction":{"version":2,"summary":"ARCHON-SUMMARY decided to drop the cache","recentUserQueries":[{"text":"q"}]}`),
		mcodeUserLine(`"content":[{"type":"text","text":"carry on"}]`))
	want := []string{RoleSummary + ": ARCHON-SUMMARY decided to drop the cache", "user: carry on"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("messages = %q, want %q", got, want)
	}
}

// The directory name carries the id in base64url: a "-" or "_" in it is not
// standard base64.
func TestMcodeTheDirectoryIDIsBase64URL(t *testing.T) {
	// "mvs_?>?" encodes to bXZzXz8-Pw in base64url, bXZzXz8+Pw in standard.
	if id, ok := decodeMcodeID("bXZzXz8-Pw"); !ok || id != "mvs_?>?" {
		t.Fatalf("id = %q ok = %v", id, ok)
	}
	if id, ok := decodeMcodeID("bXZzXz8+Pw=="); !ok || id != "mvs_?>?" {
		t.Fatalf("standard: id = %q ok = %v", id, ok)
	}
}

// What a plugin hook answered rides the tool result, as its own last text
// block, and the turn's text, after a blank line (MAr, uJn, tTn in 0.6.12; the
// tool-result block seen in a live run). With deja installed that is deja's
// own recall, and indexing it would feed recall back into the index. Output
// that only mentions the tag keeps every line.
func TestMcodeHookContextIsNotIndexed(t *testing.T) {
	got := parseMcodeLines(t,
		mcodeUserLine(`"content":[{"type":"text","text":"asked\n\n<plugin-hook-context>\nRECALL-ECHO\n</plugin-hook-context>"}]`),
		`{"message_id":"m2","turn_id":"t1","message":{"role":"toolResult","toolCallId":"c","toolName":"bash",`+
			`"content":[{"type":"text","text":"hi\n"},{"type":"text","text":"<plugin-hook-context>\nRECALL-ECHO\n</plugin-hook-context>"}],"timestamp":1792465960901}}`,
		`{"message_id":"m3","turn_id":"t1","message":{"role":"toolResult","toolCallId":"c2","toolName":"bash",`+
			`"content":[{"type":"text","text":"a <plugin-hook-context> tag\nreal line\n</plugin-hook-context> end"}],"timestamp":1792465960902}}`)
	want := []string{"user: asked", RoleToolOutput + ": hi", RoleToolOutput + ": a <plugin-hook-context> tag\nreal line\n</plugin-hook-context> end"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("messages = %q, want %q", got, want)
	}
}

// A compaction marker heads the history it kept; a read resumed past it takes
// the title from the first real turn, as the full read does.
func TestMcodeTheTitleSurvivesAnAppendAfterACompactionMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, mcodeTranscriptName)
	head := mcodeUserLine(`"content":[{"type":"text","text":"<system-reminder>S</system-reminder>"}],"archonCompaction":{"version":2,"summary":"S","recentUserQueries":[]}`) + "\n" +
		mcodeUserLine(`"content":[{"type":"text","text":"original question"}]`) + "\n"
	if err := os.WriteFile(path, []byte(head+mcodeUserLine(`"content":[{"type":"text","text":"later question"}]`)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tail, err := ParseMcodeFileFromOffset(path, int64(len(head)))
	if err != nil || len(tail) != 1 {
		t.Fatalf("tail = %v, %v", tail, err)
	}
	if tail[0].Title != "original question" {
		t.Fatalf("title = %q, want the first real turn", tail[0].Title)
	}
}

// A range written as 3266.0 is an integer to the runtime.
func TestMcodeAnIntegralFloatOffsetIsAnOffset(t *testing.T) {
	got := parseMcodeLines(t, mcodeUserLine(`"content":[{"type":"text","text":"xxtyped"}],"canonicalTextRange":{"startOffset":2.0,"endOffset":7e0}`))
	if len(got) != 1 || got[0] != "user: typed" {
		t.Fatalf("messages = %q", got)
	}
}

// A history rewrite the runtime staged and left behind is the same session,
// not a second one; it is bookkeeping.
func TestMcodeAStagedHistoryRewriteIsNotASecondSession(t *testing.T) {
	root := filepath.Join(t.TempDir(), "v2", "sessions")
	t.Setenv("DEJA_MCODE_ROOT", root)
	dir := filepath.Join(root, "2026", "10", "22", "15-47-55-700-session_x")
	staged := filepath.Join(dir, ".history-mutation-1", mcodeTranscriptName)
	for _, p := range []string{filepath.Join(dir, mcodeTranscriptName), staged} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(mcodeUserLine(`"content":[{"type":"text","text":"hi"}]`)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(McodeSessionFiles()); n != 1 {
		t.Fatalf("transcripts = %d, want 1", n)
	}
	found := false
	for _, p := range McodeSidecarFiles() {
		found = found || p == staged
	}
	if !found {
		t.Fatal("the staged copy is neither a transcript nor bookkeeping")
	}
}

// The transcript records no directory; the runtime's database does
// (local_runtime_sessions.workspace_dir), and the session is filed under it.
func TestMcodeTheProjectIsTheWorkspaceTheRuntimeRecorded(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	base := t.TempDir()
	root := filepath.Join(base, "v2", "sessions")
	t.Setenv("DEJA_MCODE_ROOT", root)
	ws := filepath.Join(base, "work", "billing-api")
	dir := filepath.Join(root, "2026", "10", "22", "15-47-55-700-session_x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"sessionId":"mvs_ws"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, mcodeTranscriptName), []byte(mcodeUserLine(`"content":[{"type":"text","text":"hi"}]`)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Without the database the project is unknown.
	if ss := LoadMcode(); len(ss) != 1 || ss[0].Project != "-" {
		t.Fatalf("sessions = %#v, want one with no project", ss)
	}
	db := McodeStateDB()
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	tree := filepath.Join(base, "work", "billing-api-wt")
	sql := "create table local_runtime_sessions (session_id text primary key, workspace_dir text, project_workspace_dir text, is_default_workspace integer not null default 0);" +
		"insert into local_runtime_sessions values ('mvs_ws', '" + strings.ReplaceAll(tree, "'", "''") + "', '" + strings.ReplaceAll(ws, "'", "''") + "', 0);" +
		"insert into local_runtime_sessions values ('mvs_default', '" + strings.ReplaceAll(filepath.Join(base, ".minimax", "workspace"), "'", "''") + "', null, 1);" +
		"insert into local_runtime_sessions values ('mvs_rel', 'relative/dir', '', 0);"
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v %s", err, out)
	}
	// A worktree session is filed under the repository, and its workspace is
	// the worktree.
	ss := LoadMcode()
	if len(ss) != 1 || ss[0].Project != projectName(ws) {
		t.Fatalf("project = %q, want %q", ss[0].Project, projectName(ws))
	}
	if got := McodeSessionWorkspace("mvs_ws"); got != tree {
		t.Fatalf("workspace = %q, want %q", got, tree)
	}
	// The runtime's own scratch directory is no project.
	if McodeSessionWorkspace("mvs_default") != "" {
		t.Fatal("a default-workspace chat was given a project")
	}
	if McodeSessionWorkspace("mvs_rel") != "" || McodeSessionWorkspace("") != "" {
		t.Fatal("a relative or missing workspace came back")
	}
}

// A PreCompact hook finds the session by id: the directory name carries it in
// base64url, and the manifest has the last word.
func TestMcodeSessionPathFindsTheSessionByID(t *testing.T) {
	root := filepath.Join(t.TempDir(), "v2", "sessions")
	t.Setenv("DEJA_MCODE_ROOT", root)
	dir := filepath.Join(root, "2026", "10", "22", "15-47-55-700-session_bXZzXz8-Pw")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, mcodeTranscriptName)
	if err := os.WriteFile(path, []byte(mcodeUserLine(`"content":[{"type":"text","text":"hi"}]`)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := McodeSessionPath("mvs_?>?"); got != path {
		t.Fatalf("path = %q, want %q", got, path)
	}
	if McodeSessionPath("mvs_other") != "" || McodeSessionPath("") != "" {
		t.Fatal("an id the store does not hold came back with a path")
	}
	if _, err := ReadCompactionStore("mcode", "mvs_other"); err == nil {
		t.Fatal("captured a session the store does not hold")
	}
	tr, err := ReadCompactionStore("mcode", "mvs_?>?")
	if err != nil || tr.Harness != "mcode" || tr.Path != path {
		t.Fatalf("capture = %#v, %v", tr, err)
	}
}

// archonCompaction marks a line the way the runtime tests it: by truthiness.
func TestMcodeACompactionMarkerIsTruthy(t *testing.T) {
	for _, v := range []string{`true`, `1`, `"x"`, `[]`} {
		if got := parseMcodeLines(t, mcodeUserLine(`"content":[{"type":"text","text":"typed"}],"archonCompaction":`+v)); len(got) != 0 {
			t.Fatalf("archonCompaction %s: messages = %q, want none", v, got)
		}
	}
	for _, v := range []string{`null`, `false`, `0`, `""`} {
		if got := parseMcodeLines(t, mcodeUserLine(`"content":[{"type":"text","text":"typed"}],"archonCompaction":`+v)); len(got) != 1 {
			t.Fatalf("archonCompaction %s: messages = %q, want the turn", v, got)
		}
	}
}
