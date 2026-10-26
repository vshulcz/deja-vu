package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/tui"
)

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	aged(t, p)
}

// aged sets a file an hour back: a session written in the last minutes is
// taken for one still running and refused.
func aged(t *testing.T, ps ...string) {
	t.Helper()
	old := time.Now().Add(-time.Hour)
	for _, p := range ps {
		_ = filepath.Walk(p, func(q string, _ os.FileInfo, err error) error {
			if err == nil {
				_ = os.Chtimes(q, old, old) //nolint:gosec // the test walks its own temp tree
			}
			return nil
		})
	}
}

func indexed(t *testing.T, dir string) []model.Session {
	t.Helper()
	if err := index.Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	ss, _, err := index.RecentMatchingCounted(dir, 0, search.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return ss
}

// deja delete erases a Claude Code session from Claude's store, its subagent
// runs with it even when they are indexed sessions of their own, and then
// from deja.
func TestDeleteErasesFromTheAgentAndFromDeja(t *testing.T) {
	dir, root := tuiStore(t)
	tr := filepath.Join(root, "payments", "b2222222-retry.jsonl")
	sub := filepath.Join(root, "payments", "b2222222-retry", "subagents", "agent-a9.jsonl")
	writeClaudeFixture(t, sub, "agent-a9", []string{`{"type":"user","sessionId":"b2222222-retry","isSidechain":true,"agentId":"a9","cwd":"/work/payments","timestamp":"2026-03-02T10:05:00Z","message":{"role":"user","content":"look at the retry"}}`})
	aged(t, tr, filepath.Dir(filepath.Dir(sub)))
	indexed(t, dir)

	var out bytes.Buffer
	if err := runDeleteIn(dir, []string{"b2222222", "--dry-run"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), tr) || !strings.Contains(out.String(), "nothing removed") {
		t.Errorf("dry run said %q", out.String())
	}
	if _, err := os.Stat(tr); err != nil {
		t.Fatal("the dry run removed the transcript")
	}
	if err := runDeleteIn(dir, []string{"b2222222"}, strings.NewReader("y\n"), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("no terminal and no --yes: %v", err)
	}
	out.Reset()
	if err := runDeleteIn(dir, []string{"b2222222", "--yes"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{tr, filepath.Dir(filepath.Dir(sub))} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s is still in Claude's store", p)
		}
	}
	if s, ok, _ := index.FindByPrefix(dir, "b2222222-retry"); ok && s.Harness == "claude" {
		t.Errorf("deja still holds the deleted session: %s", s.Title)
	}
	if !strings.Contains(out.String(), "erased b2222222") {
		t.Errorf("delete said %q", out.String())
	}
}

// What deja must never remove: a store many sessions share however few are
// indexed, a history file, a directory with someone else's session in it, a
// session written a minute ago, and anything when the index could not be
// read to check against.
func TestDeleteRefusesWhatIsNotOneSessions(t *testing.T) {
	dir, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	other := model.Session{Harness: "claude", ID: "zzzz9999-other", Path: filepath.Join(home, "elsewhere.jsonl")}
	refuse := func(name string, s model.Session, others []model.Session, want string) {
		t.Helper()
		before, _ := os.Stat(s.Path)
		if _, err := eraseSession(dir, s, others); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", name, err, want)
		}
		if before != nil {
			if _, err := os.Stat(s.Path); err != nil {
				t.Errorf("%s: %s was removed", name, s.Path)
			}
		}
	}
	// One indexed Grok session in grok.db is still the whole database.
	db := filepath.Join(home, ".grok", "grok.db")
	writeFile(t, db, "sqlite")
	s := model.Session{Harness: "grok", ID: "0190aaaa-bbbb", Path: db}
	refuse("a database with one indexed session", s, []model.Session{s, other}, "shares with others")
	hist := filepath.Join(home, ".claude", "history.jsonl")
	writeFile(t, hist, "{}\n")
	s = model.Session{Harness: "claude", ID: "aaaa1111-x", Path: hist}
	refuse("history.jsonl", s, []model.Session{s, other}, "shares with others")
	// A directory that only carries the id in a longer name is not the
	// session's, so the session inside it, named by nothing, stays.
	sd := filepath.Join(home, ".copilot", "session-state", "bbbb2222-session-copy")
	mine, theirs := filepath.Join(sd, "events.jsonl"), filepath.Join(sd, "x", "events.jsonl")
	writeFile(t, mine, "{}\n")
	writeFile(t, theirs, "{}\n")
	aged(t, sd)
	s = model.Session{Harness: "copilot", ID: "bbbb2222-session", Path: mine}
	refuse("a directory that only contains the id", s, []model.Session{s, {Harness: "copilot", ID: "cccc3333-else", Path: theirs}}, "shares with others")
	// The transcript itself shared with another indexed session.
	own := filepath.Join(home, ".qwen", "projects", "p", "chats", "abab1212-q.jsonl")
	writeFile(t, own, "{}\n")
	s = model.Session{Harness: "qwen", ID: "abab1212-q", Path: own}
	refuse("a transcript another session reads", s, []model.Session{s, {Harness: "qwen", ID: "cdcd3434-q", Path: own}}, "also holds")
	// No index to check against.
	refuse("no index", s, nil, "could not read its index")
	// A file named by nothing.
	plain := filepath.Join(home, ".gemini", "tmp", "p", "chats", "notes.json")
	writeFile(t, plain, "{}")
	s = model.Session{Harness: "gemini", ID: "dddd4444-gem", Path: plain}
	refuse("a file that does not name the session", s, []model.Session{s, other}, "shares with others")
	// Written a minute ago.
	fresh := filepath.Join(home, ".qwen", "projects", "p", "chats", "eeee5555-q.jsonl")
	writeFile(t, fresh, "{}\n")
	_ = os.Chtimes(fresh, time.Now(), time.Now())
	s = model.Session{Harness: "qwen", ID: "eeee5555-q", Path: fresh}
	refuse("a fresh session", s, []model.Session{s, other}, "last 5 minutes")
	// Agents nobody checked, and deja's own notes.
	refuse("zed", model.Session{Harness: "zed", ID: "ffff6666-z", Path: db}, []model.Session{other}, "Zed sessions can't be deleted yet")
	refuse("a note", model.Session{Harness: "deja", ID: "deja-note-x1234567", Path: db}, []model.Session{other}, "deja forget")
}

