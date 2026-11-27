package sources

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// These cases fill in the branches the conversation tests above leave: the
// Windows roaming root, the walk and parse error paths, and the rarer shapes
// a 4.x message, tool call or tool result can take.

// On Windows the IDE's data directory is under AppData\Local, and a Roaming
// copy beside it is read too when it is a directory. The lookup asks
// runtime.GOOS, so the Roaming half only runs on Windows; elsewhere the same
// layout must not add a root.
func TestCodeBuddyIDE4RootsRoamingCopy(t *testing.T) {
	home := cb4Env(t)
	roaming := filepath.Join(home, "AppData", "Roaming", "CodeBuddyExtension", "Data")
	local := codeBuddyIDE4RootFor("windows", home)
	if want := filepath.Join(home, "AppData", "Local", "CodeBuddyExtension", "Data"); local != want {
		t.Fatalf("windows root = %q, want %q", local, want)
	}
	// A file where the Roaming directory would be is not a root.
	cb4Write(t, roaming, []byte("not a directory"))
	if got := CodeBuddyIDE4Roots(); len(got) != 1 {
		t.Errorf("roots with a Roaming file = %v, want only the default", got)
	}
	if err := os.Remove(roaming); err != nil {
		t.Fatal(err)
	}
	c := cb4Minimal(t, roaming)
	got := CodeBuddyIDE4Roots()
	if runtime.GOOS != "windows" {
		if len(got) != 1 || got[0] != CodeBuddyIDE4DefaultRoot() {
			t.Errorf("roots on %s = %v, want only the default", runtime.GOOS, got)
		}
		return
	}
	if len(got) != 2 || got[0] != local || got[1] != roaming {
		t.Fatalf("windows roots = %v, want Local then Roaming", got)
	}
	if files := CodeBuddyIDE4SessionFiles(); len(files) != 1 || files[0] != c.Index {
		t.Errorf("session files = %v, want the Roaming conversation", files)
	}
	// DEJA_CODEBUDDY_LEGACY_ROOTS replaces both, and drops empty entries.
	list := string(os.PathListSeparator) + roaming + string(os.PathListSeparator)
	t.Setenv("DEJA_CODEBUDDY_LEGACY_ROOTS", list)
	if got := CodeBuddyIDE4Roots(); len(got) != 1 || got[0] != roaming {
		t.Errorf("legacy roots = %v, want [%s]", got, roaming)
	}
}

// An empty entry in the path list is not the current directory.
func TestCodeBuddyIDE4LegacyRootsSkipEmptyEntries(t *testing.T) {
	home := cb4Env(t)
	a := filepath.Join(home, "a")
	b := filepath.Join(home, "b", "..", "b")
	sep := string(os.PathListSeparator)
	t.Setenv("DEJA_CODEBUDDY_LEGACY_ROOTS", sep+a+sep+sep+b+sep)
	got := CodeBuddyIDE4Roots()
	if len(got) != 2 || got[0] != a || got[1] != filepath.Join(home, "b") {
		t.Errorf("roots = %v, want [%s %s] cleaned", got, a, filepath.Join(home, "b"))
	}
}

