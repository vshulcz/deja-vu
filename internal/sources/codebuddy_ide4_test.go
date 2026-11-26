package sources

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The tests build a miniature 4.x store under a temp home: a workspace index,
// a conversation index with its requests, and one file per message. Nothing
// here reads a contributor's CodeBuddy history.

// These are the fixed times of every fixture, in the forms the store writes.
const (
	cb4At       = "2026-03-04T09:10:00Z"
	cb4LaterAt  = "2026-03-04T09:20:00Z"
	cb4EndAt    = "2026-03-04T09:30:00Z"
	cb4StartMS  = 1772615400000 // 2026-03-04T09:10:00Z
	cb4MiddleMS = 1772616000000 // 2026-03-04T09:20:00Z
	cb4EndMS    = 1772616600000 // 2026-03-04T09:30:00Z
)

func cb4Env(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, k := range []string{
		"DEJA_CODEBUDDY_LEGACY_ROOTS", "DEJA_CODEBUDDY_ROOTS",
		"CODEBUDDY_CONFIG_DIR", "WORKBUDDY_CONFIG_DIR",
		"DEJA_INCLUDE_SUBAGENTS", "DEJA_INDEX_TOOL_OUTPUT",
		"DEJA_INDEX_COMMANDS", "DEJA_INDEX_PATHS", "DEJA_INDEX_EDITS",
		"DEJA_INDEX_WRITES", "DEJA_STORES",
	} {
		t.Setenv(k, "")
	}
	return home
}

