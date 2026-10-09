package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/tui"
)

// tuiStore indexes three Claude sessions in two projects and returns the
// index dir and the claude root.
func tuiStore(t *testing.T) (string, string) {
	t.Helper()
	withTempStores(t)
	root := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("PATH", "")
	write := func(project, id, at, asked, answered string) {
		user, _ := json.Marshal(map[string]any{"type": "user", "sessionId": id, "cwd": "/work/" + project, "timestamp": at,
			"message": map[string]any{"role": "user", "content": asked}})
		reply, _ := json.Marshal(map[string]any{"type": "assistant", "sessionId": id, "cwd": "/work/" + project, "timestamp": at,
			"message": map[string]any{"role": "assistant", "content": []map[string]any{{"type": "text", "text": answered}}}})
		writeClaudeFixture(t, filepath.Join(root, project, id+".jsonl"), id, []string{string(user), string(reply)})
	}
	write("checkout", "a1111111-pool", "2026-03-01T10:00:00Z", "every pod loses its database connections five minutes in",
		"Retire pool connections before the proxy does: MaxConnLifetime 4m. The proxy cuts idle backends at 5m and pgx never knows.")
	write("payments", "b2222222-retry", "2026-03-02T10:00:00Z", "the retry double charges the card",
		"Derive the idempotency key from the charge id, not the request. Two retries of one request reused it and Stripe answered 409.")
	write("payments", "c3333333-hook", "2026-03-03T10:00:00Z", "webhook signature fails after key rotation",
		"Verify against every published key and log which one matched, so the rotation can finish without dropping webhooks.")
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	return dir, root
}

func newTestTUI(t *testing.T, dir string) *tuiApp {
	t.Helper()
	a := newTUIApp(dir, nil)
	a.projects = nil
	a.scope = scopeAll
	a.clock = func() time.Time { return time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC) }
	a.copy = func(string) error { return nil }
	a.loadHome()
	for _, r := range a.rows {
		a.details[sessionKey(r.s)] = tuiLoadDetail(dir, r.s)
	}
	return a
}

func screen(c *tui.Canvas) string {
	var b strings.Builder
	for y := 0; y < c.H; y++ {
		b.WriteString(c.Text(y) + "\n")
	}
	return b.String()
}

func wantOnScreen(t *testing.T, s string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(s, w) {
			t.Errorf("screen lacks %q:\n%s", w, s)
		}
	}
}

// drain runs the updates the background work posted, waiting for at least one.
func drain(t *testing.T, a *tuiApp) {
	t.Helper()
	select {
	case f := <-a.updates:
		f()
	case <-time.After(5 * time.Second):
		t.Fatal("no update arrived")
	}
	for {
		select {
		case f := <-a.updates:
			f()
		default:
			return
		}
	}
}