// The path checks reject an id the store would never write before a path is
// built from it, and a path that is not under any root.
func TestCodeBuddyIDE4PathGuards(t *testing.T) {
	home := cb4Env(t)
	data := CodeBuddyIDE4DefaultRoot()
	for _, id := range []string{"", strings.Repeat("a", 201), "conv 01", "conv.01", "Lw=="} {
		if codeBuddyIDE4SafeID(id) {
			t.Errorf("safe id %q accepted", id)
		}
	}
	if !codeBuddyIDE4SafeID(strings.Repeat("a", 200)) || !codeBuddyIDE4SafeID("Conv_01-x") {
		t.Error("a store id rejected")
	}
	hist := filepath.Join(data, "u", "CodeBuddyIDE", "u", "history")
	for _, p := range []string{
		filepath.Join(hist, "ws0001", "conv 01", "index.json"),
		filepath.Join(hist, "ws 01", "conv0001", "index.json"),
		filepath.Join(hist, "ws0001", "conv0001", "other.json"),
	} {
		if _, ok := codeBuddyIDE4Parts(p); ok {
			t.Errorf("parts accepted %s", p)
		}
		if IsCodeBuddyIDE4Session(p) {
			t.Errorf("session accepted %s", p)
		}
	}
	// Two levels under a history directory is a conversation by its path
	// alone; the same shape outside every root is not a session.
	outside := filepath.Join(home, "elsewhere", "history", "ws0001", "conv0001", "index.json")
	if id, ok := codeBuddyIDE4Parts(outside); !ok || id != "conv0001" {
		t.Errorf("parts(%s) = %q, %v", outside, id, ok)
	}
	if IsCodeBuddyIDE4Session(outside) {
		t.Error("a conversation outside every root is a session")
	}
	// Three levels under a root whose first part is not an id the store
	// writes, and four levels under a non-history root, are not conversations.
	t.Setenv("DEJA_CODEBUDDY_LEGACY_ROOTS", filepath.Join(home, "copy"))
	for _, p := range []string{
		filepath.Join(home, "copy", "ws.01", "conv0001", "index.json"),
		filepath.Join(home, "copy", "a", "ws0001", "conv0001", "index.json"),
	} {
		if _, ok := codeBuddyIDE4Parts(p); ok {
			t.Errorf("parts accepted %s", p)
		}
	}
}

// A root whose index.json lists conversations is one workspace: a child
// directory with its own index.json is a conversation of it, not a workspace
// of a history root, even when the child holds a further index.
func TestCodeBuddyIDE4WorkspaceRootDepth(t *testing.T) {
	home := cb4Env(t)
	ws := filepath.Join(home, "copy", "ws0001")
	cb4Write(t, filepath.Join(ws, "index.json"), map[string]any{"conversations": []any{cb4Entry("conv0001", "craft", "x")}})
	nested := filepath.Join(ws, "conv0001", "sub0001", "index.json")
	cb4Write(t, nested, map[string]any{"messages": []any{}})
	t.Setenv("DEJA_CODEBUDDY_LEGACY_ROOTS", ws)
	if _, ok := codeBuddyIDE4Parts(nested); ok {
		t.Error("an index under a workspace root's conversation read as one")
	}
	// The same shape under a root with no index is a history root.
	hist := filepath.Join(home, "copy2")
	under := filepath.Join(hist, "ws0001", "conv0001", "index.json")
	cb4Write(t, under, map[string]any{"messages": []any{}})
	t.Setenv("DEJA_CODEBUDDY_LEGACY_ROOTS", hist)
	if id, ok := codeBuddyIDE4Parts(under); !ok || id != "conv0001" {
		t.Errorf("parts under a history copy = %q, %v", id, ok)
	}
}