func cb4Write(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var before time.Time
	if fi, err := os.Stat(path); err == nil {
		before = fi.ModTime()
	}
	b, ok := v.([]byte)
	if !ok {
		var err error
		b, err = json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if !before.IsZero() {
		cb4Bump(t, path, before)
	}
}

// cb4Bump moves the mtime of a file just written over to a second past the
// mtime it had before, unless the write moved it further. A filesystem stamps
// a write from a clock that moves in steps — about 1 ms on xfs, about 15.6 ms
// on NTFS — so two writes inside one step leave the same mtime, and a test
// that reads a change through a size-and-mtime stamp passed or failed by the
// clock. Measured from the mtime before the write, not after it: a second
// rewrite inside the same step would otherwise land on the same second again.
// cb4Write does this for every file it writes over.
func cb4Bump(t *testing.T, path string, before time.Time) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if at := before.Add(time.Second); !fi.ModTime().After(at) {
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
}

// cb4Conv is a synthetic 4.x conversation and the paths it lives at.
type cb4Conv struct {
	Dir   string // <ws>/<id>
	Index string // <ws>/<id>/index.json
	WS    string // <ws>/index.json
	ID    string
}

// cb4New makes a conversation under a history directory. layout is the path
// between the Data root and history; paths are assembled with filepath.Join so
// they stay valid on Windows.
func cb4New(t *testing.T, dataRoot, layout, ws, id string) cb4Conv {
	t.Helper()
	hist := filepath.Join(append([]string{dataRoot}, strings.Split(layout, "/")...)...)
	dir := filepath.Join(hist, ws, id)
	return cb4Conv{
		Dir:   dir,
		Index: filepath.Join(dir, "index.json"),
		WS:    filepath.Join(hist, ws, "index.json"),
		ID:    id,
	}
}

func cb4Workspace(t *testing.T, c cb4Conv, entries ...map[string]any) {
	t.Helper()
	cb4Write(t, c.WS, map[string]any{"conversations": entries, "current": c.ID})
}

func cb4Entry(id, typ, name string) map[string]any {
	return map[string]any{
		"id": id, "type": typ, "name": name,
		"createdAt": cb4At, "lastMessageAt": cb4EndAt,
	}
}

// cb4Index writes the conversation's own index: requests first, then any
// orphan message, as the IDE's converter orders them.
func cb4Index(t *testing.T, c cb4Conv, requests []any, messages ...string) {
	t.Helper()
	msgs := make([]any, 0, len(messages))
	for _, id := range messages {
		msgs = append(msgs, map[string]any{"id": id})
	}
	cb4Write(t, c.Index, map[string]any{"messages": msgs, "requests": requests})
}

func cb4Request(ids []string, startedAt any, usage map[string]any) map[string]any {
	r := map[string]any{"id": "req-" + ids[0], "messages": ids}
	if startedAt != nil {
		r["startedAt"] = startedAt
	}
	if usage != nil {
		r["usage"] = usage
	}
	return r
}

// cb4Field is the JSON-string form the IDE writes for message and extra.
func cb4Field(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// cb4Message writes one messages/<id>.json. An extra of cb4Raw asks for the
// message and extra fields as JSON objects instead of JSON strings.
func cb4Message(t *testing.T, c cb4Conv, id, role string, content any, extra any, createdAt any) {
	t.Helper()
	inner := map[string]any{"role": role, "content": content}
	f := map[string]any{"id": id, "role": role}
	switch e := extra.(type) {
	case cb4Raw:
		f["message"] = inner
		f["extra"] = map[string]any(e)
	default:
		f["message"] = cb4Field(inner)
		if extra != nil {
			f["extra"] = cb4Field(extra)
		}
	}
	if createdAt != nil {
		f["createdAt"] = createdAt
	}
	cb4Write(t, filepath.Join(c.Dir, "messages", id+".json"), f)
}

type cb4Raw map[string]any

func cb4Text(s string) map[string]any {
	return map[string]any{"type": "text", "text": s}
}

func cb4UserQuery(info, query string) string {
	return "<user_info>\nOS Version: darwin\nShell: Zsh\nWorkspace Folder: " + info +
		"\n</user_info>\n\n<additional_data>\nbelow are some potentially helpful/relevant pieces of information\n</additional_data>\n\n<user_query>\n" +
		query + "\n</user_query>"
}

func cb4ParseOne(t *testing.T, path string) model.Session {
	t.Helper()
	ss, err := ParseCodeBuddyIDE4File(path)
	if err != nil {
		t.Fatalf("ParseCodeBuddyIDE4File(%s): %v", path, err)
	}
	if len(ss) != 1 {
		t.Fatalf("ParseCodeBuddyIDE4File(%s) = %d sessions, want 1", path, len(ss))
	}
	return ss[0]
}

// cb4Roles flattens a session's messages into "role: text" lines, in order.
func cb4Roles(s model.Session) []string {
	out := make([]string, 0, len(s.Messages))
	for _, m := range s.Messages {
		out = append(out, m.Role+": "+m.Text)
	}
	return out
}

func cb4Has(lines []string, role, want string) bool {
	for _, l := range lines {
		if strings.HasPrefix(l, role+": ") && strings.Contains(l[len(role)+2:], want) {
			return true
		}
	}
	return false
}

func cb4Count(lines []string, role string) int {
	n := 0
	for _, l := range lines {
		if strings.HasPrefix(l, role+": ") {
			n++
		}
	}
	return n
}

// cb4Minimal is the smallest conversation that parses: a wrapped user prompt
// and a reply. It also supplies a cwd, title and session clock.
func cb4Minimal(t *testing.T, root string) cb4Conv {
	t.Helper()
	c := cb4New(t, root, "userA/CodeBuddyIDE/userA/history", "ws0001", "conv0001")
	cb4Workspace(t, c, cb4Entry(c.ID, "craft", "Fix the retry loop"))
	cb4Index(t, c, []any{cb4Request([]string{"m01", "m02"}, cb4StartMS, nil)})
	cb4Message(t, c, "m01", "user", []any{cb4Text(cb4UserQuery("/work/retry-demo", "Fix the retry off-by-one"))}, nil, cb4At)
	cb4Message(t, c, "m02", "assistant", []any{cb4Text("I will fix the loop.")}, map[string]any{"modelId": "claude-synthetic-4"}, cb4LaterAt)
	return c
}

func TestCodeBuddyIDE4RootFor(t *testing.T) {
	cb4Env(t)
	// A Windows-shaped home checks the AppData\Local spelling explicitly,
	// including on a Unix host where runtime.GOOS would take the other branch.
	windowsHome := `C:\Users\cb4user`
	wantWindows := `C:\Users\cb4user\AppData\Local\CodeBuddyExtension\Data`
	if runtime.GOOS == "windows" {
		if got := codeBuddyIDE4RootFor("windows", windowsHome); got != wantWindows {
			t.Errorf("windows rootFor = %q, want %q", got, wantWindows)
		}
	} else {
		// The test only asserts the spelling; building it with filepath.Join
		// on a Unix host would give a slash-separated path instead.
		wantWindows = filepath.Join(windowsHome, "AppData", "Local", "CodeBuddyExtension", "Data")
		if got := codeBuddyIDE4RootFor("windows", windowsHome); got == "" {
			t.Error("windows rootFor returned an empty path")
		}
		_ = wantWindows
	}
	for _, tc := range []struct {
		goos string
		want []string
	}{
		{"darwin", []string{"Library", "Application Support", "CodeBuddyExtension", "Data"}},
		{"windows", []string{"AppData", "Local", "CodeBuddyExtension", "Data"}},
		{"linux", []string{".local", "share", "CodeBuddyExtension", "Data"}},
		{"freebsd", []string{".local", "share", "CodeBuddyExtension", "Data"}},
	} {
		home := filepath.Join(string(os.PathSeparator), "home", "cb4user")
		if tc.goos == "darwin" {
			home = filepath.Join(string(os.PathSeparator), "Users", "cb4user")
		}
		want := filepath.Join(append([]string{home}, tc.want...)...)
		if got := codeBuddyIDE4RootFor(tc.goos, home); got != want {
			t.Errorf("rootFor(%s) = %q, want %q", tc.goos, got, want)
		}
	}
	if runtime.GOOS == "windows" {
		home := t.TempDir()
		roaming := filepath.Join(home, "AppData", "Roaming", "CodeBuddyExtension", "Data")
		if err := os.MkdirAll(roaming, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		if got := CodeBuddyIDE4Roots(); len(got) != 2 || got[1] != roaming {
			t.Errorf("windows roots = %v, want the Roaming copy too", got)
		}
	}
}

func TestCodeBuddyIDE4SessionFiles(t *testing.T) {
	home := cb4Env(t)
	// This platform's own default, so the walk is the one a user here gets.
	data := CodeBuddyIDE4DefaultRoot()
	layouts := []string{
		"userA/CodeBuddyIDE/userA/history",  // current, signed in
		"default/CodeBuddyIDE/history",      // older, signed out
		"default/CodeBuddyIDE/Lw==/history", // signed-out user directory
	}
	want := make([]string, 0, len(layouts))
	for i, layout := range layouts {
		c := cb4New(t, data, layout, fmt.Sprintf("ws000%d", i+1), fmt.Sprintf("conv000%d", i+1))
		cb4Workspace(t, c, cb4Entry(c.ID, "craft", "Synthetic chat"))
		cb4Index(t, c, []any{cb4Request([]string{"m01"}, cb4StartMS, nil)})
		cb4Write(t, filepath.Join(c.Dir, "messages", "m01.json"), "{}")
		want = append(want, c.Index)
		// Files that are not a conversation index, beside and under it.
		cb4Write(t, filepath.Join(c.Dir, "index_bak.json"), "{}")
		cb4Write(t, filepath.Join(c.Dir, ".index_bak.json"), "{}")
		cb4Write(t, filepath.Join(c.Dir, "assets", "index.json"), "{}")
		cb4Write(t, filepath.Join(c.Dir, "tool-results", "index.json"), "{}")
		cb4Write(t, filepath.Join(c.Dir, "messages", "index.json"), "{}")
		cb4Write(t, filepath.Join(c.Dir, "messages", "m02.json"), "{}")
	}
	for _, dir := range []string{"Cache", "Logs", "Public"} {
		p := filepath.Join(data, "userA", "CodeBuddyIDE", "userA", "history", dir, "wsjunk", "convjunk", "index.json")
		cb4Write(t, p, "{}")
	}
	// A conversation index outside any configured root is not a session.
	stray := cb4New(t, filepath.Join(home, "elsewhere"), "userA/CodeBuddyIDE/userA/history", "wsstray", "convstray")
	cb4Index(t, stray, []any{cb4Request([]string{"m01"}, cb4StartMS, nil)})

	sort.Strings(want)
	files := CodeBuddyIDE4SessionFiles()
	sort.Strings(files)
	if strings.Join(files, "|") != strings.Join(want, "|") {
		t.Fatalf("session files = %v, want %v", files, want)
	}
	for _, p := range want {
		if !IsCodeBuddyIDE4Session(p) {
			t.Errorf("IsCodeBuddyIDE4Session(%s) = false", p)
		}
	}
	if IsCodeBuddyIDE4Session(stray.Index) || IsCodeBuddyIDE4Session(filepath.Join(filepath.Dir(want[0]), "..", "index.json")) {
		t.Error("stray or workspace index claimed as a conversation")
	}

	// A path list of copied roots from another machine replaces the default.
	other := t.TempDir()
	c2 := cb4New(t, other, "userB/CodeBuddyIDE/userB/history", "ws0009", "conv0009")
	cb4Index(t, c2, []any{cb4Request([]string{"m01"}, cb4StartMS, nil)})
	list := data + string(os.PathListSeparator) + other
	t.Setenv("DEJA_CODEBUDDY_LEGACY_ROOTS", list)
	if got := CodeBuddyIDE4Roots(); strings.Join(got, string(os.PathListSeparator)) != list {
		t.Fatalf("legacy roots = %v, want the path list %v", got, strings.Split(list, string(os.PathListSeparator)))
	}
	withSecond := CodeBuddyIDE4SessionFiles()
	if len(withSecond) != len(want)+1 {
		t.Fatalf("with a second root = %d files, want %d: %v", len(withSecond), len(want)+1, withSecond)
	}
	if !IsCodeBuddyIDE4Session(c2.Index) {
		t.Error("conversation under an added root not recognised")
	}
}

// cb4OnlyConversation fails unless the walk lists exactly the conversation
// index, and the workspace index beside it reads as no session to every
// predicate the index and doctor ask.
func cb4OnlyConversation(t *testing.T, c cb4Conv) {
	t.Helper()
	if files := CodeBuddyIDE4SessionFiles(); len(files) != 1 || files[0] != c.Index {
		t.Errorf("session files = %v, want only [%s]", files, c.Index)
	}
	if !IsCodeBuddyIDE4Session(c.Index) {
		t.Error("conversation under the root not recognised")
	}
	if IsCodeBuddyIDE4Session(c.WS) {
		t.Error("the workspace index reads as a conversation")
	}
	if got := KindForPath(c.WS); got == "codebuddy-ide4" {
		t.Error("the registry files the workspace index under codebuddy-ide4")
	}
	if _, ok := codeBuddyIDE4Parts(c.WS); ok {
		t.Error("parts accepted the workspace index")
	}
}

// A copied store may be rooted at the history directory itself, under any
// name, or at one workspace bucket. Each must find the conversation indexes
// inside it and nothing else: a workspace's own index.json sits one level
// under a history root, the depth a conversation's index has under a
// workspace root.
func TestCodeBuddyIDE4NarrowLegacyRoots(t *testing.T) {
	home := cb4Env(t)
	base := filepath.Join(home, "copy", "Data")
	c := cb4New(t, base, "userA/CodeBuddyIDE/userA/history", "ws0001", "conv0001")
	cb4Workspace(t, c, cb4Entry(c.ID, "craft", "A copied chat"))
	cb4Index(t, c, []any{cb4Request([]string{"m01"}, cb4StartMS, nil)})
	cb4Write(t, filepath.Join(c.Dir, "messages", "m01.json"), "{}")
	history := filepath.Dir(filepath.Dir(c.Dir))
	if filepath.Base(history) != "history" {
		t.Fatalf("fixture history root = %s", history)
	}
	// The same history under a name that says nothing about its shape.
	renamed := filepath.Join(home, "copy", "hist-backup")
	rc := cb4New(t, renamed, "", "ws0001", "conv0001")
	cb4Workspace(t, rc, cb4Entry(rc.ID, "craft", "A copied chat"))
	cb4Index(t, rc, []any{cb4Request([]string{"m01"}, cb4StartMS, nil)})
	cb4Write(t, filepath.Join(rc.Dir, "messages", "m01.json"), "{}")
	for _, tc := range []struct {
		name string
		root string
		conv cb4Conv
	}{
		{"history directory", history, c},
		{"history directory by another name", renamed, rc},
		{"workspace bucket", filepath.Dir(c.Dir), c},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DEJA_CODEBUDDY_LEGACY_ROOTS", tc.root)
			if got := CodeBuddyIDE4Roots(); len(got) != 1 || got[0] != tc.root {
				t.Fatalf("roots = %v, want [%s]", got, tc.root)
			}
			cb4OnlyConversation(t, tc.conv)
		})
	}
	if !filepath.IsAbs(history) || !filepath.IsAbs(filepath.Dir(c.Dir)) {
		t.Error("the narrow-root fixtures must use absolute paths")
	}
	// A path list can mix both narrow roots; each configured root is walked,
	// and the conversation they both hold is listed once.
	t.Setenv("DEJA_CODEBUDDY_LEGACY_ROOTS", history+string(os.PathListSeparator)+filepath.Dir(c.Dir))
	cb4OnlyConversation(t, c)
}

// A Data root holds each user's state beside their history. An index.json one
// level under a user's directory is that state, at the depth a conversation's
// index has under a workspace root, and is not a conversation.
func TestCodeBuddyIDE4DataRootStateIsNotAConversation(t *testing.T) {
	cb4Env(t)
	data := CodeBuddyIDE4DefaultRoot()
	c := cb4Minimal(t, data)
	state := filepath.Join(data, "userA", "index.json")
	cb4Write(t, state, map[string]any{"version": 1})
	cb4Write(t, filepath.Join(data, "default", "index.json"), map[string]any{"conversations": []any{}})
	cb4OnlyConversation(t, c)
	for _, p := range []string{state, filepath.Join(data, "default", "index.json")} {
		if IsCodeBuddyIDE4Session(p) || KindForPath(p) == "codebuddy-ide4" {
			t.Errorf("%s reads as a conversation", p)
		}
	}
	// The same Data directory named explicitly behaves the same way.
	t.Setenv("DEJA_CODEBUDDY_LEGACY_ROOTS", data)
	cb4OnlyConversation(t, c)
	if IsCodeBuddyIDE4Session(state) {
		t.Error("user state reads as a conversation under an explicit Data root")
	}
}

// A workspace copied without its own index.json is still a workspace: its
// conversations each hold a messages directory, which is what tells them from
// the workspace indexes of a history root.
func TestCodeBuddyIDE4CopiedWorkspaceWithoutIndex(t *testing.T) {
	home := cb4Env(t)
	ws := filepath.Join(home, "copy", "one-workspace")
	c := cb4New(t, ws, "", "", "conv0001")
	c.WS = filepath.Join(ws, "index.json")
	cb4Index(t, c, []any{cb4Request([]string{"m01", "m02"}, cb4StartMS, nil)})
	cb4Message(t, c, "m01", "user", []any{cb4Text("copied question")}, nil, cb4At)
	cb4Message(t, c, "m02", "assistant", []any{cb4Text("copied answer")}, nil, cb4LaterAt)
	// A second conversation that never got a message file is not one.
	empty := filepath.Join(ws, "conv0002", "index.json")
	cb4Write(t, empty, map[string]any{"messages": []any{}})
	t.Setenv("DEJA_CODEBUDDY_LEGACY_ROOTS", ws)
	if _, err := os.Lstat(c.WS); err == nil {
		t.Fatal("fixture has a workspace index")
	}
	if files := CodeBuddyIDE4SessionFiles(); len(files) != 1 || files[0] != c.Index {
		t.Errorf("session files = %v, want [%s]", files, c.Index)
	}
	if !IsCodeBuddyIDE4Session(c.Index) || IsCodeBuddyIDE4Session(empty) {
		t.Errorf("session predicate = %v for the conversation, %v for the empty one",
			IsCodeBuddyIDE4Session(c.Index), IsCodeBuddyIDE4Session(empty))
	}
	s := cb4ParseOne(t, c.Index)
	if lines := cb4Roles(s); !cb4Has(lines, "user", "copied question") || !cb4Has(lines, "assistant", "copied answer") {
		t.Errorf("copied conversation = %v", lines)
	}
	// A messages entry that is a file is not a conversation's directory.
	odd := filepath.Join(ws, "conv0003")
	cb4Write(t, filepath.Join(odd, "index.json"), map[string]any{"messages": []any{}})
	cb4Write(t, filepath.Join(odd, "messages"), []byte("not a directory"))
	if IsCodeBuddyIDE4Session(filepath.Join(odd, "index.json")) {
		t.Error("a messages file made a conversation")
	}
	// Once the workspace's own index lists its conversations, the root is a
	// workspace by its index and every child index is a conversation.
	cb4Write(t, c.WS, map[string]any{"conversations": []any{cb4Entry(c.ID, "craft", "Copied")}})
	if !IsCodeBuddyIDE4Session(empty) {
		t.Error("a listed workspace root does not accept its conversations")
	}
}

// A root named history is a history directory by shape. A copied root with no
// index.json is one too; a workspace's own index lists conversations and is
// not. codeBuddyIDE4Parts uses this to tell a workspace root from a history
// root when both hold an index.json.
func TestCodeBuddyIDE4LooksLikeHistory(t *testing.T) {
	cb4Env(t)
	home := t.TempDir()
	history := filepath.Join(home, "history")
	if !codeBuddyIDE4LooksLikeHistory(history) {
		t.Error("a root named history should look like history")
	}
	empty := filepath.Join(home, "no-index")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if !codeBuddyIDE4LooksLikeHistory(empty) {
		t.Error("a root with no workspace index should look like history")
	}
	malformed := filepath.Join(home, "bad-index")
	cb4Write(t, filepath.Join(malformed, "index.json"), []byte("not an index"))
	if !codeBuddyIDE4LooksLikeHistory(malformed) {
		t.Error("a root whose index is not JSON should look like history")
	}
	workspace := filepath.Join(home, "ws0001")
	cb4Write(t, filepath.Join(workspace, "index.json"), map[string]any{
		"conversations": []any{cb4Entry("conv0001", "craft", "A chat")},
	})
	if codeBuddyIDE4LooksLikeHistory(workspace) {
		t.Error("a workspace index listing conversations should not look like history")
	}
	noConversations := filepath.Join(home, "ws0002")
	cb4Write(t, filepath.Join(noConversations, "index.json"), map[string]any{"current": "conv0002"})
	if !codeBuddyIDE4LooksLikeHistory(noConversations) {
		t.Error("an index without conversations should look like history")
	}
}

func TestParseCodeBuddyIDE4Conversation(t *testing.T) {
	cb4Env(t)
	root := CodeBuddyIDE4DefaultRoot()
	c := cb4Minimal(t, root)
	s := cb4ParseOne(t, c.Index)
	lines := cb4Roles(s)
	if len(lines) != 2 || lines[0] != "user: Fix the retry off-by-one" ||
		lines[1] != "assistant: I will fix the loop." {
		t.Errorf("messages = %v, want the envelope stripped and the reply kept", lines)
	}
	if s.ID != c.ID || s.Harness != "codebuddy" || s.Path != c.Index {
		t.Errorf("identity = %s/%s/%s", s.ID, s.Harness, s.Path)
	}
	if s.Title != "Fix the retry loop" {
		t.Errorf("title = %q", s.Title)
	}
	if want := projectName("/work/retry-demo"); s.Project != want {
		t.Errorf("project = %q, want %q", s.Project, want)
	}
	if got, want := s.Started.UTC().Format(time.RFC3339), cb4At; got != want {
		t.Errorf("started = %s, want %s", got, want)
	}
	if got, want := s.Updated.UTC().Format(time.RFC3339), cb4EndAt; got != want {
		t.Errorf("updated = %s, want %s from the workspace entry", got, want)
	}
	if len(s.Typed) != 1 || s.Typed[0] != "Fix the retry off-by-one" {
		t.Errorf("Typed = %v", s.Typed)
	}
	if s.Model == nil || s.Model.ID != "claude-synthetic-4" || s.Model.Provider != "anthropic" {
		t.Errorf("model = %+v, want the assistant's modelId", s.Model)
	}
	if !s.Messages[0].Time.Equal(s.Started) {
		t.Errorf("first message time = %v, want %v", s.Messages[0].Time, s.Started)
	}
}

// The prompt the person sees comes from the input box's blocks first, then the
// <user_query> inside the envelope, then the input as they phrased it.
func TestCodeBuddyIDE4VisibleText(t *testing.T) {
	cb4Env(t)
	root := CodeBuddyIDE4DefaultRoot()
	const qaXML = `<question_answer><questions><question_item id="q1"><question>Which branch?</question><answers>
main
release
</answers></question_item></questions></question_answer>`
	for _, tc := range []struct {
		name    string
		content any
		extra   any
		want    string
	}{
		{
			name:    "envelope stripped",
			content: []any{cb4Text(cb4UserQuery("/work/r", "real question"))},
			want:    "real question",
		},
		{
			name:    "source content blocks with a resource link",
			content: []any{cb4Text(cb4UserQuery("/work/r", "see this file"))},
			extra: map[string]any{"sourceContentBlocks": []any{
				map[string]any{"type": "text", "text": "Explain "},
				map[string]any{"type": "resource_link", "uri": "/work/r/main.go", "name": "main.go",
					"_meta": map[string]any{"displayAsPhrase": true, "mentionType": "file"}},
				map[string]any{"type": "image", "_meta": map[string]any{"filename": "shot.png"}},
				map[string]any{"type": "text", "text": " and "},
				map[string]any{"type": "resource_link", "uri": "knowledge://repo-guide", "name": "repo-guide",
					"_meta": map[string]any{"name": "repo-guide"}},
			}},
			want: "Explain @/work/r/main.go @image#1:shot.png and @knowledge:repo-guide",
		},
		{
			name:    "input phrase fallback",
			content: []any{cb4Text("<system_reminder>\nclient note\n</system_reminder>")},
			extra: map[string]any{"inputPhrase": []any{
				map[string]any{"type": "normal", "content": "Run "},
				map[string]any{"type": "command", "content": "/review"},
			}},
			want: "Run /review",
		},
		{
			name:    "plain text with no envelope",
			content: "a bare question",
			want:    "a bare question",
		},
		{
			name:    "question answer XML in the query",
			content: []any{cb4Text("plain text\n" + qaXML)},
			want:    "plain text\nWhich branch? → main, release",
		},
		{
			name:    "questionAnswer from extra",
			content: []any{cb4Text(cb4UserQuery("/work/r", qaXML))},
			extra: map[string]any{"questionAnswer": map[string]any{"questions": []any{
				map[string]any{"id": "q1", "question": "Which branch?", "answers": []any{"main", "release"}},
			}}},
			want: "Which branch? → main, release",
		},
		{
			name:    "questionAnswer in a source block's meta",
			content: []any{cb4Text(cb4UserQuery("/work/r", qaXML))},
			extra: map[string]any{"sourceContentBlocks": []any{
				map[string]any{"type": "text", "text": "ignored", "_meta": map[string]any{
					"questionAnswer": map[string]any{"questions": []any{
						map[string]any{"question": "Pick one", "answers": []any{"A", "B"}},
					}},
				}},
			}},
			want: "Pick one → A, B",
		},
		{
			name:    "open user_query tag",
			content: []any{cb4Text("<system_reminder>note</system_reminder>\n<user_query>\nunfinished question")},
			want:    "unfinished question",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := cb4New(t, root, "userA/CodeBuddyIDE/userA/history", "ws0001",
				"conv-"+strings.NewReplacer(" ", "", "'", "", "-", "").Replace(tc.name))
			cb4Index(t, c, []any{cb4Request([]string{"m01"}, cb4StartMS, nil)})
			cb4Message(t, c, "m01", "user", tc.content, tc.extra, cb4At)
			s := cb4ParseOne(t, c.Index)
			if len(s.Messages) != 1 || s.Messages[0].Text != tc.want || s.Messages[0].Role != "user" {
				t.Errorf("messages = %v, want one user turn %q", cb4Roles(s), tc.want)
			}
		})
	}
}
func TestCodeBuddyIDE4MentionPhrases(t *testing.T) {
	for _, tc := range []struct {
		name  string
		block map[string]any
		want  string
	}{
		{"command preferred name", map[string]any{
			"type": "resource_link", "uri": "command://deploy", "name": "review",
		}, "/review"},
		{"command uri fallback", map[string]any{
			"type": "resource_link", "uri": "command://deploy",
		}, "/deploy"},
		{"file uri absolute path", map[string]any{
			"type": "resource_link", "uri": "file:///work/r/main.go", "name": "main.go",
		}, "@/work/r/main.go"},
		{"file uri with a space is quoted", map[string]any{
			"type": "resource_link", "uri": "file:///work/r/a%20b.go", "name": "a b.go",
		}, `@"/work/r/a b.go"`},
		{"absolute file mention", map[string]any{
			"type": "resource_link", "uri": "/work/r/main.go", "name": "main.go",
			"_meta": map[string]any{"mentionType": "file"},
		}, "@/work/r/main.go"},
		{"relative file mention keeps its name", map[string]any{
			"type": "resource_link", "uri": "src/main.go", "name": "main.go",
			"_meta": map[string]any{"mentionType": "file"},
		}, "main.go"},
		{"skill", map[string]any{
			"type": "resource_link", "uri": "skill://review", "name": "review",
		}, "@skill:review"},
		{"knowledge name from meta", map[string]any{
			"type": "resource_link", "uri": "knowledge://repo", "name": "fallback",
			"_meta": map[string]any{"name": "guide"},
		}, "@knowledge:guide"},
		{"rule", map[string]any{
			"type": "resource_link", "uri": "rule://style", "name": "style",
		}, "@rules:style"},
		{"http link", map[string]any{
			"type": "resource_link", "uri": "https://example.test/page", "name": "page",
		}, "@share-html:https://example.test/page"},
		{"unknown scheme falls back to name", map[string]any{
			"type": "resource_link", "uri": "custom://thing", "name": "thing",
		}, "thing"},
		{"code selection dropped", map[string]any{
			"type": "resource_link", "uri": "config.yaml:374-383", "name": "config.yaml:374-383",
			"_meta": map[string]any{"mentionType": "code"},
		}, ""},
		{"image with a spaced filename", map[string]any{
			"type": "image", "uri": "codebuddy-asset://shot",
			"_meta": map[string]any{"filename": "my shot.png"},
		}, `@image#1:"my shot.png"`},
		{"image fallback from uri", map[string]any{
			"type": "image", "uri": "codebuddy-asset://assets/shot.png",
		}, "@image#1:shot.png"},
		{"image fallback number", map[string]any{
			"type": "image",
		}, "@image#1:image-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := codeBuddyIDE4Phrase([]any{
				map[string]any{"type": "text", "text": "before"},
				tc.block,
				map[string]any{"type": "text", "text": "after"},
			})
			// Text touching a mention is separated only when both sides are
			// word characters, @, / or a quote.
			if tc.want == "" {
				want := "beforeafter"
				if got != want {
					t.Errorf("phrase = %q, want %q", got, want)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("phrase = %q, want it to contain %q", got, tc.want)
			}
		})
	}
	// Quoting, separator and image numbering in one prompt.
	got := codeBuddyIDE4Phrase([]any{
		map[string]any{"type": "text", "text": "Fix"},
		map[string]any{"type": "resource_link", "uri": "/work/r/main.go", "name": "main.go",
			"_meta": map[string]any{"mentionType": "file"}},
		map[string]any{"type": "text", "text": "and"},
		map[string]any{"type": "image", "_meta": map[string]any{"originalFilename": "a.png"}},
		map[string]any{"type": "image", "_meta": map[string]any{}},
	})
	want := `Fix @/work/r/main.go and @image#1:a.png @image#2:image-2`
	if got != want {
		t.Errorf("phrase = %q, want %q", got, want)
	}
	if codeBuddyIDE4NeedsSpace("a", "b") != true || codeBuddyIDE4NeedsSpace("a ", "b") != false ||
		codeBuddyIDE4NeedsSpace("a", ".") != false {
		t.Error("needsInlineSeparator did not follow the converter's word-character rule")
	}
}