// A card leads with what was asked, the conclusion dim under it, and the home
// list groups the cards by day.
func TestTUIHomeLeadsWithWhatWasAsked(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	if len(a.rows) != 3 || a.rows[0].s.ID != "c3333333-hook" {
		t.Fatalf("home rows = %+v", a.rows)
	}
	if a.rows[0].section != "Yesterday" || a.rows[1].section != "This week" || a.rows[2].section != "" {
		t.Errorf("sections = %q %q %q", a.rows[0].section, a.rows[1].section, a.rows[2].section)
	}
	s := screen(a.frame(120, 36))
	wantOnScreen(t, s, "◆ deja", "All projects", "YESTERDAY  1", "THIS WEEK  2", "3 sessions · 1 agents",
		"Claude Code · payments · yesterday", "payments · 2d ago",
		"ASKED", "CONCLUDED", "Read", "Resume", "Continue in")
	lines, found := strings.Split(s, "\n"), false
	for i, ln := range lines {
		if strings.Contains(ln, "webhook signature fails after key rotation") && !strings.Contains(ln, "ASKED") {
			if i+2 >= len(lines) || !strings.Contains(lines[i+1], "Claude Code · payments") ||
				!strings.Contains(lines[i+2], "Verify against every published key") {
				t.Errorf("card is not question, meta, conclusion:\n%s", s)
			}
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no card leads with the question:\n%s", s)
	}
	// Narrow, the list takes the width and the preview goes.
	s = screen(a.frame(80, 30))
	if strings.Contains(s, "ASKED") {
		t.Errorf("no preview at 80 columns:\n%s", s)
	}
	wantOnScreen(t, s, "the retry double charges the card", "Derive the idempotency key")
	// Short, the agent strip goes before the list does.
	s = screen(a.frame(100, 14))
	wantOnScreen(t, s, "webhook signature fails")
}

func TestTUIDayGroups(t *testing.T) {
	now := time.Date(2026, 3, 4, 0, 30, 0, 0, time.UTC)
	for at, want := range map[time.Time]string{
		{}:                         "Earlier",
		now.Add(time.Hour):         "Today",
		now.Add(-20 * time.Minute): "Today",
		now.Add(-time.Hour):        "Yesterday",
		now.AddDate(0, 0, -2):      "This week",
		now.AddDate(0, 0, -6):      "This week",
		now.AddDate(0, 0, -7):      "Earlier",
	} {
		if got := dayGroup(at, now); got != want {
			t.Errorf("dayGroup(%v) = %q, want %q", at, got, want)
		}
	}
}

func TestTUISearchAndEmptyState(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	a.query = []rune("idempotency")
	hits, err := tuiSearch(dir, search.Options{Query: "idempotency", Limit: 80})
	if err != nil || len(hits) != 1 {
		t.Fatalf("hits = %d, %v", len(hits), err)
	}
	a.setRows(nil, hits, len(hits))
	s := screen(a.frame(120, 36))
	wantOnScreen(t, s, "1 SESSION ACROSS 1 AGENT", "MATCHED", "1  ")
	a.listFocus = true
	wantOnScreen(t, screen(a.frame(120, 36)), "/  search")

	a.query = []rune("kafka rebalance")
	a.setRows(nil, []search.Hit{}, 0)
	s = screen(a.frame(120, 36))
	wantOnScreen(t, s, "Nothing for “kafka rebalance”", "Drop a word")
	a.scope = scopeHere
	wantOnScreen(t, screen(a.frame(120, 36)), "All projects")
	a.query = nil
	wantOnScreen(t, screen(a.frame(120, 36)), "No sessions here yet")
	a.scope = scopeKept
	wantOnScreen(t, screen(a.frame(120, 36)), "Checking")
	a.keptLoaded = true
	wantOnScreen(t, screen(a.frame(60, 20)), "Nothing deleted yet")
}

// Typing searches in the background, and the line editing keys work on the
// query.
func TestTUITypingSearches(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	for _, r := range "pool" {
		a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: r})
	}
	for a.searching {
		drain(t, a)
	}
	if len(a.rows) != 1 || a.rows[0].s.ID != "a1111111-pool" {
		t.Fatalf("rows after typing = %+v", a.rows)
	}
	wantOnScreen(t, screen(a.frame(120, 36)), "searched 3 sessions in")
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyCtrl, Rune: 'w'})
	if len(a.query) != 0 {
		t.Errorf("ctrl-w drops the word, left %q", string(a.query))
	}
	a.handle(tui.Event{Kind: tui.EvPaste, Text: "double\ncharges"})
	if string(a.query) != "double charges" {
		t.Errorf("a paste is one line, got %q", string(a.query))
	}
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyBackspace})
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyCtrl, Rune: 'u'})
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEsc})
	if !a.quit {
		t.Error("esc on an empty box quits")
	}
}

func TestTUIListKeys(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	key := func(k tui.Key, r rune) { a.handle(tui.Event{Kind: tui.EvKey, Key: k, Rune: r}) }
	key(tui.KeyDown, 0)
	if !a.listFocus || a.sel != 1 {
		t.Fatalf("down moves into the list: focus %v sel %d", a.listFocus, a.sel)
	}
	key(tui.KeyRune, 'j')
	key(tui.KeyRune, 'j')
	key(tui.KeyRune, 'k')
	key(tui.KeyPgDn, 0)
	key(tui.KeyPgUp, 0)
	key(tui.KeyEnd, 0)
	key(tui.KeyHome, 0)
	key(tui.KeyCtrl, 'n')
	key(tui.KeyCtrl, 'p')
	key(tui.KeyUp, 0)
	if a.sel != 0 {
		t.Errorf("sel = %d", a.sel)
	}
	key(tui.KeyRune, '?')
	if a.modal != modalHelp {
		t.Fatal("? opens the keys")
	}
	wantOnScreen(t, screen(a.frame(120, 36)), "Keys", "continue in any agent")
	key(tui.KeyRune, 'x')
	key(tui.KeyRune, 'c')
	if a.toast == "" || !a.toastGood {
		t.Errorf("c copies the context, toast %q", a.toast)
	}
	wantOnScreen(t, screen(a.frame(120, 36)), "Context copied")
	key(tui.KeyRune, '/')
	if a.listFocus {
		t.Error("/ goes back to the box")
	}
	key(tui.KeyDown, 0)
	key(tui.KeyRune, 'z')
	if a.listFocus || string(a.query) != "z" {
		t.Errorf("a letter that is no action types: focus %v query %q", a.listFocus, string(a.query))
	}
	key(tui.KeyEsc, 0)
	key(tui.KeyTab, 0)
	key(tui.KeyBackTab, 0)
	key(tui.KeyTab, 0)
	if a.scope != scopeKept {
		t.Errorf("scope = %d", a.scope)
	}
	a.handle(tui.Event{Kind: tui.EvResize, W: 100, H: 30})
	key(tui.KeyRune, 'q')
	key(tui.KeyCtrl, 'c')
	if !a.quit {
		t.Error("ctrl-c quits")
	}
}

