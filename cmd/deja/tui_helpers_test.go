package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

func TestTUIDataHelpers(t *testing.T) {
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	for at, want := range map[time.Time]string{
		{}:                         "-",
		now.Add(time.Hour):         "Mar 4",
		now.Add(-30 * time.Second): "just now",
		now.Add(-5 * time.Minute):  "5m ago",
		now.Add(-3 * time.Hour):    "3h ago",
		now.Add(-72 * time.Hour):   "3d ago",
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
	pos, split := continueGrid(ts[:5], 3)
	if split != 2 || pos[2] != [2]int{1, 0} || pos[4] != [2]int{1, 2} {
		t.Errorf("grid = %v split %d", pos, split)
	}
	if gridMove(pos, 4, -1) != 1 || gridMove(pos, 0, 1) != 2 || gridMove(pos, 9, 1) != 9 || gridMove(pos, 0, -1) != 0 {
		t.Error("gridMove")
	}
	if gridCols(80) != 3 || gridCols(50) != 2 || gridCols(20) != 1 {
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