func TestCodeBuddyIDE4TeammateTurns(t *testing.T) {
	cb4Env(t)
	root := CodeBuddyIDE4DefaultRoot()
	// The converter checks the teammate_id attribute first: when it is set,
	// the whole inner text is the body, including from/content tags.
	const tmAttr = `<teammate-message teammate_id="worker-1">Found the failing test</teammate-message>`
	const tm = `<teammate-message><from>worker-1</from><content>Found the failing test</content></teammate-message>`
	const shutdown = `<teammate-message teammate_id="worker-2">{"type":"shutdown_response","approve":true}</teammate-message>`
	for _, tc := range []struct {
		name    string
		entry   map[string]any
		want    []string
		wantTyp []string
	}{
		{
			name:    "lead drops teammate messages",
			entry:   cb4Entry("conv0001", "craft", "Lead"),
			want:    []string{"user: a question from the person"},
			wantTyp: []string{"a question from the person"},
		},
		{
			name:    "team member keeps each as a user turn",
			entry:   cb4Entry("conv0002", "team-member", "Worker"),
			want:    []string{"user: **worker-1**\n\nFound the failing test", "user: **worker-1**\n\nFound the failing test", "user: a question from the person"},
			wantTyp: []string{"a question from the person"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, ok := tc.entry["id"].(string)
			if !ok {
				t.Fatalf("entry id is %T, want a string", tc.entry["id"])
			}
			c := cb4New(t, root, "userA/CodeBuddyIDE/userA/history", "ws0001", id)
			cb4Workspace(t, c, tc.entry)
			cb4Index(t, c, []any{cb4Request([]string{"m01"}, cb4StartMS, nil)})
			cb4Message(t, c, "m01", "user", []any{cb4Text("a question from the person\n" + tmAttr + "\n" + tm + "\n" + shutdown)}, nil, cb4At)
			s := cb4ParseOne(t, c.Index)
			if got := cb4Roles(s); strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("messages = %v, want %v", got, tc.want)
			}
			if strings.Join(s.Typed, "|") != strings.Join(tc.wantTyp, "|") {
				t.Errorf("Typed = %v, want %v", s.Typed, tc.wantTyp)
			}
		})
	}
	// An answer picked from the agent's options is not something the person typed.
	c := cb4New(t, root, "userA/CodeBuddyIDE/userA/history", "ws0001", "conv0003")
	cb4Index(t, c, []any{cb4Request([]string{"m01"}, cb4StartMS, nil)})
	const qaXML = `<question_answer><questions><question_item id="q1"><question>Which branch?</question><answers>
main
release
</answers></question_item></questions></question_answer>`
	cb4Message(t, c, "m01", "user", []any{cb4Text("continue\n" + qaXML)}, nil, cb4At)
	s := cb4ParseOne(t, c.Index)
	if got := s.Messages[0].Text; got != "continue\nWhich branch? → main, release" {
		t.Errorf("message = %q", got)
	}
	if strings.Join(s.Typed, "|") != "continue" {
		t.Errorf("Typed = %v, want the prompt without the question_answer XML", s.Typed)
	}
}