func TestTUIMouse(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	a.frame(120, 36)
	var card zone
	for _, z := range a.zones {
		if z.y0 > 6 && z.x0 == 1 {
			card = z
			break
		}
	}
	click := func(x, y int) {
		a.handle(tui.Event{Kind: tui.EvMouse, Button: tui.MouseLeft, Press: true, X: x, Y: y})
	}
	click(card.x0+3, card.y0)
	click(card.x0+3, card.y0)
	if a.view != viewReader {
		t.Error("a double click opens the session")
	}
	a.handle(tui.Event{Kind: tui.EvMouse, Button: tui.MouseWheelDown, Press: true})
	a.handle(tui.Event{Kind: tui.EvMouse, Button: tui.MouseWheelUp, Press: true})
	a.view = viewList
	a.handle(tui.Event{Kind: tui.EvMouse, Button: tui.MouseWheelDown, Press: true})
	a.handle(tui.Event{Kind: tui.EvMouse, Button: tui.MouseWheelUp, Press: true})
	a.handle(tui.Event{Kind: tui.EvMouse, Button: tui.MouseLeft, Press: false})
	a.frame(120, 36)
	click(30, 0) // the "All projects" tab
	if a.scope != scopeAll {
		t.Errorf("tab click scope = %d", a.scope)
	}
}

func TestTUIReader(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	a.query = []rune("proxy")
	a.openReader()
	s := screen(a.frame(120, 36))
	wantOnScreen(t, s, "Claude Code", "you", "webhook signature")
	a.view = viewList
	a.sel = 2
	a.openReader()
	s = screen(a.frame(120, 36))
	wantOnScreen(t, s, "hit 1 of 1", "MaxConnLifetime")
	for _, r := range "nNjkgGbt q" {
		a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: r})
		a.view = viewReader
	}
	for _, k := range []tui.Key{tui.KeyUp, tui.KeyDown, tui.KeyPgDn, tui.KeyPgUp, tui.KeyEnd, tui.KeyHome} {
		a.handle(tui.Event{Kind: tui.EvKey, Key: k})
	}
	a.handle(tui.Event{Kind: tui.EvMouse, Button: tui.MouseWheelDown})
	a.handle(tui.Event{Kind: tui.EvPaste})
	a.frame(60, 10)
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: 'c'})
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: '?'})
	a.modal = modalNone
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEsc})
	if a.view != viewList {
		t.Error("esc leaves the reader")
	}
	// A session not read yet draws its placeholder instead of nothing.
	a.details = map[string]*tuiDetail{}
	a.openReader()
	wantOnScreen(t, screen(a.frame(120, 36)), "▀▀▀▀")
}

