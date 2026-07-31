package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// seedReuse builds a store and records recalls against it. Session ids are
// written with an age so the settle filter can be exercised.
func seedReuse(t *testing.T, sessions map[string]struct {
	title string
	age   time.Duration
	times int
}) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-r")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	for sid, s := range sessions {
		when := time.Now().Add(-s.age).UTC().Format("2006-01-02T15:04:05Z")
		body := fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":"/w/r","timestamp":%q,"message":{"role":"user","content":%q}}`,
			sid, when, s.title) + "\n"
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	for sid, s := range sessions {
		for i := 0; i < s.times; i++ {
			usage.RecordServedSessions(dir, usage.KindRecall, 100, 1, false, 1000, []string{sid})
		}
	}
	return dir
}

type seedSession = struct {
	title string
	age   time.Duration
	times int
}

func TestFindReusedMemoryNamesTheMostRecalled(t *testing.T) {
	dir := seedReuse(t, map[string]seedSession{
		"r1": {"pgbouncer prepared statements", 72 * time.Hour, 4},
		"r2": {"readme wording pass", 72 * time.Hour, 2},
		"r3": {"one off question", 72 * time.Hour, 1},
	})
	got, ok := findReusedMemory(dir)
	if !ok {
		t.Fatal("nothing named, though one session was recalled four times")
	}
	if !strings.Contains(got.Title, "pgbouncer") {
		t.Fatalf("named %q, want the most-recalled one", got.Title)
	}
	if got.Times != 4 {
		t.Fatalf("times = %d, want 4", got.Times)
	}
}

// The session currently being worked on is the one an agent recalls most,
// because it is the one being worked on. Naming it is a mirror, not a memory
// — measured while building this: the top session on the real store was the
// session doing the building, at 67 recalls.
func TestFindReusedMemoryIgnoresTodaysWork(t *testing.T) {
	dir := seedReuse(t, map[string]seedSession{
		"live": {"the session happening right now", time.Minute, 40},
		"old":  {"pgbouncer prepared statements", 72 * time.Hour, 3},
	})
	got, ok := findReusedMemory(dir)
	if !ok {
		t.Fatal("nothing named")
	}
	if strings.Contains(got.Title, "right now") {
		t.Fatalf("named the session still being worked on: %q (%d×)", got.Title, got.Times)
	}
	if !strings.Contains(got.Title, "pgbouncer") {
		t.Fatalf("named %q", got.Title)
	}
}

// Once is an answer, not a memory: naming it would make the line permanent
// noise on a store where nothing has actually been re-used.
func TestFindReusedMemorySilentBelowTwo(t *testing.T) {
	dir := seedReuse(t, map[string]seedSession{
		"r1": {"asked once", 72 * time.Hour, 1},
	})
	if got, ok := findReusedMemory(dir); ok {
		t.Fatalf("named %q at %d×, floor is %d", got.Title, got.Times, reusedMinTimes)
	}
}

func TestFindReusedMemorySilentWithoutUsage(t *testing.T) {
	dir := seedReuse(t, map[string]seedSession{
		"r1": {"never recalled", 72 * time.Hour, 0},
	})
	if _, ok := findReusedMemory(dir); ok {
		t.Fatal("nothing was ever served, so nothing can be re-used")
	}
	hermeticEnv(t)
	if _, ok := findReusedMemory(filepath.Join(t.TempDir(), "missing")); ok {
		t.Fatal("no index, nothing to name")
	}
}

// A session whose title deja never recovered cannot be named, and printing a
// count with no subject is what the issue is complaining about.
func TestFindReusedMemorySkipsUntitledSessions(t *testing.T) {
	dir := seedReuse(t, map[string]seedSession{
		"r1": {"real title here", 72 * time.Hour, 2},
	})
	// A key the manifest has never heard of, recalled more than the real one.
	for i := 0; i < 9; i++ {
		usage.RecordServedSessions(dir, usage.KindRecall, 100, 1, false, 1000, []string{"ghost-session"})
	}
	got, ok := findReusedMemory(dir)
	if !ok {
		t.Fatal("nothing named")
	}
	if !strings.Contains(got.Title, "real title") {
		t.Fatalf("named %q, want the one that resolves to a session", got.Title)
	}
}

// A session that resolves but whose title is blank is the other half, and the
// one the name of the test above implies: it exercises the title check rather
// than the does-it-exist check.
func TestFindReusedMemorySkipsBlankTitles(t *testing.T) {
	dir := seedReuse(t, map[string]seedSession{
		"blank": {"   ", 72 * time.Hour, 9},
		"named": {"pgbouncer prepared statements", 72 * time.Hour, 2},
	})
	got, ok := findReusedMemory(dir)
	if !ok {
		t.Fatal("nothing named, though a titled session was re-used twice")
	}
	if !strings.Contains(got.Title, "pgbouncer") {
		t.Fatalf("named %q — a blank title is not a memory", got.Title)
	}
}

