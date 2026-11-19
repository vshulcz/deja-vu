package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/termwidth"
	"github.com/vshulcz/deja-vu/internal/tui"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// i and p copy the session's id and its project's path, from the list and the
// reader; F asks first and then forgets the session the way the command does.
func TestTUISessionActions(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	var copied string
	a.copy = func(s string) error { copied = s; return nil }
	a.forget = func(id string) error { return runForget(dir, []string{"--session", id}) }
	key := func(r rune) { a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: r}) }
	s := a.rows[0].s
	a.listFocus = true
	key('i')
	if copied != s.ID || !strings.Contains(a.toast, "Session id copied") {
		t.Errorf("i copied %q, toast %q", copied, a.toast)
	}
	a.openReader()
	a.frame(120, 36)
	key('p')
	if copied != s.Project || copied == "" || !strings.Contains(a.toast, "Project path copied") {
		t.Errorf("p copied %q, toast %q", copied, a.toast)
	}
	labels := ""
	var labelsSb32 strings.Builder
	for _, it := range a.paletteItems() {
		labelsSb32.WriteString(it.key + " " + it.label + "\n")
	}
	labels += labelsSb32.String()
	for _, want := range []string{"i Copy the session id", "p Copy the project path", "F Forget this session…"} {
		if !strings.Contains(labels, want) {
			t.Errorf("palette lacks %q:\n%s", want, labels)
		}
	}
	// Esc on the question forgets nothing.
	key('F')
	if a.modal != modalForget {
		t.Fatal("F asks first")
	}
	sc := screen(a.frame(80, 24))
	wantOnScreen(t, sc, "Forget this session?", "own file stays", "Keep it", "▀")
	// The help names the three only under ^k; the keys still work.
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEsc})
	a.openModal(modalHelp)
	help := screen(a.frame(120, 36))
	wantOnScreen(t, help, "copy id, path, forget…")
	if strings.Contains(help, "copy the session id") || strings.Contains(help, "forget it") {
		t.Errorf("help lists the small actions as keys:\n%s", help)
	}
	a.modal = modalNone
	key('F')
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEsc})
	if a.modal != modalNone || len(a.rows) != 3 {
		t.Fatalf("esc cancels: modal %d rows %d", a.modal, len(a.rows))
	}
	key('F')
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEnter})
	drain(t, a)
	if a.view != viewList || len(a.rows) != 2 || !strings.HasPrefix(a.toast, "Forgotten.") {
		t.Fatalf("after forget: view %d rows %d toast %q", a.view, len(a.rows), a.toast)
	}
	for _, r := range a.rows {
		if r.s.ID == s.ID {
			t.Error("the forgotten session is still listed")
		}
	}
	// A forget that fails says why and leaves the list alone.
	a.forget = func(string) error { return runForget(dir, []string{"--session", "no-such-id"}) }
	a.askForget()
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEnter})
	drain(t, a)
	if !strings.HasPrefix(a.toast, "Not forgotten: nothing matched") || len(a.rows) != 2 {
		t.Errorf("failed forget: toast %q rows %d", a.toast, len(a.rows))
	}
}

// typeQuery puts q in the box and waits for the search and, when it comes
// back empty, for the look beyond it.
func typeQuery(t *testing.T, a *tuiApp, q string) {
	t.Helper()
	a.query = []rune(q)
	a.reload()
	drain(t, a)
	if len(a.rows) == 0 {
		deadline := time.Now().Add(2 * time.Minute)
		for a.beyond.listed != a.listed && time.Now().Before(deadline) {
			drain(t, a)
		}
	}
}

// An empty answer offers the way forward: the other projects when the query
// is only there, the spelling the history uses when a word is off.
func TestTUIEmptyOffersAWayForward(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)

	typeQuery(t, a, "in:checkout idempotency")
	s := screen(a.frame(120, 36))
	wantOnScreen(t, s, "Nothing for", "Not in checkout, but other projects have it.", "↵  1 session in other projects")
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEnter})
	if string(a.query) != "idempotency" {
		t.Fatalf("↵ drops the in: filter, query = %q", string(a.query))
	}
	drain(t, a)
	if len(a.rows) != 1 {
		t.Errorf("rows = %d", len(a.rows))
	}

	typeQuery(t, a, "claude: signtre")
	s = screen(a.frame(120, 36))
	wantOnScreen(t, s, "Did you mean “signature”? It finds 1 session.", "Search “signature”", "Drop a word")
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEnter})
	if string(a.query) != "claude: signature" {
		t.Fatalf("↵ takes the spelling and keeps the filter, query = %q", string(a.query))
	}

	typeQuery(t, a, "kafka rebalance")
	s = screen(a.frame(120, 36))
	wantOnScreen(t, s, "Drop a word")
	if strings.Contains(s, "Did you mean") || strings.Contains(s, "other projects") {
		t.Errorf("nothing to offer, offered:\n%s", s)
	}
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEnter})
	if string(a.query) != "kafka rebalance" || a.view != viewList {
		t.Errorf("↵ with no offer does nothing: %q view %d", string(a.query), a.view)
	}
}

func TestDidYouMean(t *testing.T) {
	dir, _ := tuiStore(t)
	for q, want := range map[string]string{
		"signtre rotaton": "signature rotation",
		"webhook":         "",
		"kafka":           "",
		"xyz":             "",
	} {
		if got := index.DidYouMean(dir, q); got != want {
			t.Errorf("DidYouMean(%q) = %q, want %q", q, got, want)
		}
	}
}

// Every view and modal draws at any size, down to a single cell.
func TestTUITinyTerminal(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	for _, m := range []int{modalNone, modalContinue, modalAgents, modalHelp, modalPalette} {
		a.modal = modalNone
		if m != modalNone {
			a.openModal(m)
		}
		for _, sz := range [][2]int{{1, 1}, {2, 2}, {5, 3}, {20, 5}, {40, 10}} {
			a.frame(sz[0], sz[1])
		}
	}
}

// A search that lands after the box was cleared does not replace the home
// list.
func TestTUILateSearchAfterClear(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	a.query = []rune("idempotency")
	a.startSearch()
	a.query = nil
	a.loadHome()
	// Overtaken before it ran, the search is not run at all.
	a.bg.Wait()
	select {
	case f := <-a.updates:
		f()
	default:
	}
	if len(a.rows) != 3 {
		t.Errorf("home has %d rows after a late search", len(a.rows))
	}
}

// A word typed at speed runs one search, not one per letter: on a large
// history each was a full pass and together they held the screen still.
func TestTUITypingRunsTheLastSearchOnly(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	for _, r := range "webhook" {
		a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: r})
	}
	a.bg.Wait()
	n := 0
	for len(a.updates) > 0 {
		(<-a.updates)()
		n++
	}
	if n != 1 || a.searching || len(a.rows) == 0 {
		t.Errorf("%d searches answered, searching=%v, %d rows", n, a.searching, len(a.rows))
	}
}

// The empty box takes ? as help, as the footer says.
func TestTUIQuestionMarkOpensHelp(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: '?'})
	if a.modal != modalHelp || len(a.query) != 0 {
		t.Errorf("modal=%d query=%q", a.modal, string(a.query))
	}
}

func TestTUIReaderHitAfterResize(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	a.query = []rune("webhook")
	for i, r := range a.rows {
		if r.s.ID == "c3333333-hook" {
			a.sel = i
		}
	}
	a.openReader()
	a.frame(120, 30)
	a.reader.hit, a.reader.layoutW = 7, 0
	s := screen(a.frame(160, 30))
	if strings.Contains(s, "hit 8") {
		t.Errorf("the hit counter ran past the hits:\n%s", s)
	}
}

// A session picked on the screen is handed on from the index as it is. A
// refresh there waited out a whole rebuild after an upgrade, under a closed
// screen, before the agent opened.
func TestTUIHandsOnWithoutARefresh(t *testing.T) {
	dir, root := tuiStore(t)
	write := func(id, at, text string) {
		user, err := json.Marshal(map[string]any{"type": "user", "sessionId": id, "cwd": "/work/payments", "timestamp": at,
			"message": map[string]any{"role": "user", "content": text}})
		if err != nil {
			t.Fatal(err)
		}
		writeClaudeFixture(t, filepath.Join(root, "payments", id+".jsonl"), id, []string{string(user)})
	}
	// A newer session whose id the picked one's is a prefix of.
	write("c3333333-hookx", "2026-03-05T10:00:00Z", "a newer session under a longer id")
	if err := index.Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	write("d4444444-new", "2026-03-06T10:00:00Z", "a session that arrived after the screen opened")
	picked, ok, err := index.FindByIdentity(dir, "claude", "c3333333-hook")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	pickedOnScreen = &picked
	t.Cleanup(func() { pickedOnScreen = nil })
	if s, ok, err := findByPrefix(dir, "c3333333-hook"); err != nil || !ok || s.ID != "c3333333-hook" {
		t.Fatalf("findByPrefix = %v %v %v", s.ID, ok, err)
	}
	if s, err := handoffSource(dir, "c3333333-hook"); err != nil || s.ID != "c3333333-hook" {
		t.Fatalf("handoffSource = %v %v", s.ID, err)
	}
	if _, ok, _ := index.FindByPrefix(dir, "d4444444"); ok {
		t.Error("handing on a picked session refreshed the index first")
	}
	pickedOnScreen = nil
	if _, ok, _ := findByPrefix(dir, "d4444444"); !ok {
		t.Error("from a shell the lookup no longer brings the index up to date")
	}
}