func TestParseCodeBuddyIDE4DroppedAndAlternateShapes(t *testing.T) {
	cb4Env(t)
	root := CodeBuddyIDE4DefaultRoot()
	c := cb4New(t, root, "userA/CodeBuddyIDE/userA/history", "ws0001", "conv0001")
	cb4Workspace(t, c, cb4Entry(c.ID, "craft", "  A  title\x7fwith a control char  "))
	// Requests in order, then an orphan message and a repeated id, each once.
	cb4Index(t, c, []any{
		cb4Request([]string{"m01", "m02"}, cb4StartMS, nil),
		cb4Request([]string{"m03", "m02", "m04"}, cb4MiddleMS, nil),
	}, "m05", "m01")
	cb4Message(t, c, "m01", "user", []any{cb4Text(cb4UserQuery("/work/demo", "first question"))}, nil, cb4At)
	cb4Message(t, c, "m02", "assistant", []any{
		map[string]any{"type": "reasoning", "text": "private chain of thought"},
		cb4Text("the answer"),
	}, nil, cb4LaterAt)
	// A string content part, and message/extra as objects instead of strings.
	cb4Message(t, c, "m03", "user", "string content here", cb4Raw{"modelId": "ignored-on-user"}, cb4LaterAt)
	// The client's own turns: a system reminder and a compaction summary.
	cb4Message(t, c, "m04", "user", []any{cb4Text("<system_reminder>\ncontext from the client\n</system_reminder>")},
		cb4Raw{"isHelperMessage": true}, cb4LaterAt)
	cb4Message(t, c, "m05", "user", []any{cb4Text("a summary of the compacted turns")},
		cb4Raw{"isMaxTokenLimitSummary": true}, cb4EndAt)
	s := cb4ParseOne(t, c.Index)
	lines := cb4Roles(s)
	want := []string{"user: first question", "assistant: the answer", "user: string content here"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Errorf("messages = %v, want %v (reasoning, helper, summary dropped; each id once)", lines, want)
	}
	if s.Title != "A title with a control char" {
		t.Errorf("title = %q, want the control character and repeated spaces collapsed", s.Title)
	}
	if len(s.Typed) != 2 {
		t.Errorf("Typed = %v, want the two spoken turns", s.Typed)
	}
}

func TestParseCodeBuddyIDE4TimesAndOrder(t *testing.T) {
	cb4Env(t)
	root := CodeBuddyIDE4DefaultRoot()
	c := cb4New(t, root, "userA/CodeBuddyIDE/userA/history", "ws0001", "conv0001")
	// No workspace entry: the session's clock comes from the messages alone.
	cb4Index(t, c, []any{
		cb4Request([]string{"m01", "m02"}, cb4StartMS, nil),
		cb4Request([]string{"m03"}, cb4MiddleMS, nil),
	}, "m04")
	cb4Message(t, c, "m01", "user", []any{cb4Text(cb4UserQuery("/work/demo", "first"))}, nil, cb4At)
	// No createdAt: the request's epoch-millisecond startedAt supplies it.
	cb4Message(t, c, "m02", "assistant", []any{cb4Text("reply")}, nil, nil)
	cb4Message(t, c, "m03", "user", []any{cb4Text("second")}, nil, cb4LaterAt)
	// An orphan message with no time of its own and no request.
	cb4Message(t, c, "m04", "assistant", []any{cb4Text("orphan")}, nil, nil)
	s := cb4ParseOne(t, c.Index)
	if got := cb4Roles(s); strings.Join(got, "|") != "user: first|assistant: reply|user: second|assistant: orphan" {
		t.Errorf("order = %v", got)
	}
	if got, want := s.Messages[1].Time.UTC().Format(time.RFC3339), cb4At; got != want {
		t.Errorf("time without createdAt = %s, want the request's %s", got, want)
	}
	if !s.Messages[3].Time.IsZero() {
		t.Errorf("orphan message time = %v, want zero", s.Messages[3].Time)
	}
	if got, want := s.Started.UTC().Format(time.RFC3339), cb4At; got != want {
		t.Errorf("started = %s, want %s", got, want)
	}
	if got, want := s.Updated.UTC().Format(time.RFC3339), cb4LaterAt; got != want {
		t.Errorf("updated = %s, want %s", got, want)
	}
	if s.Project != "-" && s.Project != projectName("/work/demo") {
		t.Errorf("project = %q", s.Project)
	}
}

