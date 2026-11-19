package sources

import (
	"encoding/json"
	"errors"
	"github.com/vshulcz/deja-vu/internal/model"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// The paths a parser reads are written by someone else's machine, and the
// fixtures in this repository all look like this one: ASCII, no spaces, no
// drive letters. That is how a Copilot Chat resource went into the files record
// percent-encoded and nobody here could see it — every path on this machine
// decodes to itself (#3498, #3505).
//
// So one hostile path, through the parsers that record what a turn touched. Not
// every harness: the ones whose transcripts are JSONL deja writes fixtures for,
// which is where a path arrives as a field rather than as a URI.
func TestAFilesRecordKeepsAHostilePathIntact(t *testing.T) {
	// A space, a non-ASCII character, a character that is a shell metacharacter
	// elsewhere, and a windows drive letter — the four shapes that have broken
	// something in a path before.
	for _, want := range []string{
		"/Users/me/my app/main.go",
		"/Users/me/проект/счёт.go",
		"/Users/me/a(b)/c d/e.go",
		"/c:/Users/me/my app/main.go",
	} {
		t.Run(want, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("HOME", filepath.Join(root, "home"))
			t.Setenv("USERPROFILE", filepath.Join(root, "home"))
			t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(root, "claude"))
			dir := filepath.Join(root, "claude", "-work-app")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			// The shape Claude Code writes an edit in: the path is a field of
			// the tool call, so whatever is in it has to come back unchanged.
			line := `{"type":"assistant","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","cwd":"/work/app",` +
				`"message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":` +
				`{"file_path":` + jsonString(want) + `,"old_string":"a","new_string":"b"}}]}}` + "\n"
			path := filepath.Join(dir, "s1.jsonl")
			if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
				t.Fatal(err)
			}
			ss, err := ParseClaudeFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(ss) != 1 {
				t.Fatalf("sessions = %d, want the one transcript", len(ss))
			}
			var got []string
			for _, m := range ss[0].Messages {
				if m.Role == RoleFiles || m.Role == RoleEdit {
					// An edit record is the path on its first line.
					got = append(got, strings.SplitN(m.Text, "\n", 2)[0])
				}
			}
			if len(got) == 0 {
				t.Fatalf("no record names the file at all: %#v", ss[0].Messages)
			}
			for _, p := range got {
				if p != want {
					t.Errorf("record has %q, want %q", p, want)
				}
			}
		})
	}
}

// jsonString quotes a path the way a transcript does, so the fixture is written
// by the same rules the parser reads it by.
func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString("\\n")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func TestParseAmpThread(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_AMP_ROOT", filepath.Join(root, "threads"))

	path := filepath.Join(root, "threads", "thread-abc.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const created int64 = 1767337445000
	data := `{"id":"thread-abc","title":"Fix the parser","created":1767337445000,"env":{"initial":{"trees":[{"uri":"file:///tmp/amp-project"}]}},"messages":[{"role":"user","content":[{"type":"text","text":"Please fix the parser."},{"type":"image","url":"file:///tmp/ignored.png"}]},{"role":"assistant","content":[{"type":"text","text":"I found the issue."},{"type":"text","text":"The parser now handles it."}]},{"role":"system","content":[{"type":"text","text":"ignored system message"}]}]}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	sessions, err := ParseAmpFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	s := sessions[0]
	if s.Harness != "amp" || s.ID != "thread-abc" || s.Title != "Fix the parser" {
		t.Fatalf("identity = %#v", s)
	}
	if s.Project != "tmp/amp-project" {
		t.Fatalf("project = %q, want tmp/amp-project", s.Project)
	}
	if s.Path != path {
		t.Fatalf("path = %q, want %q", s.Path, path)
	}
	stamp := time.UnixMilli(created)
	if !s.Started.Equal(stamp) || !s.Updated.Equal(stamp) {
		t.Fatalf("session timestamps = %v / %v, want %v", s.Started, s.Updated, stamp)
	}
	if len(s.Messages) != 2 {
		t.Fatalf("got %d messages, want user and assistant only: %#v", len(s.Messages), s.Messages)
	}
	if s.Messages[0].Role != "user" || s.Messages[0].Text != "Please fix the parser." {
		t.Fatalf("user message = %#v", s.Messages[0])
	}
	if s.Messages[1].Role != "assistant" || s.Messages[1].Text != "I found the issue.\nThe parser now handles it." {
		t.Fatalf("assistant message = %#v", s.Messages[1])
	}
	for i, message := range s.Messages {
		if !message.Time.Equal(stamp) {
			t.Errorf("message %d timestamp = %v, want thread created %v", i, message.Time, stamp)
		}
	}
}

func TestAmpProjectReadsAWindowsDriveURI(t *testing.T) {
	cases := map[string]string{
		"file:///tmp/proj":      "/tmp/proj",
		"file:///C:/work/app":   filepath.FromSlash("C:/work/app"),
		"file:///d:/x":          filepath.FromSlash("d:/x"),
		"https://example.com/a": "fallback",
	}
	for uri, want := range cases {
		if got := ampProject(uri, "fallback"); got != want {
			t.Errorf("ampProject(%q) = %q, want %q", uri, got, want)
		}
	}
}

func TestParseAmpFallsBackToTitleWithoutWorkingDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread.json")
	data := `{"id":"fallback-1","title":"Untitled work","created":1767337445000,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	sessions, err := ParseAmpFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Project != "Untitled work" {
		t.Fatalf("fallback project = %#v, want title", sessions)
	}
}

func TestParseAmpRejectsMalformedOrTruncatedJSON(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "malformed", body: `{"id":"bad",`},
		{name: "truncated message", body: `{"id":"bad","title":"broken","created":1767337445000,"messages":[{"role":"user","content":[{"type":"text","text":"cut`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bad.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := ParseAmpFile(path); err == nil {
				t.Fatal("malformed Amp file parsed without an error")
			}
		})
	}
}

func TestParseAmpRejectsTrailingData(t *testing.T) {
	for _, trailing := range []string{`{"id":"second"}`, "junk"} {
		t.Run(trailing, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "trailing.json")
			body := `{"id":"first","title":"First","created":1767337445000,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}` + trailing
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := ParseAmpFile(path); err == nil {
				t.Fatalf("parsed valid JSON followed by %q", trailing)
			}
		})
	}
}

func TestParseAmpAllowsTrailingWhitespace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "whitespace.json")
	body := `{"id":"whitespace","title":"Whitespace","created":1767337445000,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}` + " \n\t"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseAmpFile(path); err != nil {
		t.Fatalf("rejected trailing JSON whitespace: %v", err)
	}
}

