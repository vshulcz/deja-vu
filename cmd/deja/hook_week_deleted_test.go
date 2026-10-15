package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/termwidth"
)

const (
	seedGone = "5a1c0de0-1111-4222-8333-444455556666"
	seedLive = "7b2d1ef1-2222-4333-8444-555566667777"
)

// seedClaudeDeleted indexes Claude Code sessions, deletes the first id's
// transcript and its directory of subagent runs the way the cleanupPeriodDays
// sweep does, then lets an index pass find them gone. subagents is how many
// child runs the deleted session launched.
func seedClaudeDeleted(t *testing.T, subagents int, ids ...string) string {
	t.Helper()
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	root := filepath.Join(tmp, "claude", "projects")
	proj := filepath.Join(root, "-work-api")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("CODEX_HOME", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	write := func(p, body string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range ids {
		write(filepath.Join(proj, id+".jsonl"), `{"type":"user","sessionId":"`+id+`","uuid":"u1","parentUuid":null,"timestamp":"2026-09-01T10:00:00Z","message":{"role":"user","content":"why does the pool exhaust"}}`+"\n")
	}
	for i := range subagents {
		agent := "a76" + strconv.Itoa(i) + "d0f31f74ee0f2"
		line := func(role, text string) string {
			return `{"type":"` + role + `","isSidechain":true,"agentId":"` + agent + `","sessionId":"` + ids[0] + `","timestamp":"2026-09-01T10:00:0` + strconv.Itoa(i) + `Z","cwd":"/work/api","message":{"role":"` + role + `","content":"` + text + `"}}` + "\n"
		}
		write(filepath.Join(proj, ids[0], "subagents", "agent-"+agent+".jsonl"), line("user", "review the diff")+line("assistant", "two findings"))
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(proj, ids[0]+".jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(proj, ids[0])); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func setWeekStamp(t *testing.T, dir string, at time.Time) {
	t.Helper()
	if err := os.WriteFile(dir+".weeknote", []byte(strconv.FormatInt(at.Unix(), 10)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Claude Code deletes transcripts after cleanupPeriodDays and says nothing,
// and deja keeping them said nothing either. The week note says it, on a week
// with no recall too, with an id the person can paste. It says the files were
// deleted, not who deleted them: a person's own rm is kept the same way.
func TestWeekNoteSaysWhichSessionsWereDeletedFromDisk(t *testing.T) {
	dir := seedClaudeDeleted(t, 0, seedGone, seedLive)
	setWeekStamp(t, dir, time.Now().Add(-8*24*time.Hour))
	// A second on: the window ends before the second the note is written in,
	// and the next one starts there.
	got := weekNoteAt(dir, time.Now().Add(time.Second))
	want := "1 Claude Code session deleted this week; deja kept it — deja resume " + shortID(seedGone) + " --write-back"
	if got != want {
		t.Fatalf("week note = %q\nwant        %q", got, want)
	}
	if termwidth.Columns(got) > noteWidth {
		t.Errorf("the line is %d columns, wider than %d", termwidth.Columns(got), noteWidth)
	}
	// Said once: the next week has nothing new to say.
	if again := weekNoteAt(dir, time.Now().Add(8*24*time.Hour)); again != "" {
		t.Errorf("the same deletion was reported twice: %q", again)
	}
}

// A session that launched subagents is one session, though each child run is
// a transcript of its own under <id>/subagents and goes with it. Counted by
// file, one session with three children read as four, and the id offered could
// be a child's.
func TestWeekNoteCountsADeletedSessionNotItsSubagents(t *testing.T) {
	dir := seedClaudeDeleted(t, 3, seedGone, seedLive)
	setWeekStamp(t, dir, time.Now().Add(-8*24*time.Hour))
	got := weekNoteAt(dir, time.Now().Add(time.Second))
	want := "1 Claude Code session deleted this week; deja kept it — deja resume " + shortID(seedGone) + " --write-back"
	if got != want {
		t.Fatalf("week note = %q\nwant        %q", got, want)
	}
}

// An id whose short form names two sessions is printed whole, and a line too
// wide for it names the screen that lists them instead.
func TestWeekNoteNamesTheDeletedTabWhenTheIDDoesNotFit(t *testing.T) {
	dir := seedClaudeDeleted(t, 0, seedGone, "5a1c0de0-1111-9999-8333-444455556666")
	setWeekStamp(t, dir, time.Now().Add(-8*24*time.Hour))
	got := weekNoteAt(dir, time.Now().Add(time.Second))
	want := "1 Claude Code session deleted this week; deja kept it — the Deleted tab of deja lists them"
	if got != want {
		t.Fatalf("week note = %q\nwant        %q", got, want)
	}
}

// A deletion from before the note's window is old news.
func TestWeekNoteLeavesOutADeletionBeforeItsWindow(t *testing.T) {
	dir := seedClaudeDeleted(t, 0, seedGone, seedLive)
	later := time.Now().Add(time.Hour)
	setWeekStamp(t, dir, later)
	if got := weekNoteAt(dir, later.Add(8*24*time.Hour)); got != "" {
		t.Errorf("a deletion before the window was reported: %q", got)
	}
}

func TestWeekDeletedLineGroupsAgentsAndOffersWhatEachCanDo(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.Local)
	k := func(h, id string, ago time.Duration) index.KeptSession {
		return index.KeptSession{SessionMeta: index.SessionMeta{Harness: h, ID: id}, At: now.Add(-ago)}
	}
	week := now.Add(-7 * 24 * time.Hour)
	dir := filepath.Join(t.TempDir(), "index")
	cases := []struct {
		name  string
		kept  []index.KeptSession
		since time.Time
		want  string
	}{
		{"none", nil, week, ""},
		{"two agents, newest first", []index.KeptSession{k("amp", "T-1a2b3c4d5e6f7a8b", time.Hour), k("claude", "c1", 2*time.Hour), k("claude", "c2", 3*time.Hour)}, week,
			"3 sessions from Claude Code and Amp deleted this week; deja kept them — deja show T-1a2b3c4d5e"},
		{"too many to name", []index.KeptSession{k("claude", "c1", time.Hour), k("claude", "c2", time.Hour), k("codex", "x1", time.Hour), k("amp", "a1", time.Hour), k("gemini", "g1", time.Hour)}, week,
			"5 sessions deleted this week; deja kept them — deja resume c1 --write-back"},
		{"a longer gap says how long", []index.KeptSession{k("claude", "c1", time.Hour)}, now.Add(-30 * 24 * time.Hour),
			"1 Claude Code session deleted in the last 30 days; deja kept it — deja resume c1 --write-back"},
	}
	for _, c := range cases {
		if got := weekDeletedLine(dir, c.kept, c.since, now); got != c.want {
			t.Errorf("%s:\n got  %q\n want %q", c.name, got, c.want)
		}
	}
	// The widest line a real month gives still carries the command: a
	// three-digit count, the longer window wording, Claude Code's short id.
	var many []index.KeptSession
	for range 437 {
		many = append(many, k("claude", seedGone, time.Hour))
	}
	got := weekDeletedLine(dir, many, now.Add(-30*24*time.Hour), now)
	if termwidth.Columns(got) > noteWidth || !strings.HasSuffix(got, "--write-back") {
		t.Errorf("437 sessions in 30 days: %q (%d columns)", got, termwidth.Columns(got))
	}
}