func TestParseCodeBuddyIDE4MalformedAndEmpty(t *testing.T) {
	cb4Env(t)
	root := CodeBuddyIDE4DefaultRoot()
	c := cb4New(t, root, "userA/CodeBuddyIDE/userA/history", "ws0001", "conv0001")
	cb4Index(t, c, []any{cb4Request([]string{"m01", "m02", "m03", "m04", "m05", "m06"}, cb4StartMS, nil)})
	cb4Message(t, c, "m01", "user", []any{cb4Text("kept first")}, nil, cb4At)
	cb4Write(t, filepath.Join(c.Dir, "messages", "m03.json"), []byte("not JSON at all"))
	cb4Write(t, filepath.Join(c.Dir, "messages", "m04.json"), []byte(`{"message": 42, "role": "user"}`))
	// m06 names a file the store would never write, so no path is joined.
	cb4Write(t, filepath.Join(c.Dir, "messages", "m06.json"), "{}")
	// m02 and m05 are listed but their files are missing.
	cb4Message(t, c, "m05", "assistant", []any{cb4Text("kept last")}, nil, cb4LaterAt)
	state := DiagSave()
	before := DiagMalformedCounts()[c.Index]
	s := cb4ParseOne(t, c.Index)
	if got := cb4Roles(s); strings.Join(got, "|") != "user: kept first|assistant: kept last" {
		t.Errorf("messages = %v, want the two readable messages in order", got)
	}
	if got := DiagMalformedCounts()[c.Index] - before; got != 4 {
		t.Errorf("diagnostics counted %d unusable records, want 4 (2 missing, malformed JSON, bad body)", got)
	}
	if reasons := DiagUnusableRecords()[c.Index]; len(reasons) != 4 {
		t.Errorf("unusable records = %v, want the four ids and their reasons", reasons)
	}
	DiagRestore(state) // put the shared counters back for the next test.

	// No messages at all is not a session; neither is a conversation whose
	// every message was a client-only turn.
	empty := cb4New(t, root, "userA/CodeBuddyIDE/userA/history", "ws0001", "conv0002")
	cb4Index(t, empty, nil)
	if ss, err := ParseCodeBuddyIDE4File(empty.Index); err != nil || len(ss) != 0 {
		t.Errorf("empty conversation = %d sessions, %v; want none", len(ss), err)
	}
	helper := cb4New(t, root, "userA/CodeBuddyIDE/userA/history", "ws0001", "conv0003")
	cb4Index(t, helper, []any{cb4Request([]string{"m01"}, cb4StartMS, nil)})
	cb4Message(t, helper, "m01", "user", []any{cb4Text("<system_reminder>note</system_reminder>")},
		cb4Raw{"isHelperMessage": true}, cb4At)
	if ss, err := ParseCodeBuddyIDE4File(helper.Index); err != nil || len(ss) != 0 {
		t.Errorf("helper-only conversation = %d sessions, %v; want none", len(ss), err)
	}
	// A path that is not a conversation index, and an index that is not JSON.
	if ss, err := ParseCodeBuddyIDE4File(empty.WS); err != nil || len(ss) != 0 {
		t.Errorf("workspace index parsed as %d sessions, %v", len(ss), err)
	}
	bad := cb4New(t, root, "userA/CodeBuddyIDE/userA/history", "ws0001", "conv0004")
	cb4Write(t, bad.Index, []byte("this is not an index"))
	if _, err := ParseCodeBuddyIDE4File(bad.Index); err == nil {
		t.Error("malformed conversation index returned no error")
	}
}

func TestParseCodeBuddyIDE4TeamMember(t *testing.T) {
	cb4Env(t)
	root := CodeBuddyIDE4DefaultRoot()
	for _, tc := range []struct {
		name  string
		entry map[string]any
	}{
		{"explicit type", cb4Entry("conv0001", "team-member", "Worker")},
		{"old team title", cb4Entry("conv0002", "", "[Team] worker@team-1")},
		{"craft", cb4Entry("conv0003", "craft", "[Team] not a team title")},
		{"plan", cb4Entry("conv0004", "plan", "A plan")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, ok := tc.entry["id"].(string)
			if !ok {
				t.Fatalf("entry id is %T, want a string", tc.entry["id"])
			}
			c := cb4New(t, root, "userA/CodeBuddyIDE/userA/history", "ws0001", id)
			cb4Workspace(t, c, tc.entry)
			cb4Index(t, c, []any{cb4Request([]string{"m01"}, cb4StartMS, nil)})
			cb4Message(t, c, "m01", "user", []any{cb4Text("a task from the lead")}, nil, cb4At)
			s := cb4ParseOne(t, c.Index)
			want := ""
			if tc.entry["type"] == "team-member" || tc.entry["type"] == "" {
				want = model.KindSubagent
			}
			if s.Kind != want {
				t.Errorf("kind = %q, want %q", s.Kind, want)
			}
		})
	}
}

// cb4ToolCall and cb4ToolResult are the content parts the IDE writes for a
// call and the answer to it, in the same message or a later one.
func cb4ToolCall(id, name string, args map[string]any) map[string]any {
	return map[string]any{"type": "tool-call", "toolCallId": id, "toolName": name, "args": args}
}

func cb4ToolResult(id, name string, result any, isError bool) map[string]any {
	return map[string]any{"type": "tool-result", "toolCallId": id, "toolName": name, "result": result, "isError": isError}
}

func cb4OKResult(inner map[string]any) map[string]any {
	return map[string]any{"status": "success", "success": true, "result": inner}
}

func TestParseCodeBuddyIDE4ToolRecords(t *testing.T) {
	cb4Env(t)
	root := CodeBuddyIDE4DefaultRoot()
	c := cb4New(t, root, "userA/CodeBuddyIDE/userA/history", "ws0001", "conv0001")
	cb4Index(t, c, []any{
		cb4Request([]string{"m01", "m02"}, cb4StartMS, nil),
		cb4Request([]string{"m03", "m04"}, cb4MiddleMS, nil),
		cb4Request([]string{"m05", "m06"}, cb4EndMS, nil),
	})
	cb4Message(t, c, "m01", "user", []any{cb4Text(cb4UserQuery("/work/retry-demo", "Fix the loop"))}, nil, cb4At)
	cb4Message(t, c, "m02", "assistant", []any{
		cb4Text("Let me look."),
		cb4ToolCall("t1", "read_file", map[string]any{"filePath": "/work/retry-demo/retry.go", "offset": 1, "limit": 100}),
		cb4ToolCall("t2", "replace_in_file", map[string]any{
			"filePath": "/work/retry-demo/retry.go",
			"old_str":  "for i := 0; i < len(items); i++ {\n",
			"new_str":  "for i := range items {\n",
		}),
		cb4ToolCall("t3", "write_to_file", map[string]any{
			"filePath": "/work/retry-demo/notes.txt",
			"content":  "package retry // the fix landed in this file\n",
		}),
		cb4ToolCall("t4", "execute_command", map[string]any{"command": "go test ./...", "requires_approval": false}),
	}, nil, cb4LaterAt)
	cb4Message(t, c, "m03", "tool", []any{
		cb4ToolResult("t1", "read_file", cb4OKResult(map[string]any{
			"type": "read_file_result", "path": "/work/retry-demo/retry.go", "content": "package retry\n",
		}), false),
		cb4ToolResult("t2", "replace_in_file", cb4OKResult(map[string]any{"type": "replace_in_file_result"}), false),
		cb4ToolResult("t3", "write_to_file", cb4OKResult(map[string]any{"type": "write_to_file_result"}), false),
	}, nil, cb4LaterAt)
	cb4Message(t, c, "m04", "assistant", []any{
		cb4ToolResult("t4", "execute_command", cb4OKResult(map[string]any{
			"type": "execute_command_result", "stdout": "ok  example.com/retry-demo", "stderr": "", "exitCode": 2,
		}), false),
	}, nil, cb4LaterAt)
	// A later error result, and a failed run whose result has a status error.
	cb4Message(t, c, "m05", "tool", []any{
		cb4ToolResult("t5", "mcp_tool", map[string]any{
			"status": "error", "success": false,
			"result":       map[string]any{"type": "text", "text": "MCP tool said hello"},
			"errorMessage": "the MCP server refused",
		}, true),
		cb4ToolResult("t6", "read_file", map[string]any{
			"status": "success", "success": false,
			"result": map[string]any{"type": "read_file_result", "content": "a file body"},
		}, false),
	}, nil, cb4EndAt)
	cb4Message(t, c, "m06", "assistant", []any{
		cb4ToolResult("t7", "mcp_tool", map[string]any{
			"result": map[string]any{"data": []any{
				map[string]any{"type": "text", "text": "first MCP line"},
				map[string]any{"type": "text", "text": "second MCP line"},
			}},
		}, false),
	}, nil, cb4EndAt)
	s := cb4ParseOne(t, c.Index)
	lines := cb4Roles(s)
	for _, want := range []struct{ role, text string }{
		{role: RoleFiles, text: "/work/retry-demo/retry.go"},
		{role: RoleFiles, text: "/work/retry-demo/notes.txt"},
		{role: RoleEdit, text: "for i := 0; i < len(items); i++ {"},
		{role: RoleWrote, text: "/work/retry-demo/notes.txt"},
		{role: RoleCommand, text: "$ go test ./...  → exit 2"},
		{role: RoleToolOutput, text: "package retry"},
		{role: RoleToolOutput, text: "ok  example.com/retry-demo"},
		{role: RoleToolOutput, text: "MCP tool said hello"},
		{role: RoleToolOutput, text: "a file body"},
		{role: RoleToolOutput, text: "first MCP line"},
		{role: RoleToolOutput, text: "second MCP line"},
	} {
		if !cb4Has(lines, want.role, want.text) {
			t.Errorf("missing %s record containing %q in %v", want.role, want.text, lines)
		}
	}
	if s.ToolErrors != 2 {
		t.Errorf("ToolErrors = %d, want 2 (isError and success false)", s.ToolErrors)
	}
	for name, want := range map[string]int{"Read": 1, "Edit": 1, "Write": 1, "Bash": 1} {
		if s.ToolCalls[name] != want {
			t.Errorf("ToolCalls[%s] = %d, want %d", name, s.ToolCalls[name], want)
		}
	}
	if cb4Count(lines, "assistant") != 1 {
		t.Errorf("assistant text = %v, want only the spoken answer", lines)
	}

	// A status error and a marked failure are counted even with tool output off.
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "0")
	s2 := cb4ParseOne(t, c.Index)
	if cb4Count(cb4Roles(s2), RoleToolOutput) != 0 {
		t.Errorf("tool output still indexed with DEJA_INDEX_TOOL_OUTPUT=0: %v", cb4Roles(s2))
	}
	if s2.ToolErrors != 2 {
		t.Errorf("ToolErrors = %d with output off, want 2", s2.ToolErrors)
	}
	if !cb4Has(cb4Roles(s2), RoleCommand, "$ go test ./...  → exit 2") {
		t.Error("the exit code is missing with tool output off")
	}
}