func TestLoadAmpSkipsMalformedThread(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_AMP_ROOT", filepath.Join(root, "threads"))
	if err := os.MkdirAll(AmpRoot(), 0o755); err != nil {
		t.Fatal(err)
	}
	valid := `{"id":"valid","title":"Valid","created":1767337445000,"messages":[{"role":"user","content":[{"type":"text","text":"valid text"}]}]}`
	for name, body := range map[string]string{"valid.json": valid, "truncated.json": `{"id":"truncated",`} {
		if err := os.WriteFile(filepath.Join(AmpRoot(), name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	loaded := LoadAmp()
	if len(loaded) != 1 || loaded[0].ID != "valid" {
		t.Fatalf("LoadAmp = %#v, want only valid thread", loaded)
	}
}

func TestAmpDiscoveryAndRegistry(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "xdg"))
	t.Setenv("DEJA_AMP_ROOT", "")
	wantDefault := filepath.Join(root, "xdg", "amp", "threads")
	if got := AmpRoot(); got != wantDefault {
		t.Fatalf("default AmpRoot = %q, want %q", got, wantDefault)
	}

	override := filepath.Join(root, "custom-amp")
	t.Setenv("DEJA_AMP_ROOT", override)
	path := filepath.Join(override, "thread-1.json")
	if err := os.MkdirAll(override, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"id":"thread-1","title":"Discovery","created":1767337445000,"messages":[{"role":"user","content":[{"type":"text","text":"find me"}]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	files := AmpThreadFiles()
	if len(files) != 1 || files[0] != path {
		t.Fatalf("AmpThreadFiles = %v, want [%s]", files, path)
	}
	loaded := LoadAmp()
	if len(loaded) != 1 || loaded[0].ID != "thread-1" {
		t.Fatalf("LoadAmp = %#v", loaded)
	}
	if !IsKnownHarness("amp") || KindForPath(path) != "amp" {
		t.Fatalf("Amp was not registered: known=%v kind=%q", IsKnownHarness("amp"), KindForPath(path))
	}
}

// An Amp thread records its tool calls as tool_use blocks and their results as
// tool_result blocks with a run, and stamps user turns with meta.sentAt and
// assistant turns with usage.timestamp. Reading only text blocks under the
// thread's created time lost every command and edit (#4356).
func TestParseAmpKeepsToolCallsAndMessageTimes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "T-0f3c.json")
	data := `{"v":7,"id":"T-0f3c","created":1774950000000,"title":"Fix the retry loop",
"env":{"initial":{"trees":[{"uri":"file:///tmp/proj"}]}},
"messages":[
{"role":"user","content":[{"type":"text","text":"fix the retry loop in client.go"}],"meta":{"sentAt":1774950001000}},
{"role":"assistant","content":[{"type":"text","text":"Running the tests first."},{"type":"tool_use","id":"toolu_01","name":"Bash","complete":true,"input":{"cmd":"go test ./...","cwd":"/tmp/proj"}}],"usage":{"timestamp":"2026-03-31T09:40:05Z"}},
{"role":"user","content":[{"type":"tool_result","toolUseID":"toolu_01","run":{"status":"done","result":{"output":"--- FAIL: TestRetry (0.00s)","exitCode":1}}}]},
{"role":"assistant","content":[{"type":"tool_use","id":"toolu_02","name":"edit_file","complete":true,"input":{"path":"/tmp/proj/client.go","old_str":"for i := 0; i <= max; i++","new_str":"for i := 0; i < max; i++"}}]},
{"role":"user","content":[{"type":"tool_result","toolUseID":"toolu_02","run":{"status":"done","result":{"diff":"@@ -1 +1 @@"}}}]},
{"role":"assistant","content":[{"type":"tool_use","id":"toolu_03","name":"create_file","complete":true,"input":{"path":"/tmp/proj/retry_test.go","content":"package proj\n\nfunc TestRetryStops() {}\n"}}]},
{"role":"assistant","content":[{"type":"text","text":"Fixed the off-by-one in the retry loop."}]},
{"role":"user","content":[{"type":"text","text":"thanks"}],"meta":{"sentAt":1774950300000}}]}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseAmpFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("ParseAmpFile = %v, %v", ss, err)
	}
	s := ss[0]
	byRole := map[string][]string{}
	for _, m := range s.Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
	}
	// The run's exitCode rides on the command (#4530).
	if got := byRole[RoleCommand]; len(got) != 1 || got[0] != "$ go test ./...  → exit 1" {
		t.Errorf("commands = %q", got)
	}
	if got := byRole[RoleToolOutput]; len(got) != 1 || got[0] != "--- FAIL: TestRetry (0.00s)" {
		t.Errorf("tool output = %q", got)
	}
	if got := byRole[RoleFiles]; len(got) != 2 || got[0] != "/tmp/proj/client.go" || got[1] != "/tmp/proj/retry_test.go" {
		t.Errorf("files = %q", got)
	}
	if got := byRole[RoleEdit]; len(got) != 1 || got[0] != "/tmp/proj/client.go\nfor i := 0; i <= max; i++" {
		t.Errorf("edits = %q", got)
	}
	if got := byRole[RoleWrote]; len(got) == 0 {
		t.Errorf("no wrote record for the edit or the new file")
	}

	first, last := s.Messages[0], s.Messages[len(s.Messages)-1]
	if first.Role != "user" || !first.Time.Equal(time.UnixMilli(1774950001000)) {
		t.Errorf("first message = %s at %v, want user at sentAt", first.Role, first.Time)
	}
	if last.Text != "thanks" || !last.Time.Equal(time.UnixMilli(1774950300000)) {
		t.Errorf("last message = %q at %v, want thanks at its sentAt", last.Text, last.Time)
	}
	want := time.Date(2026, 3, 31, 9, 40, 5, 0, time.UTC)
	if m := s.Messages[1]; m.Role != "assistant" || !m.Time.Equal(want) {
		t.Errorf("assistant turn at %v, want its usage.timestamp %v", m.Time, want)
	}
	if !s.Updated.Equal(last.Time) {
		t.Errorf("session updated %v, want the last turn %v", s.Updated, last.Time)
	}
}

// The per-turn times are optional: a thread whose usage.timestamp is an epoch
// number, or whose sentAt is a string, failed to decode whole and lost every
// turn, where before #4356 the reader never looked at either field.
func TestParseAmpKeepsAThreadWhoseTurnTimesDrift(t *testing.T) {
	path := filepath.Join(t.TempDir(), "T-drift.json")
	data := `{"v":7,"id":"T-drift","created":1774950000000,"title":"drift",
"messages":[
{"role":"user","content":[{"type":"text","text":"why does the retry loop spin"}],"meta":{"sentAt":"2026-03-31T09:40:01Z"}},
{"role":"assistant","content":[{"type":"text","text":"It never stops."}],"usage":{"timestamp":1774950005000}}]}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseAmpFile(path)
	if err != nil || len(ss) != 1 || len(ss[0].Messages) != 2 {
		t.Fatalf("ParseAmpFile = %v, %v", ss, err)
	}
	if got := ss[0].Messages[0].Time; !got.Equal(time.Date(2026, 3, 31, 9, 40, 1, 0, time.UTC)) {
		t.Errorf("user turn at %v, want its string sentAt", got)
	}
	if got := ss[0].Messages[1].Time; !got.Equal(time.UnixMilli(1774950005000)) {
		t.Errorf("assistant turn at %v, want its numeric usage.timestamp", got)
	}
}

// The parser table in ARCHITECTURE.md is the first place a reader looks to
// decide whether their tool is read at all, and nothing checked it: CodeWhale
// had a parser, a registry page and a test, and no row here, while the sentence
// above the table still counted the stores it used to hold. A table that is one
// harness short reads as an answer rather than an omission.
func TestTheParserTableHasARowForEveryHarness(t *testing.T) {
	root := filepath.Join("..", "..")

	b, err := os.ReadFile(filepath.Join(root, "docs", "registry", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		Harnesses []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"harnesses"`
	}
	if err = json.Unmarshal(b, &reg); err != nil {
		t.Fatal(err)
	}
	if len(reg.Harnesses) == 0 {
		t.Fatal("no harnesses in registry.json — this checks nothing")
	}

	doc, err := os.ReadFile(filepath.Join(root, "docs", "ARCHITECTURE.md"))
	if err != nil {
		t.Fatal(err)
	}
	arch := string(doc)
	from := strings.Index(arch, "## Source parsers")
	if from < 0 {
		t.Fatal("docs/ARCHITECTURE.md has no source-parser section")
	}
	next := strings.Index(arch[from+1:], "\n## ")
	if next < 0 {
		t.Fatal("the source-parser section runs to the end of the file")
	}
	arch = arch[from : from+1+next]

	for _, h := range reg.Harnesses {
		if !strings.Contains(arch, "| "+h.DisplayName+" |") {
			t.Errorf("the parser table in docs/ARCHITECTURE.md has no row for %s (%s)", h.DisplayName, h.ID)
		}
	}

	// The count in the sentence above the table is the other half: the rows are
	// the harnesses plus deja's own notes.
	rows := strings.Count(arch, "\n| ") - 2 // the header and its rule
	if want := len(reg.Harnesses) + 1; rows != want {
		t.Errorf("the parser table has %d rows, want %d — the harnesses plus deja notes", rows, want)
	}
}

// Codex edits through apply_patch, so a Codex session read back showed none
// of the code it changed. The patch is read the way Codex applies it: a file
// added whole, a file updated hunk by hunk, a rename, a deletion skipped, and
// the same text passed through an older build's shell call.
func TestChangesReadsACodexSessionsPatches(t *testing.T) {
	patch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: /work/app/pkg/a.go",
		"@@ func retry()",
		" \tfor i := 0; i < n; i++ {",
		"-\t\tif err == nil {",
		"+\t\tif err == nil || i == max {",
		" \t\t\tbreak",
		"*** Add File: pkg/new.go",
		"+package pkg",
		"+",
		"+const max = 5",
		"*** Delete File: pkg/old.go",
		"*** Update File: pkg/b.go",
		"*** Move to: pkg/c.go",
		"@@",
		"-one",
		"+uno",
		"*** End Patch",
		"",
	}, "\n")
	shellArgs := mustJSON(t, map[string]any{"command": []string{"apply_patch", "*** Begin Patch\n*** Update File: pkg/d.go\n@@\n-x\n+y\n*** End Patch\n"}})
	rec := func(typ string, payload any) string {
		b := mustJSON(t, map[string]any{"type": typ, "payload": payload})
		return string(b)
	}
	lines := []string{
		rec("session_meta", map[string]any{"id": "s", "cwd": "/work/app"}),
		rec("response_item", map[string]any{"type": "custom_tool_call", "name": "apply_patch", "input": patch}),
		rec("response_item", map[string]any{"type": "function_call", "name": "shell", "arguments": string(shellArgs)}),
		rec("response_item", map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "I would apply *** Begin Patch\n*** Add File: talk.go\n+no"}}}),
	}
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changes := Changes(model.Session{Harness: "codex", ID: "s", Path: path})
	got := make([]string, 0, len(changes))
	for _, c := range changes {
		got = append(got, c.Path+"|"+c.Old+"|"+c.New+"|"+map[bool]string{true: "new"}[c.Created])
	}
	want := []string{
		"pkg/a.go|\tfor i := 0; i < n; i++ {\n\t\tif err == nil {\n\t\t\tbreak|\tfor i := 0; i < n; i++ {\n\t\tif err == nil || i == max {\n\t\t\tbreak|",
		"pkg/new.go||package pkg\n\nconst max = 5|new",
		"pkg/c.go|one|uno|",
		"pkg/d.go|x|y|",
	}
	if strings.Join(got, "\n--\n") != strings.Join(want, "\n--\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n--\n"), strings.Join(want, "\n--\n"))
	}
}

// Two patches in one string are both read, and a fork's inherited
// session_meta does not move the directory its paths are read against.
func TestChangesReadsEveryCodexPatchAgainstTheFilesOwnDirectory(t *testing.T) {
	rec := func(typ string, payload any) string {
		b := mustJSON(t, map[string]any{"type": typ, "payload": payload})
		return string(b)
	}
	two := "*** Begin Patch\n*** Update File: /w/child/a.go\n@@\n-1\n+2\n*** End Patch\n*** Begin Patch\n*** Update File: b.go\n@@\n-3\n+4\n*** End Patch\n"
	lines := []string{
		rec("session_meta", map[string]any{"id": "child", "cwd": "/w/child", "forked_from_id": "parent"}),
		rec("session_meta", map[string]any{"id": "parent", "cwd": "/w/parent"}),
		rec("response_item", map[string]any{"type": "custom_tool_call", "name": "apply_patch", "input": two}),
	}
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changes := Changes(model.Session{Harness: "codex", ID: "child", Path: path})
	got := make([]string, 0, len(changes))
	for _, c := range changes {
		got = append(got, c.Path+":"+c.New)
	}
	if strings.Join(got, " ") != "a.go:2 b.go:4" {
		t.Fatalf("got %v", got)
	}
}

// An edit the harness refused changed nothing: Claude Code answers it with an
// error result (a denied call, a file not read first, text not found), Codex
// with a failing exit code. Read back as a change, the session showed code it
// never wrote.
func TestChangesLeaveOutEditsThatFailed(t *testing.T) {
	write := func(lines ...string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "s.jsonl")
		if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	paths := func(cs []Change) string {
		out := make([]string, 0, len(cs))
		for _, c := range cs {
			out = append(out, c.Path+":"+c.New)
		}
		return strings.Join(out, " ")
	}

	const id = "aaaa0001-1111-4000-8000-d6e7f8a9b0c1"
	edit := func(call, file, newText string) string {
		return `{"type":"assistant","sessionId":"` + id + `","cwd":"/w","message":{"content":[{"type":"tool_use","id":"` + call + `","name":"Edit","input":{"file_path":"/w/` + file + `","old_string":"a","new_string":"` + newText + `"}}]}}`
	}
	result := func(call string, isErr bool, text string) string {
		e := "false"
		if isErr {
			e = "true"
		}
		return `{"type":"user","sessionId":"` + id + `","message":{"content":[{"type":"tool_result","tool_use_id":"` + call + `","is_error":` + e + `,"content":"` + text + `"}]}}`
	}
	claude := write(
		edit("t1", "denied.go", "NEVER"), result("t1", true, "Permission for this action was denied"),
		edit("t2", "kept.go", "b"), result("t2", false, "The file has been updated."),
		edit("t3", "unanswered.go", "c"),
	)
	if got := paths(Changes(model.Session{Harness: "claude", ID: id, Path: claude})); got != "kept.go:b unanswered.go:c" {
		t.Errorf("claude: %q", got)
	}

	rec := func(typ string, payload any) string {
		b := mustJSON(t, map[string]any{"type": typ, "payload": payload})
		return string(b)
	}
	patch := func(file, newText string) string {
		return "*** Begin Patch\n*** Update File: " + file + "\n@@\n-a\n+" + newText + "\n*** End Patch\n"
	}
	shellArgs := mustJSON(t, map[string]any{"command": []string{"apply_patch", patch("old.go", "NEVER")}})
	codex := write(
		rec("session_meta", map[string]any{"id": "s", "cwd": "/w"}),
		rec("response_item", map[string]any{"type": "custom_tool_call", "call_id": "c1", "name": "apply_patch", "input": patch("bad.go", "NEVER")}),
		rec("response_item", map[string]any{"type": "custom_tool_call_output", "call_id": "c1", "output": "Exit code: 1\nWall time: 0.1 seconds\nOutput:\napply_patch verification failed: Failed to find expected lines"}),
		rec("response_item", map[string]any{"type": "custom_tool_call", "call_id": "c2", "name": "apply_patch", "input": patch("good.go", "b")}),
		rec("response_item", map[string]any{"type": "custom_tool_call_output", "call_id": "c2", "output": "Exit code: 0\nWall time: 0.1 seconds\nOutput:\nSuccess. Updated the following files:\nM good.go\n"}),
		rec("response_item", map[string]any{"type": "custom_tool_call", "call_id": "c4", "name": "apply_patch", "input": patch("echo.go", "d")}),
		rec("response_item", map[string]any{"type": "custom_tool_call_output", "call_id": "c4", "output": "Success. Updated the following files:\nM echo.go\n+echo \"Exit code: 1\"\n"}),
		rec("response_item", map[string]any{"type": "function_call", "call_id": "c3", "name": "shell", "arguments": string(shellArgs)}),
		rec("response_item", map[string]any{"type": "function_call_output", "call_id": "c3", "output": `{"output":"error: patch failed","metadata":{"exit_code":1}}`}),
	)
	if got := paths(Changes(model.Session{Harness: "codex", ID: "s", Path: codex})); got != "good.go:b echo.go:d" {
		t.Errorf("codex: %q", got)
	}
}

// A Gemini tool call whose result reports an error changed nothing (#4974):
// the generic reader took every edit call it found, so a `replace` Gemini
// refused showed as code the session wrote, beside the edit that succeeded.
func TestGenericChangesSkipsFailedEditCalls(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_GEMINI_ROOT", root)
	_, chats := geminiTree(t)
	lines := `{"sessionId":"sess-failed-edit-1","projectHash":"abc","startTime":"2026-07-20T21:07:00.000Z","lastUpdated":"2026-07-20T21:08:00.000Z","kind":"main"}
{"id":"u1","timestamp":"2026-07-20T21:07:01.000Z","type":"user","content":[{"text":"edit pool.go"}]}
{"id":"g1","timestamp":"2026-07-20T21:07:02.000Z","type":"gemini","content":"","model":"gemini-3","toolCalls":[{"id":"replace__1","name":"replace","args":{"file_path":"/Users/x/coding/gateway/pool.go","old_string":"MaxConns: 10","new_string":"MaxConns: 40"},"result":[{"functionResponse":{"id":"replace__1","name":"replace","response":{"error":"old_string not found"}}}],"status":"error"},{"id":"replace__2","name":"replace","args":{"file_path":"/Users/x/coding/gateway/pool.go","old_string":"Timeout: 1s","new_string":"Timeout: 5s"},"result":[{"functionResponse":{"id":"replace__2","name":"replace","response":{"output":"ok"}}}],"status":"success"}]}
{"id":"u2","timestamp":"2026-07-20T21:07:03.000Z","type":"user","content":[{"functionResponse":{"id":"replace__1","name":"replace","response":{"error":"old_string not found"}}},{"functionResponse":{"id":"replace__2","name":"replace","response":{"output":"ok"}}}]}
{"id":"g2","timestamp":"2026-07-20T21:07:04.000Z","type":"gemini","content":"Done.","model":"gemini-3"}
`
	p := filepath.Join(chats, "session-2026-07-20T21-07-sess-failed-edit-1.jsonl")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	changes := Changes(model.Session{Harness: "gemini", ID: "sess-failed-edit-1", Path: p})
	got := make([]string, 0, len(changes))
	for _, c := range changes {
		got = append(got, c.Old+"|"+c.New)
	}
	if want := "Timeout: 1s|Timeout: 5s"; strings.Join(got, " ") != want {
		t.Fatalf("got  %q\nwant %q", strings.Join(got, " "), want)
	}
}

// The same holds for a patch: an apply_patch call marked failed applied
// nothing, so its text is not a change either.
func TestGenericChangesSkipsAFailedPatchCall(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_GEMINI_ROOT", root)
	_, chats := geminiTree(t)
	patch := `*** Begin Patch\n*** Update File: /Users/x/coding/gateway/pool.go\n@@\n-MaxConns: 10\n+MaxConns: 40\n*** End Patch`
	call := func(id, status string) string {
		return `{"id":"` + id + `","name":"apply_patch","args":{"input":"` + patch + `"},"status":"` + status + `"}`
	}
	lines := `{"sessionId":"sess-failed-patch-1","projectHash":"abc","startTime":"2026-07-20T21:07:00.000Z","lastUpdated":"2026-07-20T21:08:00.000Z","kind":"main"}
{"id":"u1","timestamp":"2026-07-20T21:07:01.000Z","type":"user","content":[{"text":"edit pool.go"}]}
{"id":"g1","timestamp":"2026-07-20T21:07:02.000Z","type":"gemini","content":"","model":"gemini-3","toolCalls":[` + call("p1", "error") + `]}
`
	p := filepath.Join(chats, "session-2026-07-20T21-07-sess-failed-patch-1.jsonl")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	s := model.Session{Harness: "gemini", ID: "sess-failed-patch-1", Path: p}
	if got := Changes(s); len(got) != 0 {
		t.Fatalf("a failed apply_patch came back as %d changes: %+v", len(got), got)
	}
	// The control: the same call marked success is read.
	if err := os.WriteFile(p, []byte(strings.Replace(lines, `"status":"error"`, `"status":"success"`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Changes(s); len(got) != 1 {
		t.Fatalf("a successful apply_patch came back as %d changes", len(got))
	}
}

// Kimi Code's Edit and Write calls, read through the Kimi loader and the
// generic reader. Kimi's wire.jsonl carries no cwd, so paths are shown
// relative to the common directory of the changed files (here both sit in
// the same one).
func TestChangesReadsKimiEditAndWriteCalls(t *testing.T) {
	root := fixtureRoot(t, "kimi", "DEJA_KIMI_ROOT")
	p := filepath.Join(root, "sessions", "wd_demo_0123456789ab", "session_changes01", "agents", "main", "wire.jsonl")
	ss, err := ParseKimiFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("ParseKimiFile: %v, %d sessions", err, len(ss))
	}
	changes := Changes(ss[0])
	got := make([]string, 0, len(changes))
	for _, c := range changes {
		got = append(got, c.Path+"|"+c.Old+"|"+c.New)
	}
	if want := "pool.go|MaxConns: 10|MaxConns: 40 pool_test.go||package demo"; strings.Join(got, " ") != want {
		t.Fatalf("got  %q\nwant %q", strings.Join(got, " "), want)
	}
}

// A harness with no edit reader of its own showed none of the code a session
// changed. Copilot CLI's edit and create calls, read with the generic reader:
// the edit and the new file come back relative to the session's directory,
// and a view of a file — a path with content, the shape a write also has —
// does not count as one.
func TestChangesReadsAnotherHarnessesEditCalls(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COPILOT_ROOT", root)
	lines := copilotFixtureLines()[:4]
	lines = append(lines,
		`{"type":"tool.execution_start","data":{"toolCallId":"t1","toolName":"view","arguments":{"path":"/Users/x/coding/gateway/pool.go"}},"timestamp":"2026-07-20T05:13:59.000Z"}`,
		`{"type":"tool.execution_complete","data":{"toolCallId":"t1","result":{"path":"/Users/x/coding/gateway/pool.go","content":"READ ONLY"}},"timestamp":"2026-07-20T05:13:59.100Z"}`,
		`{"type":"tool.execution_start","data":{"toolCallId":"t2","toolName":"edit","arguments":{"path":"/Users/x/coding/gateway/pool.go","old_str":"MaxConns: 10","new_str":"MaxConns: 40"}},"timestamp":"2026-07-20T05:13:59.200Z"}`,
		`{"type":"tool.execution_start","data":{"toolCallId":"t3","toolName":"create","arguments":{"path":"/Users/x/coding/gateway/pool_test.go","file_text":"package gateway"}},"timestamp":"2026-07-20T05:13:59.300Z"}`,
		// Outside any call: a prompt quoting an edit and a patch, and a read
		// result whose object happens to be named like a write.
		`{"type":"system.message","data":{"role":"system","content":"{\"path\":\"/Users/x/coding/gateway/doc.go\",\"old_str\":\"EXAMPLE\",\"new_str\":\"EXAMPLE2\"}"},"timestamp":"2026-07-20T05:13:59.400Z"}`,
		`{"type":"system.message","data":{"role":"system","content":"Edit with *** Begin Patch\n*** Add File: example.go\n+EXAMPLE\n*** End Patch"},"timestamp":"2026-07-20T05:13:59.500Z"}`,
		`{"type":"tool.execution_complete","data":{"toolCallId":"t4","result":{"name":"create-user.ts","path":"/Users/x/coding/gateway/create-user.ts","content":"READ ONLY"}},"timestamp":"2026-07-20T05:13:59.600Z"}`,
		`{"type":"assistant.message","data":{"content":"raised the pool"},"timestamp":"2026-07-20T05:14:00.973Z"}`,
	)
	p := writeCopilotFixture(t, root, "7cf44517-55ca-435c-893a-3fde1973a44e", lines)
	changes := Changes(model.Session{Harness: "copilot", ID: "7cf44517-55ca-435c-893a-3fde1973a44e", Path: p})
	got := make([]string, 0, len(changes))
	for _, c := range changes {
		got = append(got, c.Path+"|"+c.Old+"|"+c.New)
	}
	if want := "pool.go|MaxConns: 10|MaxConns: 40 pool_test.go||package gateway"; strings.Join(got, " ") != want {
		t.Fatalf("got  %q\nwant %q", strings.Join(got, " "), want)
	}
}

// Qwen Code writes tool calls as functionCall parts with name and args; its
// edit and write_file calls come back through the generic reader.
func TestChangesReadsQwenEditCalls(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_QWEN_ROOT", root)
	p := filepath.Join(root, "projects", "-workspace-registry-demo", "chats", "registry-qwen-edit.jsonl")
	copyFixture(t, "fixtures/registry/qwen/projects/-workspace-registry-demo/chats/registry-qwen-edit.jsonl", p)
	changes := Changes(model.Session{Harness: "qwen", ID: "registry-qwen-edit", Path: p})
	got := make([]string, 0, len(changes))
	for _, c := range changes {
		got = append(got, c.Path+"|"+c.Old+"|"+c.New)
	}
	if want := "pool.go|MaxConns: 10|MaxConns: 40 pool_test.go||package demo"; strings.Join(got, " ") != want {
		t.Fatalf("got  %q\nwant %q", strings.Join(got, " "), want)
	}
}

// Gemini CLI's replace and write_file calls on toolCalls, read through the
// Gemini loader and the generic reader, with a failed replace ignored.
func TestChangesReadsGeminiReplaceAndWriteFileCalls(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_GEMINI_ROOT", root)
	p := filepath.Join(root, "tmp", "demo-project", "chats", "session-changes.jsonl")
	copyFixture(t, "fixtures/registry/gemini/session-changes.jsonl", p)
	ss, err := ParseGeminiFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("ParseGeminiFile: %v, %d sessions", err, len(ss))
	}
	changes := Changes(ss[0])
	got := make([]string, 0, len(changes))
	for _, c := range changes {
		got = append(got, c.Path+"|"+c.Old+"|"+c.New)
	}
	if want := "pool.go|MaxConns: 10|MaxConns: 40 pool_test.go||package demo"; strings.Join(got, " ") != want {
		t.Fatalf("got  %q\nwant %q", strings.Join(got, " "), want)
	}
}

// Changes reads a Claude Code transcript's edits back in order, relative to
// the directory the session ran in, every sub-edit of a MultiEdit included,
// and nothing from another session's lines in the same file.
func TestChangesReadsEditsBackFromTheTranscript(t *testing.T) {
	const id = "aaaa0001-1111-4000-8000-d6e7f8a9b0c1"
	lines := []string{
		`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/work/app/pkg/a.go","old_string":"x := 1","new_string":"x := 2"}}]}}`,
		`{"type":"assistant","sessionId":"other-session","cwd":"/work/app","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/work/app/pkg/b.go","old_string":"FOREIGN","new_string":"FOREIGN2"}}]}}`,
		`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","message":{"content":[{"type":"tool_use","name":"MultiEdit","input":{"file_path":"pkg/c.go","edits":[{"old_string":"one","new_string":"uno"},{"old_string":"two","new_string":"dos"}]}}]}}`,
		`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","message":{"content":[{"type":"tool_use","name":"Write","input":{"file_path":"/etc/hosts.bak","content":"elsewhere"}}]}}`,
		// A transcript written on Windows reads the same anywhere.
		`{"type":"assistant","sessionId":"` + id + `","cwd":"C:\\work\\app","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"C:\\work\\app\\pkg\\d.go","old_string":"a","new_string":"b"}}]}}`,
		`not json`,
	}
	path := filepath.Join(t.TempDir(), id+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Changes(model.Session{Harness: "claude", ID: id, Path: path})
	paths := make([]string, 0, len(got))
	for _, c := range got {
		paths = append(paths, c.Path+":"+c.New)
	}
	want := "pkg/a.go:x := 2|pkg/c.go:uno|pkg/c.go:dos|/etc/hosts.bak:elsewhere|pkg/d.go:b"
	if strings.Join(paths, "|") != want {
		t.Fatalf("got %v\nwant %s", paths, want)
	}
	// A file outside the project, or a scratch *_test.py created outside test
	// directories, is not a change to the project.
	got = append(got,
		Change{Path: "sub/foo_test.py", New: "assert True", Created: true},
		Change{Path: "tests/foo_test.py", Old: "1", New: "2"},
	)
	fin := FinalChanges(got)
	if len(fin) != 5 || fin[len(fin)-1].Path != "tests/foo_test.py" {
		t.Fatalf("final %v", fin)
	}
	if Changes(model.Session{Harness: "gemini", Path: path}) != nil {
		t.Fatal("a harness whose edits it cannot read gives nothing, not a guess")
	}

	// Antigravity CLI records edit_file, write_file, and write_to_file on
	// PLANNER_RESPONSE tool_calls with JSON-encoded or plain string arguments.
	agyLines := []string{
		`{"source":"USER_EXPLICIT","type":"USER_INPUT","content":"hello"}`,
		`{"source":"MODEL","type":"GENERIC","content":"mentioned \"edit_file\" in prose"}`,
		`{"source":"MODEL","type":"PLANNER_RESPONSE","tool_calls":[{"name":"run_command","args":{"CommandLine":"go test ./...","Cwd":"/work/app"}},{"name":"read_file","args":{"Path":"/work/app/pkg/a.go"}},{"name":"edit_file","args":{"Path":"\"/work/app/pkg/a.go\"","Target":"\"x := 1\"","Replacement":"\"x := 2\""}},{"name":"write_file","args":{"Path":"/work/app/pkg/b.go","Content":"package pkg\n"}},{"name":"write_to_file","args":{"TargetFile":"file:///work/app/pkg/c.go","CodeContent":"package pkg // c\n"}}]}`,
	}
	agyPath := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(agyPath, []byte(strings.Join(agyLines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	agyGot := Changes(model.Session{Harness: "antigravity", ID: id, Path: agyPath})
	if len(agyGot) != 3 || agyGot[0].Path != "pkg/a.go" || agyGot[0].Old != "x := 1" || agyGot[0].New != "x := 2" || !agyGot[1].Created || agyGot[1].Path != "pkg/b.go" || !agyGot[2].Created || agyGot[2].Path != "pkg/c.go" {
		t.Fatalf("antigravity Changes = %+v", agyGot)
	}
	if Changes(model.Session{Harness: "antigravity", ID: id, Path: filepath.Join(t.TempDir(), "missing.jsonl")}) != nil {
		t.Fatal("missing antigravity transcript should return nil")
	}
}

// Every WorkBuddy chat under one workspace root is one project (#4735); a
// folder a user named is still its own.
func TestChatFolderNamesItsWorkspaceRoot(t *testing.T) {
	for cwd, want := range map[string]string{
		"/Users/a/WorkBuddy AI/2026-10-05-15-06-37":            "a/WorkBuddy AI",
		"/Users/a/WorkBuddy AI/2026-10-05-15-06-37/":           "a/WorkBuddy AI",
		`C:\Users\a\WorkBuddy\2026-10-05-15-06-37`:             "a/WorkBuddy",
		"/Users/a/coding/deja-vu/.claude/worktrees/agent-a1b2": "coding/deja-vu",
		`C:\Users\a\deja-vu\.claude\worktrees\agent-a1b2`:      "a/deja-vu",
		"/Users/a/src/2026-10-05":                              "src/2026-10-05",
		"/Users/a/src/app":                                     "src/app",
		"/2026-10-05-15-06-37":                                 "2026-10-05-15-06-37",
		`C:\2026-10-05-15-06-37`:                               "2026-10-05-15-06-37",
	} {
		if got := cwdProjectName(cwd); got != want {
			t.Errorf("cwdProjectName(%q) = %q, want %q", cwd, got, want)
		}
	}
	if got := decodeProjectBase("-Users-a-goprojects-repo--claude-worktrees-pin-manifest"); got != "goprojects/repo" {
		t.Errorf("decodeProjectBase(worktree) = %q, want goprojects/repo", got)
	}
	wtRoot, ok := ChatWorkspaceRoot(filepath.FromSlash("/home/a/goprojects/deja-vu/.claude/worktrees/fix-5761"))
	if !ok || wtRoot != filepath.FromSlash("/home/a/goprojects/deja-vu") {
		t.Fatalf("ChatWorkspaceRoot(worktree) = %q %v", wtRoot, ok)
	}
	root, ok := ChatWorkspaceRoot(filepath.FromSlash("/home/a/WorkBuddy AI/2026-10-05-15-06-37"))
	if !ok || root != filepath.FromSlash("/home/a/WorkBuddy AI") {
		t.Fatalf("ChatWorkspaceRoot = %q %v", root, ok)
	}
	if _, ok := ChatWorkspaceRoot(filepath.FromSlash("/home/a/app")); ok {
		t.Fatal("a folder the user named was taken for a chat folder")
	}
	// A lone timestamp folder at a filesystem root has no root of its own.
	for _, cwd := range []string{"/2026-10-05-15-06-37", `C:\2026-10-05-15-06-37`} {
		if root, ok := ChatWorkspaceRoot(filepath.FromSlash(cwd)); ok {
			t.Errorf("ChatWorkspaceRoot(%q) = %q, want none", cwd, root)
		}
	}
}

// Cherry Studio writes Claude Code transcripts from a desktop app, and appends
// the same API call three or four times as the stream progresses: a new uuid
// each time, the same requestId and message id, the text growing. Read plainly
// that is one reply stored three times in prefixes — measured on this fixture
// before the collapse: "the advisory", "the advisory lock was", "the advisory
// lock was never released" (#3644).
const cherrySnapshotTranscript = `{"type":"user","sessionId":"cs-1","timestamp":"2026-07-17T09:00:00.000Z","cwd":"/work/api","message":{"role":"user","content":"why does the migration hang"}}
{"type":"assistant","uuid":"u1","requestId":"req-1","sessionId":"cs-1","timestamp":"2026-07-17T09:00:01.000Z","message":{"id":"msg-1","role":"assistant","content":[{"type":"text","text":"the advisory"}]}}
{"type":"assistant","uuid":"u2","requestId":"req-1","sessionId":"cs-1","timestamp":"2026-07-17T09:00:01.500Z","message":{"id":"msg-1","role":"assistant","content":[{"type":"text","text":"the advisory lock was"}]}}
{"type":"assistant","uuid":"u3","requestId":"req-1","sessionId":"cs-1","timestamp":"2026-07-17T09:00:02.000Z","message":{"id":"msg-1","role":"assistant","content":[{"type":"text","text":"the advisory lock was never released"}]}}
{"type":"assistant","uuid":"u4","requestId":"req-2","sessionId":"cs-1","timestamp":"2026-07-17T09:00:05.000Z","message":{"id":"msg-2","role":"assistant","content":[{"type":"text","text":"rerun the migration with a lock timeout"}]}}
`

func writeCherryStore(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	root := filepath.Join(home, "CherryStudio", "Data", "Agents", ".claude", "projects", "-work-api")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cs-1.jsonl"), []byte(cherrySnapshotTranscript), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CHERRYSTUDIO_ROOTS", filepath.Dir(root))
	return root
}

func TestCherryStudioCollapsesAStreamingRun(t *testing.T) {
	writeCherryStore(t)

	files := CherryStudioSessionFiles()
	if len(files) != 1 {
		t.Fatalf("files = %v", files)
	}
	ss := LoadCherryStudio()
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	s := ss[0]
	if s.Harness != "cherrystudio" {
		t.Errorf("harness = %q — a Cherry Studio session must not report as Claude Code", s.Harness)
	}
	var assistant []string
	for _, m := range s.Messages {
		if m.Role == "assistant" {
			assistant = append(assistant, m.Text)
		}
	}
	if len(assistant) != 2 {
		t.Fatalf("assistant messages = %d %q, want one per API call", len(assistant), assistant)
	}
	if assistant[0] != "the advisory lock was never released" {
		t.Errorf("the collapsed reply is %q, want the complete snapshot", assistant[0])
	}
	if assistant[1] != "rerun the migration with a lock timeout" {
		t.Errorf("the second call was folded into the first: %q", assistant[1])
	}
	// The user turn survives the collapse.
	if len(s.Messages) < 3 || s.Messages[0].Role != "user" {
		t.Errorf("the question is missing: %+v", s.Messages)
	}
}

// The same file read through the stock Claude parser keeps every snapshot,
// which is what makes the collapse a property of this store rather than a
// change to Claude Code's own reader.
func TestClaudeItselfIsUnchangedByTheCollapse(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "cs-1.jsonl")
	if err := os.WriteFile(p, []byte(cherrySnapshotTranscript), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseClaudeFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d", len(ss))
	}
	n := 0
	for _, m := range ss[0].Messages {
		if m.Role == "assistant" {
			n++
		}
	}
	if n != 4 {
		t.Errorf("the stock reader kept %d assistant records, want all four", n)
	}
	if ss[0].Harness != "claude" {
		t.Errorf("harness = %q, want claude", ss[0].Harness)
	}
}

// A stock Claude transcript must not be claimed by this harness, and a Cherry
// Studio one must not be claimed by Claude's kind.
func TestCherryStudioAndClaudeDoNotClaimEachOther(t *testing.T) {
	root := writeCherryStore(t)
	cherry := filepath.Join(root, "cs-1.jsonl")

	stockHome := t.TempDir()
	stock := filepath.Join(stockHome, ".claude", "projects", "-work-api")
	if err := os.MkdirAll(stock, 0o755); err != nil {
		t.Fatal(err)
	}
	stockFile := filepath.Join(stock, "s-1.jsonl")
	if err := os.WriteFile(stockFile, []byte(cherrySnapshotTranscript), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", stock)

	kinds := map[string]string{}
	for _, h := range Registry() {
		for _, k := range h.Kinds {
			for _, p := range []string{cherry, stockFile} {
				if k.Match(p) {
					kinds[p] += h.Name + ":" + k.Name + " "
				}
			}
		}
	}
	if !strings.Contains(kinds[cherry], "cherrystudio") {
		t.Errorf("the Cherry Studio transcript is claimed by %q", kinds[cherry])
	}
	if strings.Contains(kinds[stockFile], "cherrystudio") {
		t.Errorf("a stock Claude transcript is claimed by cherrystudio: %q", kinds[stockFile])
	}
}

// cherryHome points every app-dir lookup at a fresh home, so the default
// Cherry Studio data dir is a temp dir on every platform.
func cherryHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DEJA_CHERRYSTUDIO_ROOTS", "")
	return home
}

func copyFixture(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", from))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Cherry Studio runs agents on three runtimes and gives each its own store
// under Data/Agents: .claude, .pi/sessions and .dsh/sessions. Before the fix
// only the Claude one was read, so a Cherry agent on pi or dsh left nothing in
// the index (#4342).
func TestCherryStudioReadsItsPiAndDshAgents(t *testing.T) {
	cherryHome(t)
	agents := filepath.Join(cherryStudioAppDirs()[0], "Data", "Agents")
	pi := filepath.Join(agents, ".pi", "sessions", "2026-07-17T10-00-00-000Z_c0ffee00-0000-4000-8000-000000000002.jsonl")
	dsh := filepath.Join(agents, ".dsh", "sessions", "--work-pgbouncer-lab--", "session-c0ffee00-0000-4000-8000-000000000003", "session.jsonl")
	copyFixture(t, "fixtures/registry/pi/session.jsonl", pi)
	copyFixture(t, "fixtures/registry/deepseek/sessions/--work-pgbouncer-lab--/session-eaf5c9ac-0e47-4d2f-b982-8bae306062d1/session.jsonl", dsh)

	got := map[string]bool{}
	for _, s := range LoadCherryStudio() {
		if s.Harness != "cherrystudio" {
			t.Errorf("%s: harness = %q, want cherrystudio", s.Path, s.Harness)
		}
		if len(s.Messages) == 0 {
			t.Errorf("%s: no messages", s.Path)
		}
		got[s.Path] = true
	}
	if !got[pi] || !got[dsh] {
		t.Fatalf("read %v, want the pi and the dsh session", got)
	}
	// The incremental path must route each file to the same reader: the dsh
	// log would otherwise fall to the deepseek kind, which matches by name.
	for _, p := range []string{pi, dsh} {
		if k := KindForPath(p); !strings.HasPrefix(k, "cherrystudio") {
			t.Errorf("%s is claimed by kind %q", p, k)
		}
	}
}

// Cherry Studio lets a user move its data dir; the new place is kept in
// ~/.cherrystudio/boot-config.json under app.user_data_path, a map from the
// executable to the directory. Before the fix deja only looked at the default
// and found nothing after a move (#4347).
func TestCherryStudioFollowsAMovedDataDir(t *testing.T) {
	home := cherryHome(t)
	moved := filepath.Join(t.TempDir(), "moved-data")
	cfg := `{"app.disable_hardware_acceleration":false,"app.user_data_path":{"/Applications/Cherry Studio.app/Contents/MacOS/Cherry Studio":` + strconvQuote(moved) + `},"temp.user_data_relocation":null}`
	if err := os.MkdirAll(filepath.Join(home, ".cherrystudio"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".cherrystudio", "boot-config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(moved, "Data", "Agents", ".claude", "projects", "-work-api", "cs-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(cherrySnapshotTranscript), 0o644); err != nil {
		t.Fatal(err)
	}
	files := CherryStudioSessionFiles()
	if len(files) != 1 || files[0] != p {
		t.Fatalf("files = %v, want the transcript in the moved dir", files)
	}
	if k := KindForPath(p); k != "cherrystudio" {
		t.Errorf("kind = %q, want cherrystudio", k)
	}
}

func strconvQuote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// Cherry Studio runs dsh with DSH_HOME inside its data dir, so a deja it starts
// inherits a DeepSeek root that is Cherry's store. The log belongs to Cherry
// Studio alone; listed by deepseek too, it was counted under both (#4342).
func TestCherryStudioDshLogIsNotAlsoDeepSeeks(t *testing.T) {
	cherryHome(t)
	dshHome := filepath.Join(cherryStudioAppDirs()[0], "Data", "Agents", ".dsh")
	log := filepath.Join(dshHome, "sessions", "--work-pgbouncer-lab--", "session-c0ffee00-0000-4000-8000-000000000003", "session.jsonl")
	copyFixture(t, "fixtures/registry/deepseek/sessions/--work-pgbouncer-lab--/session-eaf5c9ac-0e47-4d2f-b982-8bae306062d1/session.jsonl", log)
	t.Setenv("DEJA_DEEPSEEK_ROOT", "")
	t.Setenv("DSH_HOME", dshHome)

	if !slices.Contains(CherryStudioSessionFiles(), log) {
		t.Fatalf("Cherry Studio does not list its own dsh log, so this measures nothing")
	}
	if got := DeepSeekSessionFiles(); slices.Contains(got, log) {
		t.Errorf("deepseek lists Cherry Studio's dsh log too: %q", got)
	}
}

// A data dir moved to the home directory makes the legacy <dir>/.claude/projects
// root Claude Code's own store. Cherry Studio must not list those sessions as
// its own (#4347).
func TestCherryStudioMovedToHomeLeavesClaudesStoreAlone(t *testing.T) {
	home := cherryHome(t)
	t.Setenv("DEJA_CLAUDE_ROOT", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	stock := filepath.Join(home, ".claude", "projects", "-work-api", "s-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(stock), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stock, []byte(cherrySnapshotTranscript), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".cherrystudio"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := mustJSON(t, map[string]any{"app.user_data_path": map[string]string{"/Applications/Cherry Studio.app/Contents/MacOS/Cherry Studio": home}})
	if err := os.WriteFile(filepath.Join(home, ".cherrystudio", "boot-config.json"), cfg, 0o644); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(ClaudeFiles(), stock) {
		t.Fatalf("Claude does not list its own transcript, so this measures nothing")
	}
	if got := CherryStudioSessionFiles(); slices.Contains(got, stock) {
		t.Errorf("Cherry Studio lists Claude Code's own transcript: %q", got)
	}
}

// A move can leave the old data dir behind. The directory the app runs from
// comes first, so a reader that takes one file of the app's — its database,
// for doctor — reads the live one rather than the copy left at the default
// (#4347).
func TestCherryStudioMovedDirComesBeforeTheDefault(t *testing.T) {
	home := cherryHome(t)
	moved := filepath.Join(home, "elsewhere", "CherryStudio")
	if err := os.MkdirAll(filepath.Join(home, ".cherrystudio"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := mustJSON(t, map[string]any{"app.user_data_path": map[string]string{"/Applications/Cherry Studio.app/Contents/MacOS/Cherry Studio": moved}})
	if err := os.WriteFile(filepath.Join(home, ".cherrystudio", "boot-config.json"), cfg, 0o644); err != nil {
		t.Fatal(err)
	}
	dirs := cherryStudioAppDirs()
	if len(dirs) != 2 || dirs[0] != moved || dirs[1] != cherryStudioDefaultDir() {
		t.Errorf("app dirs = %q, want the moved one, then the default", dirs)
	}
}

// A tail that carries more snapshots of the call the stored part ended on
// rewrites a stored reply and cannot be appended; a tail that starts a new
// call can (#4346).
func TestCherryStudioResumesOnlyOnANewCall(t *testing.T) {
	lines := strings.SplitAfter(cherrySnapshotTranscript, "\n")
	p := filepath.Join(t.TempDir(), "cs-1.jsonl")
	if err := os.WriteFile(p, []byte(cherrySnapshotTranscript), 0o644); err != nil {
		t.Fatal(err)
	}
	at := func(n int) int64 { return int64(len(strings.Join(lines[:n], ""))) }
	if CherryStudioResumes(p, at(2)) {
		t.Error("a tail continuing req-1 was allowed to append to its half reply")
	}
	if !CherryStudioResumes(p, at(4)) {
		t.Error("a tail starting req-2 was refused")
	}
	if !CherryStudioResumes(p, at(1)) {
		t.Error("a tail after the user line was refused")
	}
}

// Resumes reads the tail only up to the first line that names a call. A
// stream's snapshots run back to back, so once another call starts the stored
// one is done, and reading on cost a search the whole tail before the inline
// append cap could hand it off (#4346).
func TestCherryStudioResumesStopsAtTheNextCall(t *testing.T) {
	lines := strings.SplitAfter(cherrySnapshotTranscript, "\n")
	at := int64(len(strings.Join(lines[:2], "")))
	// After the stored half of req-1: a new call, then a stray req-1 line the
	// check must not reach.
	body := strings.Join(lines[:2], "") + lines[4] + lines[1]
	p := filepath.Join(t.TempDir(), "cs-1.jsonl")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if !CherryStudioResumes(p, at) {
		t.Error("read past the call that started after the stored one")
	}
}

const testClaudeMemSchema = `
create table sdk_sessions (
  sdk_session_id text primary key,
  claude_session_id text,
  project text,
  created_at_epoch integer,
  completed_at_epoch integer,
  last_activity_epoch integer
);

create table user_prompts (
  id integer primary key autoincrement,
  sdk_session_id text,
  prompt_text text,
  created_at_epoch integer
);

create table observations (
  id integer primary key autoincrement,
  sdk_session_id text,
  title text,
  subtitle text,
  narrative text,
  text text,
  facts text,
  files_modified text,
  created_at_epoch integer
);

create table session_summaries (
  id integer primary key autoincrement,
  sdk_session_id text,
  request text,
  investigated text,
  learned text,
  completed text,
  next_steps text,
  notes text,
  created_at_epoch integer
);
`

func createTestClaudeMemDB(t *testing.T, sql string) string {
	t.Helper()
	if !SQLite3Available() {
		t.Skip("sqlite3 CLI not available")
	}
	db := filepath.Join(t.TempDir(), "claude-mem.db")
	cmd := exec.CommandContext(t.Context(), "sqlite3", db)
	cmd.Stdin = strings.NewReader(testClaudeMemSchema + sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create claude-mem sqlite fixture: %v: %s", err, out)
	}
	return db
}

func TestClaudeMemDBIngestsObservationsAndSummaries(t *testing.T) {
	sql := `
insert into sdk_sessions values (
  'sdk-abc-123',
  'claude-xyz-789',
  '/Users/alice/projects/demo-proj',
  1700000000,
  1700000100,
  1700000200
);

insert into user_prompts values (
  1,
  'sdk-abc-123',
  'how do we parse claude-mem sqlite database',
  1700000010
);

insert into observations values (
  1,
  'sdk-abc-123',
  'SQLite Ingestion Title',
  'Subtitle finding',
  'Observed that observations table holds observations',
  'Detailed observation text body',
  'Fact 1: uses sqlite3 CLI',
  '["internal/sources/claudemem.go", "internal/sources/claude.go"]',
  1700000050
);

insert into session_summaries values (
  1,
  'sdk-abc-123',
  'Build claude-mem ingestion',
  'Investigated schema and tables',
  'Learned observation and summary shapes',
  'Completed implementation of claudemem.go',
  'Next steps write tests',
  'Notes: verified against live schema',
  1700000100
);
`
	db := createTestClaudeMemDB(t, sql)
	t.Setenv("DEJA_CLAUDE_MEM_DB", db)

	if !IsClaudeMemDB(db) {
		t.Fatalf("IsClaudeMemDB(%q) = false, want true", db)
	}
	if !UnderClaudeRoot(db) {
		t.Fatalf("UnderClaudeRoot(%q) = false, want true", db)
	}
	if !slices.Contains(ClaudeFiles(), db) {
		t.Fatalf("ClaudeFiles() does not contain %q", db)
	}

	sessions := LoadClaude()
	var found *model.Session
	for i := range sessions {
		if sessions[i].ID == "cm-sdk-abc-123" {
			found = &sessions[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("LoadClaude() did not return session cm-sdk-abc-123, got: %v", sessions)
	}

	if found.Harness != "claude" {
		t.Errorf("Harness = %q, want %q", found.Harness, "claude")
	}
	if found.Project != "projects/demo-proj" {
		t.Errorf("Project = %q, want %q", found.Project, "projects/demo-proj")
	}
	if found.Title != "Build claude-mem ingestion" {
		t.Errorf("Title = %q, want %q", found.Title, "Build claude-mem ingestion")
	}

	var hasPrompt, hasObservation, hasSummary, hasEdit bool
	for _, m := range found.Messages {
		if m.Role == "user" && strings.Contains(m.Text, "how do we parse claude-mem sqlite database") {
			hasPrompt = true
		}
		if m.Role == "assistant" && strings.Contains(m.Text, "SQLite Ingestion Title") && strings.Contains(m.Text, "Detailed observation text body") {
			hasObservation = true
		}
		if m.Role == "assistant" && strings.Contains(m.Text, "Investigated schema and tables") && strings.Contains(m.Text, "Completed implementation of claudemem.go") {
			hasSummary = true
		}
		if m.Role == RoleEdit && (m.Text == "internal/sources/claudemem.go" || m.Text == "internal/sources/claude.go") {
			hasEdit = true
		}
	}

	if !hasPrompt {
		t.Errorf("missing user prompt in messages: %v", found.Messages)
	}
	if !hasObservation {
		t.Errorf("missing observation assistant message: %v", found.Messages)
	}
	if !hasSummary {
		t.Errorf("missing summary assistant message: %v", found.Messages)
	}
	if !hasEdit {
		t.Errorf("missing RoleEdit message from files_modified: %v", found.Messages)
	}

	// Test incremental / watermark query
	sinceOld, err := ParseClaudeMemDBSince(db, time.Unix(1700000100, 0))
	if err != nil {
		t.Fatalf("ParseClaudeMemDBSince err: %v", err)
	}
	if len(sinceOld) == 0 {
		t.Fatalf("ParseClaudeMemDBSince before last_activity returned 0 sessions")
	}

	sinceNew, err := ParseClaudeMemDBSince(db, time.Unix(1700000500, 0))
	if err != nil {
		t.Fatalf("ParseClaudeMemDBSince err: %v", err)
	}
	if len(sinceNew) != 0 {
		t.Fatalf("ParseClaudeMemDBSince after last_activity returned %d sessions, want 0", len(sinceNew))
	}
}

const clawCodeCall = `{"type":"message","message":{"role":"assistant","blocks":[{"type":"tool_use","id":"call-cc-1","name":"bash","input":"{\"command\":\"go test ./...\"}"}]}}` + "\n"

func clawCodeResult(outputJSON string, isError bool) string {
	b, err := json.Marshal(outputJSON)
	if err != nil {
		panic(err)
	}
	errFlag := "false"
	if isError {
		errFlag = "true"
	}
	return `{"type":"message","message":{"role":"tool","blocks":[{"type":"tool_result","tool_use_id":"call-cc-1","tool_name":"bash","output":` + string(b) + `,"is_error":` + errFlag + `}]}}` + "\n"
}

func clawCodeExitStore(outputJSON string, isError bool) func(*testing.T, string) string {
	return func(t *testing.T, dir string) string {
		t.Helper()
		return writeExitFixture(t, filepath.Join(dir, "cc-exit.jsonl"),
			`{"type":"session_meta","session_id":"cc-exit","workspace_root":"/workspace/claw-demo","created_at_ms":1759305600000}`+"\n"+
				`{"type":"message","message":{"role":"user","blocks":[{"type":"text","text":"run tests"}]}}`+"\n"+
				clawCodeCall+
				clawCodeResult(outputJSON, isError))
	}
}

func TestParseClawCodeJSONLAndTools(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess-1.jsonl")

	// prompt_history duplicates the first user prompt text; only its timestamp
	// should carry over without emitting a second user record.
	lines := []string{
		`{"type":"session_meta","session_id":"sess-1","workspace_root":"/workspace/claw-demo","created_at_ms":1759305600000}`,
		`{"type":"prompt_history","prompt":"fix retry backoff","timestamp_ms":1759305601000}`,
		`{"type":"message","message":{"role":"user","blocks":[{"type":"text","text":"fix retry backoff"}]}}`,
		`{"type":"message","message":{"role":"assistant","blocks":[{"type":"text","text":"Checking retry logic"},{"type":"tool_use","id":"t1","name":"read_file","input":"{\"path\":\"/workspace/claw-demo/retry.go\"}"},{"type":"tool_use","id":"t2","name":"write_file","input":"{\"path\":\"/workspace/claw-demo/new.go\",\"content\":\"package demo\\nfunc clawDemoHandler() string { return \\\"ok\\\" }\\n\"}"},{"type":"tool_use","id":"t3","name":"edit_file","input":"{\"path\":\"/workspace/claw-demo/retry.go\",\"old_string\":\"1s\",\"new_string\":\"2s\"}"},{"type":"tool_use","id":"t4","name":"bash","input":"{\"command\":\"go test ./...\"}"}]}}`,
		`{"type":"message","message":{"role":"tool","blocks":[{"type":"tool_result","tool_use_id":"t4","tool_name":"bash","output":"{\"stdout\":\"ok\",\"stderr\":\"\",\"interrupted\":false}","is_error":false}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	sessions, err := ParseClawCodeFile(path)
	if err != nil {
		t.Fatalf("ParseClawCodeFile: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	s := sessions[0]
	if s.Harness != "clawcode" || s.Project != "workspace/claw-demo" || s.ID != "sess-1" {
		t.Fatalf("unexpected metadata: %+v", s)
	}
	if got := ClawCodeSessionDir(path); got != "/workspace/claw-demo" {
		t.Fatalf("ClawCodeSessionDir() = %q, want /workspace/claw-demo", got)
	}

	byRole := map[string][]string{}
	for _, m := range s.Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
		if m.Time.IsZero() {
			t.Fatalf("message role %q missing timestamp", m.Role)
		}
	}
	if got := len(byRole["user"]); got != 1 {
		t.Fatalf("user messages = %d, want 1 (prompt_history must not duplicate)", got)
	}
	if cmds := strings.Join(byRole[RoleCommand], "\n"); !strings.Contains(cmds, "$ go test ./...") {
		t.Fatalf("command records = %v, want $ go test ./...", byRole[RoleCommand])
	}
	if outs := strings.Join(byRole[RoleToolOutput], "\n"); !strings.Contains(outs, "ok") {
		t.Fatalf("tool output = %v, want ok", byRole[RoleToolOutput])
	}
	files := strings.Join(byRole[RoleFiles], "\n")
	for _, wantFile := range []string{"/workspace/claw-demo/retry.go", "/workspace/claw-demo/new.go"} {
		if !strings.Contains(files, wantFile) {
			t.Fatalf("files %q missing %q", files, wantFile)
		}
	}
	if wrote := strings.Join(byRole[RoleWrote], "\n"); !strings.Contains(wrote, "/workspace/claw-demo/new.go") {
		t.Fatalf("wrote = %q, want new.go", wrote)
	}
	if edits := strings.Join(byRole[RoleEdit], "\n"); !strings.Contains(edits, "1s") {
		t.Fatalf("edits = %q, want 1s", edits)
	}
}

func TestParseClawCodeLegacyJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.json")
	payload := `{
		"version": 1,
		"workspace_root": "/workspace/claw-legacy",
		"created_at_ms": 1759305600000,
		"messages": [
			{"role": "user", "blocks": [{"type": "text", "text": "check ip 203.0.113.10"}]},
			{"role": "assistant", "blocks": [{"type": "tool_use", "id": "b1", "name": "bash", "input": "{\"command\":\"go version\"}"}]}
		]
	}`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}

	sessions, err := ParseClawCodeFile(path)
	if err != nil {
		t.Fatalf("ParseClawCodeFile legacy: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Project != "workspace/claw-legacy" {
		t.Fatalf("unexpected legacy sessions: %+v", sessions)
	}
	if got := ClawCodeSessionDir(path); got != "/workspace/claw-legacy" {
		t.Fatalf("ClawCodeSessionDir legacy = %q, want /workspace/claw-legacy", got)
	}

	nativePath := filepath.Join(dir, "native.json")
	nativeBody, err := json.Marshal(map[string]any{
		"version":        1,
		"workspace_root": dir,
		"messages":       []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nativePath, nativeBody, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ClawCodeSessionDir(nativePath); got != filepath.Clean(dir) {
		t.Fatalf("ClawCodeSessionDir native = %q, want %q", got, filepath.Clean(dir))
	}

	relPath := filepath.Join(dir, "rel.json")
	if err := os.WriteFile(relPath, []byte(`{"version":1,"workspace_root":"relative/dir","messages":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ClawCodeSessionDir(relPath); got != "" {
		t.Fatalf("ClawCodeSessionDir relative = %q, want empty string", got)
	}
}

func TestClawCodeDiscoveryAndFilters(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_CLAWCODE_ROOT", "")
	t.Setenv("DEJA_CLAWCODE_ROOTS", "")
	t.Setenv("CLAW_CONFIG_HOME", "")

	cfgHome := filepath.Join(home, "custom-claw")
	t.Setenv("CLAW_CONFIG_HOME", cfgHome)
	if got := ClawCodeConfigDir(); got != cfgHome {
		t.Fatalf("ClawCodeConfigDir() = %q, want %q", got, cfgHome)
	}
	if wantRoot := filepath.Join(cfgHome, "sessions"); ClawCodeRoot() != wantRoot {
		t.Fatalf("ClawCodeRoot() = %q, want %q", ClawCodeRoot(), wantRoot)
	}

	sessDir := filepath.Join(cfgHome, "sessions")
	extraRoot := filepath.Join(home, "extra-claw")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(extraRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAWCODE_ROOTS", extraRoot)

	if roots := ClawCodeRoots(); len(roots) != 2 {
		t.Fatalf("ClawCodeRoots() = %v, want 2 roots", roots)
	}

	good1 := filepath.Join(sessDir, "a.jsonl")
	good2 := filepath.Join(extraRoot, "b.json")
	meta := filepath.Join(sessDir, "a.meta.json")
	deleted := filepath.Join(sessDir, "old.deleted.json")

	validLine := `{"type":"message","message":{"role":"user","blocks":[{"type":"text","text":"hi"}]}}` + "\n"
	legacyBody := `{"messages":[{"role":"user","blocks":[{"type":"text","text":"legacy"}]}]}`
	for path, body := range map[string]string{
		good1:   validLine,
		good2:   legacyBody,
		meta:    `{"session_id":"a"}`,
		deleted: legacyBody,
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if !ClawCodeUnderRoot(good1) || !ClawCodeUnderRoot(good2) {
		t.Fatalf("ClawCodeUnderRoot should match %q and %q", good1, good2)
	}
	if ClawCodeUnderRoot(meta) || ClawCodeUnderRoot(deleted) {
		t.Fatalf("ClawCodeUnderRoot must skip .meta.json and .deleted.json")
	}

	files := ClawCodeSessionFiles()
	joined := strings.Join(files, "\n")
	if !strings.Contains(joined, good1) || !strings.Contains(joined, good2) {
		t.Fatalf("ClawCodeSessionFiles() missing expected files: %v", files)
	}
	if strings.Contains(joined, ".meta.json") || strings.Contains(joined, ".deleted.json") {
		t.Fatalf("ClawCodeSessionFiles() included skipped metadata/deleted file: %v", files)
	}

	loaded := LoadClawCode()
	if len(loaded) != 2 {
		t.Fatalf("LoadClawCode() = %d sessions, want 2", len(loaded))
	}

	t.Setenv("DEJA_CLAWCODE_ROOT", sessDir)
	if got := ClawCodeRoots(); len(got) != 1 || got[0] != sessDir {
		t.Fatalf("DEJA_CLAWCODE_ROOT override = %v, want [%s]", got, sessDir)
	}
}

func TestParseClawCodeFileFromOffsetAndResumes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stream.jsonl")
	head := `{"type":"session_meta","session_id":"stream","workspace_root":"/workspace/claw-demo","created_at_ms":1759305600000}` + "\n" +
		`{"type":"message","message":{"role":"user","blocks":[{"type":"text","text":"first"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(head), 0o600); err != nil {
		t.Fatal(err)
	}
	offset := int64(len(head))

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	tail := `{"type":"message","message":{"role":"assistant","blocks":[{"type":"text","text":"second answer"}]}}` + "\n"
	if _, werr := f.WriteString(tail); werr != nil {
		_ = f.Close()
		t.Fatal(werr)
	}
	if cerr := f.Close(); cerr != nil {
		t.Fatal(cerr)
	}

	if !clawCodeResumes(path, offset) {
		t.Fatalf("clawCodeResumes on plain assistant tail = false, want true")
	}

	sessions, err := ParseClawCodeFileFromOffset(path, offset)
	if err != nil {
		t.Fatalf("ParseClawCodeFileFromOffset: %v", err)
	}
	if len(sessions) != 1 || len(sessions[0].Messages) != 1 || sessions[0].Messages[0].Text != "second answer" {
		t.Fatalf("unexpected incremental sessions: %+v", sessions)
	}
	if sessions[0].ID != "stream" || sessions[0].Project != "workspace/claw-demo" {
		t.Fatalf("incremental session lost header metadata: %+v", sessions[0])
	}
}

// Roo Code and Cline append an <environment_details> block — visible files,
// open tabs, the clock, the cost, the mode, a workspace listing — to every
// user turn. It is the host's, not the person's, and it went into the index
// inside the user message (#3255).
func TestRooEnvironmentDetailsIsNotTheUsersText(t *testing.T) {
	env := "<environment_details>\n# VSCode Visible Files\ninternal/retry/retry.go\n\n# Current Time\n" +
		"Current time in ISO 8601 UTC format: 2026-09-08T01:33:20.000Z\n\n# Current Mode\n<slug>code</slug>\n<name>Code</name>\n</environment_details>"
	turns := []map[string]any{
		{"role": "user", "content": []map[string]string{
			{"type": "text", "text": "<task>\nfix the flaky vantrell retry\n</task>"},
			{"type": "text", "text": env},
		}},
		{"role": "assistant", "content": []map[string]string{{"type": "text", "text": "pinned the deadline per attempt"}}},
		{"role": "user", "content": []map[string]string{
			{"type": "text", "text": "also cover it with a test"},
			{"type": "text", "text": env},
		}},
	}
	dir := filepath.Join(t.TempDir(), "tasks", "1757300000001")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b := mustJSON(t, turns)
	path := filepath.Join(dir, "api_conversation_history.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseRooTask(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	want := []string{"fix the flaky vantrell retry", "pinned the deadline per attempt", "also cover it with a test"}
	if len(ss[0].Messages) != len(want) {
		t.Fatalf("messages = %+v", ss[0].Messages)
	}
	for i, m := range ss[0].Messages {
		if m.Text != want[i] {
			t.Errorf("message %d = %q, want %q", i, m.Text, want[i])
		}
	}
}

// A person's sentence about the block stays; only the block itself goes.
func TestUnwrapClineTaskKeepsWordsAroundTheBlock(t *testing.T) {
	got := unwrapClineTask("<task>\nwhy is <environment_details> in my history\n</task>\n<environment_details>\n# Current Cost\n$0.12\n</environment_details>")
	if got != "why is <environment_details> in my history" {
		t.Errorf("got %q", got)
	}
}

// A store written with CRLF carries the block with \r before every \n.
func TestUnwrapClineTaskDropsACRLFBlock(t *testing.T) {
	got := unwrapClineTask("<task>\r\nfix the build\r\n</task>\r\n<environment_details>\r\n# Current Cost\r\n$0.12\r\n</environment_details>\r\n")
	if got != "fix the build" {
		t.Errorf("got %q", got)
	}
}

// Roo Code and Cline append an <environment_details> block to every user turn —
// visible files, open tabs, the clock, the running cost, the mode, a workspace
// listing — and deja indexed it inside the person's message. So a query on
// "files", "current" or any open tab's path hit every Roo and Cline session,
// and ctx printed the listing where the reader's own question goes (#3255).
func TestClineDropsTheHostsBlockFromAUserTurn(t *testing.T) {
	const question = "fix the flaky vantrell retry: the second attempt reuses the first timeout"
	env := "<environment_details>\n# VSCode Visible Files\ninternal/retry/retry.go\n" +
		"# VSCode Open Tabs\ninternal/retry/retry.go\n" +
		"# Current Time\nCurrent time in ISO 8601 UTC format: 2026-09-08T01:33:20.000Z\n" +
		"# Current Cost\n$0.12\n# Current Mode\n<slug>code</slug>\n</environment_details>"

	for _, tc := range []struct{ name, in string }{
		// Roo appends the block after the task text, in the same turn.
		{"after the text", question + "\n" + env},
		// And inside the <task> envelope, which is where Cline puts it.
		{"inside the task envelope", "<task>\n" + question + "\n" + env + "\n</task>"},
		{"before the text", env + "\n" + question},
	} {
		got := unwrapClineTask(tc.in)
		if got != question {
			t.Errorf("%s: user turn =\n  %q\nwant\n  %q", tc.name, got, question)
		}
	}

	// An unterminated block — the listing cut mid-write — takes the rest with
	// it rather than leaving half a listing in the message.
	if got := unwrapClineTask(question + "\n<environment_details>\n# VSCode Visible"); got != question {
		t.Errorf("an unterminated block survived: %q", got)
	}

	// Two blocks in one turn, which a resumed task carries.
	if got := unwrapClineTask(env + "\n" + question + "\n" + env); got != question {
		t.Errorf("a second block survived: %q", got)
	}

	// What the person wrote is untouched, including a message that talks about
	// the block or opens with a path.
	for _, mine := range []string{
		"why is environment_details in my context",
		"/etc/hosts is wrong on the build box",
		question,
	} {
		if got := unwrapClineTask(mine); got != mine {
			t.Errorf("the reader's message changed: %q -> %q", mine, got)
		}
	}
}

// The same through the parser, since the block lands in the store: it has to be
// gone from the message every surface reads, not only from the unwrapper.
func TestRooTaskIndexesTheQuestionWithoutTheListing(t *testing.T) {
	const question = "fix the flaky vantrell retry"
	dir := filepath.Join(t.TempDir(), "tasks", "1767225700000")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	turns := []map[string]any{
		{"role": "user", "content": question +
			"\n<environment_details>\n# Current Cost\n$0.12\n</environment_details>"},
		{"role": "assistant", "content": "the second attempt reuses the deadline, not the timeout"},
	}
	b, err := json.Marshal(turns)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "api_conversation_history.json")
	if err = os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}

	ss, err := ParseRooTask(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d", len(ss))
	}
	for _, m := range ss[0].Messages {
		if strings.Contains(m.Text, "environment_details") || strings.Contains(m.Text, "Current Cost") {
			t.Errorf("the host's block reached the index as %s: %q", m.Role, m.Text)
		}
	}
	found := false
	for _, m := range ss[0].Messages {
		if strings.TrimSpace(m.Text) == question {
			found = true
		}
	}
	if !found {
		t.Errorf("the question did not survive: %#v", ss[0].Messages)
	}
}

func TestParseClineModernSession(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	path := filepath.Join(root, "fixtures", "registry", "cline", "modern", "sessions", "session_synthetic_01", "session_synthetic_01.messages.json")
	ss, err := ParseClineFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d", len(ss))
	}
	s := ss[0]
	if s.ID != "session_synthetic_01" || s.Harness != "cline" {
		t.Fatalf("identity wrong: %+v", s)
	}
	if s.Title != "Synthetic parser example" {
		t.Fatalf("title = %q", s.Title)
	}
	if s.Project == "cline" || s.Project == "" {
		t.Fatalf("project must derive from cwd, got %q", s.Project)
	}
	if len(s.Messages) != 2 {
		t.Fatalf("messages = %d", len(s.Messages))
	}
	if s.Messages[1].Text != "Hello!" {
		t.Fatalf("thinking block must not leak: %q", s.Messages[1].Text)
	}
}

func TestParseClineModernSkipsNonLeadAgents(t *testing.T) {
	dir := t.TempDir()
	sess := filepath.Join(dir, "session_x")
	if err := os.MkdirAll(sess, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(sess, "session_x.messages.json")
	data := `{"version":1,"agent":"subagent-3","sessionId":"session_x","messages":[{"role":"user","content":[{"type":"text","text":"internal"}],"ts":1767225600000}]}`
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseClineFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 0 {
		t.Fatalf("non-lead agent transcript must be skipped, got %d sessions", len(ss))
	}
}

func TestParseClineLegacyTask(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	path := filepath.Join(root, "fixtures", "registry", "cline", "legacy", "tasks", "1767225600000", "api_conversation_history.json")
	ss, err := ParseClineFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d", len(ss))
	}
	s := ss[0]
	if s.ID != "cline-task-1767225600000" {
		t.Fatalf("id = %q", s.ID)
	}
	if s.Title != "Say hello" {
		t.Fatalf("title from taskHistory wrong: %q", s.Title)
	}
	if s.Messages[0].Text != "Say hello" {
		t.Fatalf("task envelope must be unwrapped: %q", s.Messages[0].Text)
	}
	if s.Started.Year() != 2025 && s.Started.Year() != 2026 {
		t.Fatalf("timestamp not from taskHistory: %v", s.Started)
	}
}

func TestClineSessionFilesHonorsOverrides(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	t.Setenv("DEJA_CLINE_ROOT", filepath.Join(root, "fixtures", "registry", "cline", "modern", "sessions"))
	t.Setenv("DEJA_CLINE_ROOTS", filepath.Join(root, "fixtures", "registry", "cline", "legacy"))
	files := ClineSessionFiles()
	if len(files) != 2 {
		t.Fatalf("files = %v", files)
	}
	ss := LoadCline()
	if len(ss) != 2 {
		t.Fatalf("sessions = %d", len(ss))
	}
}

// The roots are what doctor walks and what it names, so they have to be the
// tasks trees themselves rather than the extension roots holding them (#3399).
func TestClineStoreRootsNamesEachTasksTree(t *testing.T) {
	tmp := t.TempDir()
	sessions := filepath.Join(tmp, "cline", "data", "sessions")
	one := filepath.Join(tmp, "vscode", "globalStorage", "saoudrizwan.claude-dev")
	two := filepath.Join(tmp, "cursor", "globalStorage", "saoudrizwan.claude-dev")
	for _, d := range []string{one, two} {
		if err := os.MkdirAll(filepath.Join(d, "tasks"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_CLINE_ROOT", sessions)
	t.Setenv("DEJA_CLINE_ROOTS", one+string(os.PathListSeparator)+two)

	got := ClineStoreRoots()
	want := []string{sessions, filepath.Join(one, "tasks"), filepath.Join(two, "tasks")}
	if len(got) != len(want) {
		t.Fatalf("roots = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("root %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// Cline's legacy extension stamps `ts: Date.now()` on the user turns and the
// completed assistant turns it appends to api_conversation_history.json, and
// taskHistory.json's ts is when the task was last touched. The roo fix (#4420)
// for the same store shape.
func TestClineLegacyTurnsKeepTheirOwnTime(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "tasks", "1759300000000")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	at := func(hm string) time.Time {
		v, err := time.Parse(time.RFC3339, "2026-10-01T"+hm+":00Z")
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	ms := func(hm string) string { return itoa64(at(hm).UnixMilli()) }
	body := `[
	 {"role":"user","ts":` + ms("08:00") + `,"content":[{"type":"text","text":"<task>\nwhy does the retry loop spin\n</task>"}]},
	 {"role":"assistant","ts":` + ms("08:10") + `,"content":[{"type":"text","text":"Checking the loop."},{"type":"tool_use","id":"t1","name":"execute_command","input":{"command":"go test ./retry/..."}}]},
	 {"role":"user","ts":` + ms("08:20") + `,"content":[{"type":"tool_result","tool_use_id":"t1","content":"FAIL retry_test.go:12"}]},
	 {"role":"assistant","content":[{"type":"text","text":"No ts on this one."}]},
	 {"role":"assistant","ts":` + ms("09:00") + `,"content":[{"type":"text","text":"The cap was never applied."}]}
	]`
	path := filepath.Join(dir, "api_conversation_history.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	history := `[{"id":"1759300000000","ts":` + ms("09:00") + `,"task":"why does the retry loop spin"}]`
	if err := os.WriteFile(filepath.Join(root, "state", "taskHistory.json"), []byte(history), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseClineFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	want := map[string]time.Time{
		"why does the retry loop spin": at("08:00"),
		"Checking the loop.":           at("08:10"),
		"$ go test ./retry/...":        at("08:10"),
		"FAIL retry_test.go:12":        at("08:20"),
		// A turn without its own ts keeps the old scheme.
		"No ts on this one.":         at("09:00").Add(3 * time.Second),
		"The cap was never applied.": at("09:00"),
	}
	for _, m := range ss[0].Messages {
		w, ok := want[m.Text]
		if !ok {
			continue
		}
		if !m.Time.Equal(w) {
			t.Errorf("%q at %s, want %s", m.Text, m.Time.UTC().Format(time.RFC3339), w.Format(time.RFC3339))
		}
		delete(want, m.Text)
	}
	if len(want) != 0 {
		t.Errorf("messages not found: %v", want)
	}
	if !ss[0].Started.Equal(at("08:00")) {
		t.Errorf("started %s, want 08:00", ss[0].Started.UTC())
	}
}

// One session that works, in the shapes cline's own tool schemas declare:
// `run_commands` takes a list under `commands`, `read_files` a list of read
// requests under `files`, and the editor names the replaced text `old_text`.
// All three differ from every other harness, and reading the singular keys
// indexed none of it.
const clineWorkSession = `{"version":1,"agent":"lead","sessionId":"session_w","messages":[
{"role":"user","content":[{"type":"text","text":"the build is broken"}],"ts":1767225600000},
{"role":"assistant","content":[
  {"type":"text","text":"looking at it"},
  {"type":"tool_use","id":"c1","name":"run_commands","input":{"commands":["go vet ./...","go test ./..."]}}
],"ts":1767225601000},
{"role":"user","content":[
  {"type":"tool_result","tool_use_id":"c1","content":"pattern ./...: directory prefix . does not contain main module"}
],"ts":1767225602000},
{"role":"assistant","content":[
  {"type":"tool_use","id":"c2","name":"read_files","input":{"files":[{"path":"/w/app/go.mod"},{"path":"/w/app/main.go"}]}}
],"ts":1767225603000},
{"role":"assistant","content":[
  {"type":"tool_use","id":"c3","name":"editor","input":{"path":"/w/app/main.go","old_text":"println(\"hello\")","new_text":"println(\"goodbye\")"}}
],"ts":1767225604000},
{"role":"assistant","content":[
  {"type":"tool_use","id":"c4","name":"ask_question","input":{"question":"proceed?"}}
],"ts":1767225605000}
]}`

func writeClineWork(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	sess := filepath.Join(dir, "session_w")
	if err := os.MkdirAll(sess, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(sess, "session_w.messages.json")
	if err := os.WriteFile(p, []byte(clineWorkSession), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseClineEmitsWorkRecords(t *testing.T) {
	ss, err := ParseClineFile(writeClineWork(t))
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	byRole := map[string][]string{}
	for _, m := range ss[0].Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
	}

	// Both commands from one call: the singular key would have found neither.
	want := []string{"$ go vet ./...", "$ go test ./..."}
	if got := byRole[RoleCommand]; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("commands = %q, want both from the one call", got)
	}
	// Both files from the read list, plus the edited one.
	wantFiles := []string{"/w/app/go.mod\n/w/app/main.go", "/w/app/main.go"}
	if got := byRole[RoleFiles]; strings.Join(got, "|") != strings.Join(wantFiles, "|") {
		t.Errorf("files = %q, want the read list and the edit", got)
	}
	// old_text, not old_string: the wrong key stores a path with no span.
	if got := byRole[RoleEdit]; len(got) != 1 ||
		got[0] != "/w/app/main.go\nprintln(\"hello\")" ||
		strings.Contains(strings.Join(got, ""), "goodbye") {
		t.Errorf("edit = %q, want only the bytes that stopped existing", got)
	}
	if got := byRole[RoleToolOutput]; len(got) != 1 ||
		!strings.Contains(got[0], "does not contain main module") {
		t.Errorf("tool output = %q, want the failure the command hit", got)
	}
	// A call that is neither a file, a command nor an edit contributes nothing.
	for _, texts := range byRole {
		if strings.Contains(strings.Join(texts, ""), "proceed?") {
			t.Error("a question tool became a work record")
		}
	}
}

func TestParseClineWorkRecordsSwitchOff(t *testing.T) {
	t.Setenv("DEJA_INDEX_COMMANDS", "0")
	t.Setenv("DEJA_INDEX_PATHS", "0")
	t.Setenv("DEJA_INDEX_EDITS", "0")
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "0")
	ss, err := ParseClineFile(writeClineWork(t))
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v", err)
	}
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleCommand, RoleFiles, RoleEdit, RoleToolOutput:
			t.Fatalf("record %q survived its switch", m.Role)
		}
	}
	if len(ss[0].Messages) != 2 {
		t.Fatalf("want the two spoken turns back, got %d", len(ss[0].Messages))
	}
}

// Cline CLI 3.0.67 writes a run_commands or read_files result as a list of
// {query, result, error, success} entries, one per command or file. Read for
// text keys only, a failing command's stderr reached neither search nor the
// fix pairs (#4315).
func TestParseClineReadsListToolResults(t *testing.T) {
	const sess = `{"version":1,"sessionId":"s_list","messages":[
{"role":"user","content":[{"type":"text","text":"run the tests"}],"ts":1767225600000},
{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"run_commands","input":{"commands":["git status --short","python3 -m pytest -q"]}}],"ts":1767225601000},
{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","name":"run_commands","content":[
  {"query":"git status --short","result":"?? retry.py\n","success":true},
  {"query":"python3 -m pytest -q","result":"[Command exited with code 1]\n\n[stderr]\nNameError: name 'attempt' is not defined\n","error":"Command exited with code 1","success":false}]}],"ts":1767225602000},
{"role":"user","content":[{"type":"tool_result","tool_use_id":"c2","name":"read_files","content":[
  {"query":"/w/retry.py","result":"1 | def retry(n):","success":true}]}],"ts":1767225603000},
{"role":"user","content":[{"type":"tool_result","tool_use_id":"c3","name":"run_commands","content":[
  {"query":"make","error":"spawn make ENOENT","success":false}]}],"ts":1767225604000},
{"role":"user","content":[{"type":"tool_result","tool_use_id":"c4","name":"run_commands","content":[
  {"query":"npm test","result":"PASS a.test.js\n","error":"Command timed out after 30s","success":false},
  {"query":"true","result":"","success":false}]}],"ts":1767225605000}
]}`
	dir := filepath.Join(t.TempDir(), "s_list")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "s_list.messages.json")
	if err := os.WriteFile(p, []byte(sess), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseClineFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	var out []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleToolOutput {
			out = append(out, m.Text)
		}
	}
	all := strings.Join(out, "\n")
	for _, want := range []string{"?? retry.py", "NameError: name 'attempt' is not defined", "def retry(n)", "spawn make ENOENT", "PASS a.test.js", "Command timed out after 30s"} {
		if !strings.Contains(all, want) {
			t.Errorf("tool output %q is missing %q", out, want)
		}
	}
	if strings.Count(all, "Command exited with code 1") != 1 {
		t.Errorf("an error already in the result is repeated: %q", out)
	}
	if len(out) != 4 {
		t.Errorf("got %d tool outputs, want one per tool_result: %q", len(out), out)
	}
}

func writeCodeArtsFixture(t *testing.T, root string) string {
	t.Helper()
	// Two-level bucket tree: sessions/<launch-bucket>/<session-uuid>/
	dir := filepath.Join(root, "2026-10-30-05-47-28-a198caeba310ddb3", "ff119714-beda-42a7-b9c2-06ee66fb0927")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := `{
  "session_id": "ff119714-beda-42a7-b9c2-06ee66fb0927",
  "title": "用 scp 拉取 onetab 到本地工作目录",
  "working_directory": "S:\\work\\WCIS",
  "created_at": "2026-10-29T21:47:28.195777600Z",
  "updated_at": "2026-10-30T10:05:26.482245Z"
}`
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	history := "" +
		`{"id":"m1","role":"User","content":{"SinglePart":{"Text":{"text":"把 onetab 拉到本地"}}},"timestamp":"2026-10-29T21:47:28.572955600Z"}` + "\n" +
		`{"id":"m2","role":"Assistant","content":{"SinglePart":{"Text":{"text":"好的，开始拉取"}}},"reasoning_content":"先确认路径","timestamp":"2026-10-29T21:47:31Z"}` + "\n" +
		`{"id":"m3","role":"Tool","content":{"MultiPart":[{"ToolResult":{"tool_call_id":"c1","result":{"tool_output":"done"}}}]},"timestamp":"2026-10-29T21:47:32Z"}` + "\n" +
		`{malformed json` + "\n" +
		`{"id":"m4","role":"Assistant","content":{"MultiPart":[{"Text":{"text":"拉取完成"}},{"ToolCall":{"id":"c2","name":"bash"}}]},"timestamp":"2026-10-29T21:47:40Z"}` + "\n" +
		// The byte-range index is not a session and must never be listed.
		""
	if err := os.WriteFile(filepath.Join(dir, "history.jsonl"), []byte(history), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "messages.index.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCodeArtsParse(t *testing.T) {
	root := t.TempDir()
	dir := writeCodeArtsFixture(t, root)
	t.Setenv("DEJA_CODEARTS_ROOT", root)

	files := CodeArtsFiles()
	if len(files) != 1 || filepath.Base(files[0]) != "history.jsonl" {
		t.Fatalf("CodeArtsFiles() = %v, want one history.jsonl", files)
	}
	ss, err := ParseCodeArtsFile(files[0])
	if err != nil {
		t.Fatalf("ParseCodeArtsFile: %v", err)
	}
	if len(ss) != 1 {
		t.Fatalf("got %d sessions, want 1", len(ss))
	}
	s := ss[0]
	if s.Harness != "codearts" || s.ID != "ff119714-beda-42a7-b9c2-06ee66fb0927" {
		t.Fatalf("harness=%q id=%q", s.Harness, s.ID)
	}
	if s.Title != "用 scp 拉取 onetab 到本地工作目录" {
		t.Fatalf("title = %q", s.Title)
	}
	if s.Project == "" {
		t.Fatal("project empty: working_directory from meta.json was not read")
	}
	if s.Started.IsZero() || !s.Started.Equal(time.Date(2026, 10, 29, 21, 47, 28, 195777600, time.UTC)) {
		t.Fatalf("started = %v", s.Started)
	}
	// 3 spoken messages plus m3's ToolResult body as RoleToolOutput; m4's empty
	// ToolCall without input emits no spurious work record; malformed line skipped.
	if len(s.Messages) != 4 {
		t.Fatalf("got %d messages, want 4: %+v", len(s.Messages), s.Messages)
	}
	want := []struct{ role, text string }{
		{"user", "把 onetab 拉到本地"},
		{"assistant", "好的，开始拉取"},
		{RoleToolOutput, "done"},
		{"assistant", "拉取完成"},
	}
	for i, w := range want {
		if s.Messages[i].Role != w.role || s.Messages[i].Text != w.text {
			t.Fatalf("message %d = %+v, want role=%q text=%q", i, s.Messages[i], w.role, w.text)
		}
	}
	if !IsCodeArtsHistory(filepath.Join(dir, "history.jsonl")) {
		t.Fatal("IsCodeArtsHistory(history.jsonl) = false")
	}
	if IsCodeArtsHistory(filepath.Join(dir, "messages.index.jsonl")) {
		t.Fatal("IsCodeArtsHistory(messages.index.jsonl) = true")
	}
	if IsCodeArtsHistory(filepath.Join(root+"-kernel", "b", "s", "history.jsonl")) {
		t.Fatal("IsCodeArtsHistory(sibling root) = true")
	}
}

func TestCodeArtsParseFromOffset(t *testing.T) {
	root := t.TempDir()
	dir := writeCodeArtsFixture(t, root)
	t.Setenv("DEJA_CODEARTS_ROOT", root)
	p := filepath.Join(dir, "history.jsonl")
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	// A resumed read past the existing bytes yields nothing new but still
	// returns the session shell with its id/title from meta.json (#2870).
	ss, err := parseCodeArtsFileFromOffset(p, fi.Size())
	if err != nil {
		t.Fatalf("parseCodeArtsFileFromOffset: %v", err)
	}
	if len(ss) != 0 {
		t.Fatalf("got %d sessions at EOF, want 0", len(ss))
	}

	extra := `{"id":"m5","role":"User","content":{"SinglePart":{"Text":{"text":"再补一条"}}},"timestamp":"2026-10-30T11:00:00Z"}` + "\n"
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(extra)
	_ = f.Close()
	ss, err = parseCodeArtsFileFromOffset(p, fi.Size())
	if err != nil {
		t.Fatalf("parseCodeArtsFileFromOffset after append: %v", err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 1 || ss[0].Messages[0].Text != "再补一条" {
		t.Fatalf("resumed parse = %+v, want one appended user message", ss)
	}
	if ss[0].ID != "ff119714-beda-42a7-b9c2-06ee66fb0927" {
		t.Fatalf("resumed parse lost session id: %q", ss[0].ID)
	}
}

func TestCodeArtsLoad(t *testing.T) {
	root := t.TempDir()
	writeCodeArtsFixture(t, root)
	t.Setenv("DEJA_CODEARTS_ROOT", root)
	ss := LoadCodeArts()
	if len(ss) != 1 || ss[0].Harness != "codearts" {
		t.Fatalf("LoadCodeArts() = %+v, want one codearts session", ss)
	}
}

func TestCodeArtsWorkRecordsAndExitJoin(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "2026-10-30-bucket", "ca-work-id")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(`{"session_id":"ca-work-id","working_directory":"/Users/alex/repo"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	history := "" +
		`{"id":"m1","role":"User","content":{"SinglePart":{"Text":{"text":"fix retry loop"}}},"timestamp":"2026-10-30T10:00:00Z"}` + "\n" +
		`{"id":"m2","role":"Assistant","content":{"MultiPart":[` +
		`{"ToolCall":{"id":"c1","name":"Edit","input":{"file_path":"/Users/alex/repo/retry.go","old_string":"sleep(1)","new_string":"return backoffWithJitter(attempt, maxBackoffDuration)"}}},` +
		`{"ToolCall":{"id":"c2","name":"write_file","input":{"path":"/Users/alex/repo/new.go","content":"func newRetryTransport(base http.RoundTripper) http.RoundTripper {\n"}}},` +
		`{"ToolCall":{"id":"c3","name":"Bash","input":{"command":"go test ./..."}}}` +
		`]},"timestamp":"2026-10-30T10:00:01Z"}` + "\n"
	tail := `{"id":"m3","role":"Tool","content":{"MultiPart":[{"ToolResult":{"tool_call_id":"c3","exit_code":2,"result":{"tool_output":"FAIL retry_test.go"}}}]},"timestamp":"2026-10-30T10:00:02Z"}` + "\n"

	p := filepath.Join(dir, "history.jsonl")
	if err := os.WriteFile(p, []byte(history+tail), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodeArtsFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("ParseCodeArtsFile = %+v, err=%v", ss, err)
	}
	byRole := map[string][]string{}
	for _, msg := range ss[0].Messages {
		byRole[msg.Role] = append(byRole[msg.Role], msg.Text)
	}
	if got := byRole[RoleFiles]; len(got) != 1 || got[0] != "/Users/alex/repo/retry.go\n/Users/alex/repo/new.go" {
		t.Errorf("RoleFiles = %v", got)
	}
	if got := byRole[RoleEdit]; len(got) != 1 || got[0] != "/Users/alex/repo/retry.go\nsleep(1)" {
		t.Errorf("RoleEdit = %v", got)
	}
	if got := byRole[RoleWrote]; len(got) != 2 {
		t.Errorf("RoleWrote = %v, want 2 entries", got)
	}
	if got := byRole[RoleCommand]; len(got) != 1 || got[0] != "$ go test ./...  → exit 2" {
		t.Errorf("RoleCommand = %v", got)
	}
	if got := byRole[RoleToolOutput]; len(got) != 1 || got[0] != "FAIL retry_test.go" {
		t.Errorf("RoleToolOutput = %v", got)
	}
	// An appended ToolResult answering a call before offset must force a whole re-read.
	if codeartsResumes(p, int64(len(history))) {
		t.Error("codeartsResumes returned true when tail answers an earlier command call")
	}
}

// CodeBuddy 2.161.2 writes how a command ended into the result's text, as an
// `Exit Code: N` line, and keeps status "completed" either way. Read without
// it, a failed make was stored as a bare `$ make …` (#4703). These records
// carry no structured rawResponse, so the text is all there is.
func TestCodeBuddyCommandCarriesExitCode(t *testing.T) {
	home := codeBuddyEnv(t)
	path := filepath.Join(home, ".codebuddy", "projects", "-work-app", "s1.jsonl")
	writeCodeBuddyFile(t, path, strings.Join([]string{
		`{"id":"u1","timestamp":1791187200000,"type":"message","role":"user","content":[{"type":"input_text","text":"build it"}],"sessionId":"s1","cwd":"/work/app"}`,
		`{"id":"a1","timestamp":1791187201000,"type":"function_call","name":"Bash","callId":"call_1","arguments":"{\"command\":\"make build-everything\"}","sessionId":"s1","cwd":"/work/app"}`,
		`{"id":"r1","timestamp":1791187202000,"type":"function_call_result","name":"Bash","callId":"call_1","status":"completed","output":{"type":"text","text":"Command: make build-everything\nStdout: (empty)\nStderr: make: *** No rule to make target\n\nExit Code: 2\nSignal: (none)"},"sessionId":"s1","cwd":"/work/app"}`,
		`{"id":"a2","timestamp":1791187203000,"type":"function_call","name":"Bash","callId":"call_2","arguments":"{\"command\":\"make all\"}","sessionId":"s1","cwd":"/work/app"}`,
		`{"id":"r2","timestamp":1791187204000,"type":"function_call_result","name":"Bash","callId":"call_2","status":"completed","output":{"type":"text","text":"Command: make all\nStdout: ok\nStderr: (empty)\nExit Code: 0\nSignal: (none)"},"sessionId":"s1","cwd":"/work/app"}`,
		// The PowerShell builder leaves the line out on success.
		`{"id":"a3","timestamp":1791187205000,"type":"function_call","name":"PowerShell","callId":"call_3","arguments":"{\"command\":\"go test ./...\"}","sessionId":"s1","cwd":"/work/app"}`,
		`{"id":"r3","timestamp":1791187206000,"type":"function_call_result","name":"PowerShell","callId":"call_3","status":"completed","output":{"type":"text","text":"Command: go test ./...\nStdout: ok"},"sessionId":"s1","cwd":"/work/app"}`,
		// Killed by a signal: no code to state.
		`{"id":"a4","timestamp":1791187207000,"type":"function_call","name":"Bash","callId":"call_4","arguments":"{\"command\":\"make serve\"}","sessionId":"s1","cwd":"/work/app"}`,
		`{"id":"r4","timestamp":1791187208000,"type":"function_call_result","name":"Bash","callId":"call_4","status":"completed","output":{"type":"text","text":"Command: make serve\nStdout: (empty)\nStderr: (empty)\nExit Code: (none)\nSignal: SIGTERM"},"sessionId":"s1","cwd":"/work/app"}`,
		// Output that prints a status-shaped line of its own.
		`{"id":"a5","timestamp":1791187209000,"type":"function_call","name":"Bash","callId":"call_5","arguments":"{\"command\":\"go test ./parser/...\"}","sessionId":"s1","cwd":"/work/app"}`,
		`{"id":"r5","timestamp":1791187210000,"type":"function_call_result","name":"Bash","callId":"call_5","status":"completed","output":{"type":"text","text":"Command: go test ./parser/...\nStdout: Exit Code: 0\nFAIL\nStderr: (empty)\nExit Code: 1\nSignal: (none)"},"sessionId":"s1","cwd":"/work/app"}`,
		// Backgrounded, and never launched: no code either way.
		`{"id":"a6","timestamp":1791187211000,"type":"function_call","name":"Bash","callId":"call_6","arguments":"{\"command\":\"go test ./slow/...\"}","sessionId":"s1","cwd":"/work/app"}`,
		`{"id":"r6","timestamp":1791187212000,"type":"function_call_result","name":"Bash","callId":"call_6","status":"completed","output":{"type":"text","text":"Command: go test ./slow/...\nStatus: Running in background with task_id: t1\nReason: Auto-backgrounded after hitting foreground timeout"},"sessionId":"s1","cwd":"/work/app"}`,
		`{"id":"a7","timestamp":1791187213000,"type":"function_call","name":"PowerShell","callId":"call_7","arguments":"{\"command\":\"go vet ./...\"}","sessionId":"s1","cwd":"/work/app"}`,
		`{"id":"r7","timestamp":1791187214000,"type":"function_call_result","name":"PowerShell","callId":"call_7","status":"completed","output":{"type":"text","text":"Command: go vet ./...\nError: [SandboxError] could not be launched"},"sessionId":"s1","cwd":"/work/app"}`,
	}, "\n")+"\n")
	ss, err := ParseCodeBuddyFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	var cmds []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleCommand {
			cmds = append(cmds, m.Text)
		}
	}
	want := []string{"$ make build-everything  → exit 2", "$ make all  → exit 0", "$ go test ./...  → exit 0", "$ make serve", "$ go test ./parser/...  → exit 1", "$ go test ./slow/...", "$ go vet ./..."}
	if strings.Join(cmds, "|") != strings.Join(want, "|") {
		t.Fatalf("commands:\n got %q\nwant %q", cmds, want)
	}
}

// /compact leaves its instruction prompt in the transcript as a user message
// marked only agent:"compact" — CodeBuddy's own isInjectedUserItem skips it,
// and it is nobody's words (#4704).
func TestCodeBuddySkipsCompactionPrompt(t *testing.T) {
	home := codeBuddyEnv(t)
	path := filepath.Join(home, ".codebuddy", "projects", "-work-app", "s2.jsonl")
	writeCodeBuddyFile(t, path, strings.Join([]string{
		`{"id":"u1","timestamp":1791187200000,"type":"message","role":"user","content":[{"type":"input_text","text":"fix the parser"}],"providerData":{"agent":"cli"},"sessionId":"s2","cwd":"/work/app"}`,
		`{"id":"u2","timestamp":1791187260000,"type":"message","role":"user","content":[{"type":"input_text","text":"**IMPORTANT CONSTRAINTS:**\n- Do NOT use any tools"}],"providerData":{"agent":"compact"},"sessionId":"s2","cwd":"/work/app"}`,
		`{"id":"a2","timestamp":1791187261000,"type":"message","role":"assistant","content":[{"type":"output_text","text":"<conversation_history_summary>x</conversation_history_summary>"}],"providerData":{"agent":"compact","isCompactInternal":true,"isSummary":true},"sessionId":"s2","cwd":"/work/app"}`,
	}, "\n")+"\n")
	ss, err := ParseCodeBuddyFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	for _, m := range ss[0].Messages {
		if strings.Contains(m.Text, "IMPORTANT CONSTRAINTS") {
			t.Fatalf("compaction prompt indexed as %s: %q", m.Role, m.Text)
		}
	}
	if len(ss[0].Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(ss[0].Messages))
	}
}

// A compaction hook names the live CodeBuddy transcript. Its records carry
// sessionId the way Claude's do, so it was taken for one and came back with
// no session: nothing was captured and no packet followed (#4705).
func TestCodeBuddyTranscriptIsReadForCompaction(t *testing.T) {
	home := codeBuddyEnv(t)
	path := filepath.Join(home, ".codebuddy", "projects", "-work-app", "cb-1.jsonl")
	writeCodeBuddyFile(t, path, strings.Join([]string{
		`{"id":"u1","timestamp":1791187200000,"type":"message","role":"user","content":[{"type":"input_text","text":"fix the parser test"}],"providerData":{"agent":"cli"},"sessionId":"cb-1","cwd":"/work/app"}`,
		`{"timestamp":1791187200100,"type":"file-history-snapshot","messageId":"u1","snapshot":{},"cwd":"/work/app"}`,
		`{"id":"a1","timestamp":1791187201000,"type":"function_call","name":"Bash","callId":"call_1","arguments":"{\"command\":\"go test ./parser/...\"}","sessionId":"cb-1","cwd":"/work/app"}`,
		`{"id":"r1","timestamp":1791187202000,"type":"function_call_result","name":"Bash","callId":"call_1","status":"completed","output":{"type":"text","text":"Command: go test ./parser/...\nStdout: --- FAIL: TestParseSeed\nwant 3, got 4\nFAIL\nStderr: (empty)\nExit Code: 1\nSignal: (none)"},"sessionId":"cb-1","cwd":"/work/app"}`,
		`{"id":"a2","timestamp":1791187203000,"type":"function_call","name":"Edit","callId":"call_2","arguments":"{\"file_path\":\"/work/app/parse.go\",\"old_string\":\"a\",\"new_string\":\"b\"}","sessionId":"cb-1","cwd":"/work/app"}`,
		`{"id":"m2","timestamp":1791187204000,"type":"message","role":"assistant","content":[{"type":"output_text","text":"The parser test fails: want 3, got 4. I decided to fix parse.go next."}],"sessionId":"cb-1","cwd":"/work/app"}`,
	}, "\n")+"\n")
	tr, err := ReadCompactionTranscript(path, "cb-1")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Harness != "codebuddy" || tr.Workspace != "/work/app" {
		t.Fatalf("harness %q workspace %q", tr.Harness, tr.Workspace)
	}
	if len(tr.ToolCalls) != 2 || tr.ToolCalls[0].ID != "call_1" || tr.ToolCalls[1].Name != "Edit" || tr.LastToolID != "call_2" {
		t.Fatalf("tool calls %+v", tr.ToolCalls)
	}
	var cmd bool
	for _, m := range tr.Session.Messages {
		cmd = cmd || m.Role == RoleCommand && m.Text == "$ go test ./parser/...  → exit 1"
	}
	if !cmd || tr.Session.ID != "cb-1" {
		t.Fatalf("session %q messages %+v", tr.Session.ID, tr.Session.Messages)
	}
	if _, err := ReadCompactionTranscript(path, "cb-2"); err == nil {
		t.Fatal("another session id was accepted for this transcript")
	}
}

// The SessionStart guard for #4705 has to know a CodeBuddy transcript even
// outside the configured roots, or the packet is spent where it is dropped.
func TestCodeBuddyTranscriptKnownOutsideItsRoots(t *testing.T) {
	codeBuddyEnv(t)
	elsewhere := filepath.Join(t.TempDir(), "s9.jsonl")
	writeCodeBuddyFile(t, elsewhere, `{"timestamp":1,"type":"file-history-snapshot","snapshot":{}}`+"\n"+
		`{"id":"u1","timestamp":1791187200000,"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}],"sessionId":"s9","cwd":"/work/app"}`+"\n")
	if !IsCodeBuddyTranscript(elsewhere) {
		t.Fatal("a CodeBuddy transcript outside the roots was not recognised")
	}
	claude := filepath.Join(t.TempDir(), "c.jsonl")
	writeCodeBuddyFile(t, claude, `{"type":"user","sessionId":"c","message":{"role":"user","content":"hi"}}`+"\n")
	if IsCodeBuddyTranscript(claude) || IsCodeBuddyTranscript("") {
		t.Fatal("a Claude transcript was taken for CodeBuddy's")
	}
}

// An output too large for the transcript is moved to tool-results/<callId>.txt
// and the result keeps a <persisted-output> preview of its first 2 KB, with no
// Exit Code line. The structured result still records the code.
func TestCodeBuddyPersistedOutputKeepsExitCode(t *testing.T) {
	home := codeBuddyEnv(t)
	path := filepath.Join(home, ".codebuddy", "projects", "-work-app", "s2.jsonl")
	preview := `<persisted-output>\nOutput too large (67.4KB). Full output saved to: /home/u/.codebuddy/projects/-work-app/s2/tool-results/call_1.txt\n\nPreview (first 2048 bytes):\nCommand: go test -v ./...\nStdout: === RUN TestA\n...\n</persisted-output>`
	raw := func(code string, interrupted bool) string {
		i := "false"
		if interrupted {
			i = "true"
		}
		return `"providerData":{"toolResult":{"content":"x","rawResponse":{"exitCode":` + code + `,"signal":null,"interrupted":` + i + `}}}`
	}
	writeCodeBuddyFile(t, path, strings.Join([]string{
		`{"id":"u1","timestamp":1791187200000,"type":"message","role":"user","content":[{"type":"input_text","text":"run the tests"}],"sessionId":"s2","cwd":"/work/app"}`,
		`{"id":"a1","timestamp":1791187201000,"type":"function_call","name":"Bash","callId":"call_1","arguments":"{\"command\":\"go test -v ./...\"}","sessionId":"s2","cwd":"/work/app"}`,
		`{"id":"r1","timestamp":1791187202000,"type":"function_call_result","name":"Bash","callId":"call_1","status":"completed","output":{"type":"text","text":"` + preview + `"},` + raw("1", false) + `,"sessionId":"s2","cwd":"/work/app"}`,
		`{"id":"a2","timestamp":1791187203000,"type":"function_call","name":"Bash","callId":"call_2","arguments":"{\"command\":\"go build ./...\"}","sessionId":"s2","cwd":"/work/app"}`,
		`{"id":"r2","timestamp":1791187204000,"type":"function_call_result","name":"Bash","callId":"call_2","status":"completed","output":{"type":"text","text":"` + preview + `"},` + raw("0", false) + `,"sessionId":"s2","cwd":"/work/app"}`,
		// Interrupted, or backgrounded with no code yet: nothing to state.
		`{"id":"a3","timestamp":1791187205000,"type":"function_call","name":"Bash","callId":"call_3","arguments":"{\"command\":\"go test ./slow/...\"}","sessionId":"s2","cwd":"/work/app"}`,
		`{"id":"r3","timestamp":1791187206000,"type":"function_call_result","name":"Bash","callId":"call_3","status":"completed","output":{"type":"text","text":"` + preview + `"},` + raw("130", true) + `,"sessionId":"s2","cwd":"/work/app"}`,
		`{"id":"a4","timestamp":1791187207000,"type":"function_call","name":"Bash","callId":"call_4","arguments":"{\"command\":\"go test ./bg/...\"}","sessionId":"s2","cwd":"/work/app"}`,
		`{"id":"r4","timestamp":1791187208000,"type":"function_call_result","name":"Bash","callId":"call_4","status":"completed","output":{"type":"text","text":"Command: go test ./bg/...\nStatus: Running in background with task_id: t1"},` + raw("null", false) + `,"sessionId":"s2","cwd":"/work/app"}`,
	}, "\n")+"\n")
	ss, err := ParseCodeBuddyFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	var cmds []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleCommand {
			cmds = append(cmds, m.Text)
		}
	}
	want := []string{"$ go test -v ./...  → exit 1", "$ go build ./...  → exit 0", "$ go test ./slow/...", "$ go test ./bg/..."}
	if strings.Join(cmds, "|") != strings.Join(want, "|") {
		t.Fatalf("commands:\n got %q\nwant %q", cmds, want)
	}
}

// Reading an image leaves a result whose text is a JSON list of blob
// references. Every other reader drops image content, and indexed as text it
// put a 64-hex blob id and the blob store's path into search.
func TestCodeBuddyImageReadIsNotText(t *testing.T) {
	home := codeBuddyEnv(t)
	path := filepath.Join(home, ".codebuddy", "projects", "-work-app", "s3.jsonl")
	ref := `[{\"type\":\"image_blob_ref\",\"blob_id\":\"a17ecf5531cb5cb103791f883dec00ca7f8247d6722729ec0959953c36e85be8\",\"mime\":\"image/png\",\"size\":8558,\"blob_path\":\"/home/u/.codebuddy/blobs/a1/a17ecf55.png\"}]`
	writeCodeBuddyFile(t, path, strings.Join([]string{
		`{"id":"u1","timestamp":1791187200000,"type":"message","role":"user","content":[{"type":"input_text","text":"look at the screenshot"}],"sessionId":"s3","cwd":"/work/app"}`,
		`{"id":"a1","timestamp":1791187201000,"type":"function_call","name":"Read","callId":"call_1","arguments":"{\"file_path\":\"/work/app/shot.png\"}","sessionId":"s3","cwd":"/work/app"}`,
		`{"id":"r1","timestamp":1791187202000,"type":"function_call_result","name":"Read","callId":"call_1","status":"completed","output":{"type":"text","text":"` + ref + `"},"sessionId":"s3","cwd":"/work/app"}`,
		`{"id":"m1","timestamp":1791187203000,"type":"message","role":"assistant","content":[{"type":"output_text","text":"the button is cut off"}],"sessionId":"s3","cwd":"/work/app"}`,
	}, "\n")+"\n")
	ss, err := ParseCodeBuddyFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	for _, m := range ss[0].Messages {
		if strings.Contains(m.Text, "blob_id") || strings.Contains(m.Text, "image_blob_ref") {
			t.Fatalf("%s record carries the blob reference: %q", m.Role, m.Text)
		}
	}
}

func codeBuddyEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, k := range []string{"CODEBUDDY_CONFIG_DIR", "WORKBUDDY_CONFIG_DIR", "DEJA_CODEBUDDY_ROOTS", "DEJA_INCLUDE_SUBAGENTS"} {
		t.Setenv(k, "")
	}
	return home
}

func writeCodeBuddyFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCodeBuddyRootsFollowConfigDir(t *testing.T) {
	home := codeBuddyEnv(t)
	if got, want := CodeBuddyRoots(), []string{filepath.Join(home, ".codebuddy", "projects")}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("default roots = %v, want %v", got, want)
	}

	cfg := filepath.Join(home, "work-profile")
	t.Setenv("CODEBUDDY_CONFIG_DIR", cfg)
	if got := CodeBuddyRoots(); len(got) != 1 || got[0] != filepath.Join(cfg, "projects") {
		t.Fatalf("CODEBUDDY_CONFIG_DIR roots = %v", got)
	}
	if got := CodeBuddyConfigDir(); got != cfg {
		t.Fatalf("CodeBuddyConfigDir = %q, want %q", got, cfg)
	}

	// WorkBuddy writes the same store under its own directory; it is read once
	// it exists.
	wb := filepath.Join(home, ".workbuddy", "projects")
	if err := os.MkdirAll(wb, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := CodeBuddyRoots(); len(got) != 2 || got[1] != wb {
		t.Fatalf("roots with WorkBuddy = %v", got)
	}
	wbCfg := filepath.Join(home, "wb")
	t.Setenv("WORKBUDDY_CONFIG_DIR", wbCfg)
	if err := os.MkdirAll(filepath.Join(wbCfg, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := CodeBuddyRoots(); len(got) != 2 || got[1] != filepath.Join(wbCfg, "projects") {
		t.Fatalf("WORKBUDDY_CONFIG_DIR roots = %v", got)
	}

	a, b := filepath.Join(home, "a"), filepath.Join(home, "b")
	t.Setenv("DEJA_CODEBUDDY_ROOTS", a+string(os.PathListSeparator)+b)
	if got := CodeBuddyRoots(); strings.Join(got, "|") != a+"|"+b {
		t.Fatalf("DEJA_CODEBUDDY_ROOTS roots = %v", got)
	}
}

func TestCodeBuddySessionFiles(t *testing.T) {
	home := codeBuddyEnv(t)
	root := filepath.Join(home, ".codebuddy", "projects")
	main := filepath.Join(root, "repo", "s1.jsonl")
	sub := filepath.Join(root, "repo", "s1", "subagents", "agent-a1.jsonl")
	writeCodeBuddyFile(t, main, "{}\n")
	writeCodeBuddyFile(t, sub, "{}\n")
	writeCodeBuddyFile(t, filepath.Join(root, "repo", "s1.meta.json"), "{}")
	writeCodeBuddyFile(t, filepath.Join(root, "repo", "memory", "MEMORY.md"), "x")

	if got := CodeBuddySessionFiles(); len(got) != 2 {
		t.Fatalf("session files = %v, want the session and its sub-agent", got)
	}
	if !isCodeBuddySession(main) || isCodeBuddySession(sub) {
		t.Fatalf("isCodeBuddySession main=%v sub=%v", isCodeBuddySession(main), isCodeBuddySession(sub))
	}
	writeCodeBuddyFile(t, filepath.Join(root, "repo", "stray.txt"), "x")
	if got := CodeBuddySidecarFiles(); len(got) != 2 {
		t.Fatalf("sidecars = %v", got)
	}
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "0")
	if got := CodeBuddySessionFiles(); len(got) != 1 || got[0] != main {
		t.Fatalf("with sub-agents off = %v, want [%s]", got, main)
	}
}

func TestParseCodeBuddyFixture(t *testing.T) {
	codeBuddyEnv(t)
	path := filepath.Join("..", "..", "fixtures", "registry", "codebuddy", "projects", "workspace-registry-demo", "registry-codebuddy.jsonl")
	sessions, err := ParseCodeBuddyFile(path)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("ParseCodeBuddyFile = %d sessions, %v", len(sessions), err)
	}
	s := sessions[0]
	if s.Harness != "codebuddy" || s.ID != "registry-codebuddy" || s.Project != "workspace/registry-demo" {
		t.Fatalf("identity = %q %q %q", s.Harness, s.ID, s.Project)
	}
	if s.Title != "Fix the registry demo build" {
		t.Fatalf("title = %q", s.Title)
	}
	if want := time.UnixMilli(1780000000000); !s.Started.Equal(want) {
		t.Fatalf("started = %v, want %v", s.Started, want)
	}
	got := make([]string, 0, len(s.Messages))
	for _, m := range s.Messages {
		got = append(got, m.Role+": "+m.Text)
	}
	want := []string{
		"user: why does the registry demo fail to build?",
		"assistant: The build tag is missing from main.go; checking with go build.",
		RoleCommand + ": $ go build ./...",
		RoleToolOutput + ": main.go:3:1: build constraints exclude all Go files",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("messages:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The client's own plumbing shares role user with the person: skipRun records,
// the slash-command and reminder envelopes, compaction and continuation turns
// and teammate messages are all dropped, the way CodeBuddy's own
// isRealUserMessageItem drops them.
func TestParseCodeBuddyDropsPlumbing(t *testing.T) {
	home := codeBuddyEnv(t)
	path := filepath.Join(home, ".codebuddy", "projects", "repo", "s2.jsonl")
	writeCodeBuddyFile(t, path, strings.Join([]string{
		`{"type":"message","role":"user","timestamp":1780000000000,"cwd":"/src/repo","content":[{"type":"input_text","text":"<local-command-stdout>ok</local-command-stdout>"}]}`,
		`{"type":"message","role":"user","timestamp":1780000000001,"content":[{"type":"input_text","text":"<teammate-message from=\"a\">hi</teammate-message>"}]}`,
		`{"type":"message","role":"user","timestamp":1780000000002,"content":[{"type":"input_text","text":"continue"}],"providerData":{"isMeta":true}}`,
		`{"type":"message","role":"user","timestamp":1780000000003,"content":[{"type":"input_text","text":"summary of earlier"}],"providerData":{"isCompactInternal":true}}`,
		`{"type":"message","role":"user","timestamp":1780000000004,"content":[{"type":"input_text","text":"from a teammate"}],"providerData":{"teammateMessage":{"from":"b"}}}`,
		`{"type":"message","role":"user","timestamp":1780000000005,"content":[{"type":"input_text","text":"/review"}],"providerData":{"skipRun":true}}`,
		`{"type":"reasoning","timestamp":1780000000006,"content":[{"type":"input_text","text":"thinking"}]}`,
		`{"type":"summary","timestamp":1780000000007,"summary":"compacted"}`,
		`{"type":"message","role":"user","timestamp":1780000000008,"content":"rename the flag"}`,
		`{"type":"function_call","timestamp":1780000000009,"callId":"c1","name":"Edit","arguments":"{\"file_path\":\"/src/repo/main.go\",\"old_string\":\"oldFlag\",\"new_string\":\"newFlag\"}"}`,
		`{"type":"ai-title","aiTitle":"Generated","timestamp":1780000000010}`,
		`{"type":"custom-title","customTitle":"Named by hand","timestamp":1780000000011}`,
		`not json`,
	}, "\n")+"\n")
	sessions, err := ParseCodeBuddyFile(path)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("ParseCodeBuddyFile = %d sessions, %v", len(sessions), err)
	}
	s := sessions[0]
	var user []string
	roles := map[string]bool{}
	for _, m := range s.Messages {
		roles[m.Role] = true
		if m.Role == "user" {
			user = append(user, m.Text)
		}
	}
	if strings.Join(user, "|") != "rename the flag" {
		t.Fatalf("user turns = %q", user)
	}
	if !roles[RoleFiles] || !roles[RoleEdit] {
		t.Fatalf("Edit call left no file or edit record: %v", roles)
	}
	if s.Title != "Named by hand" {
		t.Fatalf("title = %q, want the custom title over the generated one", s.Title)
	}
	if s.Project != "src/repo" {
		t.Fatalf("project = %q", s.Project)
	}
}

func TestParseCodeBuddySubagent(t *testing.T) {
	home := codeBuddyEnv(t)
	path := filepath.Join(home, ".codebuddy", "projects", "repo", "parent-1", "subagents", "agent-x.jsonl")
	writeCodeBuddyFile(t, path, `{"type":"message","role":"user","timestamp":1780000000000,"content":[{"type":"input_text","text":"look for callers"}]}`+"\n")
	sessions, err := ParseCodeBuddyFile(path)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("ParseCodeBuddyFile = %d sessions, %v", len(sessions), err)
	}
	if s := sessions[0]; s.Kind != "subagent" || s.Parent != "parent-1" || s.ID != "agent-x" {
		t.Fatalf("sub-agent = kind %q parent %q id %q", s.Kind, s.Parent, s.ID)
	}
}

// WorkBuddy's desktop app records each prompt behind its context reminders, in
// one item that ends with <user_query>. The turn is the query (#4734); a
// reminder on its own is still plumbing, and a bare prompt is unchanged.
func TestParseCodeBuddyUserQueryWrapper(t *testing.T) {
	home := codeBuddyEnv(t)
	path := filepath.Join(home, ".codebuddy", "projects", "repo", "s3.jsonl")
	wrapped := `<system-reminder data-role=\"user-context\">\n<user_info>\nOS Version: darwin\n</user_info>\n</system-reminder>\n<system-reminder data-role=\"additional-data\">\n<current_time>\nMonday\n</current_time>\n</system-reminder>\n<user_query>What backoff did we pick for the payments retry?</user_query>`
	writeCodeBuddyFile(t, path, strings.Join([]string{
		`{"type":"message","role":"user","timestamp":1780000000000,"cwd":"/src/repo","content":[{"type":"input_text","text":"` + wrapped + `"}]}`,
		`{"type":"message","role":"assistant","timestamp":1780000000001,"content":[{"type":"output_text","text":"Exponential, capped at 30s."}]}`,
		`{"type":"message","role":"user","timestamp":1780000000002,"content":[{"type":"input_text","text":"<system-reminder>\nplan mode is on\n</system-reminder>"}]}`,
		`{"type":"message","role":"user","timestamp":1780000000003,"content":"<system-reminder>x</system-reminder>\n<user_query>first</user_query>\n<user_query>and add jitter</user_query>"}`,
		`{"type":"message","role":"user","timestamp":1780000000004,"content":"why does <user_query> show up in the log"}`,
	}, "\n")+"\n")
	sessions, err := ParseCodeBuddyFile(path)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("ParseCodeBuddyFile = %d sessions, %v", len(sessions), err)
	}
	var user []string
	for _, m := range sessions[0].Messages {
		if m.Role == "user" {
			user = append(user, m.Text)
		}
	}
	want := "What backoff did we pick for the payments retry?|and add jitter|why does <user_query> show up in the log"
	if got := strings.Join(user, "|"); got != want {
		t.Fatalf("user turns = %q, want %q", got, want)
	}
}

// The saved session is one JSON file: metadata plus messages whose content is
// the Anthropic block list. A call and what it printed are work records, the
// person's words are the user turn, and the harness's own roles — system and
// developer, which is where it puts compaction and sub-agent framing — are
// nobody's speech.
func TestParseCodeWhaleSession(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CODEWHALE_ROOT", root)
	path := filepath.Join(root, "01JA7Q.json")
	body := `{
  "schema_version": 3,
  "metadata": {
    "id": "01JA7Q",
    "title": "the pool runs out under load",
    "created_at": "2026-09-19T10:00:00Z",
    "updated_at": "2026-09-19T10:06:00Z",
    "workspace": "/w/poollab",
    "model": "deepseek-chat"
  },
  "messages": [
    {"role": "user", "content": [{"type": "text", "text": "the pool runs out under load, where is the leak"}]},
    {"role": "system", "content": [{"type": "text", "text": "Summary of the compacted half: the caller never returns the connection."}]},
    {"role": "assistant", "content": [
      {"type": "thinking", "thinking": "check the acquire path first"},
      {"type": "text", "text": "Looking at the acquire path."},
      {"type": "tool_use", "id": "c1", "name": "exec_shell", "input": {"command": "go test ./internal/pool -run TestAcquire"}}
    ]},
    {"role": "user", "content": [{"type": "tool_result", "tool_use_id": "c1", "content": "pool_test.go:88: leaked 4 connections", "is_error": true}]},
    {"role": "assistant", "content": [
      {"type": "tool_use", "id": "c2", "name": "edit_file", "input": {"path": "/w/poollab/internal/pool/acquire.go", "old_string": "defer conn.Close()", "new_string": "defer pool.Put(conn)"}}
    ]}
  ]
}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	ss, err := ParseCodeWhaleFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	s := ss[0]
	if s.Harness != "codewhale" || s.ID != "01JA7Q" {
		t.Errorf("harness/id = %q/%q", s.Harness, s.ID)
	}
	if s.Project != "w/poollab" {
		t.Errorf("project = %q, want the workspace it was worked in", s.Project)
	}
	if s.Title != "the pool runs out under load" {
		t.Errorf("title = %q", s.Title)
	}

	byRole := map[string][]string{}
	for _, m := range s.Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
	}
	if got := byRole["user"]; len(got) != 1 || got[0] != "the pool runs out under load, where is the leak" {
		t.Errorf("user turns = %q", got)
	}
	if got := byRole["assistant"]; len(got) != 1 || got[0] != "Looking at the acquire path." {
		t.Errorf("assistant turns = %q — thinking is not speech", got)
	}
	if got := byRole[RoleCommand]; len(got) != 1 || got[0] != "$ go test ./internal/pool -run TestAcquire" {
		t.Errorf("commands = %q", got)
	}
	if got := byRole[RoleToolOutput]; len(got) != 1 || got[0] != "pool_test.go:88: leaked 4 connections" {
		t.Errorf("tool output = %q — a failing run is what a later search reaches for", got)
	}
	if got := byRole[RoleFiles]; len(got) != 1 || got[0] != "/w/poollab/internal/pool/acquire.go" {
		t.Errorf("files = %q", got)
	}
	if got := byRole[RoleEdit]; len(got) != 1 {
		t.Errorf("edits = %q, want the span the edit replaced", got)
	}

	// The harness's own summary is not a message of anyone's.
	for _, texts := range byRole {
		for _, text := range texts {
			if text == "Summary of the compacted half: the caller never returns the connection." {
				t.Error("a system-role record was indexed as something someone said")
			}
		}
	}

	// No per-message timestamps in the file: the session's own start is the
	// clock, one millisecond per record, so two identical turns stay two.
	for i := 1; i < len(s.Messages); i++ {
		if !s.Messages[i].Time.After(s.Messages[i-1].Time) && s.Messages[i].Time != s.Messages[i-1].Time {
			t.Fatalf("record %d goes back in time", i)
		}
	}
}

// Two identical turns are two records, which they cannot be under one stamp.
func TestCodeWhaleTellsTwoIdenticalTurnsApart(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CODEWHALE_ROOT", root)
	path := filepath.Join(root, "dup.json")
	body := `{"metadata":{"id":"dup","title":"retry","created_at":"2026-09-19T10:00:00Z","updated_at":"2026-09-19T10:01:00Z","workspace":"/w/dup"},
"messages":[
 {"role":"user","content":[{"type":"text","text":"run it again"}]},
 {"role":"user","content":[{"type":"text","text":"run it again"}]}
]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodeWhaleFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	if n := len(ss[0].Messages); n != 2 {
		t.Fatalf("messages = %d, want both turns", n)
	}
	if ss[0].Messages[0].Time.Equal(ss[0].Messages[1].Time) {
		t.Error("both turns carry one stamp, so the index stores one of them")
	}
}

// CodeWhale 0.10.0 saves a <turn_meta> text block in every user message, the
// harness's own date, workspace and posture; the person's words are the rest.
func TestCodeWhaleDropsTurnMeta(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CODEWHALE_ROOT", root)
	path := filepath.Join(root, "meta.json")
	body := `{"metadata":{"id":"meta","title":"t","created_at":"2026-10-07T10:00:00Z","updated_at":"2026-10-07T10:01:00Z","workspace":"/w/m"},
"messages":[{"role":"user","content":[{"type":"text","text":"say hi"},{"type":"text","text":"<turn_meta>\nCurrent local date: 2026-10-07\nCurrent workspace: /w/m\n</turn_meta>"}]}]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodeWhaleFile(path)
	if err != nil || len(ss) != 1 || len(ss[0].Messages) != 1 {
		t.Fatalf("parse: %v, %+v", err, ss)
	}
	if got := ss[0].Messages[0].Text; got != "say hi" {
		t.Errorf("text = %q, want the person's words alone", got)
	}
}

// `codewhale fork` records the session it came from, and that file is the only
// place the edge exists.
func TestCodeWhaleForkKnowsItsParent(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CODEWHALE_ROOT", root)
	path := filepath.Join(root, "child.json")
	body := `{"metadata":{"id":"child","title":"fork","created_at":"2026-09-19T11:00:00Z","updated_at":"2026-09-19T11:01:00Z","workspace":"/w/fork","parent_session_id":"parent-1"},
"messages":[{"role":"user","content":[{"type":"text","text":"carry on from the fork point"}]}]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodeWhaleFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	if ss[0].Kind != "fork" || ss[0].Parent != "parent-1" {
		t.Errorf("kind/parent = %q/%q", ss[0].Kind, ss[0].Parent)
	}
}

// The sessions directory holds bookkeeping beside the transcripts: the offline
// queue, the ownership ledger, the legacy checkpoint slot. None is a session,
// and the file list must not offer them to the parser.
func TestCodeWhaleSessionFilesSkipItsBookkeeping(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CODEWHALE_ROOT", root)
	if err := os.MkdirAll(filepath.Join(root, "checkpoints"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"01JA7Q.json", "offline_queue.json", "session_boot_owners.json", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "checkpoints", "latest.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := CodeWhaleSessionFiles()
	if len(got) != 1 || filepath.Base(got[0]) != "01JA7Q.json" {
		t.Fatalf("session files = %v, want the transcript alone", got)
	}
	if !isCodeWhaleSession(filepath.Join(root, "01JA7Q.json")) {
		t.Error("the transcript does not match its own kind")
	}
	for _, name := range []string{"offline_queue.json", "session_boot_owners.json"} {
		if isCodeWhaleSession(filepath.Join(root, name)) {
			t.Errorf("%s matches as a transcript", name)
		}
	}
	if isCodeWhaleSession(filepath.Join(root, "checkpoints", "latest.json")) {
		t.Error("a checkpoint matches as a transcript")
	}
}

// The store moved with the rebrand and the harness still migrates the old root,
// so both are read — and an explicit $CODEWHALE_HOME is an isolation boundary
// in its own resolver, which deja does not reach outside of either.
func TestCodeWhaleReadsTheLegacyRootUnlessHomeIsExplicit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEWHALE_HOME", "")
	t.Setenv("DEJA_CODEWHALE_ROOT", "")
	legacy := filepath.Join(home, ".deepseek", "sessions")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	roots := CodeWhaleRoots()
	if len(roots) != 2 || roots[1] != legacy {
		t.Fatalf("roots = %v, want the legacy sessions directory beside the current one", roots)
	}

	t.Setenv("CODEWHALE_HOME", filepath.Join(home, "isolated"))
	if roots := CodeWhaleRoots(); len(roots) != 1 {
		t.Fatalf("roots = %v, want only the explicit home", roots)
	}
}

// Since 0.9.6 new CodeWhale turns use one small toolbox — read, write, edit,
// bash — and edit takes edits[{oldText,newText}]. Knowing only the older names
// left a session with its commands and none of the files it read or changed
// (#4360).
func TestCodeWhaleReadsTheSmallToolbox(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CODEWHALE_ROOT", root)
	path := filepath.Join(root, "0d6f.json")
	body := `{"metadata":{"id":"0d6f","title":"retry","created_at":"2026-09-30T10:00:00Z","workspace":"/tmp/proj"},
"messages":[
 {"role":"user","content":[{"type":"text","text":"fix the retry loop in client.go"}]},
 {"role":"assistant","content":[
  {"type":"tool_use","id":"call_1","name":"bash","input":{"command":"go test ./...","cwd":"/tmp/proj"}},
  {"type":"tool_use","id":"call_2","name":"read","input":{"path":"/tmp/proj/client.go"}},
  {"type":"tool_use","id":"call_3","name":"edit","input":{"path":"/tmp/proj/client.go","edits":[{"oldText":"for i := 0; i <= max; i++","newText":"for i := 0; i < max; i++"}]}},
  {"type":"tool_use","id":"call_4","name":"write","input":{"path":"/tmp/proj/retry_test.go","content":"func TestRetryStopsAtMax(t *testing.T) { if got := retry(3); got != 3 { t.Fatal(got) } }\n"}}
 ]}
]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodeWhaleFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	byRole := map[string][]string{}
	for _, m := range ss[0].Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
	}
	if got := byRole[RoleCommand]; len(got) != 1 || got[0] != "$ go test ./..." {
		t.Errorf("commands = %q", got)
	}
	if got := byRole[RoleFiles]; len(got) != 1 || got[0] != "/tmp/proj/client.go\n/tmp/proj/retry_test.go" {
		t.Errorf("files = %q, want what read, edit and write named", got)
	}
	if got := byRole[RoleEdit]; len(got) != 1 || got[0] != "/tmp/proj/client.go\nfor i := 0; i <= max; i++" {
		t.Errorf("edits = %q, want the span from edits[].oldText", got)
	}
	if got := byRole[RoleWrote]; len(got) != 2 {
		t.Errorf("wrote = %q, want the edit's newText and the written file", got)
	}
}

// CodeWhale keeps session_boot_owners.json, a map of session id to boot id,
// beside the transcripts. Under any other name it was walked as a transcript
// and doctor counted one file too many (#4361).
func TestCodeWhaleSkipsTheBootOwnersLedger(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CODEWHALE_ROOT", root)
	for name, body := range map[string]string{
		"0d6f.json":                `{"metadata":{"id":"0d6f"},"messages":[]}`,
		"session_boot_owners.json": `{"0d6f":"boot_1a2b3c4d5e6f"}`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := CodeWhaleSessionFiles(); len(got) != 1 || filepath.Base(got[0]) != "0d6f.json" {
		t.Errorf("session files = %v, want the transcript alone", got)
	}
	ledger := filepath.Join(root, "session_boot_owners.json")
	if isCodeWhaleSession(ledger) {
		t.Error("the boot-owners ledger matches as a transcript")
	}
	if got := CodeWhaleSidecarFiles(); len(got) != 1 || got[0] != ledger {
		t.Errorf("sidecars = %v, want the ledger placed", got)
	}
}

// edit takes its edits the way models send them, and CodeWhale folds two
// shapes onto edits[] before running: the array as a JSON string, and a single
// top-level oldText/newText. The transcript keeps what the model sent, so
// reading only the array lost those edits (#4360).
func TestCodeWhaleReadsAnEditInEveryShapeItRuns(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CODEWHALE_ROOT", root)
	path := filepath.Join(root, "0d70.json")
	body := `{"metadata":{"id":"0d70","title":"retry","created_at":"2026-09-30T10:00:00Z","workspace":"/tmp/proj"},
"messages":[
 {"role":"user","content":[{"type":"text","text":"fix the retry loop"}]},
 {"role":"assistant","content":[
  {"type":"tool_use","id":"call_1","name":"edit","input":{"path":"/tmp/proj/client.go","edits":"[{\"oldText\":\"i <= max\",\"newText\":\"for attempt := 0; attempt < maxRetries; attempt++\"},{\"oldText\":\"sleep(1)\",\"newText\":\"time.Sleep(backoff * time.Duration(attempt))\"}]"}},
  {"type":"tool_use","id":"call_2","name":"edit","input":{"path":"/tmp/proj/server.go","oldText":"retries := 0","newText":"retries := defaultRetriesForServer"}}
 ]}
]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodeWhaleFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	var edits, wrote []string
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleEdit:
			edits = append(edits, m.Text)
		case RoleWrote:
			wrote = append(wrote, m.Text)
		}
	}
	want := []string{"/tmp/proj/client.go\ni <= max", "/tmp/proj/client.go\nsleep(1)", "/tmp/proj/server.go\nretries := 0"}
	if len(edits) != len(want) || edits[0] != want[0] || edits[1] != want[1] || edits[2] != want[2] {
		t.Errorf("edits = %q, want %q", edits, want)
	}
	if len(wrote) != 3 {
		t.Errorf("wrote = %q, want one per edit", wrote)
	}
}

// CodeWhale's edit_file takes search/replace as its own names and folds the
// other harnesses' spellings onto them (tools/file.rs EDIT_ALIASES). Reading
// old_string alone left a search/replace call with a files record and no edit
// or wrote record (#4404).
func TestCodeWhaleReadsAnEditWrittenWithSearchAndReplace(t *testing.T) {
	for _, keys := range [][2]string{{"search", "replace"}, {"old_string", "new_string"}, {"old_str", "new_str"}, {"oldText", "newText"}, {"old_text", "replacement"}} {
		raw := `[{"type":"tool_use","id":"t1","name":"edit_file","input":{"path":"/tmp/proj/client.go","` +
			keys[0] + `":"for i := 0; i <= max; i++","` + keys[1] + `":"for i := 0; i < max; i++"}}]`
		var edit, wrote int
		for _, m := range codeWhaleWorkRecords(json.RawMessage(raw), time.Time{}) {
			switch m.Role {
			case RoleEdit:
				edit++
			case RoleWrote:
				wrote++
			}
		}
		if edit != 1 || wrote != 1 {
			t.Errorf("%s/%s: %d edit and %d wrote records, want one of each", keys[0], keys[1], edit, wrote)
		}
	}
}

// CodeWhale keeps write_file {path, content} registered for sessions saved
// before 0.9.6 (tools/file.rs WriteFileTool), and fim_edit {path,
// prefix_anchor, suffix_anchor} returns the text it generated between them as
// FimEditResult in the result (tools/fim.rs). Both had a files record and
// nothing blame could match (#595).
func TestCodeWhaleWriteFileAndFimEditRecordTheWrittenSide(t *testing.T) {
	const generated = "\tfor attempt := 0; attempt < maxAttempts; attempt++ {\n\t\tif err := fn(); err == nil {"
	fim := vocabJSON(map[string]any{"success": true, "path": "/tmp/proj/fim.go", "generated_text": generated,
		"prefix_end": 42, "suffix_start": 97, "message": "FIM edit applied to `/tmp/proj/fim.go`."})

	ss := cwSaved(t, "Successfully wrote 61 bytes to /tmp/proj/jitter.go", false,
		map[string]any{"name": "write_file", "input": map[string]any{"path": "/tmp/proj/jitter.go", "content": jitter}})
	vocabCheck(t, ss, []string{vocabFiles("/tmp/proj/jitter.go"), vocabWrote("/tmp/proj/jitter.go", jitter)}, nil)

	ss = cwSaved(t, fim, false,
		map[string]any{"name": "fim_edit", "input": map[string]any{"path": "/tmp/proj/fim.go", "prefix_anchor": "func retry(fn func() error) error {", "suffix_anchor": "\treturn errGiveUp"}})
	vocabCheck(t, ss, []string{vocabFiles("/tmp/proj/fim.go"), vocabWrote("/tmp/proj/fim.go", generated)}, nil)

	// A fim_edit that came back as an error wrote nothing.
	ss = cwSaved(t, fim, true,
		map[string]any{"name": "fim_edit", "input": map[string]any{"path": "/tmp/proj/fim.go", "prefix_anchor": "a", "suffix_anchor": "b"}})
	vocabCheck(t, ss, nil, []string{vocabWrote("/tmp/proj/fim.go", generated)})

	// A provider that numbers calls per turn reuses an id: each result is
	// filed under the call it answered, not the last call with that id.
	second := "\t\treturn fmt.Errorf(\"retry: gave up after %d attempts\", maxAttempts)"
	call := func(path string) any {
		return map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "call_0", "name": "fim_edit",
			"input": map[string]any{"path": path, "prefix_anchor": "a", "suffix_anchor": "b"}}}}
	}
	res := func(text string) any {
		return map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "call_0",
			"content": vocabJSON(map[string]any{"success": true, "generated_text": text})}}}
	}
	doc := map[string]any{
		"schema_version": 1,
		"metadata":       map[string]any{"id": "cw-2", "created_at": "2026-10-01T10:00:00Z", "workspace": "/tmp/proj"},
		"messages": []any{
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "fix the retry loop"}}},
			call("/tmp/proj/first.go"), res(generated), call("/tmp/proj/second.go"), res(second),
		},
	}
	ss = vocabParse(t, ParseCodeWhaleFile, vocabWrite(t, filepath.Join(t.TempDir(), "cw-2.json"), vocabJSON(doc)))
	vocabCheck(t, ss, []string{vocabWrote("/tmp/proj/first.go", generated), vocabWrote("/tmp/proj/second.go", second)},
		[]string{vocabWrote("/tmp/proj/second.go", generated)})
}

// Codex compresses a rollout once it is seven days old — `COMPRESSED_SUFFIX`
// and `MIN_ROLLOUT_AGE` in its own rollout/compression.rs, run by a background
// worker — and reads either name itself. deja wanted `.jsonl` alone, so on a
// machine where that worker had run, every session older than a week left the
// index without a word: not a candidate, therefore not a skip either (#3640).
//
// `archived_sessions` is the same store's second directory, Codex's own
// `ARCHIVED_SESSIONS_SUBDIR`, and a session moved into it used to disappear on
// the next pass.
const codexRolloutFixture = `{"timestamp":"2026-07-17T09:00:00.000Z","type":"session_meta","payload":{"id":"thread-7","cwd":"/work/api"}}
{"timestamp":"2026-07-17T09:00:01.000Z","type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"why does the migration hang"}]}}
{"timestamp":"2026-07-17T09:00:02.000Z","type":"response_item","payload":{"role":"assistant","content":[{"type":"output_text","text":"the advisory lock was never released"}]}}
`

func writeCodexRollout(t *testing.T, dir string, compress bool) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(dir, "rollout-thread-7.jsonl")
	if err := os.WriteFile(plain, []byte(codexRolloutFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if !compress {
		return plain
	}
	out := plain + ".zst"
	if err := exec.CommandContext(t.Context(), "zstd", "-q", "-o", out, plain).Run(); err != nil {
		t.Fatalf("zstd: %v", err)
	}
	if err := os.Remove(plain); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCodexReadsACompressedRollout(t *testing.T) {
	if !ZstdAvailable() {
		t.Skip("zstd CLI not installed")
	}
	root := t.TempDir()
	t.Setenv("DEJA_CODEX_ROOT", root)
	t.Setenv("DEJA_XCODE_CODEX_ROOT", filepath.Join(root, "absent"))
	writeCodexRollout(t, filepath.Join(root, "sessions", "2026", "07", "17"), true)

	files := CodexFiles()
	if len(files) != 1 || !strings.HasSuffix(files[0], ".jsonl.zst") {
		t.Fatalf("a compressed rollout is not a candidate: %v", files)
	}

	ss := LoadCodex()
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1: %+v", len(ss), ss)
	}
	if ss[0].ID != "thread-7" {
		t.Errorf("id = %q, want the thread id from the meta line", ss[0].ID)
	}
	if len(ss[0].Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(ss[0].Messages))
	}
	if !strings.Contains(ss[0].Messages[1].Text, "advisory lock") {
		t.Errorf("the reply did not survive decompression: %q", ss[0].Messages[1].Text)
	}
	// The session keeps the name of the file on disk, not of the temporary copy
	// it was read through: `deja doctor` reports a transcript no longer present
	// by comparing this against the store.
	if !strings.HasSuffix(ss[0].Path, ".jsonl.zst") {
		t.Errorf("path = %q, want the compressed file", ss[0].Path)
	}
}

// A compressed rollout was read whole and decompressed into a buffer before the
// first line was scanned, so one 4 GB rollout took more memory than the rest of
// the store. Almost all of it is records the parser drops; the same bytes read
// as plain .jsonl stayed at one line's worth.
func TestCodexStreamsACompressedRollout(t *testing.T) {
	if !ZstdAvailable() {
		t.Skip("zstd CLI not installed")
	}
	if testing.Short() {
		t.Skip("compresses a 64 MB rollout")
	}
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("CODEX_HOME", root)
	t.Setenv("DEJA_CODEX_ROOT", root)
	t.Setenv("DEJA_XCODE_CODEX_ROOT", filepath.Join(root, "absent"))
	const size = 64 << 20
	ignored := `{"type":"turn_context","payload":{"context":"` + strings.Repeat("x", size/2) + `"}}` + "\n"
	cmd := exec.CommandContext(t.Context(), "zstd", "-q", "-c")
	cmd.Stdin = io.MultiReader(strings.NewReader(codexRolloutFixture), strings.NewReader(ignored), strings.NewReader(ignored))
	compressed, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "rollout-thread-7.jsonl.zst")
	if err = os.WriteFile(path, compressed, 0o600); err != nil {
		t.Fatal(err)
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	ss, err := ParseCodexRollout(path)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "thread-7" || len(ss[0].Messages) != 2 ||
		!hasMessage(ss[0], "user", "why does the migration hang") ||
		!hasMessage(ss[0], "assistant", "the advisory lock was never released") {
		t.Fatalf("compressed messages: %+v", ss)
	}
	alloc := after.TotalAlloc - before.TotalAlloc
	t.Logf("reading a %d MB rollout allocated %d bytes", size>>20, alloc)
	// The streamed read allocated 16 MB locally; leave room for runtime noise.
	if alloc > 24<<20 {
		t.Errorf("reading a %d MB rollout allocated %d MB, want at most 24 MB", size>>20, alloc>>20)
	}
}

// A file that is not there, or that holds no frame to salvage, is an error rather
// than an empty rollout: the empty answer would be remembered as a read.
func TestZstdScanReportsAFileItCannotRead(t *testing.T) {
	if !ZstdAvailable() {
		t.Skip("zstd CLI not installed")
	}
	dir := t.TempDir()
	drain := func(r io.Reader) error {
		_, err := io.Copy(io.Discard, r)
		return err
	}
	missing := filepath.Join(dir, "missing.jsonl.zst")
	if err := zstdScan(missing, "codex", drain); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a missing file: %v", err)
	}
	garbage := filepath.Join(dir, "garbage.jsonl.zst")
	if err := os.WriteFile(garbage, []byte("not a zstd frame"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := zstdScan(garbage, "codex", drain); err == nil {
		t.Error("a file with no frame in it read as an empty rollout")
	}
}

// The scan runs while zstd is still writing. A parse that stops early, with an
// error or a panic safeParse recovers further up, must not leave zstd blocked on
// a pipe nobody reads.
func TestZstdScanStopsOnErrorOrPanic(t *testing.T) {
	if !ZstdAvailable() {
		t.Skip("zstd CLI not installed")
	}
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	path := filepath.Join(root, "rollout.jsonl.zst")
	// More than a pipe holds: waiting without killing would block on stdout.
	if err := os.WriteFile(path, zstdFrame(t, strings.Repeat("x", 2<<20)), 0o600); err != nil {
		t.Fatal(err)
	}
	want := errors.New("scan stopped")
	for _, panics := range []bool{false, true} {
		name := "error"
		if panics {
			name = "panic"
		}
		t.Run(name, func(t *testing.T) {
			defer func() {
				if got := recover(); got != nil || panics {
					if err, ok := got.(error); !panics || !ok || !errors.Is(err, want) {
						t.Errorf("panic = %v, want %v", got, want)
					}
				}
			}()
			err := zstdScan(path, "codex", func(r io.Reader) error {
				var first [1]byte
				if _, err := io.ReadFull(r, first[:]); err != nil {
					t.Fatal(err)
				}
				if panics {
					panic(want)
				}
				return want
			})
			if !errors.Is(err, want) {
				t.Errorf("scan error = %v, want %v", err, want)
			}
		})
	}
}

func TestCodexReadsTheArchivedDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CODEX_ROOT", root)
	t.Setenv("DEJA_XCODE_CODEX_ROOT", filepath.Join(root, "absent"))
	writeCodexRollout(t, filepath.Join(root, "archived_sessions", "2026", "07", "17"), false)

	ss := LoadCodex()
	if len(ss) != 1 || ss[0].ID != "thread-7" {
		t.Fatalf("an archived session is not read: %+v", ss)
	}
	// It is a transcript, not unaccounted content: doctor's sidecar row must
	// not claim it.
	for _, f := range CodexSidecarFiles() {
		if strings.Contains(f, "archived_sessions") {
			t.Errorf("an archived rollout is counted as a sidecar: %s", f)
		}
	}
}

// Codex materializes a compressed rollout back to plain before appending, so
// both names exist for one session while that happens. Reading both would
// double the session; the plain one wins because it is the one being written.
func TestCodexReadsOneFilePerSession(t *testing.T) {
	if !ZstdAvailable() {
		t.Skip("zstd CLI not installed")
	}
	root := t.TempDir()
	t.Setenv("DEJA_CODEX_ROOT", root)
	t.Setenv("DEJA_XCODE_CODEX_ROOT", filepath.Join(root, "absent"))
	dir := filepath.Join(root, "sessions", "2026", "07", "17")
	writeCodexRollout(t, dir, true)
	writeCodexRollout(t, dir, false)

	if n := len(CodexFiles()); n != 2 {
		t.Fatalf("both files should be visible to diagnostics, got %d", n)
	}
	ss := LoadCodex()
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1 — the same session under two names", len(ss))
	}
	if strings.HasSuffix(ss[0].Path, ".zst") {
		t.Errorf("the compressed copy won: %s", ss[0].Path)
	}
}

// Without zstd the store says so, the way DeepSeek Harness and Zed already do.
// A store of plain rollouts must not claim a problem it does not have.
func TestCodexSaysWhenItNeedsZstd(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CODEX_ROOT", root)
	t.Setenv("DEJA_XCODE_CODEX_ROOT", filepath.Join(root, "absent"))
	writeCodexRollout(t, filepath.Join(root, "sessions", "2026", "07", "17"), false)
	if got := SkipReason("codex"); got != "" {
		t.Errorf("a plain store explains nothing, got %q", got)
	}

	// A compressed rollout beside it: the answer depends on the tool, and on a
	// machine that has it there is still nothing to explain.
	if err := os.WriteFile(filepath.Join(root, "sessions", "2026", "07", "17",
		"rollout-thread-8.jsonl.zst"), []byte("not a real frame"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := SkipReason("codex")
	if ZstdAvailable() {
		if got != "" {
			t.Errorf("zstd is installed, so nothing is missing, got %q", got)
		}
		return
	}
	if got != "zstd CLI not found" {
		t.Errorf("SkipReason = %q, want the missing tool named", got)
	}
}

func writeRollout(t *testing.T, name string, lines []string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// A sub-agent spawned with fork_context starts with a copy of its parent's
// conversation, the parent's session_meta first, all stamped at spawn time.
// Read as the child's own, the child took the parent's first prompt as its
// title and every parent word as its own (#5210). Layout from codex-cli
// 0.149.0: the copy carries no thread_settings_applied, the child writes one
// before its first turn.
func TestCodexForkContextChildKeepsOnlyItsOwnTurns(t *testing.T) {
	const child, parent = "01a14d6f-9404-7821-a405-0a7adcaf41e9", "01a14d6c-c3d5-7f61-990c-0d5dd47a0c10"
	p := writeRollout(t, "rollout-2026-10-18T08-15-20-"+child+".jsonl", []string{
		`{"timestamp":"2026-10-18T05:15:20.470Z","type":"session_meta","payload":{"id":"` + child + `","forked_from_id":"` + parent + `","cwd":"/w/app","source":{"subagent":{"thread_spawn":{"parent_thread_id":"` + parent + `","depth":1}}}}}`,
		`{"timestamp":"2026-10-18T05:15:20.470Z","type":"session_meta","payload":{"id":"` + parent + `","cwd":"/w/app","source":"cli"}}`,
		`{"timestamp":"2026-10-18T05:15:20.470Z","type":"event_msg","payload":{"type":"task_started","turn_id":"t1"}}`,
		`{"timestamp":"2026-10-18T05:15:20.471Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"parent asks about EMU-E5"}]}}`,
		`{"timestamp":"2026-10-18T05:15:20.471Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"t1"}}`,
		`{"timestamp":"2026-10-18T05:15:20.472Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"parent spawns the helper"}]}}`,
		`{"timestamp":"2026-10-18T05:15:20.480Z","type":"event_msg","payload":{"type":"thread_settings_applied","thread_settings":{"model":"m"}}}`,
		`{"timestamp":"2026-10-18T05:15:20.481Z","type":"event_msg","payload":{"type":"task_started","turn_id":"t9"}}`,
		`{"timestamp":"2026-10-18T05:15:20.500Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"child task ECHIDNA-E7"}]}}`,
		`{"timestamp":"2026-10-18T05:15:20.510Z","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","call_id":"c1","input":"*** Begin Patch\n*** Update File: app.go\n@@\n+var echidnaLimit = 2048 // ECHIDNA-E7 child line\n*** End Patch\n"}}`,
		`{"timestamp":"2026-10-18T05:15:26.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}}`,
	})
	ss, err := ParseCodexRollout(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions = %d, err %v", len(ss), err)
	}
	s := ss[0]
	if s.ID != child || s.Kind != "subagent" || s.Parent != parent {
		t.Errorf("identity = %s %s %s, want the child under its parent", s.ID, s.Kind, s.Parent)
	}
	var users []string
	wrote := false
	for _, m := range s.Messages {
		if m.Role == "user" {
			users = append(users, m.Text)
		}
		wrote = wrote || m.Role == RoleWrote
	}
	if len(users) != 1 || users[0] != "child task ECHIDNA-E7" {
		t.Errorf("user turns = %q, want only the child's own task", users)
	}
	if !wrote {
		t.Error("the child's own edit lost its wrote record")
	}

	// Control: with no thread_settings_applied after the inherited meta, a
	// format that marks no boundary, nothing is dropped (#3933 shape).
	q := writeRollout(t, "rollout-2026-06-01T02-00-00-c.jsonl", []string{
		`{"timestamp":"2026-06-01T02:00:00Z","type":"session_meta","payload":{"id":"c","forked_from_id":"a","cwd":"/w"}}`,
		`{"timestamp":"2026-06-01T01:00:00Z","type":"session_meta","payload":{"id":"a","cwd":"/w"}}`,
		`{"timestamp":"2026-06-01T02:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"child question"}]}}`,
	})
	ss, err = ParseCodexRollout(q)
	if err != nil || len(ss) != 1 || len(ss[0].Messages) != 1 {
		t.Fatalf("unmarked fork: %+v, err %v, want its one turn kept", ss, err)
	}
}

// A model with no apply_patch tool runs apply_patch through exec_command, and
// Codex applies it all the same. Read as a command only, the edit left no
// files or wrote record and blame found no session for the line (#5211).
func TestCodexShellApplyPatchRecordsTheEdit(t *testing.T) {
	p := writeRollout(t, "rollout-2026-10-18T08-04-16-s.jsonl", []string{
		`{"timestamp":"2026-10-18T05:04:16Z","type":"session_meta","payload":{"id":"s","cwd":"/w/app"}}`,
		`{"timestamp":"2026-10-18T05:04:17Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"create parent.txt"}]}}`,
		`{"timestamp":"2026-10-18T05:04:18Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"c1","arguments":"{\"cmd\":\"apply_patch <<'EOF'\\n*** Begin Patch\\n*** Update File: parent.txt\\n@@\\n-QUOKKA-Q0 the line this edit replaces\\n+QUOKKA-Q1 written by the parent\\n*** End Patch\\nEOF\"}"}}`,
		`{"timestamp":"2026-10-18T05:04:19Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"Exit code: 0\nWall time: 0 seconds\nOutput:\nSuccess. Updated the following files:\nM parent.txt\n"}}`,
	})
	ss, err := ParseCodexRollout(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions = %d, err %v", len(ss), err)
	}
	byRole := map[string][]string{}
	for _, m := range ss[0].Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
	}
	if got := strings.Join(byRole[RoleFiles], "\n"); got != "/w/app/parent.txt" {
		t.Errorf("files = %q, want the patched file", got)
	}
	h, _ := HashWrittenLine("QUOKKA-Q1 written by the parent")
	if len(byRole[RoleWrote]) != 1 {
		t.Fatalf("wrote = %q, want one record", byRole[RoleWrote])
	}
	if path, has := WroteRecordHas(byRole[RoleWrote][0], h); !has || path != "/w/app/parent.txt" {
		t.Errorf("wrote = %q, want the added line under parent.txt", byRole[RoleWrote])
	}
	if len(byRole[RoleEdit]) != 1 || !strings.Contains(byRole[RoleEdit][0], "QUOKKA-Q0") {
		t.Errorf("edit = %q, want the replaced line", byRole[RoleEdit])
	}
}

// Codex applies a shell apply_patch in the call's workdir, not the session's
// cwd: filed under the cwd, a patch run from a subdirectory named a file that
// does not exist and blame found no writer.
func TestCodexShellApplyPatchResolvesAgainstItsWorkdir(t *testing.T) {
	p := writeRollout(t, "rollout-2026-10-18T08-04-16-w.jsonl", []string{
		`{"timestamp":"2026-10-18T05:04:16Z","type":"session_meta","payload":{"id":"w","cwd":"/w/app"}}`,
		`{"timestamp":"2026-10-18T05:04:17Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"edit sub/parent.txt"}]}}`,
		`{"timestamp":"2026-10-18T05:04:18Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"c1","arguments":"{\"cmd\":\"apply_patch <<'EOF'\\n*** Begin Patch\\n*** Update File: parent.txt\\n@@\\n-QUOKKA-W0 the line this edit replaces\\n+QUOKKA-W1 written from sub\\n*** End Patch\\nEOF\",\"workdir\":\"sub\"}"}}`,
		`{"timestamp":"2026-10-18T05:04:19Z","type":"response_item","payload":{"type":"function_call","name":"shell_command","call_id":"c2","arguments":"{\"command\":\"apply_patch <<'EOF'\\n*** Begin Patch\\n*** Add File: other.txt\\n+QUOKKA-W2 written from /w/lib\\n*** End Patch\\nEOF\",\"workdir\":\"/w/lib\"}"}}`,
	})
	ss, err := ParseCodexRollout(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions = %d, err %v", len(ss), err)
	}
	var files []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleFiles {
			files = append(files, m.Text)
		}
	}
	if got := strings.Join(files, "\n"); got != "/w/app/sub/parent.txt\n/w/lib/other.txt" {
		t.Errorf("files = %q, want each patch under its call's workdir", got)
	}
}

// A history.jsonl line whose session has a rollout repeats that rollout's
// prompt. The full build has always dropped it; the per-file pass read
// history.jsonl alone and kept it, so the session was asked twice and owned by
// the history line (#4180). A line with no rollout is still the only record of
// its session and stays.
func TestCodexHistoryLeavesOutSessionsThatHaveARollout(t *testing.T) {
	root := t.TempDir()
	day := filepath.Join(root, "sessions", "2026", "10", "01")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	withRollout := "01a0f6ce-5201-7150-9e18-9a127afed808"
	archived := "01a0f6ce-0000-7150-9e18-9a127afed808"
	alone := "01a0f6ce-9999-7150-9e18-9a127afed808"
	if err := os.WriteFile(filepath.Join(day, "rollout-2026-10-01T12-31-51-"+withRollout+".jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "archived_sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "archived_sessions", "rollout-2026-09-01T10-00-00-"+archived+".jsonl.zst"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	hist := filepath.Join(root, "history.jsonl")
	body := `{"session_id":"` + withRollout + `","ts":1790847117,"text":"asked in the TUI"}` + "\n" +
		`{"session_id":"` + archived + `","ts":1790847118,"text":"archived since"}` + "\n" +
		`{"session_id":"` + alone + `","ts":1790847119,"text":"rollout long gone"}` + "\n"
	if err := os.WriteFile(hist, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodexHistoryFromOffset(hist, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != alone {
		ids := make([]string, 0, len(ss))
		for _, s := range ss {
			ids = append(ids, s.ID)
		}
		t.Errorf("history sessions = %v, want only %s", ids, alone)
	}
}

// A shell apply_patch that opens with `cd sub &&` edits sub/<header>, and the
// reader filed it under the call's workdir or the session's cwd instead, so
// blame credited a file of the same name there (#5306).
func TestCodexShellApplyPatchFollowsALeadingCD(t *testing.T) {
	p := writeRollout(t, "rollout-2026-10-18T08-04-16-cd.jsonl", []string{
		`{"timestamp":"2026-10-18T05:04:16Z","type":"session_meta","payload":{"id":"cd","cwd":"/w/app"}}`,
		`{"timestamp":"2026-10-18T05:04:17Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"edit sub/parent.txt"}]}}`,
		`{"timestamp":"2026-10-18T05:04:18Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"c1","arguments":"{\"cmd\":\"cd sub && apply_patch <<'EOF'\\n*** Begin Patch\\n*** Update File: parent.txt\\n@@\\n-WOMBAT-C0 the line this edit replaces\\n+WOMBAT-C1 written after cd\\n*** End Patch\\nEOF\"}"}}`,
		`{"timestamp":"2026-10-18T05:04:19Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"c2","arguments":"{\"cmd\":\"apply_patch <<'EOF'\\n*** Begin Patch\\n*** Update File: parent.txt\\n@@\\n-WOMBAT-C1\\n+WOMBAT-C2 written with workdir\\n*** End Patch\\nEOF\",\"workdir\":\"sub\"}"}}`,
		`{"timestamp":"2026-10-18T05:04:20Z","type":"response_item","payload":{"type":"function_call","name":"shell_command","call_id":"c3","arguments":"{\"command\":\"cd /w/lib && apply_patch <<'EOF'\\n*** Begin Patch\\n*** Add File: other.txt\\n+WOMBAT-C3 written in /w/lib\\n*** End Patch\\nEOF\"}"}}`,
		`{"timestamp":"2026-10-18T05:04:21Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"c4","arguments":"{\"cmd\":\"cd \\\"deep dir\\\" && apply_patch <<'EOF'\\n*** Begin Patch\\n*** Add File: inner.txt\\n+WOMBAT-C4 written in a quoted dir under the workdir\\n*** End Patch\\nEOF\",\"workdir\":\"sub\"}"}}`,
	})
	ss, err := ParseCodexRollout(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions = %d, err %v", len(ss), err)
	}
	var files []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleFiles {
			files = append(files, m.Text)
		}
	}
	want := "/w/app/sub/parent.txt\n/w/app/sub/parent.txt\n/w/lib/other.txt\n/w/app/sub/deep dir/inner.txt"
	if got := strings.Join(files, "\n"); got != want {
		t.Errorf("files =\n%s\nwant each patch where its cd left it:\n%s", got, want)
	}
}

func TestShellPatchDir(t *testing.T) {
	tests := []struct {
		name, cmd, dir, want string
	}{
		{"no cd", "apply_patch <<'EOF'", "/w/app", "/w/app"},
		{"relative", "cd sub && apply_patch <<'EOF'", "/w/app", "/w/app/sub"},
		{"absolute", "cd /w/lib && apply_patch", "/w/app", "/w/lib"},
		{"semicolon", "cd sub; apply_patch", "/w/app", "/w/app/sub"},
		{"chained", "cd sub && cd inner && apply_patch", "/w/app", "/w/app/sub/inner"},
		{"double quoted", `cd "a b" && apply_patch`, "/w/app", "/w/app/a b"},
		{"single quoted", `cd 'a b' && apply_patch`, "/w/app", "/w/app/a b"},
		{"leading blanks", "  cd sub && apply_patch", "/w/app", "/w/app/sub"},
		{"parent", "cd .. && apply_patch", "/w/app/sub", "/w/app"},
		{"home is not followed", "cd ~ && apply_patch", "/w/app", "/w/app"},
		{"variable is not followed", "cd $TMP && apply_patch", "/w/app", "/w/app"},
		{"dash is not followed", "cd - && apply_patch", "/w/app", "/w/app"},
		{"cd with no command after", "cd sub", "/w/app", "/w/app"},
		{"cd not chained", "cd sub || apply_patch", "/w/app", "/w/app"},
		{"unclosed quote", `cd "sub && apply_patch`, "/w/app", "/w/app"},
		{"cd later in the command", "echo hi && cd sub && apply_patch", "/w/app", "/w/app"},
		{"a word starting with cd", "cdx sub && apply_patch", "/w/app", "/w/app"},
		{"windows dir", `cd sub && apply_patch`, `C:\w\app`, `C:\w\app\sub`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ShellPatchDir(tc.cmd, tc.dir)
			if tc.name == "windows dir" {
				got = strings.ReplaceAll(got, "/", `\`)
			}
			if got != tc.want {
				t.Errorf("ShellPatchDir(%q, %q) = %q, want %q", tc.cmd, tc.dir, got, tc.want)
			}
		})
	}
}

// Command Code 1.73 writes a `session` header and then one `message` envelope
// per line, with Claude's content blocks inside and its own tool names. The
// reader looked for `role` at the top level and indexed none of it (#4370).
// The lines are the ones command-code 1.73.4 wrote, cut down.
const commandCodeV3 = `{"type":"session","version":3,"id":"1583a430-5538-475d-9190-0d726c7a374f","timestamp":"2026-10-01T20:09:14.839Z","cwd":"/private/tmp/proj-cc"}
{"type":"message","id":"f82f93b8","parentId":null,"timestamp":"2026-10-01T20:09:15.324Z","message":{"role":"user","content":[{"type":"text","text":"fix the retry loop"}],"meta":{"source":"user"}}}
{"type":"message","id":"18f845f1","parentId":"f82f93b8","timestamp":"2026-10-01T20:09:15.324Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"call_baf6","name":"shell_command","input":{"command":"git status --short && cat retry.cfg"}}],"meta":{"source":"model"}},"model":"shim/luna"}
{"type":"message","id":"3fa018fa","parentId":"18f845f1","timestamp":"2026-10-01T20:09:15.324Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_baf6","content":[{"type":"text","text":"?? retry.cfg\nretry = 3\n"}]}],"meta":{"source":"tool"}}}
{"type":"message","id":"f0e0356d","parentId":"3fa018fa","timestamp":"2026-10-01T20:09:31.013Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"call_7654","name":"read_file","input":{"file_path":"/tmp/proj-cc/retry.cfg"}}],"meta":{"source":"model"}}}
{"type":"message","id":"323f220d","parentId":"f0e0356d","timestamp":"2026-10-01T20:09:34.930Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"call_bbb6","name":"edit_file","input":{"file_path":"/tmp/proj-cc/retry.cfg","old_string":"retry = 3","new_string":"retry = 5"}}],"meta":{"source":"model"}}}
{"type":"message","id":"5dbfba1f","parentId":"323f220d","timestamp":"2026-10-01T20:09:38.707Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"call_da97","name":"write_file","input":{"file_path":"/tmp/proj-cc/backoff.txt","content":"backoff = exponential with jitter\n"}}],"meta":{"source":"model"}}}
{"type":"message","id":"e07b0c50","parentId":"58a914d2","timestamp":"2026-10-01T20:09:42.458Z","message":{"role":"assistant","content":[{"type":"text","text":"Added backoff.txt with an exponential backoff note."}],"meta":{"source":"model"}}}
`

func TestCommandCodeReadsTheV3Transcript(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COMMANDCODE_ROOT", root)
	dir := filepath.Join(root, "private-tmp-proj-cc")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "1583a430-5538-475d-9190-0d726c7a374f.jsonl")
	if err := os.WriteFile(path, []byte(commandCodeV3), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCommandCodeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want the v3 transcript read", len(ss))
	}
	s := ss[0]
	if s.ID != "1583a430-5538-475d-9190-0d726c7a374f" {
		t.Errorf("id = %q", s.ID)
	}
	if want := "tmp/proj-cc"; s.Project != want {
		t.Errorf("project = %q, want %q from the header's cwd", s.Project, want)
	}
	byRole := map[string][]string{}
	for _, m := range s.Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
		if m.Time.IsZero() {
			t.Errorf("%s record has no time", m.Role)
		}
	}
	has := func(role, sub string) {
		t.Helper()
		for _, txt := range byRole[role] {
			if strings.Contains(txt, sub) {
				return
			}
		}
		t.Errorf("no %s record holding %q; got %q", role, sub, byRole[role])
	}
	has("user", "fix the retry loop")
	has("assistant", "exponential backoff")
	has(RoleToolOutput, "retry = 3")
	has(RoleCommand, "$ git status --short && cat retry.cfg")
	has(RoleFiles, "/tmp/proj-cc/retry.cfg")
	has(RoleFiles, "/tmp/proj-cc/backoff.txt")
	has(RoleEdit, "/tmp/proj-cc/retry.cfg\nretry = 3")
	has(RoleWrote, "/tmp/proj-cc/backoff.txt")

	// An appended turn read from an offset keeps the session the header names.
	more := `{"type":"message","id":"x1","parentId":"e07b0c50","timestamp":"2026-10-01T20:18:14.182Z","message":{"role":"user","content":[{"type":"text","text":"what file did you add"}]}}` + "\n"
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString(more); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	ss, err = ParseCommandCodeFileFromOffset(path, int64(len(commandCodeV3)))
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != s.ID || ss[0].Project != s.Project {
		t.Fatalf("appended turn = %+v, want it under the header's id and project", ss)
	}
}

// The client's own filter: a migration backup and the prompt history beside a
// transcript are not conversations. The .v2.bak is the old copy of a session
// the .jsonl now holds, so reading it would count the session twice.
func TestCommandCodeSkipsBackupAndPromptFiles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COMMANDCODE_ROOT", root)
	dir := filepath.Join(root, "private-tmp-proj-cc")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.jsonl", "a.checkpoints.jsonl", "a.v2.bak", "a.prompts.jsonl"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(commandCodeV3), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files := CommandCodeSessionFiles()
	if len(files) != 1 || filepath.Base(files[0]) != "a.jsonl" {
		t.Fatalf("files = %v, want a.jsonl alone", files)
	}
	if CommandCodeUnderRoot(filepath.Join(dir, "a.prompts.jsonl")) {
		t.Error("the prompt history was claimed as a transcript")
	}
	// doctor counts these as a deliberate skip, not as files it failed to read.
	if err := os.WriteFile(filepath.Join(dir, "a.meta.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if beside := CommandCodeCheckpointFiles(); len(beside) != 3 {
		t.Errorf("beside = %v, want checkpoints, prompts and meta", beside)
	}
}

// The allowlist carried make and gradle and not just, task or mise, so a
// repository driven by a justfile recorded none of its own commands. That costs
// more than the command table: the point-of-action hook asks whether a session
// ran the command before it speaks, so on those repositories it could never
// speak about a deploy or a migration at all.
func TestWorthIndexingKeepsTheTaskRunners(t *testing.T) {
	for _, c := range []string{
		"just build",
		"just deploy staging",
		"task test",
		"task db:migrate",
		"mise run lint",
		"cd repo && just release",
	} {
		if !worthIndexing(c) {
			t.Errorf("dropped a task runner: %q", c)
		}
	}
	// Each of these names is an ordinary English word, so they only count at
	// the start of a segment — otherwise every sentence about a task became a
	// command this machine had run.
	for _, c := range []string{
		"echo 'just do it'",
		"ls tasks/",
		"cat notes.md | grep task",
	} {
		if worthIndexing(c) {
			t.Errorf("kept a word that names no runner: %q", c)
		}
	}
	// A runner named inside another command's argument is that command's
	// business, not a run of its own — the rule the allowlist already applies
	// to `grep "go test" log`. These are the cases the anchor decides: the
	// segment is neither trivial nor otherwise meaningful, so without it the
	// word alone would carry them in.
	for _, c := range []string{
		"ssh devbox task deploy",
		"xargs just build",
		"time mise run lint",
		// The one this rule kept when it was judged per pipe: a quoted
		// alternation is cut on its own `\|` and the fragment reads like a
		// run. In real-world stores, one line in 119,024.
		`grep -n "worktree names\|task signal" cmd/deja/doctor.go`,
	} {
		if worthIndexing(c) {
			t.Errorf("kept a runner named as another command's argument: %q", c)
		}
	}
}

// The row names what sqlite3 said, not its exit code: the first stderr line,
// or nothing when the error carries none (#3190).
func TestExitStderrLineIsTheFirstLineSqliteWrote(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	cmd := exec.CommandContext(t.Context(), "sh", "-c", "echo 'Error: in prepare, file is not a database (26)' >&2; echo second >&2; exit 26")
	_, err := cmd.Output()
	if got := ExitStderrLine(err); got != "Error: in prepare, file is not a database (26)" {
		t.Fatalf("line = %q", got)
	}
	if got := ExitStderrLine(errors.New("plain")); got != "" {
		t.Fatalf("a plain error gave %q", got)
	}
	if got := ExitStderrLine(nil); got != "" {
		t.Fatalf("nil gave %q", got)
	}
}
