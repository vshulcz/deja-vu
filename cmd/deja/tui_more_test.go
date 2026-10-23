package main

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/tui"
)

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
	if len(ts) != 4 || ts[0].id != "codex" || ts[1].id != "gemini" || ts[2].id != "claude" || ts[3].more != 40 {
		t.Fatalf("shown = %+v", ts)
	}
	s := screen(a.frame(80, 24))
	wantOnScreen(t, s, "Codex CLI  last used", "+40 not installed here", "Copy for Codex CLI")
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