// A directory the walk may not enter is named for doctor and the rest of the
// store is still listed. Permission bits mean nothing to root or on Windows.
func TestCodeBuddyIDE4WalkUnreadableDirectory(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits do not stop this user")
	}
	cb4Env(t)
	data := CodeBuddyIDE4DefaultRoot()
	c := cb4Minimal(t, data)
	locked := filepath.Join(data, "userB", "CodeBuddyIDE", "userB", "history")
	if err := os.MkdirAll(filepath.Join(locked, "ws0002"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	BeginWalk()
	if files := CodeBuddyIDE4SessionFiles(); len(files) != 1 || files[0] != c.Index {
		t.Errorf("session files = %v, want the readable conversation", files)
	}
	if _, ok := WalkFailures()[locked]; !ok {
		t.Errorf("walk failures = %v, want %s", WalkFailures(), locked)
	}
	BeginWalk()
}

// A walk that meets a file it cannot use keeps going: a missing root lists
// nothing, a symlinked root is walked through and reported under the
// configured spelling, and an index.json that is a directory is skipped.
func TestCodeBuddyIDE4WalkShapes(t *testing.T) {
	home := cb4Env(t)
	if got := walkCodeBuddyIDE4(filepath.Join(home, "absent")); len(got) != 0 {
		t.Errorf("walk of a missing root = %v", got)
	}
	target := filepath.Join(home, "target-data")
	c := cb4Minimal(t, target)
	// A directory named index.json where a conversation index would be.
	if err := os.MkdirAll(filepath.Join(filepath.Dir(c.Dir), "conv0009", "index.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := walkCodeBuddyIDE4(target); len(got) != 1 || got[0] != c.Index {
		t.Errorf("walk = %v, want [%s]", got, c.Index)
	}
	link := filepath.Join(home, "linked-data")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	t.Setenv("DEJA_CODEBUDDY_LEGACY_ROOTS", link)
	want := filepath.Join(link, strings.TrimPrefix(c.Index, target+string(os.PathSeparator)))
	if got := CodeBuddyIDE4SessionFiles(); len(got) != 1 || got[0] != want {
		t.Errorf("walk through a symlinked root = %v, want [%s]", got, want)
	}
	if !IsCodeBuddyIDE4Session(want) {
		t.Error("the configured spelling is not a session")
	}
}

// codeBuddyIDE4ReadFile refuses what is not a regular file, by name.
func TestCodeBuddyIDE4ReadCappedNotRegular(t *testing.T) {
	dir := t.TempDir()
	if _, err := codeBuddyIDE4ReadFile(dir); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("codeBuddyIDE4ReadFile(dir) error = %v", err)
	}
	// A transcript that exists but cannot be opened is not readable.
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		return
	}
	p := filepath.Join(dir, "locked.jsonl")
	cb4Write(t, p, []byte("{}\n"))
	if err := os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0o644) })
	if codeBuddyIDE4Readable(p) {
		t.Error("an unopenable transcript reported as readable")
	}
}

// A conversation index the reader cannot open is an error for the pass to
// count, and a message id that is not a file name is skipped by name.
func TestParseCodeBuddyIDE4UnreadableIndexAndUnsafeMessageID(t *testing.T) {
	cb4Env(t)
	root := CodeBuddyIDE4DefaultRoot()
	c := cb4New(t, root, "userA/CodeBuddyIDE/userA/history", "ws0001", "conv0001")
	if err := os.MkdirAll(c.Index, 0o755); err != nil {
		t.Fatal(err)
	}
	if ss, err := parseCodeBuddyIDE4(c.Index); err == nil || len(ss) != 0 {
		t.Errorf("index that is a directory = %d sessions, %v; want an error", len(ss), err)
	}
	d := cb4New(t, root, "userA/CodeBuddyIDE/userA/history", "ws0001", "conv0002")
	cb4Index(t, d, []any{cb4Request([]string{"../escape", "m01"}, cb4StartMS, nil)})
	cb4Message(t, d, "m01", "user", []any{cb4Text("a safe one")}, nil, cb4At)
	state := DiagSave()
	defer DiagRestore(state)
	s := cb4ParseOne(t, d.Index)
	if got := cb4Roles(s); len(got) != 1 || got[0] != "user: a safe one" {
		t.Errorf("messages = %v", got)
	}
	if reason := DiagUnusableRecords()[d.Index]["../escape"]; reason != "message id is not a file name" {
		t.Errorf("unsafe id reason = %q", reason)
	}
}

// The visible-text fallbacks a prompt can reach: a user_query left open, and
// an envelope whose query is empty but whose input box recorded the phrase.
func TestCodeBuddyIDE4VisibleTextFallbacks(t *testing.T) {
	for _, tc := range []struct {
		name  string
		raw   string
		extra map[string]any
		want  string
	}{
		{"open user_query", "<user_info>x</user_info>\n<user_query>\nstill typing", nil, "still typing"},
		{"two user_query blocks", "<user_query>first</user_query><user_query> </user_query><user_query>second</user_query>", nil, "first\nsecond"},
		{"envelope with the phrase in extra", "<user_info>x</user_info>\n<system_reminder>y</system_reminder>",
			map[string]any{"inputPhrase": []any{map[string]any{"type": "normal", "content": "typed in the box"}}}, "typed in the box"},
		// An empty query inside an envelope block goes with the block, and
		// the text after the envelope is what is left.
		{"empty query inside a stripped block", "<rules><user_query></user_query></rules>\ntail",
			nil, "tail"},
		{"plain text", "  just text  ", nil, "just text"},
		{"no text and no extra", "", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := codeBuddyIDE4VisibleText(tc.raw, tc.extra); got != tc.want {
				t.Errorf("visible = %q, want %q", got, tc.want)
			}
		})
	}
}