func TestDeleteRefusesARunningSession(t *testing.T) {
	dir, root := tuiStore(t)
	markSessionLive(dir, "b2222222-retry")
	tr := filepath.Join(root, "payments", "b2222222-retry.jsonl")
	aged(t, tr)
	s := model.Session{Harness: "claude", ID: "b2222222-retry", Path: tr}
	if _, err := eraseSession(dir, s, indexed(t, dir)); err == nil || !strings.Contains(err.Error(), "running") {
		t.Errorf("a live session: %v", err)
	}
	if _, err := os.Stat(tr); err != nil {
		t.Error("a live session's transcript was removed")
	}
}

// Cursor's CLI keeps the conversation in chats/<hash>/<id>; the transcript
// deja reads is a copy, and removing only it left the chat in cursor-agent.
func TestDeleteTakesCursorsChatStoreToo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	id := "cf555555-f21f-4493-b8f8-7eaf2c0a80d4"
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(home, ".cursor"))
	tr := filepath.Join(home, ".cursor", "projects", "p", "agent-transcripts", id, id+".jsonl")
	store := filepath.Join(home, ".cursor", "chats", "42802a0c", id, "store.db")
	writeFile(t, tr, "{}\n")
	writeFile(t, store, "db")
	aged(t, filepath.Dir(tr), filepath.Dir(store))
	s := model.Session{Harness: "cursor", ID: id, Path: tr}
	files, err := eraseSession(t.TempDir(), s, []model.Session{s})
	if err != nil || len(files) != 2 {
		t.Fatalf("%v %v", files, err)
	}
	for _, p := range []string{filepath.Dir(tr), filepath.Dir(store)} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s is still there", p)
		}
	}
}

// Antigravity keeps the conversation in conversations/<id>.db beside
// brain/<id>; that database is the session's own and goes with it.
func TestDeleteTakesAntigravitysConversationDatabase(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	id := "f017fad5-095f-47ee-abf7-c13b7ce6844a"
	root := filepath.Join(home, ".gemini", "antigravity")
	tr := filepath.Join(root, "brain", id, ".system_generated", "logs", "overview.txt")
	db := filepath.Join(root, "conversations", id+".db")
	keep := filepath.Join(root, "conversations", "0aaaaaaa-other.db")
	pbtxt := filepath.Join(root, "annotations", id+".pbtxt")
	for _, p := range []string{tr, db, db + "-wal", keep, pbtxt} {
		writeFile(t, p, "x")
	}
	aged(t, filepath.Join(root, "brain", id))
	s := model.Session{Harness: "antigravity", ID: id, Path: tr}
	if _, err := eraseSession(t.TempDir(), s, []model.Session{s}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(root, "brain", id), db, db + "-wal", pbtxt} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s is still there", p)
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Error("another conversation's database was removed")
	}
}