func TestTUIPasteIntoModal(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	a.openContinue()
	a.handle(tui.Event{Kind: tui.EvPaste, Text: "co\ndex"})
	if string(a.m.filter) != "co dex" {
		t.Errorf("filter = %q", string(a.m.filter))
	}
	a.modal = modalHelp
	a.handle(tui.Event{Kind: tui.EvPaste, Text: "x"})
	if a.modal != modalHelp {
		t.Error("a paste closed help")
	}
}

// A refresh of the same search keeps the reader's pick, and a first build
// finishing under an early start keeps the search on screen.
func TestTUIRefreshKeepsWhatIsOnScreen(t *testing.T) {
	dir, root := tuiStore(t)
	// Asked twice, so the search names the newest and selects it.
	user, err := json.Marshal(map[string]any{"type": "user", "sessionId": "e5555555-again", "cwd": "/work/payments", "timestamp": "2026-03-04T10:00:00Z",
		"message": map[string]any{"role": "user", "content": "the retry double charges the card again"}})
	if err != nil {
		t.Fatal(err)
	}
	writeClaudeFixture(t, filepath.Join(root, "payments", "e5555555-again.jsonl"), "e5555555-again", []string{string(user)})
	if err := index.Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	a := newTestTUI(t, dir)
	a.query = []rune("retry card")
	a.startSearch()
	drain(t, a)
	if len(a.rows) < 2 {
		t.Fatalf("%d rows", len(a.rows))
	}
	a.sel = (a.sel + 1) % len(a.rows)
	picked := sessionKey(a.rows[a.sel].s)
	a.reload()
	drain(t, a)
	if got := sessionKey(a.rows[a.sel].s); got != picked {
		t.Errorf("a refresh moved the pick from %q to %q", picked, got)
	}

	a.welcome.early = true
	a.firstBuild()
	drain(t, a)
	if string(a.query) != "retry card" || a.listed != num(a.scope)+"\x00retry card" {
		t.Errorf("the finished build replaced the search: %q %q", string(a.query), a.listed)
	}
}

// A session that grew after its first read is read again.
func TestTUIGrownSessionIsReadAgain(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	s := a.rows[0].s
	delete(a.loading, sessionKey(s))
	a.want(s)
	if a.loading[sessionKey(s)] {
		t.Fatal("an unchanged session was read twice")
	}
	s.Updated = s.Updated.Add(time.Minute)
	a.want(s)
	if !a.loading[sessionKey(s)] {
		t.Error("a session that grew keeps its first read")
	}
}

// The count of every session is kept across tab switches and read again once
// the index has been written.
func TestTUIHeaderCountFollowsTheIndex(t *testing.T) {
	dir, root := tuiStore(t)
	a := newTestTUI(t, dir)
	before := len(a.allMeta)
	a.setScope(scopeHere)
	a.setScope(scopeAll)
	if len(a.allMeta) != before {
		t.Fatalf("a tab switch changed the count: %d, was %d", len(a.allMeta), before)
	}
	user, err := json.Marshal(map[string]any{"type": "user", "sessionId": "d4444444-new", "cwd": "/work/billing", "timestamp": "2026-03-04T10:00:00Z",
		"message": map[string]any{"role": "user", "content": "invoices round to the wrong cent"}})
	if err != nil {
		t.Fatal(err)
	}
	writeClaudeFixture(t, filepath.Join(root, "billing", "d4444444-new.jsonl"), "d4444444-new", []string{string(user)})
	if err := index.Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	a.loadHome()
	if len(a.allMeta) != before+1 {
		t.Fatalf("after the index grew the count is %d, want %d", len(a.allMeta), before+1)
	}
}

