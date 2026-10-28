package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// Project-first put every session of the working project that shared a word
// with the question above the one session elsewhere that answered it, so the
// answer sat tenth and never reached the page. The machine's best answer now
// keeps the second place.
func TestRecallKeepsTheMachinesBestAnswerOnThePage(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	work := t.TempDir()
	line := func(sid, cwd, role, text string) string {
		b, err := json.Marshal(map[string]any{"type": role, "sessionId": sid, "cwd": cwd, "timestamp": "2026-09-10T10:00:00Z",
			"message": map[string]any{"role": role, "content": text}})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	here := filepath.Join(work, "src", "ledgerd")
	if err := os.MkdirAll(here, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range 40 {
		sid := fmt.Sprintf("l%02d", i)
		writeClaudeFixture(t, filepath.Join(root, sources.ClaudeProjectName(here), sid+".jsonl"), sid, []string{
			line(sid, here, "user", fmt.Sprintf("unlock the ledger retention window for shard %d", i)),
			line(sid, here, "assistant", fmt.Sprintf("retention unlock for shard %d is done, 30 days now", i)),
		})
	}
	box := filepath.Join(work, "src", "gpubox")
	writeClaudeFixture(t, filepath.Join(root, sources.ClaudeProjectName(box), "box.jsonl"), "box", []string{
		line("box", box, "user", "the quokka shard retention unlock is parked until the cooling is fixed"),
		line("box", box, "assistant", "parked: quokka retention unlock waits for the cooling fix."),
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	t.Chdir(here)
	text, err := callMCPTool(dir, "deja", json.RawMessage(`{"mode":"recall","q":"quokka retention shard"}`))
	if err != nil {
		t.Fatal(err)
	}
	rows := regexp.MustCompile(`\n(\d+)\. \[[^\]]*\] (\S+) · (\S+)`).FindAllStringSubmatch(text, -1)
	if len(rows) < 2 {
		t.Fatalf("want at least two hits:\n%s", firstLines(text, 20))
	}
	if !strings.HasSuffix(rows[0][2], "ledgerd") {
		t.Fatalf("the project's own session no longer leads:\n%s", firstLines(text, 20))
	}
	if rows[1][3] != "box" || !strings.Contains(text, "cooling") {
		t.Fatalf("the session that answers is not second on the page:\n%s", firstLines(text, 20))
	}
}

// The session added second is there for the reader, not for the lines above
// the page. A question about something no session holds was headed by a
// coverage line about the added session, which holds most of the words but
// not the subject, where the page used to say nothing is about it.
func TestTheAddedAnswerDoesNotSetTheLead(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	work := t.TempDir()
	line := func(sid, cwd, role, text string) string {
		b, err := json.Marshal(map[string]any{"type": role, "sessionId": sid, "cwd": cwd, "timestamp": "2026-09-10T10:00:00Z",
			"message": map[string]any{"role": role, "content": text}})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	here := filepath.Join(work, "src", "ledgerd")
	if err := os.MkdirAll(here, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range 40 {
		sid := fmt.Sprintf("l%02d", i)
		// Half say one word of the question, half the other: no session here
		// is about it.
		word := "retention"
		if i%2 == 1 {
			word = "shard"
		}
		writeClaudeFixture(t, filepath.Join(root, sources.ClaudeProjectName(here), sid+".jsonl"), sid, []string{
			line(sid, here, "user", fmt.Sprintf("unlock the ledger %s window %d", word, i)),
			line(sid, here, "assistant", fmt.Sprintf("%s unlock %d is done, 30 days now", word, i)),
		})
	}
	for _, s := range []struct{ sid, project, user, reply string }{
		{"box", "gpubox", "the quokka shard retention unlock is parked until the cooling is fixed", "parked: quokka retention unlock waits for the cooling fix."},
		{"ice", "maps", "draw the glacier outline on the coast layer", "glacier outline drawn."},
	} {
		cwd := filepath.Join(work, "src", s.project)
		writeClaudeFixture(t, filepath.Join(root, sources.ClaudeProjectName(cwd), s.sid+".jsonl"), s.sid, []string{
			line(s.sid, cwd, "user", s.user), line(s.sid, cwd, "assistant", s.reply),
		})
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	t.Chdir(here)
	text, err := callMCPTool(dir, "deja", json.RawMessage(`{"mode":"recall","q":"quokka retention shard glacier"}`))
	if err != nil {
		t.Fatal(err)
	}
	// A page no session is about lists its leads one per line, id last.
	rows := regexp.MustCompile(`(?m)^(\d+)\. \[[^\]]*\] (\S+) .*· (\S+)$`).FindAllStringSubmatch(text, -1)
	if len(rows) < 2 || rows[1][3] != "box" {
		t.Fatalf("the machine's best is not second:\n%s", firstLines(text, 20))
	}
	if !strings.Contains(firstLines(text, 3), nothingIsAboutThis) {
		t.Errorf("the added session set the lead:\n%s", firstLines(text, 20))
	}
}

func TestLeadHits(t *testing.T) {
	hit := func(id string) search.Hit { return search.Hit{Session: model.Session{Harness: "claude", ID: id}} }
	page := []search.Hit{hit("p1"), hit("m1"), hit("p2")}
	for _, c := range []struct {
		name, added   string
		want, skipped int
	}{
		{"nothing added", "", 3, -1},
		{"added", "claude:m1", 2, 1},
		{"added, not on the page", "claude:m9", 3, -1},
	} {
		got, skipped := leadHits(page, c.added)
		if len(got) != c.want || skipped != c.skipped {
			t.Errorf("%s: got %d hits, skipped %d; want %d, %d", c.name, len(got), skipped, c.want, c.skipped)
		}
	}
}

func TestWithMachineBest(t *testing.T) {
	hit := func(id string) search.Hit { return search.Hit{Session: model.Session{Harness: "claude", ID: id}} }
	ids := func(hs []search.Hit) string {
		out := make([]string, 0, len(hs))
		for _, h := range hs {
			out = append(out, h.Session.ID)
		}
		return strings.Join(out, ",")
	}
	project := []search.Hit{hit("p1"), hit("p2"), hit("p3")}
	for _, c := range []struct {
		name            string
		merged, machine []search.Hit
		want            string
	}{
		{"moved up from the rest", append(append([]search.Hit{}, project...), hit("m1"), hit("m2")), []search.Hit{hit("m1"), hit("p2")}, "p1,m1,p2,p3,m2"},
		{"added when the rest was dropped", project, []search.Hit{hit("m1")}, "p1,m1,p2,p3"},
		{"already the project's", project, []search.Hit{hit("p3"), hit("m1")}, "p1,p2,p3"},
		{"no machine ranking", project, nil, "p1,p2,p3"},
		{"nothing in the project", nil, []search.Hit{hit("m1")}, ""},
	} {
		if got := ids(withMachineBest(c.merged, c.machine, project)); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestFitLinesDropsFromTheEnd(t *testing.T) {
	block := []string{"  what this session concluded:\n", "  → the pool is 40 now\n", "  files it touched: pool.go\n", "  it ran: go test ./... — exit 0 here\n"}
	all := 0
	for _, l := range block {
		all += len(l)
	}
	for _, c := range []struct{ room, want int }{
		{all, 4},
		{all - 1, 3},
		{len(block[0]) + len(block[1]), 2},
		{0, 2},
	} {
		if got := fitLines(append([]string(nil), block...), c.room); len(got) != c.want {
			t.Errorf("room %d: kept %d lines, want %d: %q", c.room, len(got), c.want, got)
		}
	}
	if got := fitLines(append([]string(nil), block[2:]...), 0); len(got) != 0 {
		t.Errorf("files and commands alone kept %q in no room", got)
	}
}
