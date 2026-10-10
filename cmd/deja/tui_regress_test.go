package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/termwidth"
	"github.com/vshulcz/deja-vu/internal/tui"
)

// checkCanvas fails when a row does not measure exactly the canvas width.
func checkCanvas(t *testing.T, c *tui.Canvas, ctx string) {
	t.Helper()
	for y := 0; y < c.H; y++ {
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
	rng := rand.New(rand.NewSource(1))
	runes := []rune("abwebhookretry 支付重试🚀✅?/qjknNtR[]gGo1239:ïé")
	words := []string{"codex:", "claude:", "today", "yesterday", "week", "last month", "in:payments", "in:", "foo:", "webhook", "支付", "🚀🚀"}
	keys := []tui.Key{tui.KeyUp, tui.KeyDown, tui.KeyPgUp, tui.KeyPgDn, tui.KeyHome, tui.KeyEnd, tui.KeyTab, tui.KeyBackTab,
		tui.KeyEnter, tui.KeyEsc, tui.KeyBackspace, tui.KeyLeft, tui.KeyRight}
	sizes := [][2]int{{80, 24}, {60, 20}, {40, 15}, {300, 80}, {120, 36}, {96, 18}, {95, 17}, {20, 6}}
	var log []string
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic %v after:\n%s", r, strings.Join(log[max(0, len(log)-30):], "\n"))
		}
	}()
	for step := 0; step < 400; step++ {
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
func addClaudeSession(t *testing.T, dir, root, project, id, at string, msgs ...[2]string) {
	t.Helper()
	var lines []string
	for _, m := range msgs {
		var content any = m[1]
		if m[0] == "assistant" {
			content = []map[string]any{{"type": "text", "text": m[1]}}
		}
		b, _ := json.Marshal(map[string]any{"type": m[0], "sessionId": id, "cwd": "/work/" + project, "timestamp": at,
			"message": map[string]any{"role": m[0], "content": content}})
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
	addClaudeSession(t, dir, root, "payments", "e5555555-long", "2026-03-04T09:00:00Z",
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
	for i := 0; i < 450; i++ {
		fmt.Fprintf(&b, "line %d of the build log\n", i)
	}
	b.WriteString("FATAL quokkaerror in the linker\n")
	addClaudeSession(t, dir, root, "payments", "e5555555-log", "2026-03-04T09:00:00Z",
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
	go a.loadKept()
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
	addClaudeSession(t, dir, root, "payments", "c3333333-hook", "2026-03-04T11:00:00Z",
		[2]string{"user", "webhook signature fails after key rotation"},
		[2]string{"assistant", "Verify against every published key."},
		[2]string{"user", "and now the brandnewfollowup question"})
	a.loadHome()
	openByIDNoLoad(t, a, "c3333333-hook")
	a.frame(120, 36) // the reader takes the read in hand
	go a.detailWorker()
	t.Cleanup(func() { close(a.loadQ) })
	deadline := time.Now().Add(5 * time.Second)
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
	defer func() { os.Stdout = old; null.Close() }()
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
	addClaudeSession(t, dir, root, "payments", "e5555555-nfd", "2026-03-04T09:00:00Z",
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