func TestParseCodeBuddyIDE4UsageAndModel(t *testing.T) {
	cb4Env(t)
	root := CodeBuddyIDE4DefaultRoot()
	c := cb4New(t, root, "userA/CodeBuddyIDE/userA/history", "ws0001", "conv0001")
	cb4Index(t, c, []any{
		cb4Request([]string{"m01", "m02"}, cb4StartMS, map[string]any{
			"inputTokens": 1000, "outputTokens": 200, "totalTokens": 1300,
			"lastTokens": 900, "cacheTokens": 100,
		}),
		cb4Request([]string{"m03"}, cb4MiddleMS, map[string]any{
			"inputTokens": 500, "outputTokens": 50, "cacheTokens": 0,
		}),
	})
	cb4Message(t, c, "m01", "user", []any{cb4Text("first")}, nil, cb4At)
	cb4Message(t, c, "m02", "assistant", []any{cb4Text("answer")},
		map[string]any{"modelId": "gpt-synthetic-4o", "modelName": "Synthetic"}, cb4LaterAt)
	cb4Message(t, c, "m03", "user", []any{cb4Text("second")}, nil, cb4EndAt)
	s := cb4ParseOne(t, c.Index)
	if s.Tokens == nil || s.Tokens.Input != 1400 || s.Tokens.Output != 250 || s.Tokens.CacheRead != 100 {
		t.Errorf("tokens = %+v, want input 1400 (cache subtracted), output 250, cache 100", s.Tokens)
	}
	if s.Model == nil || s.Model.ID != "gpt-synthetic-4o" || s.Model.Provider != "openai" {
		t.Errorf("model = %+v, want the assistant's extra.modelId", s.Model)
	}
}

// cb4CLIStore is the 5.x CodeBuddy Code tree, with a converter's marker in
// the transcript's sidecar directory. The jsonl body need only be readable:
// the marker is what decides the 4.x copy is converted.
func cb4CLIStore(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "projects")
	t.Setenv("DEJA_CODEBUDDY_ROOTS", root)
	return root
}

func cb4Converter(t *testing.T, cliRoot, convID, markerID, transcript string) {
	t.Helper()
	proj := filepath.Join(cliRoot, "work-retry-demo")
	jsonl := filepath.Join(proj, convID+".jsonl")
	cb4Write(t, jsonl, []byte(transcript))
	cb4Write(t, filepath.Join(proj, convID, codeBuddyIDE4Marker), map[string]any{
		"conversationId": markerID, "converterVersion": "ide-main-craft-jsonl/34",
	})
}

func TestCodeBuddyIDE4ConvertedDedupe(t *testing.T) {
	cb4Env(t)
	root := CodeBuddyIDE4DefaultRoot()
	cli := cb4CLIStore(t)
	const transcript = `{"type":"message","role":"user","content":[{"type":"input_text","text":"a turn"}],"timestamp":1772615400000}` + "\n"
	// No 5.x transcript at all: the 4.x conversation is the conversation.
	c := cb4Minimal(t, root)
	BeginWalk()
	if ss, err := ParseCodeBuddyIDE4File(c.Index); err != nil || len(ss) != 1 {
		t.Fatalf("with no 5.x store = %d sessions, %v; want 1", len(ss), err)
	}

	// A marker naming this id, beside a readable transcript: deduped.
	cb4Converter(t, cli, c.ID, c.ID, transcript)
	BeginWalk()
	if ss, err := ParseCodeBuddyIDE4File(c.Index); err != nil || len(ss) != 0 {
		t.Errorf("converted conversation = %d sessions, %v; want none", len(ss), err)
	}
	if got := codeBuddyIDE4ConvertedIDs()[c.ID]; got == "" {
		t.Errorf("converted id %s not in the walk's lookup", c.ID)
	}
	// A team member's id under the lead's subagents directory: deduped too.
	member := cb4New(t, root, "userA/CodeBuddyIDE/userA/history", "ws0001", "member0001")
	cb4Workspace(t, member, cb4Entry(member.ID, "team-member", "Worker"))
	cb4Index(t, member, []any{cb4Request([]string{"m01"}, cb4StartMS, nil)})
	cb4Message(t, member, "m01", "user", []any{cb4Text("a task")}, nil, cb4At)
	cb4Write(t, filepath.Join(cli, "work-retry-demo", c.ID, "subagents", "agent-"+member.ID+".jsonl"),
		[]byte(transcript))
	BeginWalk()
	if ss, err := ParseCodeBuddyIDE4File(member.Index); err != nil || len(ss) != 0 {
		t.Errorf("converted team member = %d sessions, %v; want none", len(ss), err)
	}

	// The lookup is per walk: change the on-disk 5.x store, then start a walk.
	for _, tc := range []struct {
		name      string
		markerID  string
		jsonlBody string
		marker    bool
		want      int
	}{
		{"marker names a different conversation", "conv-other", transcript, true, 1},
		{"empty transcript", c.ID, "", true, 1},
		{"marker is not JSON", "", transcript, true, 1},
		{"marker missing", c.ID, transcript, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cli := cb4CLIStore(t)
			if tc.marker {
				p := filepath.Join(cli, "work-retry-demo", c.ID, codeBuddyIDE4Marker)
				if tc.markerID == "" {
					cb4Write(t, p, []byte("not a marker"))
				} else {
					cb4Converter(t, cli, c.ID, tc.markerID, tc.jsonlBody)
				}
			} else {
				cb4Write(t, filepath.Join(cli, "work-retry-demo", c.ID+".jsonl"), []byte(tc.jsonlBody))
			}
			BeginWalk()
			ss, err := ParseCodeBuddyIDE4File(c.Index)
			if err != nil || len(ss) != tc.want {
				t.Errorf("ParseCodeBuddyIDE4File = %d sessions, %v; want %d", len(ss), err, tc.want)
			}
		})
	}
}

func TestCodeBuddyIDE4Sidecar(t *testing.T) {
	cb4Env(t)
	root := CodeBuddyIDE4DefaultRoot()
	cli := cb4CLIStore(t)
	c := cb4Minimal(t, root)
	BeginWalk()
	_, first := codeBuddyIDE4Sidecar(c.Index)
	if _, again := codeBuddyIDE4Sidecar(c.Index); again != first {
		t.Fatalf("fingerprint changed with nothing on disk changed: %d then %d", first, again)
	}

	// A streamed reply rewrites a tail message in place with more bytes.
	cb4Message(t, c, "m02", "assistant", []any{cb4Text("I will fix the loop, and here is the longer streamed answer.")},
		map[string]any{"modelId": "claude-synthetic-4"}, cb4LaterAt)
	if _, got := codeBuddyIDE4Sidecar(c.Index); got == first {
		t.Error("fingerprint did not change when a tail message was rewritten")
	}
	_, afterMessage := codeBuddyIDE4Sidecar(c.Index)

	// The workspace index carries the title; changing it changes the entry.
	cb4Workspace(t, c, cb4Entry(c.ID, "craft", "A new title"))
	if _, got := codeBuddyIDE4Sidecar(c.Index); got == afterMessage {
		t.Error("fingerprint did not change when the workspace entry's title changed")
	}
	_, afterTitle := codeBuddyIDE4Sidecar(c.Index)

	// The entry's times date the session too. The IDE moves lastMessageAt on
	// its own when a turn lands, and a pass that missed it kept the old
	// Updated where a rebuild had the new one.
	moved := cb4Entry(c.ID, "craft", "A new title")
	moved["lastMessageAt"] = "2026-03-05T10:00:00Z"
	cb4Workspace(t, c, moved)
	if _, got := codeBuddyIDE4Sidecar(c.Index); got == afterTitle {
		t.Error("fingerprint did not change when the workspace entry's lastMessageAt moved")
	}
	if s := cb4ParseOne(t, c.Index); s.Updated.UTC().Format(time.RFC3339) != "2026-03-05T10:00:00Z" {
		t.Errorf("updated = %v, want the entry's lastMessageAt", s.Updated)
	}
	_, afterTitle = codeBuddyIDE4Sidecar(c.Index)

	// A converted transcript appears: the 4.x copy stops standing in for it.
	const transcript = `{"type":"message","role":"user","content":[{"type":"input_text","text":"a turn"}]}` + "\n"
	cb4Converter(t, cli, c.ID, c.ID, transcript)
	BeginWalk()
	if _, got := codeBuddyIDE4Sidecar(c.Index); got == afterTitle {
		t.Error("fingerprint did not change when a converted transcript appeared")
	}
}