// The usage log records a bare id while the manifest keys by harness and id.
// Two harnesses carrying one id makes the recall count the sum of both, so
// naming either attaches a number that belongs to two — and the answer flips
// between runs, because the metas arrive from a map.
func TestFindReusedMemorySkipsAmbiguousIDs(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude", "proj-a")
	codex := filepath.Join(tmp, "codex", "sessions", "2026", "07", "20")
	for _, d := range []string{claude, codex} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	old := time.Now().Add(-72 * time.Hour).UTC().Format("2006-01-02T15:04:05Z")
	if err := os.WriteFile(filepath.Join(claude, "dup.jsonl"), []byte(
		`{"type":"user","sessionId":"dup","cwd":"/w","timestamp":"`+old+`","message":{"role":"user","content":"CLAUDE side subject"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codex, "rollout-2026-07-20T10-00-00-dup.jsonl"), []byte(
		`{"timestamp":"`+old+`","type":"session_meta","payload":{"session_id":"dup","id":"dup","cwd":"/w"}}`+"\n"+
			`{"timestamp":"`+old+`","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"text","text":"CODEX side subject"}]}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		usage.RecordServedSessions(dir, usage.KindRecall, 100, 1, false, 1000, []string{"dup"})
	}
	// Repeatedly, because the defect is non-determinism: a map decides.
	for i := 0; i < 50; i++ {
		if got, ok := findReusedMemory(dir); ok {
			t.Fatalf("named %q on an id two harnesses share; the count belongs to both", got.Title)
		}
	}
}

// A session deja could not date is not old, it is undated: the zero time is
// before any cutoff, so the settle filter admits it and the brief then prints
// "Jan 1 0001". Codex history entries with a non-RFC3339 timestamp are the
// real source of this.
func TestFindReusedMemorySkipsUndatedSessions(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude", "proj-u")
	codexRoot := filepath.Join(tmp, "codex")
	for _, d := range []string{claude, codexRoot} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", codexRoot)
	// A timestamp the parser cannot read leaves Updated at the zero time.
	if err := os.WriteFile(filepath.Join(codexRoot, "history.jsonl"), []byte(
		`{"session_id":"undated","ts":"2026-07-28 10:00:00","text":"a memory with no parseable timestamp"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-72 * time.Hour).UTC().Format("2006-01-02T15:04:05Z")
	if err := os.WriteFile(filepath.Join(claude, "dated.jsonl"), []byte(
		`{"type":"user","sessionId":"dated","cwd":"/w","timestamp":"`+old+`","message":{"role":"user","content":"pgbouncer prepared statements"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	var undated bool
	metas, err := index.AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range metas {
		if m.ID == "undated" && m.Updated.IsZero() {
			undated = true
		}
	}
	if !undated {
		t.Skip("this store dated the undated session; the fixture no longer exercises the case")
	}
	// The undated one is recalled far more, so only the filter can keep it out.
	for i := 0; i < 9; i++ {
		usage.RecordServedSessions(dir, usage.KindRecall, 100, 1, false, 1000, []string{"undated"})
	}
	for i := 0; i < 2; i++ {
		usage.RecordServedSessions(dir, usage.KindRecall, 100, 1, false, 1000, []string{"dated"})
	}
	got, ok := findReusedMemory(dir)
	if !ok {
		t.Fatal("nothing named, though a dated session was re-used twice")
	}
	if got.Age.IsZero() || strings.Contains(got.Title, "no parseable") {
		t.Fatalf("named an undated session: %+v", got)
	}
}

// Titles reach the brief verbatim from user-typed text. A carriage return
// rewinds the line, an escape recolours the rest of the screen, a bell rings
// on every refresh — and the `recent` lines have printed them raw since they
// existed, so the scrub belongs in trimBriefTitle rather than at one call site.
func TestBriefTitlesCannotBreakTheScreen(t *testing.T) {
	for _, in := range []string{
		"fix pool\rDEJA · 999 recalls",
		"esc\x1b[31mred",
		"bell\x07here",
		"rlo \u202ereversed",
		"zwsp a\u200bb",
	} {
		got := trimBriefTitle(in)
		for _, r := range got {
			if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
				t.Errorf("%q survived in %q -> %q", r, in, got)
			}
		}
	}
	if got := trimBriefTitle("has\ttab"); got != "has tab" {
		t.Errorf("got %q, want the words kept apart", got)
	}
}

// End to end: a hostile title in a re-used memory must not reach the screen.
func TestBriefReusedLineIsScrubbed(t *testing.T) {
	dir := seedReuse(t, map[string]seedSession{
		"r1": {"fix pool\rDEJA 999\x1b[31m recalls", 72 * time.Hour, 4},
	})
	var out strings.Builder
	if err := runBrief(dir, &out); err != nil {
		t.Fatal(err)
	}
	// The brief writes its own colour codes, so look at the title text only.
	for _, bad := range []string{"\r", "\x07", "\x1b[31m"} {
		if strings.Contains(out.String(), bad) {
			t.Fatalf("%q reached the screen:\n%q", bad, out.String())
		}
	}
}