// opencode keeps sessions in its database and deletes one with its own
// command.
func TestDeleteOpencodeThroughItsOwnCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in opencode is a shell script")
	}
	bin, store := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(bin, "opencode"), "#!/bin/sh\n[ \"$1 $2\" = \"session delete\" ] && rm "+store+"/$3\n")
	if err := os.Chmod(filepath.Join(bin, "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/bin"+string(os.PathListSeparator)+"/usr/bin")
	writeFile(t, filepath.Join(store, "ses_abc12345"), "{}")
	if _, err := eraseSession(t.TempDir(), model.Session{Harness: "opencode", ID: "ses_abc12345", Path: "/w/q"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(store, "ses_abc12345")); err == nil {
		t.Error("opencode still has the session")
	}
}

// D asks, says it cannot be undone, and enter erases the transcript and then
// forgets the session in deja.
func TestTUIDeleteErasesEverywhere(t *testing.T) {
	dir, root := tuiStore(t)
	tr := filepath.Join(root, "payments", "b2222222-retry.jsonl")
	aged(t, tr)
	a := newTestTUI(t, dir)
	var forgot []string
	a.forget = func(id string) error { forgot = append(forgot, id); return nil }
	for i, r := range a.rows {
		if r.s.ID == "b2222222-retry" {
			a.sel = i
		}
	}
	a.listFocus = true
	// Written a minute ago: refused before the question, not after the yes.
	now := time.Now()
	_ = os.Chtimes(tr, now, now)
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: 'D'})
	if a.modal == modalDelete {
		t.Fatal("D asked about a session it would refuse")
	}
	aged(t, tr)
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: 'D'})
	if a.modal != modalDelete {
		t.Fatalf("D opened modal %d", a.modal)
	}
	wantOnScreen(t, screen(a.frame(100, 30)), "Delete this session everywhere?", "Gone from Claude Code and from deja.", "This cannot be undone.")
	// Enter keeps it: the reflexive key is not the yes.
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEnter})
	if a.modal != modalNone {
		t.Fatal("Enter left the question open")
	}
	if _, err := os.Stat(tr); err != nil {
		t.Fatal("Enter erased the session")
	}
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: 'D'})
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: 'y'})
	settle(a)
	if _, err := os.Stat(tr); err == nil {
		t.Error("the transcript is still there")
	}
	if len(forgot) != 1 || forgot[0] != "b2222222-retry" {
		t.Errorf("deja forgot %v", forgot)
	}
	if strings.Contains(a.toast, "unforget") {
		t.Errorf("the toast offers an undo that does not exist: %q", a.toast)
	}
}

// settle waits for the screen's background work, then applies what it sent,
// until nothing more arrives: the erase posts the forget, which posts again.
func settle(a *tuiApp) {
	for {
		a.bg.Wait()
		select {
		case f := <-a.updates:
			f()
		default:
			return
		}
	}
}

// A session run in /work/<id> is filed by Claude under -work-<id>, the
// project directory every other session there shares. Only the session's own
// transcript and <id>/ go; the project directory and its neighbour stay.
func TestDeleteKeepsAProjectDirectoryThatOnlyContainsTheID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	id := "5e55a0aa-1111-4222-8333-444455556666"
	proj := filepath.Join(home, ".claude", "projects", "-work-"+id)
	mine := filepath.Join(proj, id+".jsonl")
	theirs := filepath.Join(proj, "77777777-0000-4000-8000-000000000000.jsonl")
	writeFile(t, mine, "{}\n")
	writeFile(t, theirs, "{}\n")
	writeFile(t, filepath.Join(proj, "memory", "MEMORY.md"), "x")
	aged(t, proj)
	s := model.Session{Harness: "claude", ID: id, Path: mine}
	// The neighbour forgotten in deja: the index no longer names it.
	files, err := eraseSession(t.TempDir(), s, []model.Session{s})
	if err != nil || len(files) != 1 || files[0] != mine {
		t.Fatalf("erased %v, %v; want only the transcript", files, err)
	}
	for _, p := range []string{theirs, filepath.Join(proj, "memory", "MEMORY.md")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s went with it", p)
		}
	}
}