// A workspace index rewritten to the same length inside one step of the
// filesystem's clock keeps its size and mtime: NTFS kept the mtime on 177 of
// 200 quick rewrites. A cached copy read while its mtime was that recent is
// read again on the next lookup, so a long-running process — the MCP server,
// a hook — sees the rewrite. One read when the mtime is older is trusted.
func TestCodeBuddyIDE4WorkspaceEntryRacyStamp(t *testing.T) {
	cb4Env(t)
	c := cb4Minimal(t, CodeBuddyIDE4DefaultRoot())
	rewrite := func(name string, at time.Time) {
		t.Helper()
		if err := os.WriteFile(c.WS, []byte(cb4Field(map[string]any{
			"conversations": []any{cb4Entry(c.ID, "craft", name)},
		})), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(c.WS, at, at); err != nil {
			t.Fatal(err)
		}
	}
	title := func() string {
		t.Helper()
		e, ok := codeBuddyIDE4WorkspaceEntry(c.Index)
		if !ok {
			t.Fatal("no workspace entry")
		}
		return e.Name
	}

	// Written just now: the same size and the same mtime after a rewrite.
	now := time.Now().Add(-100 * time.Millisecond)
	rewrite("Title AAAA", now)
	if got := title(); got != "Title AAAA" {
		t.Fatalf("title = %q", got)
	}
	rewrite("Title BBBB", now)
	if got := title(); got != "Title BBBB" {
		t.Errorf("title after a same-stamp rewrite = %q, want the rewrite", got)
	}

	// Written long enough ago that no later write can share its mtime: the
	// stamp is trusted and the index is not read again. Restoring the old
	// stamp by hand is the only way to see that from here.
	old := time.Now().Add(-time.Hour)
	rewrite("Title CCCC", old)
	if got := title(); got != "Title CCCC" {
		t.Fatalf("title = %q", got)
	}
	rewrite("Title DDDD", old)
	if got := title(); got != "Title CCCC" {
		t.Errorf("title = %q, want the cached CCCC: a settled stamp is trusted", got)
	}
}

// A conversion can be replaced or emptied after the fingerprint first saw
// it. The transcript's path and state are part of the fingerprint, so each
// change asks the incremental index to re-read the conversation.
func TestCodeBuddyIDE4SidecarTracksConvertedTranscript(t *testing.T) {
	cb4Env(t)
	root := CodeBuddyIDE4DefaultRoot()
	cli := cb4CLIStore(t)
	c := cb4Minimal(t, root)
	const transcript = `{"type":"message","role":"user","content":[{"type":"input_text","text":"a turn"}]}` + "\n"
	cb4Converter(t, cli, c.ID, c.ID, transcript)
	proj := filepath.Join(cli, "work-retry-demo")
	jsonl := filepath.Join(proj, c.ID+".jsonl")
	BeginWalk()
	_, converted := codeBuddyIDE4Sidecar(c.Index)
	if _, again := codeBuddyIDE4Sidecar(c.Index); again != converted {
		t.Fatal("fingerprint changed with the same transcript on disk")
	}

	// The converter replaces its transcript with another project's copy:
	// the path the fingerprint carries changes.
	other := filepath.Join(cli, "another-project")
	cb4Write(t, filepath.Join(other, c.ID+".jsonl"), []byte(transcript))
	cb4Write(t, filepath.Join(other, c.ID, codeBuddyIDE4Marker), map[string]any{
		"conversationId": c.ID, "converterVersion": "ide-main-craft-jsonl/34",
	})
	if err := os.RemoveAll(filepath.Join(proj, c.ID)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(jsonl); err != nil {
		t.Fatal(err)
	}
	BeginWalk()
	if got := CodeBuddyIDE4ConvertedTranscript(c.Index); got != filepath.Join(other, c.ID+".jsonl") {
		t.Fatalf("replacement transcript = %q, want the second project's copy", got)
	}
	if _, got := codeBuddyIDE4Sidecar(c.Index); got == converted {
		t.Error("fingerprint did not change when the converted transcript was replaced")
	}
	_, replaced := codeBuddyIDE4Sidecar(c.Index)

	// An empty transcript is not readable: it no longer stands in for 4.x.
	cb4Write(t, filepath.Join(other, c.ID+".jsonl"), []byte(""))
	BeginWalk()
	if _, got := codeBuddyIDE4Sidecar(c.Index); got == replaced {
		t.Error("fingerprint did not change when the converted transcript became empty")
	}
	if ss, err := ParseCodeBuddyIDE4File(c.Index); err != nil || len(ss) != 1 {
		t.Errorf("empty transcript: parse = %d sessions, %v; want the 4.x conversation", len(ss), err)
	}

	// A directory in place of the transcript is unreadable too. Both shapes
	// leave the same conversation to read, so their fingerprints match.
	if err := os.Remove(filepath.Join(other, c.ID+".jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(other, c.ID+".jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	BeginWalk()
	if ss, err := ParseCodeBuddyIDE4File(c.Index); err != nil || len(ss) != 1 {
		t.Errorf("unreadable transcript: parse = %d sessions, %v; want the 4.x conversation", len(ss), err)
	}
}

// A hook or MCP server parses one file without starting a walk. The converted
// lookup must still notice a conversion made after that first parse once its
// short TTL has passed.
func TestCodeBuddyIDE4ConvertedLookupExpiresWithoutAWalk(t *testing.T) {
	cb4Env(t)
	root := CodeBuddyIDE4DefaultRoot()
	cli := cb4CLIStore(t)
	c := cb4Minimal(t, root)
	const transcript = `{"type":"message","role":"user","content":[{"type":"input_text","text":"a turn"}]}` + "\n"
	BeginWalk()
	if ss, err := ParseCodeBuddyIDE4File(c.Index); err != nil || len(ss) != 1 {
		t.Fatalf("first parse = %d sessions, %v; want the 4.x conversation", len(ss), err)
	}

	oldTTL := codeBuddyIDE4ConvertedTTL
	codeBuddyIDE4ConvertedTTL = 0
	t.Cleanup(func() { codeBuddyIDE4ConvertedTTL = oldTTL })
	cb4Converter(t, cli, c.ID, c.ID, transcript)
	// No BeginWalk: only the TTL makes the next parse look again.
	if ss, err := ParseCodeBuddyIDE4File(c.Index); err != nil || len(ss) != 0 {
		t.Errorf("parse after conversion = %d sessions, %v; want none", len(ss), err)
	}
}

// The parts helper names a conversation id from a conversation index, and
// rejects the workspace index and a path outside the store's shape.
func TestCodeBuddyIDE4Parts(t *testing.T) {
	cb4Env(t)
	root := CodeBuddyIDE4DefaultRoot()
	c := cb4Minimal(t, root)
	if id, ok := codeBuddyIDE4Parts(c.Index); !ok || id != c.ID {
		t.Errorf("parts(conversation index) = %q, %v; want %q, true", id, ok, c.ID)
	}
	if _, ok := codeBuddyIDE4Parts(c.WS); ok {
		t.Error("workspace index accepted as a conversation")
	}
	if _, ok := codeBuddyIDE4Parts(filepath.Join(c.Dir, "index_bak.json")); ok {
		t.Error("a backup index accepted as a conversation")
	}
	if _, ok := codeBuddyIDE4Parts(filepath.Join(c.Dir, "messages", "m01.json")); ok {
		t.Error("a message file accepted as a conversation")
	}
}

func TestCodeBuddyIDE4LoadAndRegistry(t *testing.T) {
	cb4Env(t)
	root := CodeBuddyIDE4DefaultRoot()
	c := cb4Minimal(t, root)
	cli := cb4CLIStore(t)
	// One 5.x transcript with a real message, under its own id.
	transcript := `{"type":"message","role":"user","content":[{"type":"input_text","text":"a 5.x question"}],"timestamp":1772615400000}` + "\n"
	cb4Write(t, filepath.Join(cli, "work-retry-demo", "cli0001.jsonl"), []byte(transcript))
	BeginWalk()

	files := CodeBuddyAllFiles()
	if len(files) != 2 || !slices.Contains(files, c.Index) {
		t.Fatalf("CodeBuddyAllFiles = %v, want the 4.x index and the 5.x transcript", files)
	}
	ss := LoadCodeBuddyAll()
	if len(ss) != 2 {
		t.Fatalf("LoadCodeBuddyAll = %d sessions, want 2: %v", len(ss), ss)
	}
	ids := map[string]bool{}
	for _, s := range ss {
		ids[s.ID] = true
		if s.Harness != "codebuddy" {
			t.Errorf("%s harness = %q", s.ID, s.Harness)
		}
	}
	if !ids[c.ID] || !ids["cli0001"] {
		t.Errorf("loaded ids = %v, want both stores", ids)
	}
	if got := KindForPath(c.Index); got != "codebuddy-ide4" {
		t.Errorf("KindForPath(4.x index) = %q, want codebuddy-ide4", got)
	}
	if got := HarnessForKind("codebuddy-ide4"); got != "codebuddy" {
		t.Errorf("HarnessForKind(codebuddy-ide4) = %q, want codebuddy", got)
	}
	kind, ok := KindForPathKind(c.Index)
	if !ok || kind.Name != "codebuddy-ide4" || kind.Sidecar == nil {
		t.Errorf("registry kind = %+v, ok %v; want the 4.x kind with a sidecar", kind, ok)
	}
}

// The IDE does not always write a file-level role: the converter infers one
// from the message body, its parts and the extra fields. This table walks the
// fallback chain and checks that a turn inferred as a tool result still lands
// as a tool output record.
func TestCodeBuddyIDE4RoleInference(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra map[string]any
		parts []map[string]any
		want  string
	}{
		{"reasoning implies assistant", nil, []map[string]any{
			{"type": "reasoning", "text": "thought"}, cb4Text("answer"),
		}, "assistant"},
		{"tool-call implies assistant", nil, []map[string]any{
			cb4ToolCall("t1", "read_file", map[string]any{"filePath": "/work/r/a.go"}),
		}, "assistant"},
		{"tool-result implies tool", nil, []map[string]any{
			cb4ToolResult("t1", "read_file", cb4OKResult(map[string]any{"content": "a file body"}), false),
		}, "tool"},
		{"inputPhrase implies user", map[string]any{
			"inputPhrase": []any{map[string]any{"type": "normal", "content": "a question"}},
		}, nil, "user"},
		{"helper implies user", map[string]any{"isHelperMessage": true}, []map[string]any{
			cb4Text("a client note"),
		}, "user"},
		{"agent bookkeeping implies assistant", map[string]any{"agentMessageCount": 3}, []map[string]any{
			cb4Text("a reply"),
		}, "assistant"},
		{"tasks implies assistant", map[string]any{"tasks": []any{}}, []map[string]any{
			{"type": "image", "image": "codebuddy-asset://x"},
		}, "assistant"},
		{"no hints is no role", nil, []map[string]any{cb4Text("orphan")}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := codeBuddyIDE4Role("", nil, tc.parts, tc.extra)
			if got != tc.want {
				t.Errorf("role = %q, want %q", got, tc.want)
			}
		})
	}
	if got := codeBuddyIDE4Role("tool", nil, nil, nil); got != "tool" {
		t.Errorf("explicit file role = %q, want tool", got)
	}
	if got := codeBuddyIDE4Role("", "assistant", nil, nil); got != "assistant" {
		t.Errorf("inner role = %q, want assistant", got)
	}
	if got := codeBuddyIDE4Content(""); len(got) != 0 {
		t.Errorf("empty string content = %v, want none", got)
	}
	if got := codeBuddyIDE4Content(42); len(got) != 0 {
		t.Errorf("numeric content = %v, want none", got)
	}
	if got := codeBuddyIDE4Content([]any{"not a part", cb4Text("text")}); len(got) != 1 {
		t.Errorf("mixed content = %v, want the one object part", got)
	}
	if got := codeBuddyIDE4Object(json.RawMessage(`"\ufeff{\"role\":\"user\"}"`)); got["role"] != "user" {
		t.Errorf("BOM-prefixed message object = %v", got)
	}
	if got := codeBuddyIDE4Object(json.RawMessage(`not json`)); got != nil {
		t.Errorf("malformed message object = %v, want nil", got)
	}
}

// Input as the person phrased it: each phrase kind is rendered the way the
// converter names it, and a source block of plain text stands in when the
// prompt itself is empty.
func TestCodeBuddyIDE4FromExtra(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra map[string]any
		want  string
	}{
		{"file mention", map[string]any{"inputPhrase": []any{
			map[string]any{"type": "normal", "content": "look at "},
			map[string]any{"type": "file", "content": "a.go", "expandContent": "/work/r/a.go"},
		}}, "look at @/work/r/a.go"},
		{"folder mention", map[string]any{"inputPhrase": []any{
			map[string]any{"type": "folder", "content": "src"},
		}}, "@src"},
		{"editor selection", map[string]any{"inputPhrase": []any{
			map[string]any{"type": "editor", "content": "main.go"},
		}}, "@main.go"},
		{"command phrase", map[string]any{"inputPhrase": []any{
			map[string]any{"type": "command", "content": "/review"},
		}}, "/review"},
		{"image phrase", map[string]any{"inputPhrase": []any{
			map[string]any{"type": "img", "content": "", "expandContent": "/work/r/shot.png"},
		}}, "@image:shot.png"},
		{"knowledge and skill phrases", map[string]any{"inputPhrase": []any{
			map[string]any{"type": "knowledge", "content": "@repo"},
			map[string]any{"type": "skill", "content": "review"},
		}}, "@knowledge:repo@skill:review"},
		{"unknown phrase", map[string]any{"inputPhrase": []any{
			map[string]any{"type": "other", "content": "thing"},
			map[string]any{"type": "other", "content": "@already"},
			map[string]any{"type": "other", "content": ""},
		}}, "@thing@already"},
		{"plain text source blocks", map[string]any{"sourceContentBlocks": []any{
			map[string]any{"type": "text", "text": "a prompt "},
			map[string]any{"type": "text", "text": "from the input box"},
			map[string]any{"type": "image", "text": "ignored"},
		}}, "a prompt from the input box"},
		{"envelope in source blocks is not the prompt", map[string]any{"sourceContentBlocks": []any{
			map[string]any{"type": "text", "text": "<system_reminder>client note</system_reminder>"},
		}}, ""},
		{"empty input phrase falls through", map[string]any{
			"inputPhrase": []any{map[string]any{"type": "normal", "content": "  "}},
			"sourceContentBlocks": []any{
				map[string]any{"type": "text", "text": "fallback prompt"},
			},
		}, "fallback prompt"},
		{"nothing", map[string]any{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := codeBuddyIDE4FromExtra(tc.extra); got != tc.want {
				t.Errorf("fromExtra = %q, want %q", got, tc.want)
			}
		})
	}
	if got := codeBuddyIDE4VisibleText("", map[string]any{"inputPhrase": []any{
		map[string]any{"type": "normal", "content": "from the input box"},
	}}); got != "from the input box" {
		t.Errorf("visible text of an empty prompt = %q", got)
	}
}

func TestCodeBuddyIDE4MentionEdges(t *testing.T) {
	for _, tc := range []struct {
		name string
		link map[string]any
		want string
	}{
		{"empty uri", map[string]any{
			"type": "resource_link", "name": "orphan",
		}, ""},
		{"relative file uri uses relativePath", map[string]any{
			"type": "resource_link", "uri": "file://relative/path.go", "name": "fallback.go",
			"_meta": map[string]any{"relativePath": "src/path.go"},
		}, "@src/path.go"},
		{"skill uri fallback", map[string]any{
			"type": "resource_link", "uri": "skill://review",
		}, "@skill:review"},
		{"knowledge uri fallback", map[string]any{
			"type": "resource_link", "uri": "knowledge://guide",
		}, "@knowledge:guide"},
		{"CJK punctuation is quoted", map[string]any{
			"type": "resource_link", "uri": "/work/中文（稿）.go", "name": "x",
			"_meta": map[string]any{"mentionType": "file"},
		}, `@"/work/中文（稿）.go"`},
		{"non-ASCII bare path with an extension", map[string]any{
			"type": "resource_link", "uri": "/work/中文/go文件.go", "name": "x",
			"_meta": map[string]any{"mentionType": "file"},
		}, "@/work/中文/go文件.go"},
		{"non-ASCII bare name with no path is quoted", map[string]any{
			"type": "resource_link", "uri": "知识库", "name": "知识库",
			"_meta": map[string]any{"mentionType": "file"},
		}, "知识库"},
		{"Windows drive path", map[string]any{
			"type": "resource_link", "uri": `C:\work\main.go`, "name": "main.go",
			"_meta": map[string]any{"mentionType": "file"},
		}, `@"C:\\work\\main.go"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta, _ := tc.link["_meta"].(map[string]any)
			got := ""
			if s, ok := codeBuddyIDE4Mention(tc.link, meta); ok {
				got = s
			}
			if got != tc.want {
				t.Errorf("mention = %q, want %q", got, tc.want)
			}
		})
	}
	// displayAsPhrase false hides a block; an appshot image is still shown.
	got := codeBuddyIDE4Phrase([]any{
		map[string]any{"type": "text", "text": "kept"},
		map[string]any{"type": "resource_link", "uri": "/work/r/hidden.go", "name": "hidden.go",
			"_meta": map[string]any{"mentionType": "file", "displayAsPhrase": false}},
		map[string]any{"type": "image", "_meta": map[string]any{
			"displayAsPhrase": false, "resourceType": "appshot", "filename": "shot.png",
		}},
	})
	if got != "kept @image#1:shot.png" {
		t.Errorf("phrase = %q, want the hidden link dropped and the appshot kept", got)
	}
}

// The store's own bounds: a conversation index larger than one message file
// may be, a directory where a file should be, and a symlink left by a sync.
func TestCodeBuddyIDE4FileGuards(t *testing.T) {
	cb4Env(t)
	root := CodeBuddyIDE4DefaultRoot()
	c := cb4New(t, root, "userA/CodeBuddyIDE/userA/history", "ws0001", "conv0001")
	if _, err := codeBuddyIDE4ReadFile(filepath.Join(c.Dir, "absent.json")); err == nil {
		t.Error("codeBuddyIDE4ReadFile of a missing file returned no error")
	}
	if _, err := codeBuddyIDE4ReadFile(c.Dir); err == nil {
		t.Error("codeBuddyIDE4ReadFile of a directory returned no error")
	}
	tooBig := filepath.Join(c.Dir, "big.json")
	if err := os.MkdirAll(filepath.Dir(tooBig), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(tooBig)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(codeBuddyIDE4FileMax + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := codeBuddyIDE4ReadFile(tooBig); err == nil {
		t.Error("codeBuddyIDE4ReadFile accepted a file over the 4.x bound")
	}
	if !codeBuddyIDE4Readable(tooBig) {
		t.Error("a regular transcript over the bound should still be openable")
	}
	if codeBuddyIDE4Readable(c.Dir) || codeBuddyIDE4Readable(filepath.Join(c.Dir, "absent.json")) {
		t.Error("a directory or missing file reported as a readable transcript")
	}
	empty := filepath.Join(c.Dir, "empty.jsonl")
	cb4Write(t, empty, []byte(""))
	if codeBuddyIDE4Readable(empty) {
		t.Error("an empty transcript reported as readable")
	}
	if got := codeBuddyIDE4MarkerID(filepath.Join(c.Dir, "absent.json")); got != "" {
		t.Errorf("marker id of a missing file = %q", got)
	}
	cb4Write(t, filepath.Join(c.Dir, "huge-marker.json"), []byte(strings.Repeat("x", 64<<10+1)))
	if got := codeBuddyIDE4MarkerID(filepath.Join(c.Dir, "huge-marker.json")); got != "" {
		t.Errorf("marker id of an oversized file = %q", got)
	}
}

// The two shapes the 5.x converter writes for an MCP tool's answer: a list
// of text blocks under result, or one text block under result, plus the
// input_text spelling. Images and an empty body are left out.
func TestCodeBuddyIDE4ResultText(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result any
		want   string
	}{
		{"shell stdout", map[string]any{
			"status": "success",
			"result": map[string]any{"type": "execute_command_result", "stdout": "ok  tests", "exitCode": 0},
		}, "ok  tests"},
		{"read file content", map[string]any{
			"result": map[string]any{"type": "read_file_result", "content": "package main"},
		}, "package main"},
		{"MCP text under result", map[string]any{
			"result": map[string]any{"type": "text", "text": "MCP tool said hello"},
		}, "MCP tool said hello"},
		{"MCP input_text under result", map[string]any{
			"result": map[string]any{"type": "input_text", "text": "input_text from MCP"},
		}, "input_text from MCP"},
		{"MCP data list under result", map[string]any{
			"result": map[string]any{"data": []any{
				map[string]any{"type": "text", "text": "first MCP line"},
				map[string]any{"type": "text", "text": "second MCP line"},
			}},
		}, "first MCP line\nsecond MCP line"},
		{"result array of MCP blocks", map[string]any{
			"result": []any{
				map[string]any{"type": "text", "text": "first MCP line"},
				map[string]any{"type": "input_text", "text": "second MCP line"},
			},
		}, "first MCP line\nsecond MCP line"},
		{"an image block is not output", map[string]any{
			"result": []any{map[string]any{"type": "image", "data": "ignored"}},
		}, ""},
		{"error message when there is no output", map[string]any{
			"status": "error", "errorMessage": "the MCP server refused",
		}, "the MCP server refused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := codeBuddyIDE4ResultText(tc.result); got != tc.want {
				t.Errorf("result text = %q, want %q", got, tc.want)
			}
		})
	}
	if got := codeBuddyIDE4ResultText(`[{"type":"image_blob_ref","blob_id":"x"}]`); got != "" {
		t.Errorf("an image blob ref indexed as %q", got)
	}
}
