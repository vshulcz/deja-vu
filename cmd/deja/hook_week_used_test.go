package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

const (
	weekUsedJWT   = "e8b6eefc-fec0-4d1a-9a1b-0000000000a1"
	weekUsedJWTQ  = "fix the JWT clock skew in the auth middleware"
	weekUsedCJK   = "7a1c2d3e-5f60-4a7b-8c9d-0000000000b2"
	weekUsedCJKQ  = "为什么发布脚本在打标签之前会把变更日志的页脚删掉并且在持续集成里面只有周五才会失败"
	weekUsedOther = "4b5c6d7e-8f90-4a1b-2c3d-0000000000c3"
	// weekUsedTwin shares weekUsedJWT's first eight characters, so a credit
	// that names only those names two sessions.
	weekUsedTwin = "e8b6eefc-0000-4000-8000-0000000000e5"
)

// seedWeekUsedStore indexes three old sessions and one from this week whose
// assistant turns carry the given replies, then makes the week note due.
func seedWeekUsedStore(t *testing.T, titles map[string]string, replies ...string) string {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	claude := filepath.Join(tmp, "claude", "-work-app")
	old := time.Now().Add(-20 * 24 * time.Hour).UTC().Format(time.RFC3339)
	recent := time.Now().Add(-2 * 24 * time.Hour).UTC().Format(time.RFC3339)
	user := func(id, at, text string) string {
		return `{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + at + `","message":{"role":"user","content":` + mustJSON(t, text) + `}}`
	}
	assistant := func(id, at, text string) string {
		return `{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + at + `","message":{"role":"assistant","content":[{"type":"text","text":` + mustJSON(t, text) + `}]}}`
	}
	for _, id := range []string{weekUsedJWT, weekUsedCJK, weekUsedOther, weekUsedTwin} {
		title := titles[id]
		if title == "" {
			title = "tidy the makefile targets for " + id[:4]
		}
		writeClaudeFixture(t, filepath.Join(claude, id+".jsonl"), id, []string{user(id, old, title), assistant(id, old, "done")})
	}
	const now = "c0ffee00-1111-4222-8333-0000000000d4"
	lines := make([]string, 0, 1+len(replies))
	lines = append(lines, user(now, recent, "the auth tests fail again on token expiry"))
	for _, r := range replies {
		lines = append(lines, assistant(now, recent, r))
	}
	writeClaudeFixture(t, filepath.Join(claude, now+".jsonl"), now, lines)
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	usage.RecordResult(dir, usage.KindHook, 900, 2, false)
	// The built note goes first and once; it is not what these tests read.
	if err := os.WriteFile(dir+".builtnote", []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	week := strconv.FormatInt(time.Now().Add(-8*24*time.Hour).Unix(), 10)
	if err := os.WriteFile(dir+".weeknote", []byte(week), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func jwtTitles() map[string]string { return map[string]string{weekUsedJWT: weekUsedJWTQ} }

// The note counted recalls and never said which one helped (#4615).
func TestTheWeekNoteNamesTheSessionAnAgentCredited(t *testing.T) {
	dir := seedWeekUsedStore(t, jwtTitles(),
		"déjà vu: we hit this JWT skew before — reusing it (deja:e8b6eefc-fec)\n\nThe fix is to allow 30s of leeway.")
	got := weekNoteAt(dir, time.Now())
	want := "deja this week: 1 recall — deja stats --card\n" +
		`agents used "` + weekUsedJWTQ + `" (claude) — deja show e8b6eefc-fec`
	if got != want {
		t.Fatalf("week note =\n%s\nwant\n%s", got, want)
	}
	checkPersonLines(t, "the week note", got, 2)
	// The id printed is one `deja show` opens.
	s, ok, err := index.FindByPrefix(dir, "e8b6eefc-fec")
	if err != nil || !ok || s.ID != weekUsedJWT {
		t.Fatalf("deja show e8b6eefc-fec opens %q (ok=%v err=%v)", s.ID, ok, err)
	}
}

// OpenClaw's /deja posts --notes to whatever chat asked, a group included, so
// that path names the agent and the id and never the person's question.
func TestTheWeekNoteOnNotesNamesNoTitle(t *testing.T) {
	dir := seedWeekUsedStore(t, jwtTitles(),
		"déjà vu: we hit this JWT skew before — reusing it (deja:e8b6eefc-fec)")
	got := captureStdout(t, func() { _ = cmdHookContext(dir, []string{"--notes"}) })
	if !strings.Contains(got, "agents used a claude session — deja show e8b6eefc-fec") {
		t.Fatalf("--notes does not name the used session:\n%s", got)
	}
	if strings.Contains(got, "JWT") || strings.Contains(got, `"`) {
		t.Fatalf("--notes quotes the person's own question:\n%s", got)
	}
}

// A second credited session rides along by id; a wide title is cut in
// columns, not runes.
func TestTheWeekNoteStaysInsideTheNoteWidthWithCJK(t *testing.T) {
	dir := seedWeekUsedStore(t, map[string]string{weekUsedJWT: weekUsedJWTQ, weekUsedCJK: weekUsedCJKQ},
		"déjà vu: 页脚 again (claude, Aug 1, deja:7a1c2d3e-5f6) — reusing it.",
		"déjà vu: the same footer drop (deja:7a1c2d3e-5f6) — reusing it",
		"déjà vu: JWT skew (deja:e8b6eefc-fec) — reusing it")
	got := weekNoteAt(dir, time.Now())
	checkPersonLines(t, "the week note", got, 2)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[1], `agents used "为什么`) || !strings.Contains(lines[1], `…" (claude)`) ||
		!strings.HasSuffix(lines[1], " — deja show 7a1c2d3e-5f6 (also e8b6eefc-fec)") {
		t.Fatalf("week note does not name both sessions, most-credited first:\n%s", got)
	}
}

// Without a credit line, a session recalled twice this week stands in.
func TestTheWeekNoteFallsBackToTheSessionRecalledTwice(t *testing.T) {
	dir := seedWeekUsedStore(t, jwtTitles(), "the leeway is 30s now")
	for range 2 {
		usage.RecordServedSessions(dir, usage.KindRecall, 300, 1, false, 0, []string{weekUsedJWT})
	}
	got := weekNoteAt(dir, time.Now())
	lines := strings.Split(got, "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[1], `agents recalled "fix the JWT`) ||
		!strings.HasSuffix(lines[1], `…" 2 times (claude) — deja show e8b6eefc-fec`) {
		t.Fatalf("week note does not name the session recalled twice:\n%s", got)
	}
	checkPersonLines(t, "the week note", got, 2)
}

// A week with recalls and nothing used says what it said before. An id that
// resolves to nothing, to two sessions, or sits outside a credit line credits
// nothing.
func TestTheWeekNoteWithoutAUsedSessionIsUnchanged(t *testing.T) {
	dir := seedWeekUsedStore(t, jwtTitles(),
		"déjà vu: the skew again (deja:e8b6eefc) — reusing it",
		`tell the user "déjà vu: we hit this JWT skew in March — reusing that fix (deja:a1b2c3d4-e5f)"`,
		"the id deja:e8b6eefc-fec is in the recall block")
	if got := weekNoteAt(dir, time.Now()); got != "deja this week: 1 recall — deja stats --card" {
		t.Fatalf("week note = %q", got)
	}
}