// A teammate message with neither an id attribute nor a from tag names no
// one and is dropped; one with an id and an empty body is just the name.
func TestCodeBuddyIDE4TeammateEdges(t *testing.T) {
	rest, out := codeBuddyIDE4Teammates("before\n<teammate-message>no sender</teammate-message>\n\n\n\nafter" +
		"<teammate-message teammate_id=\"w-1\">  </teammate-message>" +
		"<teammate-message><from>w-2</from></teammate-message>")
	if strings.Join(out, "|") != "**w-1**|**w-2**" {
		t.Errorf("teammates = %q", out)
	}
	if rest != "before\n\nafter" {
		t.Errorf("rest = %q", rest)
	}
	if codeBuddyIDE4Shutdown("{not json") || codeBuddyIDE4Shutdown(`{"type":"other"}`) || codeBuddyIDE4Shutdown("plain") {
		t.Error("a non-shutdown body read as a shutdown")
	}
}

// The input box's blocks are only taken for the prompt when one of them is a
// mention or an image; a command link without a name takes its uri.
func TestCodeBuddyIDE4PhraseEdges(t *testing.T) {
	if got := codeBuddyIDE4Phrase([]any{map[string]any{"type": "text", "text": "only text"}}); got != "" {
		t.Errorf("text-only blocks = %q, want none", got)
	}
	if got := codeBuddyIDE4Phrase("not a list"); got != "" {
		t.Errorf("non-list blocks = %q", got)
	}
	got := codeBuddyIDE4Phrase([]any{
		map[string]any{"type": "text", "text": ""},
		map[string]any{"type": "resource_link", "uri": "command://review"},
		map[string]any{"type": "text", "text": "now"},
		map[string]any{"type": "resource_link", "uri": "", "name": "dropped"},
	})
	if got != "/review now" {
		t.Errorf("phrase = %q, want the command named from its uri", got)
	}
	for _, tc := range []struct {
		link map[string]any
		want string
	}{
		{map[string]any{"uri": "command:///fix", "name": ""}, "/fix"},
		{map[string]any{"uri": "command://x", "name": "/named"}, "/named"},
		{map[string]any{"uri": "rule://team/style.md"}, "@rules:style.md"},
		{map[string]any{"uri": "https://example.com/a"}, "@share-html:https://example.com/a"},
		{map[string]any{"uri": "vscode://ext/thing", "name": "Thing"}, "Thing"},
		{map[string]any{"uri": "src/main.go", "name": "main.go", "_meta": map[string]any{"mentionType": "folder"}}, "main.go"},
		{map[string]any{"uri": "plain-name"}, ""},
		{map[string]any{"uri": "file:///work/a%20b.go"}, `@"/work/a b.go"`},
		// A file uri that does not unescape keeps its spelling, quoted.
		{map[string]any{"uri": "file://%zz"}, `@"%zz"`},
	} {
		meta, _ := tc.link["_meta"].(map[string]any)
		got, ok := codeBuddyIDE4Mention(tc.link, meta)
		if !ok {
			got = ""
		}
		if got != tc.want {
			t.Errorf("mention(%v) = %q, want %q", tc.link, got, tc.want)
		}
	}
	if codeBuddyIDE4BareValue("") || codeBuddyIDE4BareValue("a b") || !codeBuddyIDE4BareValue("a/b.c_d-e") {
		t.Error("bare value check")
	}
	if got := codeBuddyIDE4FileBadge("中文"); got != `"中文"` {
		t.Errorf("non-ASCII name with no path or extension = %q, want quoted", got)
	}
}