// The resolver matches the middle of an id; an erase must not.
func TestDeleteTakesTheStartOfAnIDOnly(t *testing.T) {
	dir, root := tuiStore(t)
	aged(t, filepath.Join(root, "payments"))
	indexed(t, dir)
	for _, p := range []string{"retry", "2222"} {
		err := runDeleteIn(dir, []string{p, "--yes"}, nil, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "not the start of a session id") {
			t.Errorf("%q: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "payments", "b2222222-retry.jsonl")); err != nil {
		t.Fatal("a fragment of the id erased the session")
	}
}

// A dry run lists a session written a minute ago; only the erase refuses it.
func TestDeleteDryRunListsAFreshSession(t *testing.T) {
	dir, root := tuiStore(t)
	tr := filepath.Join(root, "payments", "b2222222-retry.jsonl")
	indexed(t, dir)
	_ = os.Chtimes(tr, time.Now(), time.Now())
	var out bytes.Buffer
	if err := runDeleteIn(dir, []string{"b2222222", "--dry-run"}, nil, &out); err != nil || !strings.Contains(out.String(), tr) {
		t.Errorf("dry run: %q, %v", out.String(), err)
	}
	if err := runDeleteIn(dir, []string{"b2222222", "--yes"}, nil, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "last 5 minutes") {
		t.Errorf("erase of a fresh session: %v", err)
	}
}

// Qwen keeps a chat's sub-agent logs in subagents/<id>/ beside chats/.
func TestDeleteTakesQwensSubagentLogs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	id := "9a9a9a9a-qwen-chat"
	proj := filepath.Join(home, ".qwen", "projects", "p")
	tr := filepath.Join(proj, "chats", id+".jsonl")
	sub := filepath.Join(proj, "subagents", id, "agent-1.jsonl")
	writeFile(t, tr, "{}\n")
	writeFile(t, sub, "{}\n")
	aged(t, proj)
	s := model.Session{Harness: "qwen", ID: id, Path: tr}
	if _, err := eraseSession(t.TempDir(), s, []model.Session{s}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(sub)); err == nil {
		t.Error("the sub-agent logs stayed")
	}
}

// A subagent run on its own is refused with the reason, not as shared.
func TestDeleteSaysASubagentGoesWithItsParent(t *testing.T) {
	home := t.TempDir()
	sid := "4b4b4b4b-kimi-sess"
	p := filepath.Join(home, ".kimi", "sessions", "h", sid, "agents", "explore", "wire.jsonl")
	writeFile(t, p, "{}\n")
	s := model.Session{Harness: "kimi", ID: "explore-" + sid, Parent: sid, Path: p}
	if _, err := sessionFiles(s, []model.Session{s}); err == nil || !strings.Contains(err.Error(), "subagent run inside session") {
		t.Errorf("%v", err)
	}
}

// Help described which agents delete takes — "agents that keep a session as
// its own files, and opencode; not goose, Hermes or Zed" — instead of naming
// them, so the agents that do work were never listed and Continue, CodeBuddy,
// Kiro and the rest read as an oversight in the command rather than a gap in
// the check (#5654). The page is built from the two maps now.
func TestHelpNamesEveryAgentDeleteTakes(t *testing.T) {
	hermeticEnv(t)
	out, err := captureRun(t, "help", "delete")
	if err != nil {
		t.Fatal(err)
	}
	if len(deletable) == 0 || len(cliStores) == 0 {
		t.Fatal("no agents to name, wrong fixture")
	}
	for h := range deletable {
		if !strings.Contains(out, agentName(h)) {
			t.Errorf("`deja help delete` does not name %s, whose session files it erases:\n%s", agentName(h), out)
		}
	}
	for h := range cliStores {
		if !strings.Contains(out, agentName(h)) {
			t.Errorf("`deja help delete` does not name %s, whose sessions it deletes through the agent's own command:\n%s", agentName(h), out)
		}
	}
}

// The refusal opened with the program's own name, and the CLI prints that name
// before it: `deja: deja can't delete Continue sessions yet` (#5654). Whoever
// reads it needs the agent first and the reason after.
func TestDeleteRefusalOpensWithTheAgent(t *testing.T) {
	got := deletableRefusal("continue").Error()
	want := "Continue sessions can't be deleted yet: deja has not checked how Continue lists them"
	if got != want {
		t.Errorf("refusal = %q, want %q", got, want)
	}
	if strings.HasPrefix(got, "deja") {
		t.Errorf("refusal repeats the program's name: %q", got)
	}
}
