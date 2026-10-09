package main

import (
	"strings"
	"testing"
)

const screenText = `Harness stores:
  claude       found     ~/.claude/projects  (3 files, 3 indexed sessions)
  37 more stores not found on this machine — ` + "`deja doctor --all`" + ` lists them

MCP wiring:
  claude-code  config missing guidance missing     ~/.claude.json
  gemini       config missing guidance missing     ~/.gemini/settings.json
  zed          wired          guidance missing     ~/.config/zed/settings.json
  grok         config missing guidance missing     ~/.grok/config.toml
               a note that keeps the row

Commands:
  claude-code  missing        ~/.claude/commands/deja.md
  gemini       missing        ~/.agents/skills/deja-history/SKILL.md
               ` + "`skill`" + ` means there is no file of deja's to install: the skill is the command there

Hooks:
  claude-code  missing     ~/.claude/settings.json
  codex-hook   missing     ~/.codex/hooks.json

Index:
  location ~/.cache/deja
  exclusions 0 active patterns
  status   built (size=1 KB)
  last sync 3 records in (2026-10-01 10:00)
`

func screenReport() doctorReport {
	return doctorReport{
		Stores: []doctorStore{{Name: "claude", State: "ok", IndexedSessions: 3}, {Name: "gemini", State: "missing"}},
		MCP: []doctorMCPStatus{
			{Name: "claude-code", State: "config-missing"},
			{Name: "gemini", State: "config-missing"},
			{Name: "zed", State: "wired"},
			{Name: "grok", State: "config-missing"},
		},
	}
}

// On a terminal the rows about agents that are not here fold into one line,
// and everything that says something stays.
func TestDoctorScreenFoldsAgentsNotOnTheMachine(t *testing.T) {
	hermeticEnv(t)
	got := doctorScreen(screenText, screenReport(), false, false, 0)
	for _, want := range []string{
		"3 sessions from 1 store; 1 of 2 agents wired — `deja install --auto`\n",
		"  claude-code  config missing guidance missing     ~/.claude.json\n",
		"  zed          wired",
		"  grok         config missing guidance missing     ~/.grok/config.toml\n               a note that keeps the row\n",
		"  1 more agent not on this machine — `deja doctor --all` lists them\n",
		"  claude-code  missing     ~/.claude/settings.json\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, gone := range []string{"  gemini ", "  codex-hook "} {
		if strings.Contains(got, gone) {
			t.Errorf("%q is not on this machine and still has a row:\n%s", gone, got)
		}
	}
	if n := strings.Count(got, "1 more agent not on this machine"); n != 3 {
		t.Errorf("%d fold lines, want one per section:\n%s", n, got)
	}
	// The only skill row folded, so the note about it goes with it.
	if strings.Contains(got, "`skill` means") {
		t.Errorf("the skill note outlived every skill row:\n%s", got)
	}
}

func TestDoctorScreenAllKeepsEveryRow(t *testing.T) {
	hermeticEnv(t)
	got := doctorScreen(screenText, screenReport(), true, false, 0)
	if !strings.Contains(got, "  gemini       config missing") || !strings.Contains(got, "  gemini       missing") {
		t.Errorf("--all folded a row:\n%s", got)
	}
	if strings.Contains(got, "more agent") {
		t.Errorf("--all printed a fold line:\n%s", got)
	}
}

func TestDoctorScreenAlignsTheIndexKeys(t *testing.T) {
	hermeticEnv(t)
	got := doctorScreen(screenText, screenReport(), false, false, 0)
	for _, want := range []string{
		"  location   ~/.cache/deja\n",
		"  exclusions 0 active patterns\n",
		"  status     built (size=1 KB)\n",
		"  last sync  3 records in",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// A wrap falls at a space, never inside a word or a quoted command, and the
// rest hangs under the row's value column.
func TestDoctorWrapBreaksAtSpacesUnderTheValueColumn(t *testing.T) {
	line := "  timer        not scheduled — `deja install sync-timer` runs `deja sync` every 30 min"
	got := doctorWrap(line, 60)
	if len(got) < 2 {
		t.Fatalf("not wrapped: %q", got)
	}
	words := strings.Fields(line)
	if strings.Join(strings.Fields(strings.Join(got, " ")), " ") != strings.Join(words, " ") {
		t.Errorf("wrap changed the words: %q", got)
	}
	for _, l := range got {
		if len([]rune(l)) > 60 {
			t.Errorf("line over the width: %q", l)
		}
		if strings.Count(l, "`")%2 != 0 {
			t.Errorf("a quoted command was split: %q", l)
		}
	}
	if !strings.HasPrefix(got[1], strings.Repeat(" ", 15)) || got[1][15] == ' ' {
		t.Errorf("continuation not under the value column: %q", got[1])
	}
}

func TestDoctorScreenColoursOnlyTheState(t *testing.T) {
	hermeticEnv(t)
	got := doctorScreen(screenText, screenReport(), false, true, 0)
	if !strings.Contains(got, "  claude       "+statGreen+"found"+statReset+"     ~/.claude/projects") {
		t.Errorf("found is not green:\n%q", got)
	}
	if !strings.Contains(got, statDim+"config missing"+statReset+" guidance "+statDim+"missing"+statReset) {
		t.Errorf("missing is not dim:\n%q", got)
	}
	if strings.Contains(got, "~/.claude"+statReset) {
		t.Errorf("a path was coloured:\n%q", got)
	}
}

const sourcesTSV = "claude\t/h/.claude/projects\tsessions=3 messages=9 size=1.2 KB redacted=1\n" +
	"codex\t/h/.codex/sessions\tsessions=12 messages=40 size=10.5 KB redacted=0\n" +
	"trae\t/h/.trae/cli\tsessions=0 messages=0 size=0 B redacted=0\n" +
	"roo\t\tsessions=0 messages=0 size=0 B redacted=0\n" +
	"grok\t/h/.grok\tsessions=0 messages=0 size=0 B redacted=0\t(cannot be read — permission denied on /h/.grok)\n" +
	"aider\t/h/.aider.chat.history.md\tsessions=0 messages=0 size=0 B redacted=0\tnote=aider writes it in each project\n"

func TestSourcesScreenIsATable(t *testing.T) {
	got := sourcesScreen(sourcesTSV, false, 0)
	want := "store   sessions  messages     size  location\n" +
		"claude         3         9   1.2 KB  /h/.claude/projects  (1 redacted)\n" +
		"codex         12        40  10.5 KB  /h/.codex/sessions\n" +
		"grok           0         0      0 B  /h/.grok  (cannot be read — permission denied on /h/.grok)\n" +
		"3 more stores with no sessions — `deja sources | cat` lists every store\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestSourcesScreenWithNothingAnywhere(t *testing.T) {
	got := sourcesScreen("trae\t/h/.trae\tsessions=0 messages=0 size=0 B redacted=0\n", false, 0)
	if got != "no sessions in the 1 store deja reads — `deja sources | cat` lists them\n" {
		t.Errorf("got %q", got)
	}
}