// The input phrase's block kinds, each as the converter writes it.
func TestCodeBuddyIDE4FromExtraKinds(t *testing.T) {
	got := codeBuddyIDE4FromExtra(map[string]any{"inputPhrase": []any{
		map[string]any{"type": "normal", "content": "see "},
		map[string]any{"type": "editor", "content": "a.go", "expandContent": "/w/a.go"},
		map[string]any{"type": "command", "content": "/test"},
		map[string]any{"type": "img", "content": "/tmp/shot.png"},
		map[string]any{"type": "skill", "content": "@review"},
		map[string]any{"type": "custom", "content": "@already"},
		map[string]any{"type": "custom", "content": ""},
		map[string]any{"type": "custom", "content": "bare"},
	}})
	if want := "see @/w/a.go/test@image:shot.png@skill:review@already@bare"; got != want {
		t.Errorf("phrase = %q, want %q", got, want)
	}
	// An image path is named by its last part whichever separator the
	// machine that wrote it used, so one store reads the same on every
	// platform, as the converter's fileName has it.
	for _, p := range []string{`C:\Users\me\Pictures\shot.png`, "/home/me/Pictures/shot.png", `C:/Users/me\Pictures/shot.png`} {
		img := map[string]any{"inputPhrase": []any{map[string]any{"type": "img", "content": p}}}
		if got := codeBuddyIDE4FromExtra(img); got != "@image:shot.png" {
			t.Errorf("image %q = %q, want @image:shot.png", p, got)
		}
	}
	// An empty phrase falls back to the text blocks, unless they hold an
	// envelope of their own.
	if got := codeBuddyIDE4FromExtra(map[string]any{
		"inputPhrase":         []any{map[string]any{"type": "normal", "content": "  "}},
		"sourceContentBlocks": []any{map[string]any{"type": "text", "text": "from blocks"}},
	}); got != "from blocks" {
		t.Errorf("fallback = %q", got)
	}
	if got := codeBuddyIDE4FromExtra(map[string]any{
		"sourceContentBlocks": []any{map[string]any{"type": "text", "text": "<user_info>x</user_info>"}},
	}); got != "" {
		t.Errorf("enveloped blocks = %q, want none", got)
	}
}

// The question-answer forms: the extra's own record, the block meta's, and
// the XML. A question with no answers adds no line.
func TestCodeBuddyIDE4QuestionAnswerForms(t *testing.T) {
	qa := map[string]any{"questions": []any{
		map[string]any{"question": "Which?", "answers": []any{"a", " ", "b"}},
		map[string]any{"question": "Unanswered?", "answers": []any{}},
		map[string]any{"question": " ", "answers": []any{"x"}},
	}}
	if got := codeBuddyIDE4QuestionAnswer("", map[string]any{"questionAnswer": qa}); got != "Which? → a, b" {
		t.Errorf("extra form = %q", got)
	}
	meta := map[string]any{"sourceContentBlocks": []any{
		map[string]any{"type": "text", "text": "x"},
		map[string]any{"type": "text", "_meta": map[string]any{"questionAnswer": qa}},
	}}
	if got := codeBuddyIDE4QuestionAnswer("go on", meta); got != "go on\nWhich? → a, b" {
		t.Errorf("block meta form = %q", got)
	}
	xml := "<question_answer><question_item><answers>only answers</answers></question_item>" +
		"<question_item><question>Q?</question></question_item></question_answer>"
	if got := codeBuddyIDE4QuestionAnswer("keep "+xml, nil); got != "keep" {
		t.Errorf("XML with no usable pair = %q, want the text around it", got)
	}
}

