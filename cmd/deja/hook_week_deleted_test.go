package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/termwidth"
)

// seedDeletedClaude indexes two Claude Code sessions and deletes the first
// transcript, the way the cleanupPeriodDays sweep does, then lets an index
// pass find it gone. It returns the index directory and the deleted id.
func seedDeletedClaude(t *testing.T) (string, string) {
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
	gone := "5a1c0de0-1111-4222-8333-444455556666"
	for _, id := range []string{gone, "7b2d1ef1-2222-4333-8444-555566667777"} {
		body := `{"type":"user","sessionId":"` + id + `","uuid":"u1","parentUuid":null,"timestamp":"2026-09-01T10:00:00Z","message":{"role":"user","content":"why does the pool exhaust"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(proj, gone+".jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir, gone
}

// Claude Code deletes transcripts after cleanupPeriodDays and says nothing,
// and deja keeping them said nothing either. The week note says it, on a week
// with no recall too, with an id the person can paste.
func TestWeekNoteSaysWhichSessionsTheirAgentDeleted(t *testing.T) {
	dir, gone := seedDeletedClaude(t)
	last := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.WriteFile(dir+".weeknote", []byte(strconv.FormatInt(last.Unix(), 10)), 0o600); err != nil {
		t.Fatal(err)
	}
	// A second on: the window ends before the second the note is written in,
	// and the next one starts there.
	got := weekNoteAt(dir, time.Now().Add(time.Second))
	want := "Claude Code deleted 1 session this week; deja kept it — deja resume " + shortID(gone) + " --write-back"
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

// A deletion from before the note's window is old news.
func TestWeekNoteLeavesOutADeletionBeforeItsWindow(t *testing.T) {
	dir, _ := seedDeletedClaude(t)
	later := time.Now().Add(time.Hour)
	if err := os.WriteFile(dir+".weeknote", []byte(strconv.FormatInt(later.Unix(), 10)), 0o600); err != nil {
		t.Fatal(err)
	}
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
	var wide []index.KeptSession
	for range 1000 {
		wide = append(wide, k("copilot-chat", "5a1c0de0-1111-4222", time.Hour))
	}
	cases := []struct {
		name  string
		kept  []index.KeptSession
		since time.Time
		want  string
	}{
		{"none", nil, week, ""},
		{"two agents, newest first", []index.KeptSession{k("amp", "T-1a2b3c4d5e6f7a8b", time.Hour), k("claude", "c1", 2*time.Hour), k("claude", "c2", 3*time.Hour)}, week,
			"Claude Code and Amp deleted 3 sessions this week; deja kept them — deja show T-1a2b3c4d5e"},
		{"many agents", []index.KeptSession{k("claude", "c1", time.Hour), k("claude", "c2", time.Hour), k("codex", "x1", time.Hour), k("amp", "a1", time.Hour), k("gemini", "g1", time.Hour)}, week,
			"4 agents deleted 5 sessions this week; deja kept them — deja resume c1 --write-back"},
		{"too wide even counted", wide, now.Add(-30 * 24 * time.Hour),
			"VS Code Copilot Chat deleted 1000 sessions in the last 30 days; deja kept them — the Deleted tab of deja lists them"},
		{"a longer gap names its start", []index.KeptSession{k("claude", "c1", time.Hour)}, now.Add(-30 * 24 * time.Hour),
			"Claude Code deleted 1 session in the last 30 days; deja kept it — deja resume c1 --write-back"},
	}
	for _, c := range cases {
		if got := weekDeletedLine(dir, c.kept, c.since, now); got != c.want {
			t.Errorf("%s:\n got  %q\n want %q", c.name, got, c.want)
		}
	}
}
