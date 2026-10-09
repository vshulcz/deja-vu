package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/tui"
)

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
		cmd := exec.Command(git, append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
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
	rows := withBehind([]behindRow{{s: a1, file: "pool.go"}}, []tuiRow{{s: b1}, {s: a1}}, "RECENT")
	if len(rows) != 2 || rows[0].section != "Behind your uncommitted change" || rows[0].file != "pool.go" ||
		rows[1].s.ID != "b" || rows[1].section != "RECENT" {
		t.Errorf("rows = %+v", rows)
	}
	if got := withBehind(nil, []tuiRow{{s: b1}}, "RECENT"); len(got) != 1 || got[0].section != "" {
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