func TestTUIContinueIn(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	var copied string
	a.copy = func(s string) error { copied = s; return nil }
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyCtrl, Rune: 'o'})
	if a.modal != modalContinue {
		t.Fatal("ctrl-o opens continue-in")
	}
	s := screen(a.frame(120, 40))
	wantOnScreen(t, s, "Continue this session in…", "+41 not installed here", "Show them")
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEnter})
	wantOnScreen(t, screen(a.frame(120, 40)), "Copy for")
	for _, k := range []tui.Key{tui.KeyDown, tui.KeyRight, tui.KeyLeft, tui.KeyUp, tui.KeyTab} {
		a.handle(tui.Event{Kind: tui.EvKey, Key: k})
	}
	for _, r := range "codex" {
		a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: r})
	}
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyBackspace})
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: 'x'})
	ts := a.shownTargets()
	if len(ts) != 1 || ts[0].id != "codex" {
		t.Fatalf("filtered = %+v", ts)
	}
	wantOnScreen(t, screen(a.frame(70, 30)), "Copy for Codex CLI")
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEnter})
	if a.modal != modalNone || !strings.Contains(copied, "webhook signature") {
		t.Errorf("an agent not on this machine gets the context copied: modal %d, copied %q", a.modal, copied)
	}
	a.openContinue()
	a.m.filter = []rune("nothing like this")
	wantOnScreen(t, screen(a.frame(120, 36)), "No agent by that name.")
	a.handleModal(tui.Event{Kind: tui.EvKey, Key: tui.KeyEnter})
	a.handleModal(tui.Event{Kind: tui.EvMouse})
	a.handleModal(tui.Event{Kind: tui.EvKey, Key: tui.KeyEsc})
	// An installed one leaves the screen and hands over.
	a.openContinue()
	a.continueIn(continueTarget{id: "codex", installed: true})
	if !a.quit || a.after == nil {
		t.Error("an installed agent is started once the screen is gone")
	}
}

func TestTUIAgentsFilter(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	a.listFocus = true
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: 'a'})
	wantOnScreen(t, screen(a.frame(120, 36)), "Agents", "41 supported")
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: ' '})
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: ' '})
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: 'x'})
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: ' '})
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEnter})
	if !a.filter["claude"] || len(a.rows) != 3 {
		t.Errorf("filter = %v rows %d", a.filter, len(a.rows))
	}
	wantOnScreen(t, screen(a.frame(120, 36)), "filtered")
	a.filter = map[string]bool{"codex": true}
	a.reload()
	if len(a.rows) != 0 {
		t.Errorf("a filter on an agent with no sessions leaves nothing, got %d", len(a.rows))
	}
}

// Resume leaves the screen first, and a session its agent deleted is offered
// back before it is offered to resume.
func TestTUIResumeAndPutBack(t *testing.T) {
	dir, root := tuiStore(t)
	a := newTestTUI(t, dir)
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyCtrl, Rune: 'r'})
	if !a.quit || a.after == nil {
		t.Fatal("resume quits and runs on the way out")
	}
	path := filepath.Join(root, "payments", "c3333333-hook.jsonl")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	a = newTestTUI(t, dir)
	go a.loadKept()
	drain(t, a)
	if len(a.kept) != 1 || !a.details[sessionKey(a.rows[0].s)].gone {
		t.Fatalf("kept = %d", len(a.kept))
	}
	a.setScope(scopeKept)
	s := screen(a.frame(120, 36))
	wantOnScreen(t, s, "Deleted 1", "DELETED BY THEIR AGENT, KEPT BY DEJA", "Deleted by Claude Code", "deja kept a copy", "Put back")
	a.listFocus = true
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: 'r'})
	if a.quit || !strings.Contains(a.toast, "R puts it back") {
		t.Errorf("a deleted session is not resumed: quit %v toast %q", a.quit, a.toast)
	}
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: 'R'})
	drain(t, a)
	if _, err := os.Stat(path); err != nil || !strings.Contains(a.toast, "Put back") {
		t.Errorf("put back: %v, toast %q", err, a.toast)
	}
	// Put back, it leaves Deleted.
	if len(a.kept) != 0 || len(a.rows) != 0 || strings.Contains(screen(a.frame(120, 36)), "Deleted 1") {
		t.Errorf("still kept: %d kept, %d rows", len(a.kept), len(a.rows))
	}
	a.setScope(scopeAll)
	for i, r := range a.rows {
		if r.s.ID == "c3333333-hook" {
			a.sel = i
		}
	}
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: 'R'})
	if !strings.Contains(a.toast, "nothing to put back") {
		t.Errorf("toast %q", a.toast)
	}
}

// Without a terminal, the bare command falls back to the brief.
func TestRunTUIFallsBackWithoutATerminal(t *testing.T) {
	dir, _ := tuiStore(t)
	old := os.Stdout
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	os.Stdout = f
	err = runTUI(dir)
	os.Stdout = old
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(f.Name())
	if !strings.Contains(string(b), "deja") {
		t.Errorf("fallback printed %q", b)
	}
}