// Tool calls and results in the shapes the converter tolerates: arguments as
// a JSON string, a tool the converter does not rename, a call missing its id
// or name, a result under content rather than result, and the rarer result
// fields an MCP or IDE tool writes.
func TestParseCodeBuddyIDE4ToolShapes(t *testing.T) {
	cb4Env(t)
	root := CodeBuddyIDE4DefaultRoot()
	c := cb4New(t, root, "userA/CodeBuddyIDE/userA/history", "ws0001", "conv0001")
	cb4Index(t, c, []any{
		cb4Request([]string{"m01", "m02", "m03"}, cb4StartMS, nil),
	})
	cb4Message(t, c, "m01", "user", []any{cb4Text("run the checks")}, nil, cb4At)
	cb4Message(t, c, "m02", "assistant", []any{
		map[string]any{"type": "tool-call", "toolCallId": "s1", "toolName": "execute_command",
			"args": `{"command":"go vet ./...","requires_approval":false}`},
		map[string]any{"type": "tool-call", "toolCallId": "g1", "toolName": "grep_search",
			"args": map[string]any{"query": "retry"}},
		map[string]any{"type": "tool-call", "toolCallId": "", "toolName": "read_file"},
		map[string]any{"type": "tool-call", "toolCallId": "n1", "toolName": ""},
		map[string]any{"type": "tool-call", "toolCallId": "u1", "toolName": "ask_followup_question",
			"args": map[string]any{"question": "which?"}},
	}, nil, cb4LaterAt)
	cb4Message(t, c, "m03", "tool", []any{
		map[string]any{"type": "tool-result", "toolCallId": "s1", "content": map[string]any{
			"result": map[string]any{"stdout": "vet ok", "exitCode": 1.5},
		}},
		map[string]any{"type": "tool-result", "toolCallId": "g1", "result": map[string]any{
			"status": "cancelled", "result": map[string]any{"matches": []any{"a.go:1", "b.go:2"}, "listing": " "},
		}},
		map[string]any{"type": "tool-result", "toolCallId": "x1", "result": map[string]any{"result": "plain string result"}},
		map[string]any{"type": "tool-result", "toolCallId": "x2", "result": map[string]any{"result": 42, "errorMessage": "number result"}},
		map[string]any{"type": "tool-result", "toolCallId": "x3", "result": 7},
	}, nil, cb4EndAt)
	s := cb4ParseOne(t, c.Index)
	lines := cb4Roles(s)
	for _, want := range []struct{ role, text string }{
		{RoleCommand, "$ go vet ./..."},
		{RoleToolOutput, "vet ok"},
		{RoleToolOutput, "a.go:1\nb.go:2"},
		{RoleToolOutput, "plain string result"},
		{RoleToolOutput, "number result"},
	} {
		if !cb4Has(lines, want.role, want.text) {
			t.Errorf("missing %s %q in %v", want.role, want.text, lines)
		}
	}
	if cb4Has(lines, RoleCommand, "→ exit") {
		t.Errorf("a fractional exit code was stamped: %v", lines)
	}
	if s.ToolCalls["Bash"] != 1 || s.ToolCalls["grep_search"] != 1 || s.ToolCalls["ask_followup_question"] != 1 {
		t.Errorf("tool calls = %v", s.ToolCalls)
	}
	if s.ToolCalls["Read"] != 0 {
		t.Errorf("a call with no id was counted: %v", s.ToolCalls)
	}
	if s.ToolErrors != 1 {
		t.Errorf("ToolErrors = %d, want the cancelled one", s.ToolErrors)
	}
	// With commands off, no exit is tracked and the shell call still counts.
	t.Setenv("DEJA_INDEX_COMMANDS", "0")
	s2 := cb4ParseOne(t, c.Index)
	if cb4Count(cb4Roles(s2), RoleCommand) != 0 || s2.ToolCalls["Bash"] != 1 {
		t.Errorf("commands off: %v, calls %v", cb4Roles(s2), s2.ToolCalls)
	}
}