// Picking an agent in the picker widens loadHome to the full index so an agent
// whose newest session sits behind the initial 60-row page still lists (#5761).
func TestTUIAgentPickerReadsBeyondFirstPage(t *testing.T) {
	dir, root := tuiStore(t)
	codexRoot := t.TempDir()
	t.Setenv("DEJA_CODEX_ROOT", codexRoot)
	codexFile := filepath.Join(codexRoot, "sessions", "2026", "01", "04", "rollout-2026-01-04T10-00-00-codex-old.jsonl")
	if err := os.MkdirAll(filepath.Dir(codexFile), 0o755); err != nil {
		t.Fatal(err)
	}
	codex := strings.Join([]string{
		`{"type":"session_meta","timestamp":"2026-01-04T10:00:00Z","payload":{"session_id":"codex-old","cwd":"/work/payments"}}`,
		`{"type":"message","timestamp":"2026-01-04T10:01:00Z","payload":{"role":"user","content":"old codex session behind the 60-row page"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(codexFile, []byte(codex), 0o644); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	for i := range 65 {
		id := fmt.Sprintf("page-%02d", i)
		at := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339)
		user, err := json.Marshal(map[string]any{"type": "user", "sessionId": id, "cwd": "/work/payments", "timestamp": at,
			"message": map[string]any{"role": "user", "content": "newer claude session on the first page"}})
		if err != nil {
			t.Fatal(err)
		}
		writeClaudeFixture(t, filepath.Join(root, "payments", id+".jsonl"), id, []string{string(user)})
	}
	if err := index.Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	a := newTestTUI(t, dir)
	a.filter = map[string]bool{"codex": true}
	a.loadHome()
	if len(a.rows) != 1 || a.rows[0].s.Harness != "codex" {
		t.Fatalf("picking an agent older than the 60-row home page gave %d rows", len(a.rows))
	}
}

func TestTUIBoxFiltersParse(t *testing.T) {
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	f := parseBox([]rune("codex: idempotency in:pay Claude: yesterday foo: key"), now)
	if f.text != "idempotency foo: key" {
		t.Errorf("text = %q", f.text)
	}
	if strings.Join(f.harnesses, ",") != "codex,claude" || strings.Join(f.projects, ",") != "pay" {
		t.Errorf("harnesses %v projects %v", f.harnesses, f.projects)
	}
	if !f.since.Equal(time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC)) || !f.until.Equal(time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("yesterday = %v .. %v", f.since, f.until)
	}
	if len(f.chips) != 4 || f.chips[0] != [2]int{0, 6} {
		t.Errorf("chips = %v", f.chips)
	}
	if f.keeps(model.Session{Harness: "cursor", Updated: now.Add(-20 * time.Hour)}) ||
		!f.keeps(model.Session{Harness: "codex", Updated: now.Add(-20 * time.Hour)}) ||
		f.keeps(model.Session{Harness: "codex", Updated: now.Add(-time.Hour)}) {
		t.Error("keeps")
	}
	f = parseBox([]rune("retry last week"), now)
	if f.text != "retry" || f.when != "last week" || !f.since.Equal(now.AddDate(0, 0, -7)) || f.chips[0] != [2]int{6, 15} {
		t.Errorf("last week: %+v", f)
	}
	if o := parseBox([]rune("today x"), now).options([]string{"here"}); o.Since <= 0 || o.Projects[0] != "here" || o.Query != "x" {
		t.Errorf("options = %+v", o)
	}
	if f := parseBox([]rune("last nope: in:"), now); f.active() || f.text != "last nope: in:" {
		t.Errorf("nothing recognised: %+v", f)
	}
}

// A filter typed into the box narrows the answer and shows as a chip; the
// words around it are the query.
func TestTUIBoxFiltersApply(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	a.query = []rune("claude: in:checkout")
	a.reload()
	if len(a.rows) != 1 || a.rows[0].s.ID != "a1111111-pool" {
		t.Fatalf("filters alone list the matching sessions: %+v", a.rows)
	}
	a.query = []rune("in:payments key")
	a.reload()
	drain(t, a)
	for _, r := range a.rows {
		if !strings.Contains(r.s.Project, "payments") {
			t.Errorf("in:payments listed %s", r.s.Project)
		}
	}
	if len(a.rows) == 0 {
		t.Fatal("in:payments key found nothing")
	}
	s := screen(a.frame(120, 36))
	wantOnScreen(t, s, "in:payments key", "in payments")
	a.query = []rune("cursor: key")
	a.reload()
	drain(t, a)
	if len(a.rows) != 0 {
		t.Errorf("no cursor sessions, got %d", len(a.rows))
	}
	a.openModal(modalHelp)
	wantOnScreen(t, screen(a.frame(120, 36)), "FILTERS", "codex:", "in:api")
}

func TestTUIDataHelpers(t *testing.T) {
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	for at, want := range map[time.Time]string{
		{}:                         "-",
		now.Add(time.Hour):         "Mar 4",
		now.Add(-30 * time.Second): "just now",
		now.Add(-5 * time.Minute):  "5m ago",
		now.Add(-3 * time.Hour):    "3h ago",
		now.Add(-13 * time.Hour):   "yesterday",
		now.Add(-40 * time.Hour):   "2d ago",
		now.Add(-72 * time.Hour):   "3d ago",
		now.AddDate(0, 0, -7):      "Feb 25",
		now.AddDate(0, -1, 0):      "Feb 4",
		now.AddDate(-1, 0, 0):      "Mar 4 2025",
	} {
		if got := tuiAgo(at, now); got != want {
			t.Errorf("tuiAgo(%v) = %q, want %q", at, got, want)
		}
	}
	if !sameLine("Fixed  it by X", "fixed it by x and more") || sameLine("", "x") || sameLine("abc", "abd") {
		t.Error("sameLine")
	}
	h := search.Hit{Snippets: []string{" a  b ", "a b", "", "c"}}
	if got := tuiSnippets(h); len(got) != 2 || got[0] != "a b" {
		t.Errorf("tuiSnippets = %q", got)
	}
	if got := queryTerms(`"a" bc de`); len(got) != 2 {
		t.Errorf("queryTerms = %q", got)
	}
	if got := wrapLines("one two three four five six", 9, 2); len(got) != 2 || !strings.HasSuffix(got[1], "…") {
		t.Errorf("wrapLines = %q", got)
	}
	if wrapLines("", 10, 2) != nil || wrapLines("x", 2, 2) != nil {
		t.Error("wrapLines on nothing")
	}
	if got := tuiProject(model.Session{Project: "a/b/c/d"}); got != "c/d" {
		t.Errorf("tuiProject = %q", got)
	}
	if num(-12) != "-12" || num(0) != "0" || kb(10) != "10 B" || kb(3200) != "3.1 KB" {
		t.Error("num / kb")
	}
	if formatMS(0.84) != "0.8 ms" || formatMS(12.5) != "12 ms" {
		t.Error("formatMS")
	}
	if agentName("omp") != "omp" || agentName("nope") != "nope" {
		t.Error("agentName")
	}
	if got := tuiConclusions(model.Session{}, 2); got != nil {
		t.Errorf("tuiConclusions of nothing = %q", got)
	}
}

func TestContinueTargetsAndGrid(t *testing.T) {
	look := func(name string) (string, error) {
		if name == "claude" || name == "codex" {
			return "/bin/" + name, nil
		}
		return "", os.ErrNotExist
	}
	ts := continueTargets(look, map[string]int{"codex": 9, "claude": 2})
	if ts[0].id != "codex" || ts[1].id != "claude" || ts[2].installed {
		t.Errorf("installed and used first: %+v", ts[:3])
	}
	last := ts[len(ts)-1]
	if !last.paste || len(ts) != len(handoffTargets())+len(handoffPasteOnly) {
		t.Errorf("paste-only last, all listed: %+v (%d)", last, len(ts))
	}
	// No labels: a block starts its own row after one blank line.
	pos, line := continueGrid(ts[:5], 2)
	if pos[2] != [2]int{1, 0} || pos[4] != [2]int{2, 0} || line[1] != 0 || line[2] != 2 || line[4] != 3 {
		t.Errorf("grid = %v lines %v", pos, line)
	}
	// The folded line takes the next cell, with no gap above it.
	pos, line = continueGrid(append(ts[:1:1], continueTarget{more: 3}), 2)
	if line[1] != 0 || pos[1] != [2]int{0, 1} {
		t.Errorf("fold at line %v", line)
	}
	pos, _ = continueGrid(ts[:5], 2)
	if gridMove(pos, 4, -1) != 2 || gridMove(pos, 0, 1) != 2 || gridMove(pos, 9, 1) != 9 || gridMove(pos, 0, -1) != 0 {
		t.Error("gridMove")
	}
	if gridCols(80) != 2 || gridCols(50) != 2 || gridCols(20) != 1 {
		t.Error("gridCols")
	}
	if ts := continueTargets(nil, nil); len(ts) == 0 {
		t.Error("the default lookup lists targets too")
	}
}

// Every harness the registry names has the same display name here.
func TestAgentNamesMatchRegistry(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "registry", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg capRegistry
	if err := json.Unmarshal(b, &reg); err != nil {
		t.Fatal(err)
	}
	for _, h := range reg.Harnesses {
		if agentNames[h.ID] != h.DisplayName {
			t.Errorf("agentNames[%q] = %q, registry says %q", h.ID, agentNames[h.ID], h.DisplayName)
		}
	}
	if len(agentNames) != len(reg.Harnesses) {
		t.Errorf("agentNames has %d, registry %d", len(agentNames), len(reg.Harnesses))
	}
}

func TestTUIWantedOnlyAtATerminal(t *testing.T) {
	if tuiWanted() {
		t.Error("a test's stdout is not a terminal")
	}
	t.Setenv("DEJA_TUI", "0")
	if tuiWanted() {
		t.Error("DEJA_TUI=0 keeps to text")
	}
}

// Over ssh the local copy tool would fill the server's clipboard, so the copy
// goes through the terminal instead.
func TestTUICopyOverSSH(t *testing.T) {
	t.Setenv("SSH_TTY", "/dev/pts/1")
	if clipboardCommand() != nil {
		t.Error("no local tool over ssh")
	}
	var sent string
	if err := tuiCopy(func(s string) { sent = s }, "hi"); err != nil {
		t.Fatal(err)
	}
	if sent != "\x1b]52;c;aGk=\x07" {
		t.Errorf("OSC 52 = %q", sent)
	}
	t.Setenv("SSH_TTY", "")
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("PATH", t.TempDir())
	if c := clipboardCommand(); c != nil {
		t.Errorf("nothing on PATH, got %v", c)
	}
}

func TestTUIPalette(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyCtrl, Rune: 'k'})
	if a.modal != modalPalette {
		t.Fatal("ctrl-k opens the palette")
	}
	wantOnScreen(t, screen(a.frame(120, 36)), "type a command", "Resume in Claude Code", "Show only Claude Code")
	key := func(k tui.Key, r rune) { a.handle(tui.Event{Kind: tui.EvKey, Key: k, Rune: r}) }
	key(tui.KeyDown, 0)
	key(tui.KeyUp, 0)
	key(tui.KeyTab, 0)
	for _, r := range "only claudex" {
		key(tui.KeyRune, r)
	}
	wantOnScreen(t, screen(a.frame(120, 36)), "No command by that name.")
	key(tui.KeyEnter, 0)
	key(tui.KeyBackspace, 0)
	if items := a.shownPalette(); len(items) != 1 {
		t.Fatalf("filtered palette = %+v", items)
	}
	key(tui.KeyEnter, 0)
	if a.modal != modalNone || !a.filter["claude"] {
		t.Errorf("the command ran: modal %d filter %v", a.modal, a.filter)
	}
	a.openPalette()
	key(tui.KeyEsc, 0)
	if a.modal != modalNone {
		t.Error("esc closes the palette")
	}
	// The palette in the reader offers what the reader can do, and a
	// session its agent deleted offers to be put back.
	a.view = viewReader
	a.reader.s = a.rows[0].s
	a.details[sessionKey(a.rows[0].s)].gone = true
	labels := ""
	var labelsSb53 strings.Builder
	for _, it := range a.paletteItems() {
		labelsSb53.WriteString(it.label + "\n")
	}
	labels += labelsSb53.String()
	if strings.Contains(labels, "Read this session") || !strings.Contains(labels, "Put it back") {
		t.Errorf("reader palette:\n%s", labels)
	}
	for _, it := range a.paletteItems() {
		if strings.HasPrefix(it.label, "Show every agent") || strings.HasPrefix(it.label, "Show all") {
			it.do()
		}
	}
	if len(a.filter) != 0 || a.scope != scopeAll {
		t.Errorf("filter %v scope %d", a.filter, a.scope)
	}
}

func TestTUIHistory(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	a.remember() // nothing typed: nothing kept
	for _, q := range []string{"pool", "retry", "pool"} {
		a.query = []rune(q)
		a.remember()
	}
	if got := strings.Join(a.history, ","); got != "retry,pool" {
		t.Fatalf("history = %q", got)
	}
	b := newTestTUI(t, dir)
	if strings.Join(b.history, ",") != "retry,pool" {
		t.Fatalf("history read back = %q", b.history)
	}
	up := func() { b.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyUp}) }
	up()
	if string(b.query) != "pool" {
		t.Errorf("first ↑ = %q", string(b.query))
	}
	up()
	up()
	if string(b.query) != "retry" {
		t.Errorf("↑ stops at the oldest, got %q", string(b.query))
	}
	b.recall(1)
	b.recall(1)
	if len(b.query) != 0 || b.histAt != -1 {
		t.Errorf("past the newest clears: %q %d", string(b.query), b.histAt)
	}
	for b.searching {
		drain(t, b)
	}
	b.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: 'x'})
	if b.histAt != -1 {
		t.Error("typing leaves history")
	}
	long := make([]string, tuiHistoryMax+5)
	for i := range long {
		long[i] = "q" + num(i)
	}
	b.history, b.query = long, []rune("last")
	b.rows = a.rows
	b.remember()
	if len(b.history) != tuiHistoryMax || b.history[len(b.history)-1] != "last" {
		t.Errorf("history is capped, got %d", len(b.history))
	}
	empty := newTestTUI(t, t.TempDir()+"/none")
	empty.recall(-1)
	if loadTUIHistory(t.TempDir()+"/none") != nil {
		t.Error("no file, no history")
	}
}

func TestTUIDejaVuAndDigits(t *testing.T) {
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	rows := []tuiRow{
		{s: model.Session{ID: "a", Harness: "claude", Title: "Retry double charges on checkout", Updated: now.Add(-48 * time.Hour)}, snips: []string{"x"}},
		{s: model.Session{ID: "b", Harness: "codex", Title: "why does retry double charge", Updated: now.Add(-2 * time.Hour)}, snips: []string{"x"}},
		{s: model.Session{ID: "c", Harness: "codex", Title: "something else", Updated: now}, snips: []string{"x"}},
	}
	if n, s := dejaVu("retry double", rows); n != 2 || s.ID != "b" {
		t.Errorf("dejaVu = %d %q", n, s.ID)
	}
	if n, _ := dejaVu("retry", rows); n != 0 {
		t.Error("one word is not a question asked before")
	}
	if n, _ := dejaVu("retry checkout", rows); n != 0 {
		t.Error("one session is not déjà vu")
	}
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	a.rows, a.query = rows, []rune("retry double")
	a.details = map[string]*tuiDetail{}
	s := screen(a.frame(120, 36))
	wantOnScreen(t, s, "Déjà vu.", "Asked in 2 sessions before", "reading…", "hand it to an agent")
	a.details[sessionKey(rows[1].s)] = &tuiDetail{conclusions: []string{"Derive the key from the charge id"}}
	wantOnScreen(t, screen(a.frame(120, 36)), "Derive the key from the charge id")
	a.listFocus = true
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: '3'})
	if a.sel != 2 {
		t.Errorf("3 picks the third card, sel %d", a.sel)
	}
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: '9'})
	if a.sel != 2 {
		t.Errorf("past the end is the last, sel %d", a.sel)
	}
}

func TestTUIReaderRoles(t *testing.T) {
	s := model.Session{ID: "x", Harness: "codex", Messages: []model.Message{
		{Role: "user", Text: "the build fails"},
		{Role: "command", Text: "go test ./..."},
		{Role: "tool-output", Text: strings.Repeat("FAIL line one\nok two\n", 10)},
		{Role: "edit", Text: "- old\n+ new"},
		{Role: "wrote", Text: "deadbeef"},
		{Role: "files", Text: "a.go"},
		{Role: "summary", Text: "compacted"},
		{Role: "odd", Text: "?"},
		{Role: "assistant", Text: "Fixed it."},
	}}
	r := readerState{s: s, d: &tuiDetail{full: s}}
	r.layout(100, func(model.Message) string { return "" })
	var heads []string
	folded := false
	for _, l := range r.lines {
		if l.role != 0 {
			heads = append(heads, l.text)
		}
		folded = folded || strings.Contains(l.text, "more line")
	}
	if got := strings.Join(heads, ","); got != "you,ran,output,edited,files,summary,odd,Codex CLI" {
		t.Errorf("heads = %q", got)
	}
	if !folded {
		t.Error("long output folds to its first lines")
	}
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	a.view, a.reader = viewReader, r
	a.reader.layoutW = 0
	wantOnScreen(t, screen(a.frame(100, 40)), "go test ./...", "FAIL line")
}

func TestTUITheme(t *testing.T) {
	defer setTheme("dark")
	for env, want := range map[[2]string]string{
		{"light", ""}: "light", {"dark", "0;15"}: "dark", {"", "0;15"}: "light", {"", "15;0"}: "dark", {"", ""}: "dark",
		{"", "0;default;7"}: "light", {"Dracula", "0;15"}: "dracula", {"solarized", "0;15"}: "light",
	} {
		t.Setenv("DEJA_THEME", env[0])
		t.Setenv("COLORFGBG", env[1])
		if got := terminalTheme(); got != want {
			t.Errorf("terminalTheme(%v) = %q, want %q", env, got, want)
		}
	}
	setTheme("dark")
	dark := cBase
	setTheme("light")
	if cBase == dark {
		t.Error("the light set replaces the colours")
	}
	setTheme("dracula")
	if cBase != tui.Hex("#282a36") || cAcc != tui.Hex("#bd93f9") {
		t.Error("dracula paints Dracula's background and purple")
	}
	setTheme("solarized")
	if cBase != dark {
		t.Error("an unknown theme falls back to dark")
	}
}

// The agents continued into before lead the picker, the newest picked, and
// the ones not on this machine fold into one line a filter still searches.
func TestTUIContinueRecentAndFold(t *testing.T) {
	dir, _ := tuiStore(t)
	bin := t.TempDir()
	// PATH lookup on Windows only finds a name with an executable extension.
	name := "claude"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	if err := os.WriteFile(tuiContinuedPath(dir), []byte("codex\nnot-an-agent\ngemini\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := newTestTUI(t, dir)
	a.openContinue()
	ts := a.shownTargets()
	if len(ts) != 4 || ts[0].id != "codex" || ts[1].id != "gemini" || ts[2].id != "claude" || ts[3].more != 46 {
		t.Fatalf("shown = %+v", ts)
	}
	s := screen(a.frame(80, 24))
	wantOnScreen(t, s, "Codex CLI  last used", "+46 not installed here", "Copy for Codex CLI")
	if strings.Contains(s, "RECENT") || strings.Contains(s, "ON THIS MACHINE") {
		t.Error("the picker draws no section labels")
	}
	for _, r := range "kiro" {
		a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: r})
	}
	if ts := a.shownTargets(); len(ts) != 1 || ts[0].id != "kiro" {
		t.Fatalf("a filter reaches the folded agents: %+v", ts)
	}
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEnter})
	if got := loadTUIContinued(dir); strings.Join(got, ",") != "kiro,codex,gemini" {
		t.Errorf("continued = %v", got)
	}
	a.openContinue()
	if ts := a.shownTargets(); ts[0].id != "kiro" || a.m.sel != 0 {
		t.Errorf("the newest pick leads and is selected: %+v", ts[0])
	}
}

// Without truecolor every colour rounds to the xterm-256 cube; two surfaces
// that land on one entry make the selected card and the panels vanish.
func TestThemeSurfacesStayApartIn256Colours(t *testing.T) {
	for name, p := range palettes {
		base := tui.To256(tui.Hex(p[0]))
		for _, hex := range p[2:4] { // the card surface and the panels
			if n := tui.To256(tui.Hex(hex)); n == base {
				t.Errorf("%s: %s lands on the background's xterm %d", name, hex, n)
			}
		}
	}
}

// Key chips and buttons fill with surf2 on surf panels; in light theme both
// rounded to xterm 254 and the chips lost their fill (#5107). Truecolor keeps
// the palette's own hex.
func TestThemeChipsStayApartFromTheirPanelIn256Colours(t *testing.T) {
	t.Cleanup(func() { setTheme("dark") })
	for name, p := range palettes {
		setTheme(name)
		if s, s2 := tui.To256(cSurf), tui.To256(cSurf2); s == s2 {
			t.Errorf("%s: surf and surf2 both land on xterm %d", name, s)
		}
		c := tui.NewCanvas(1, 1)
		c.Fill(0, 0, 1, 1, cSurf2)
		if l, want := c.Lines(tui.ModeTrue)[0], "48;2;"+rgbArgs(p[3]); !strings.Contains(l, want) {
			t.Errorf("%s: truecolor surf2 = %q, want %s", name, l, want)
		}
	}
}

// The header bar and code blocks fill with mantle on a base page; in light
// theme both rounded to xterm 255 and the bar melted into the page (#5355).
// Mantle has no grey between base and surf to take, so it must also keep off
// the entries the chips and panels use.
func TestThemeHeaderStaysApartFromThePageIn256Colours(t *testing.T) {
	t.Cleanup(func() { setTheme("dark") })
	for name := range palettes {
		setTheme(name)
		m := tui.To256(cMantle)
		for what, c := range map[string]tui.Color{"base": cBase, "surf": cSurf, "surf2": cSurf2} {
			if tui.To256(c) == m {
				t.Errorf("%s: mantle and %s both land on xterm %d", name, what, m)
			}
		}
	}
	dir, _ := tuiStore(t)
	t.Setenv("DEJA_THEME", "light")
	a := newTestTUI(t, dir)
	setTheme("light")
	lines := a.frame(100, 30).Lines(tui.Mode256)
	bg := regexp.MustCompile(`48;5;(\d+)`)
	header := bg.FindStringSubmatch(lines[0])
	page := "48;5;" + strconv.Itoa(tui.To256(cBase))
	if len(header) == 0 || header[0] == page {
		t.Errorf("light header bar fills with %v, the page with %s:\n%q", header, page, lines[0])
	}
	if !strings.Contains(strings.Join(lines[1:], "\n"), page) {
		t.Errorf("control: no cell below the header fills with the page's %s", page)
	}
}

func rgbArgs(hex string) string {
	c := tui.Hex(hex)
	r, g, b := uint64(c)>>16&0xff, uint64(c)>>8&0xff, uint64(c)&0xff
	return strconv.FormatUint(r, 10) + ";" + strconv.FormatUint(g, 10) + ";" + strconv.FormatUint(b, 10)
}

// The search list read and ranked every session a word matched before it
// drew the first card: 16 s for a common word on a 40k-session store
// (#5487). It reads a page now, heads the list with every match all the same,
// and reads the next page as the selection nears the end, keeping the cards
// already listed where they were.
func TestTUIListReadsAPageAtATime(t *testing.T) {
	work := filepath.Join(t.TempDir(), "work", "shopfront")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	n := search.PoolSize(tuiPage) + 600
	dir := commonWordStore(t, n, func(int) string { return work })
	read := 0
	prev := tuiIndexSearch
	tuiIndexSearch = func(dir string, o search.Options, progress io.Writer) (index.SearchResult, error) {
		r, err := prev(dir, o, progress)
		read += len(r.Sessions)
		return r, err
	}
	t.Cleanup(func() { tuiIndexSearch = prev })

	a := newTestTUI(t, dir)
	typeQuery(t, a, "deploy")
	if len(a.rows) != tuiPage {
		t.Fatalf("listed %d cards, want a page of %d", len(a.rows), tuiPage)
	}
	if read >= n {
		t.Errorf("read %d sessions to list %d of %d", read, tuiPage, n)
	}
	if label, _ := a.sectionLabel(); !strings.HasPrefix(label, grouped(n)+" sessions") {
		t.Errorf("the list is headed %q, want every one of the %d matches", label, n)
	}
	first := make([]string, len(a.rows))
	for i, r := range a.rows {
		first[i] = sessionKey(r.s)
	}

	// Short of the end, nothing more is read.
	read = 0
	a.move(tuiPage - tuiMoreAhead - 2)
	if a.loadingMore || read != 0 {
		t.Fatalf("asked for more %d cards from the end", len(a.rows)-a.sel)
	}
	a.move(len(a.rows))
	drain(t, a)
	if len(a.rows) != 2*tuiPage {
		t.Fatalf("after scrolling to the end the list holds %d cards, want %d", len(a.rows), 2*tuiPage)
	}
	for i, k := range first {
		if sessionKey(a.rows[i].s) != k {
			t.Fatalf("card %d moved when the next page arrived", i+1)
		}
	}
	if a.sel != tuiPage-1 {
		t.Errorf("selection is on card %d, want it to stay on %d", a.sel+1, tuiPage)
	}
}

// A quoted phrase over two common words matches on the postings far more
// sessions than say it, and the pool held few of those that do: one card
// under "1,206 sessions" where five said the phrase. A page that comes back
// short while sessions were left unread is read whole.
func TestTUIShortPageIsReadWhole(t *testing.T) {
	work := filepath.Join(t.TempDir(), "work", "shopfront")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	n := search.PoolSize(tuiPage) + 300
	dir := storeOf(t, n, func(int) string { return work }, func(i int) string {
		if i < 5 {
			return fmt.Sprintf("alpha beta settled it, run %d", i)
		}
		return fmt.Sprintf("alpha went first and beta after, run %d", i)
	})
	hits, total, err := tuiSearchPage(dir, search.Options{Query: `"alpha beta"`, Limit: tuiPage})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 5 || total != 5 {
		t.Errorf("listed %d of %d, want the 5 sessions that say it", len(hits), total)
	}
}

// At 80 columns the hints ran out before the way out; it keeps the right edge
// now, and the hints between give way first.
func TestTUIFooterKeepsTheWayOut(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	lines := strings.Split(screen(a.frame(80, 24)), "\n")
	if foot := lines[23]; !strings.HasSuffix(strings.TrimRight(foot, " "), "esc  quit") {
		t.Errorf("list footer = %q", foot)
	}
	a.openReader()
	lines = strings.Split(screen(a.frame(80, 24)), "\n")
	if foot := lines[23]; !strings.HasSuffix(strings.TrimRight(foot, " "), "esc  back") || !strings.Contains(foot, "scroll") {
		t.Errorf("reader footer = %q", foot)
	}
}

// The help columns are sized to what they hold. Thirds of the box cut most
// labels short at 80 columns and one at any width.
func TestTUIHelpLabelsWhole(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	a.modal = modalHelp
	for _, w := range []int{79, 80, 100, 140} {
		s := screen(a.frame(w, 24))
		wantOnScreen(t, s, "tab  this project / all / deleted", "n N  next / previous match",
			"R  put a deleted one back", "in:api one project", "esc  back to the list", "scroll anywhere.")
	}
	// Wide enough, the three sections sit side by side.
	s := screen(a.frame(140, 36))
	for ln := range strings.SplitSeq(s, "\n") {
		if strings.Contains(ln, "FIND") && !strings.Contains(ln, "ACT") {
			t.Errorf("sections not side by side: %q", ln)
		}
	}
}

// A deleted session among live ones says so beside its agent; its headline
// starts on the column every other headline does.
func TestTUIDeletedCardKeepsTheHeadlineColumn(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	a.keptIDs = map[string]bool{sessionKey(a.rows[0].s): true}
	s := screen(a.frame(120, 36))
	wantOnScreen(t, s, "▌ webhook signature fails", "· deleted")
	a.setScope(scopeKept)
	a.kept, a.keptLoaded = []model.Session{a.allMeta[0]}, true
	a.loadHome()
	if s := screen(a.frame(120, 36)); strings.Contains(s, "· deleted") {
		t.Errorf("the Deleted tab says deleted on every card:\n%s", s)
	}
}

// The agent strip over this project's list counts this project, not the
// machine.
func TestTUIStripCountsTheScope(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	wantOnScreen(t, screen(a.frame(120, 36)), "Claude Code 3")
	a.projects = []string{"payments"}
	a.setScope(scopeHere)
	s := screen(a.frame(120, 36))
	wantOnScreen(t, s, "Claude Code 2")
}

// Prose in the reader wraps at a reading measure, not at the terminal's edge.
func TestTUIReaderMeasure(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	d := a.details[sessionKey(a.rows[0].s)]
	d.full.Messages[len(d.full.Messages)-1].Text = strings.Repeat("rotate the signing key ", 30)
	a.openReader()
	widest := 0
	for _, ln := range strings.Split(screen(a.frame(200, 30)), "\n")[1:] {
		if strings.Contains(ln, "rotate") {
			widest = max(widest, termwidth.Columns(strings.TrimRight(ln, " ")))
		}
	}
	if widest == 0 || widest > readerMeasure+4 {
		t.Errorf("widest prose line = %d columns", widest)
	}
}

// The query's words are lit in a card's headline and in ASKED, not only in the
// matched line under them.
func TestTUIHeadlineLightsTheQuery(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	a.query = []rune("webhook")
	hits, err := tuiSearch(dir, search.Options{Query: "webhook", Limit: 80})
	if err != nil || len(hits) != 1 {
		t.Fatalf("hits = %d, %v", len(hits), err)
	}
	a.setRows(nil, hits, len(hits))
	a.p.mono = true // a match is drawn reversed, which the plain text shows
	c := a.frame(120, 36)
	lit := 0
	for y, ln := range c.Lines(tui.ModeMono) {
		if strings.Contains(c.Text(y), "webhook signature fails") && strings.Contains(ln, ";7m") {
			lit++
		}
	}
	// The headline, ASKED and MATCHED; only MATCHED was before.
	if lit != 3 {
		t.Errorf("rows lit = %d, want 3", lit)
	}
}

func TestTUIReaderFindAndTurns(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	s := model.Session{ID: "turns-1", Harness: "claude"}
	for i := range 12 {
		text := "step " + num(i+1) + " of the migration"
		if i == 4 || i == 9 {
			text += ", the flaky cutover"
		}
		s.Messages = append(s.Messages,
			model.Message{Role: "user", Text: text},
			model.Message{Role: "assistant", Text: strings.Repeat("checked it\n", 4)})
	}
	a.view, a.reader = viewReader, readerState{s: s, d: &tuiDetail{full: s}}
	key := func(r rune) { a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: r}) }
	wantOnScreen(t, screen(a.frame(100, 24)), "turn 1 of 12", "[ ]  turns", "/  find")

	key(']')
	key(']')
	sc := screen(a.frame(100, 24))
	wantOnScreen(t, sc, "turn 3 of 12", "step 3 of the migration")
	key('[')
	wantOnScreen(t, screen(a.frame(100, 24)), "turn 2 of 12")
	// Scrolled into the middle of a turn, [ goes back to its start.
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyDown})
	key('[')
	wantOnScreen(t, screen(a.frame(100, 24)), "turn 2 of 12")

	// / opens the line; letters type into it rather than acting.
	key('/')
	wantOnScreen(t, screen(a.frame(100, 24)), "find in this session", "esc  close")
	for _, r := range "cutover" {
		key(r)
	}
	if a.view != viewReader || string(a.reader.find) != "cutover" {
		t.Fatalf("find typed %q", string(a.reader.find))
	}
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEnter})
	sc = screen(a.frame(100, 24))
	wantOnScreen(t, sc, "hit 1 of 2", "turn 5 of 12", "step 5 of the migration, the flaky cutover")
	key('n')
	wantOnScreen(t, screen(a.frame(100, 24)), "hit 2 of 2", "turn 10 of 12")

	// A word that is not there keeps the matches it had.
	key('/')
	for _, r := range "kafka" {
		key(r)
	}
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEnter})
	if a.reader.finding || len(a.reader.hits) != 2 || !strings.Contains(a.toast, "kafka") {
		t.Errorf("a miss: finding %v, hits %d, toast %q", a.reader.finding, len(a.reader.hits), a.toast)
	}
	// esc closes the line and leaves the reader open.
	key('/')
	a.handle(tui.Event{Kind: tui.EvPaste, Text: "x y"})
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyBackspace})
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEsc})
	if a.reader.finding || a.view != viewReader {
		t.Error("esc closes the / line only")
	}
	// Scrolled to the end, the last turn is the one on screen.
	key('G')
	wantOnScreen(t, screen(a.frame(100, 24)), "turn 12 of 12")
	key('[')
	wantOnScreen(t, screen(a.frame(100, 24)), "turn 11 of 12")
	key(']')
	key(']')
	wantOnScreen(t, screen(a.frame(100, 24)), "turn 12 of 12")
	key('?')
	wantOnScreen(t, screen(a.frame(100, 30)), "find in the session", "next / previous turn")
}

// checkCanvas fails when a row does not measure exactly the canvas width.
func checkCanvas(t *testing.T, c *tui.Canvas, ctx string) {
	t.Helper()
	for y := range c.H {
		if got := termwidth.Columns(c.Text(y)); got != c.W {
			t.Fatalf("%s: row %d measures %d, canvas %d: %q", ctx, y, got, c.W, c.Text(y))
		}
	}
}

func pump(a *tuiApp) {
	for {
		select {
		case f := <-a.updates:
			f()
		default:
			return
		}
	}
}

func TestTUIRandomInputKeepsTheFrame(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	a.forget = func(string) error { return nil }
	rng := rand.New(rand.NewSource(1)) //nolint:gosec // seeded fuzz input, not crypto
	runes := []rune("abwebhookretry 支付重试🚀✅?/qjknNtR[]gGo1239:ïé")
	words := []string{"codex:", "claude:", "today", "yesterday", "week", "last month", "in:payments", "in:", "foo:", "webhook", "支付", "🚀🚀"}
	keys := []tui.Key{tui.KeyUp, tui.KeyDown, tui.KeyPgUp, tui.KeyPgDn, tui.KeyHome, tui.KeyEnd, tui.KeyTab, tui.KeyBackTab,
		tui.KeyEnter, tui.KeyEsc, tui.KeyBackspace, tui.KeyLeft, tui.KeyRight}
	sizes := [][2]int{{80, 24}, {60, 20}, {40, 15}, {300, 80}, {120, 36}, {96, 18}, {95, 17}, {20, 6}}
	log := make([]string, 0, 400)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic %v after:\n%s", r, strings.Join(log[max(0, len(log)-30):], "\n"))
		}
	}()
	for step := range 400 {
		var ev tui.Event
		switch rng.Intn(7) {
		case 0, 1:
			ev = tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: runes[rng.Intn(len(runes))]}
		case 2, 3:
			ev = tui.Event{Kind: tui.EvKey, Key: keys[rng.Intn(len(keys))]}
		case 4:
			ev = tui.Event{Kind: tui.EvPaste, Text: words[rng.Intn(len(words))] + " "}
		case 5:
			ev = tui.Event{Kind: tui.EvKey, Key: tui.KeyCtrl, Rune: []rune("uwknpdy")[rng.Intn(7)]}
		case 6:
			sz := sizes[rng.Intn(len(sizes))]
			ev = tui.Event{Kind: tui.EvMouse, Press: true, Button: []int{tui.MouseLeft, tui.MouseWheelUp, tui.MouseWheelDown}[rng.Intn(3)],
				X: rng.Intn(sz[0]), Y: rng.Intn(sz[1])}
		}
		log = append(log, fmt.Sprintf("%d view=%d modal=%d ev=%+v q=%q", step, a.view, a.modal, ev, string(a.query)))
		a.handle(ev)
		a.quit, a.after, a.leaving = false, nil, ""
		if rng.Intn(20) == 0 {
			time.Sleep(70 * time.Millisecond)
		}
		pump(a)
		if len(a.rows) > 0 && (a.sel < 0 || a.sel >= len(a.rows)) {
			t.Fatalf("sel %d of %d", a.sel, len(a.rows))
		}
		sz := sizes[rng.Intn(len(sizes))]
		checkCanvas(t, a.frame(sz[0], sz[1]), log[len(log)-1])
	}
}

// addClaudeSession adds one Claude session with the given messages to the store.
func addClaudeSession(t *testing.T, dir, root, id, at string, msgs ...[2]string) {
	t.Helper()
	const project = "payments"
	lines := make([]string, 0, len(msgs))
	for _, m := range msgs {
		var content any = m[1]
		if m[0] == "assistant" {
			content = []map[string]any{{"type": "text", "text": m[1]}}
		}
		b, err := json.Marshal(map[string]any{"type": m[0], "sessionId": id, "cwd": "/work/" + project, "timestamp": at,
			"message": map[string]any{"role": m[0], "content": content}})
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	writeClaudeFixture(t, filepath.Join(root, project, id+".jsonl"), id, lines)
	if err := index.Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
}

func openByID(t *testing.T, a *tuiApp, id string) {
	t.Helper()
	for i, r := range a.rows {
		if r.s.ID == id {
			a.sel = i
			a.openReader()
			a.reader.d = tuiLoadDetail(a.dir, r.s)
			return
		}
	}
	t.Fatalf("no row %s", id)
}

// A match far along an unwrapped line is drawn on screen.
func TestTUIReaderShowsAHitPastTheEdge(t *testing.T) {
	dir, root := tuiStore(t)
	long := strings.Repeat("padding ", 30) + "zanzibarneedle at the end"
	addClaudeSession(t, dir, root, "e5555555-long", "2026-03-04T09:00:00Z",
		[2]string{"user", "where does the log mention it"},
		[2]string{"assistant", "Here is the line:\n\n    " + long})
	a := newTestTUI(t, dir)
	a.query = []rune("zanzibarneedle")
	openByID(t, a, "e5555555-long")
	s := screen(a.frame(100, 30))
	if strings.Contains(s, "hit 1 of 1") && !strings.Contains(s, "zanzibarneedle") {
		t.Errorf("reader says hit 1 of 1 but the match is not on screen:\n%s", s)
	}
}

// A match past line 400 of one message is found with /.
func TestTUIFindReachesPastTheUnfoldCap(t *testing.T) {
	dir, root := tuiStore(t)
	var b strings.Builder
	for i := range 450 {
		fmt.Fprintf(&b, "line %d of the build log\n", i)
	}
	b.WriteString("FATAL quokkaerror in the linker\n")
	addClaudeSession(t, dir, root, "e5555555-log", "2026-03-04T09:00:00Z",
		[2]string{"user", "why does the build fail"},
		[2]string{"assistant", b.String()})
	a := newTestTUI(t, dir)
	openByID(t, a, "e5555555-log")
	a.frame(100, 30)
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: '/'})
	for _, r := range "quokkaerror" {
		a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: r})
	}
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEnter})
	if strings.Contains(a.toast, "No “quokkaerror” in this session") {
		t.Errorf("find says %q, but the session holds the word", a.toast)
	}
}

// Forgetting a session in the Deleted tab takes its card and count away.
func TestTUIForgetInDeletedTabDropsTheCard(t *testing.T) {
	dir, root := tuiStore(t)
	if err := os.Remove(filepath.Join(root, "payments", "c3333333-hook.jsonl")); err != nil {
		t.Fatal(err)
	}
	a := newTestTUI(t, dir)
	a.spawn(a.loadKept)
	drain(t, a)
	a.setScope(scopeKept)
	if len(a.rows) != 1 {
		t.Fatalf("kept rows %d", len(a.rows))
	}
	var forgot []string
	a.forget = func(id string) error {
		forgot = append(forgot, id)
		return forgetSessionForTest(dir, id)
	}
	a.listFocus = true
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: 'F'})
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEnter})
	drain(t, a)
	s := screen(a.frame(120, 36))
	if len(a.rows) != 0 || strings.Contains(s, "Deleted 1") {
		t.Errorf("forgot %v; Deleted tab still lists %d rows:\n%s", forgot, len(a.rows), s)
	}
}

// The reader open on a session that grew shows the new read once it arrives.
func TestTUIReaderTakesTheNewRead(t *testing.T) {
	dir, root := tuiStore(t)
	a := newTestTUI(t, dir)
	// newTestTUI read every session itself; what it queued is not wanted.
	for len(a.loadQ) > 0 {
		<-a.loadQ
	}
	a.loading = map[string]bool{}
	addClaudeSession(t, dir, root, "c3333333-hook", "2026-03-04T11:00:00Z",
		[2]string{"user", "webhook signature fails after key rotation"},
		[2]string{"assistant", "Verify against every published key."},
		[2]string{"user", "and now the brandnewfollowup question"})
	a.loadHome()
	openByIDNoLoad(t, a, "c3333333-hook")
	a.frame(120, 36) // the reader takes the read in hand
	go a.detailWorker()
	t.Cleanup(func() { close(a.loadQ) })
	deadline := time.Now().Add(2 * time.Minute) // polled; only a hang waits this long
	for time.Now().Before(deadline) {
		pump(a)
		if d := a.details[sessionKey(a.reader.s)]; d != nil && len(d.full.Messages) >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if d := a.details[sessionKey(a.reader.s)]; d == nil || len(d.full.Messages) < 3 {
		t.Fatalf("new read did not arrive: row updated %v msgs-in-row %d, detail %+v", a.reader.s.Updated, len(a.reader.s.Messages), d.full.Updated)
	}
	s := screen(a.frame(120, 36))
	if !strings.Contains(s, "brandnewfollowup") {
		t.Errorf("the reader still shows the read from before the session grew:\n%s", s)
	}
}

func forgetSessionForTest(dir, id string) error {
	old := os.Stdout
	null, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	os.Stdout = null
	defer func() { os.Stdout = old; _ = null.Close() }()
	return runForget(dir, []string{"--session", id})
}

func openByIDNoLoad(t *testing.T, a *tuiApp, id string) {
	t.Helper()
	for i, r := range a.rows {
		if r.s.ID == id {
			a.sel = i
			a.openReader()
			return
		}
	}
	t.Fatalf("no row %s", id)
}

// Text a macOS tool wrote decomposed is found by / typed composed.
func TestTUIFindMatchesDecomposedText(t *testing.T) {
	dir, root := tuiStore(t)
	addClaudeSession(t, dir, root, "e5555555-nfd", "2026-03-04T09:00:00Z",
		[2]string{"user", "where is the config"},
		[2]string{"assistant", "It lives under ~/Documents/Résumé/config.toml now."})
	a := newTestTUI(t, dir)
	openByID(t, a, "e5555555-nfd")
	s := screen(a.frame(100, 30))
	if !strings.Contains(s, "Résumé") {
		t.Fatalf("screen draws it composed? %s", s)
	}
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: '/'})
	for _, r := range "Résumé" {
		a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: r})
	}
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEnter})
	if len(a.reader.hits) == 0 {
		t.Errorf("find on screen text %q gave toast %q", "Résumé", a.toast)
	}
}

// A long paste in the box keeps a frame fast.
func TestTUILongQueryFrameIsLinear(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	a.query = []rune(strings.Repeat("abcdefghij ", 2000))
	start := time.Now()
	a.frame(120, 36)
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Errorf("one frame with a 22k-rune query took %v", d)
	}
}

// A session with a few turns, two commands and an edit: the preview tells how
// it went under the answer, with the commands marked by how they ended.
func TestTUIPreviewTellsHowTheSessionWent(t *testing.T) {
	withTempStores(t)
	root := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("PATH", "")
	const id = "d4444444-sched"
	var lines []string
	add := func(typ string, content any) {
		b, err := json.Marshal(map[string]any{"type": typ, "sessionId": id, "cwd": "/work/payments",
			"timestamp": "2026-03-03T10:00:00Z", "message": map[string]any{"role": typ, "content": content}})
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	text := func(s string) []map[string]any { return []map[string]any{{"type": "text", "text": s}} }
	tool := func(id, cmd, out string, failed bool) {
		add("assistant", []map[string]any{{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{"command": cmd}}})
		add("user", []map[string]any{{"type": "tool_result", "tool_use_id": id, "is_error": failed, "content": out}})
	}
	add("user", "the scheduler times out under load")
	add("assistant", text("Traced it to a context cancelled one layer too early. Fixed by passing the parent context through."))
	tool("t1", "go test -race ./internal/scheduler/...", "Exit code 1\n--- FAIL: TestLoad", true)
	add("user", "still seeing it after passing the parent context through")
	add("assistant", text("The proxy closes idle connections first. Lowering the keepalive under the proxy timeout fixes it."))
	add("assistant", []map[string]any{{"type": "tool_use", "id": "e1", "name": "Edit",
		"input": map[string]any{"file_path": "/work/payments/internal/pool/pool.go", "old_string": "30 * time.Minute", "new_string": "4 * time.Minute"}}})
	tool("t2", "go test ./...", "ok  payments 1.2s", false)
	add("user", "and the dashboards should show it as well please")
	add("assistant", text("Added a pool gauge to the scheduler dashboard so a closed connection shows up."))
	writeClaudeFixture(t, filepath.Join(root, "payments", id+".jsonl"), id, lines)
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	a := newTestTUI(t, dir)
	if len(a.rows) != 1 {
		t.Fatalf("rows = %+v", a.rows)
	}
	st := a.details[sessionKey(a.rows[0].s)].story
	if len(st.runs) != 2 || st.runs[0].state != runFailed || st.runs[1].state != runOK || st.failed != 1 {
		t.Errorf("runs = %+v", st.runs)
	}
	if !reflect.DeepEqual(st.edited, []string{"internal/pool/pool.go"}) {
		t.Errorf("edited = %q", st.edited)
	}
	s := screen(a.frame(140, 44))
	wantOnScreen(t, s, "HOW IT WENT", "› still seeing it after passing", "The proxy closes idle connections",
		"RAN", "2 commands · 1 failed", "✗ go test -race", "✓ go test ./...",
		"EDITED", "internal/pool/pool.go", "Read")
	if strings.Contains(s, "TOUCHED") {
		t.Errorf("the edited list replaces the touched line:\n%s", s)
	}
	// Short, the story gives way and the buttons stay.
	s = screen(a.frame(140, 22))
	wantOnScreen(t, s, "ASKED", "Read")
	if strings.Contains(s, "EDITED") && !strings.Contains(s, "internal/pool") {
		t.Errorf("a heading without its lines:\n%s", s)
	}
}

func TestTUIStoryLayoutHelpers(t *testing.T) {
	if got := storyShare(20, []int{10, 2, 1}); !reflect.DeepEqual(got, []int{10, 2, 1}) {
		t.Errorf("room for all: %v", got)
	}
	// Tight, every section gets a line before any gets a second.
	if got := storyShare(8, []int{10, 4, 4}); !reflect.DeepEqual(got, []int{1, 1, 1}) {
		t.Errorf("tight share = %v", got)
	}
	if got := storyShare(1, []int{3}); got[0] != 0 {
		t.Errorf("no room for a heading and a line: %v", got)
	}
	if got := elide(10, 5); !reflect.DeepEqual(got, []int{0, -1, 7, 8, 9}) {
		t.Errorf("elide = %v", got)
	}
	if got := elide(3, 5); !reflect.DeepEqual(got, []int{0, 1, 2}) {
		t.Errorf("elide short = %v", got)
	}
	if got := elide(9, 2); !reflect.DeepEqual(got, []int{7, 8}) {
		t.Errorf("elide two = %v", got)
	}
	if got := projectRelative("/work/payments/internal/pool.go", "payments"); got != "internal/pool.go" {
		t.Errorf("projectRelative = %q", got)
	}
	if got := projectRelative("/else/x.go", "payments"); got != "/else/x.go" {
		t.Errorf("projectRelative outside = %q", got)
	}
}

// tuiStore indexes three Claude sessions in two projects and returns the
// index dir and the claude root.
func tuiStore(t *testing.T) (string, string) {
	t.Helper()
	withTempStores(t)
	root := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("PATH", "")
	write := func(project, id, at, asked, answered string) {
		user, err := json.Marshal(map[string]any{"type": "user", "sessionId": id, "cwd": "/work/" + project, "timestamp": at,
			"message": map[string]any{"role": "user", "content": asked}})
		if err != nil {
			t.Fatal(err)
		}
		reply, err := json.Marshal(map[string]any{"type": "assistant", "sessionId": id, "cwd": "/work/" + project, "timestamp": at,
			"message": map[string]any{"role": "assistant", "content": []map[string]any{{"type": "text", "text": answered}}}})
		if err != nil {
			t.Fatal(err)
		}
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
	// Background work still writing into the store would race the temp
	// dirs' removal; cleanups run last-in first, so this goes before them.
	t.Cleanup(a.bg.Wait)
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
	for y := range c.H {
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

// drain waits for the screen's background work to end, then runs what it
// posted, until nothing more is spawned; at least one update must arrive. It
// waits on the work, not a fixed time: a forget's rebuild on a loaded windows
// runner outlasted the old 5s (#5160). The deadline only catches a hang.
func drain(t *testing.T, a *tuiApp) {
	t.Helper()
	ran := 0
	for {
		idle := make(chan struct{})
		go func() { a.bg.Wait(); close(idle) }()
		select {
		case <-idle:
		case <-time.After(2 * time.Minute):
			t.Fatal("background work still running after 2m")
		}
		n := 0
		for len(a.updates) > 0 {
			(<-a.updates)()
			n++
		}
		if n == 0 {
			break
		}
		ran += n
	}
	if ran == 0 {
		t.Fatal("no update arrived")
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
	wantOnScreen(t, s, "Continue this session in…", "+49 not installed here", "Show them")
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
	wantOnScreen(t, screen(a.frame(120, 36)), "Agents", "49 supported")
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
	a.spawn(a.loadKept)
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

func TestTUIFirstRunBuildsBehindTheCat(t *testing.T) {
	_, _ = tuiStore(t)
	dir := t.TempDir()
	a := newTestTUI(t, dir)
	a.view = viewWelcome
	a.now = time.Now()
	s := screen(a.frame(120, 30))
	wantOnScreen(t, s, "Reading your agents' history")
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: 'x'})
	wantOnScreen(t, screen(a.frame(120, 30)), "Still reading")
	// Keys wait while it reads; esc still leaves.
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEnter})
	if a.view != viewWelcome {
		t.Fatal("left the welcome before the index was built")
	}
	a.firstBuild()
	drain(t, a)
	if !a.welcome.done || a.welcome.err != nil {
		t.Fatalf("welcome = %+v", a.welcome)
	}
	s = screen(a.frame(120, 30))
	wantOnScreen(t, s, "Claude Code", "3 sessions", "Indexed 3 sessions from 1 agent in", "start")
	// Typing on the welcome starts the search with that letter.
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: 'w'})
	if a.view != viewList || string(a.query) != "w" {
		t.Fatalf("view %d query %q", a.view, string(a.query))
	}
}

func TestTUIWelcomeStates(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	a.view = viewWelcome
	a.welcome = welcomeState{done: true, err: errors.New("disk full")}
	wantOnScreen(t, screen(a.frame(100, 24)), "Could not read the history: disk full", "deja doctor")
	a.welcome.err, a.allMeta = nil, nil
	wantOnScreen(t, screen(a.frame(70, 24)), "No agent history on this machine yet")
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: 'q'})
	if !a.quit {
		t.Error("q did not quit the welcome")
	}
	a.quit = false
	a.handle(tui.Event{Kind: tui.EvMouse})
	if a.quit || a.view != viewWelcome {
		t.Error("a click moved the welcome")
	}
	if formatSeconds(1500*time.Millisecond) != "1.5s" || formatSeconds(42*time.Second) != "42s" {
		t.Error(formatSeconds(1500*time.Millisecond), formatSeconds(42*time.Second))
	}
	seen := map[string]bool{}
	for ms := int64(0); ms < 1300; ms += 320 {
		seen[wagFrame(time.UnixMilli(ms)).TailSet] = true
	}
	if len(seen) < 3 {
		t.Errorf("the tail took %d positions", len(seen))
	}
}

func TestTUIBehindTheUncommittedChange(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), git, append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "pool.go"), []byte("package pool\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "pool.go")
	run("commit", "-qm", "init")
	if err := os.WriteFile(filepath.Join(repo, "pool.go"), []byte("package pool\n\nconst x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := changedFiles(repo); strings.Join(got, ",") != "pool.go" {
		t.Errorf("changed = %v", got)
	}
	if got := changedFiles(t.TempDir()); got != nil {
		t.Errorf("outside a repository: %v", got)
	}

	a1 := model.Session{Harness: "claude", ID: "a"}
	b1 := model.Session{Harness: "claude", ID: "b"}
	rows := withBehind([]behindRow{{s: a1, file: "pool.go"}}, []tuiRow{{s: b1, section: "Today"}, {s: a1}})
	if len(rows) != 2 || rows[0].section != "Behind your uncommitted change" || rows[0].file != "pool.go" ||
		rows[1].s.ID != "b" || rows[1].section != "Today" {
		t.Errorf("rows = %+v", rows)
	}
	// The card that moved up hands its date heading on.
	c1 := model.Session{Harness: "claude", ID: "c"}
	rows = withBehind([]behindRow{{s: a1, file: "pool.go"}}, []tuiRow{{s: a1, section: "Today"}, {s: b1}, {s: c1, section: "Earlier"}})
	if len(rows) != 3 || rows[1].section != "Today" || rows[2].section != "Earlier" {
		t.Errorf("heading handed on: %+v", rows)
	}
	if got := withBehind(nil, []tuiRow{{s: b1}}); len(got) != 1 || got[0].section != "" {
		t.Errorf("no block: %+v", got)
	}

	dir, _ := tuiStore(t)
	if got := tuiBehind(dir, repo, []string{"pool.go", "missing.go"}); len(got) != 0 {
		t.Errorf("no session touched pool.go: %+v", got)
	}
	a := newTestTUI(t, dir)
	a.cwd = repo
	a.view = viewList
	a.loadBehind()
	drain(t, a)
	if len(a.rows) != 3 {
		t.Errorf("home after an empty block: %d rows", len(a.rows))
	}
}

func TestTUIBuildProgressAndNews(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	a.view = viewWelcome
	prog := &tuiProgress{}
	prog.Phase("reading sessions", 200)
	prog.Advance(50)
	prog.Harness("codex", 1200, 9)
	prog.Harness("cursor", 0, 0)
	a.welcome = welcomeState{started: a.clock().Add(-12 * time.Second), prog: prog}
	wantOnScreen(t, screen(a.frame(120, 30)), "Reading sessions", "25%", "Codex CLI", "1,200", "12s")
	if strings.Contains(screen(a.frame(120, 30)), "Cursor") {
		t.Error("a store with no sessions was listed")
	}
	prog.Phase("finding transcripts", 0)
	a.frame(120, 30) // a stage of unknown length draws the sliding bead
	wantOnScreen(t, screen(a.frame(70, 32)), "Finding your agents' transcripts")
	if grouped(27262) != "27,262" || grouped(999) != "999" || grouped(-1500) != "-1,500" {
		t.Error(grouped(27262), grouped(999), grouped(-1500))
	}

	a.view = viewList
	a.news, a.newsVer, a.modal = []string{"Bare `deja` opens a screen."}, "9.9.9", modalNews
	s := screen(a.frame(120, 30))
	wantOnScreen(t, s, "New in deja 9.9.9", "Bare deja opens a screen.", "any key")
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: 'x'})
	if a.modal != modalNone || len(a.query) != 0 {
		t.Errorf("the key that closes the news also did something: modal %d query %q", a.modal, string(a.query))
	}

	a.leaving = "Continuing in Codex CLI…"
	wantOnScreen(t, screen(a.frame(120, 30)), "Continuing in Codex CLI", "Claude Code")
}
