package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestReusedTitlesAreOrderedAndCapped(t *testing.T) {
	dir := seedReuse(t, map[string]seedSession{
		"r1": {"first subject", 72 * time.Hour, 5},
		"r2": {"second subject", 72 * time.Hour, 3},
		"r3": {"third subject", 72 * time.Hour, 2},
		"r4": {"one off", 72 * time.Hour, 1},
	})
	got := reusedTitles(dir, 2)
	if len(got) != 2 {
		t.Fatalf("got %d, want the cap of 2", len(got))
	}
	if !strings.Contains(got[0].Title, "first") || !strings.Contains(got[1].Title, "second") {
		t.Fatalf("wrong order: %+v", got)
	}
	if all := reusedTitles(dir, 0); len(all) != 3 {
		t.Fatalf("uncapped returned %d, want the three above the floor", len(all))
	}
}

func TestBriefNamesTheReusedMemory(t *testing.T) {
	dir := seedReuse(t, map[string]seedSession{
		"r1": {"pgbouncer prepared statements", 72 * time.Hour, 4},
	})
	var out strings.Builder
	if err := runBrief(dir, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "pgbouncer") {
		t.Fatalf("the brief names no re-used memory:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "4×") {
		t.Fatalf("the brief drops the count:\n%s", out.String())
	}
}