// The exit code an execute_command result reports, and the result text of
// the remaining shapes.
func TestCodeBuddyIDE4ExitCodeAndResultTextShapes(t *testing.T) {
	for _, tc := range []struct {
		name string
		v    any
		code int
		ok   bool
	}{
		{"float", map[string]any{"result": map[string]any{"exitCode": 2.0}}, 2, true},
		{"fraction", map[string]any{"result": map[string]any{"exitCode": 1.5}}, 0, false},
		{"string", map[string]any{"result": map[string]any{"exitCode": "1"}}, 0, false},
		{"no inner result", map[string]any{"exitCode": 1.0}, 0, false},
		{"not a map", "exit 1", 0, false},
	} {
		code, ok := codeBuddyIDE4ExitCode(tc.v)
		if code != tc.code || ok != tc.ok {
			t.Errorf("%s: exit = %d, %v; want %d, %v", tc.name, code, ok, tc.code, tc.ok)
		}
	}
	for _, tc := range []struct {
		name string
		v    any
		want string
	}{
		{"plain string", "printed", "printed"},
		{"list of strings", []any{"a", "", map[string]any{"type": "image"}, "b"}, "a\nb"},
		{"result string", map[string]any{"result": "inner"}, "inner"},
		{"stdout list", map[string]any{"result": map[string]any{"stdout": []any{"x", "y"}, "stderr": "warn"}}, "x\ny\nwarn"},
		{"no fields, error message", map[string]any{"result": map[string]any{"type": "edit"}, "errorMessage": "denied"}, "denied"},
		{"no fields at all", map[string]any{"result": map[string]any{"type": "edit"}}, ""},
		{"a number", 3.0, ""},
		{"nil", nil, ""},
	} {
		if got := codeBuddyIDE4ResultText(tc.v); got != tc.want {
			t.Errorf("%s: result text = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Write-back refuses each CodeBuddy session it cannot place, by its reason:
// a WorkBuddy transcript, a sub-agent's, a path that is not <id>.jsonl, and a
// folder whose directory no transcript records and no longer decodes.
func TestWriteBackCodeBuddyRefusals(t *testing.T) {
	home := cb4Env(t)
	cli := filepath.Join(home, "cli-projects")
	t.Setenv("DEJA_CODEBUDDY_ROOTS", cli)
	t.Setenv("CODEBUDDY_CONFIG_DIR", filepath.Join(home, "cli-config"))
	wb := filepath.Join(home, "wb")
	t.Setenv("WORKBUDDY_CONFIG_DIR", wb)
	t.Setenv("DEJA_CODEBUDDY_LEGACY_ROOTS", filepath.Join(home, "legacy"))
	for _, tc := range []struct {
		name, id, path, kind, want string
	}{
		{"WorkBuddy", "s1", filepath.Join(wb, "projects", "p", "s1.jsonl"), "", "WorkBuddy"},
		{"sub-agent", "s2", filepath.Join(cli, "p", "s2.jsonl"), "subagent", "no <id>.jsonl path"},
		{"not jsonl", "s3", filepath.Join(cli, "p", "s3.json"), "", "no <id>.jsonl path"},
		{"name is another id", "s4", filepath.Join(cli, "p", "other.jsonl"), "", "no <id>.jsonl path"},
		{"folder that decodes to nothing", "s5", filepath.Join(cli, "-no-such-dir-cb4", "s5.jsonl"), "", "does not keep the directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := writeBackSample("codebuddy", tc.id, tc.path)
			s.Kind = tc.kind
			_, _, err := renderCodeBuddy(s, writeBackSampleTurns())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one saying %q", err, tc.want)
			}
		})
	}
}

// The folder's directory comes from a sibling transcript when one records it,
// and the scan stops after the bounded number of siblings.
func TestCodeBuddyFolderCWDFromSiblings(t *testing.T) {
	home := cb4Env(t)
	work := filepath.Join(home, "work", "proj")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(home, "cli", strings.TrimPrefix(claudeEncodePath(work), "-"))
	line := `{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}],"cwd":` + jsonQuote(work) + "}\n"
	cb4Write(t, filepath.Join(folder, "a.jsonl"), []byte(line))
	if got := codeBuddyFolderCWD(folder); got != work {
		t.Errorf("cwd = %q, want %q", got, work)
	}
	// More siblings than the scan reads, none recording a directory that
	// fits, and a folder name that decodes to nothing on disk.
	far := filepath.Join(home, "cli", "-no-such-dir-cb4x")
	for i := range claudeFolderScanFiles + 2 {
		cb4Write(t, filepath.Join(far, strings.Repeat("f", i+1)+".jsonl"), []byte("{}\n"))
	}
	if got := codeBuddyFolderCWD(far); got != "" {
		t.Errorf("cwd = %q, want none", got)
	}
}

// An envelope whose <user_query> is empty, with nothing in the extra to fall
// back on, is a turn the client sent on its own: no message and nothing typed.
// An empty query before another block leaks no tag, and a query the IDE left
// open is still read to the end.
func TestParseCodeBuddyIDE4EmptyUserQuery(t *testing.T) {
	cb4Env(t)
	root := CodeBuddyIDE4DefaultRoot()
	c := cb4New(t, root, "userA/CodeBuddyIDE/userA/history", "ws0001", "conv0001")
	cb4Index(t, c, []any{cb4Request([]string{"m01", "m02", "m03", "m04", "m05"}, cb4StartMS, nil)})
	cb4Message(t, c, "m01", "user", []any{cb4Text("<user_info>\nWorkspace Folder: /work/q\n</user_info>\n<user_query>\n</user_query>")}, nil, cb4At)
	cb4Message(t, c, "m02", "assistant", []any{cb4Text("first reply")}, nil, cb4At)
	cb4Message(t, c, "m03", "user", []any{cb4Text("<user_info>x</user_info>\n<user_query></user_query>\n<additional_data>y</additional_data>")}, nil, cb4LaterAt)
	cb4Message(t, c, "m04", "user", []any{cb4Text("<user_info>x</user_info>\n<user_query>\nstill typing")}, nil, cb4LaterAt)
	cb4Message(t, c, "m05", "assistant", []any{cb4Text("second reply")}, nil, cb4EndAt)
	s := cb4ParseOne(t, c.Index)
	want := []string{"assistant: first reply", "user: still typing", "assistant: second reply"}
	if got := cb4Roles(s); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("messages = %q, want %q", got, want)
	}
	if strings.Join(s.Typed, "|") != "still typing" {
		t.Errorf("Typed = %q, want only the open query", s.Typed)
	}
	for _, m := range s.Messages {
		if strings.Contains(m.Text, "user_query") || strings.Contains(m.Text, "additional_data") {
			t.Errorf("a tag leaked into %q", m.Text)
		}
	}
	if want := projectName("/work/q"); s.Project != want {
		t.Errorf("project = %q, want the workspace folder of the dropped prompt", s.Project)
	}
	for raw, want := range map[string]string{
		"<user_info>\nWorkspace Folder: /w\n</user_info>\n<user_query>\n</user_query>":              "",
		"<user_info>x</user_info>\n<user_query></user_query>\n<additional_data>y</additional_data>": "",
		"<user_query>\n</user_query>":                          "",
		"<user_query>done</user_query><user_query>\nopen tail": "done",
		"<user_query> </user_query><user_query>\nopen tail":    "open tail",
	} {
		if got := codeBuddyIDE4VisibleText(raw, nil); got != want {
			t.Errorf("visible(%q) = %q, want %q", raw, got, want)
		}
	}
}

// A folder no transcript names still gives the directory its name decodes to
// on disk, the way the CLI encoded it.
func TestCodeBuddyFolderCWDDecodesTheFolderName(t *testing.T) {
	// The resolved temp dir: Windows hands it out under its 8.3 name
	// (RUNNER~1), which no directory listing carries, so the folder name
	// never decodes back to it (#4455).
	home, err := filepath.EvalSymlinks(cb4Env(t))
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(home, "workdir", "projx")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(home, "cli", strings.TrimPrefix(claudeEncodePath(work), "-"))
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := codeBuddyFolderCWD(folder); got != work {
		t.Errorf("cwd = %q, want the decoded %q", got, work)
	}
}
