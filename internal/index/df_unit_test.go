package index

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Rarity has two honest units and neither works alone. Counting whole sessions
// calls a subject word common, because on a real store a handful of marathon
// sessions hold most of everything and all of them mention it. Counting
// messages, capped per session, fixes that and breaks the other direction: the
// word one long session is *about* saturates that session and reads as filler —
// which is what the name of the thing someone worked on all month looks like.
//
// So both are computed and the rarer verdict wins. This holds that: a word
// living in one long session stays rare, and a word spread thinly across the
// whole store does not become rare just because no single session repeats it.
func TestRarityTakesTheRarerOfTheTwoVerdicts(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))

	proj := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(sid, text string, at time.Time) string {
		return fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/app","message":{"role":"user","content":%q}}`,
			sid, at.UTC().Format(time.RFC3339), text) + "\n"
	}
	start := time.Now().Add(-72 * time.Hour)

	// One marathon that is about quetzalcoatl and says it in every turn.
	var marathon string
	for i := 0; i < 300; i++ {
		marathon += line("marathon", fmt.Sprintf("quetzalcoatl deploy step %d, checked the branch again", i), start.Add(time.Duration(i)*time.Minute))
	}
	if err := os.WriteFile(filepath.Join(proj, "marathon.jsonl"), []byte(marathon), 0o644); err != nil {
		t.Fatal(err)
	}
	// A hundred ordinary sessions, each saying "branch" once. Nothing repeats
	// it, so a per-session cap never binds on it.
	for i := 0; i < 100; i++ {
		sid := fmt.Sprintf("ordinary-%d", i)
		body := line(sid, fmt.Sprintf("looked at the branch and the tests for task %d", i), start.Add(time.Duration(i)*time.Hour))
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	_, _, _, idfOf, err := ProjectRelevant(dir, []string{"work/app"}, []string{"quetzalcoatl", "branch"}, 8)
	if err != nil {
		t.Fatal(err)
	}
	subject, ordinary := idfOf["quetzalcoatl"], idfOf["branch"]
	if subject <= ordinary {
		t.Fatalf("the word one session is about scores %.2f, the word everywhere scores %.2f — the signal is inverted", subject, ordinary)
	}
	if subject < dejaVuStrongIDFFloor {
		t.Errorf("a word living in a single session scores %.2f, below the strong floor %.2f — saturating one session made it read as filler",
			subject, dejaVuStrongIDFFloor)
	}
	if ordinary >= dejaVuStrongIDFFloor {
		t.Errorf("a word in a hundred sessions scores %.2f, at or above the strong floor %.2f", ordinary, dejaVuStrongIDFFloor)
	}
}

// The other direction, which is the shape a real store has: a handful of
// marathon sessions hold most of the text, an ordinary word fills them, and a
// subject word is mentioned a few times in slightly more of them. Counting
// sessions then says the subject is the commoner of the two — measured on a
// real store, pgbouncer in 16 sessions scored 1.31 while branch in 13 scored
// 1.50, the most specific word in the store ranking below the most ordinary
// one.
func TestRarityIsNotInvertedByMarathonSessions(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))

	proj := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-200 * time.Hour)
	say := func(sid, text string, i int) string {
		return fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/app","message":{"role":"user","content":%q}}`,
			sid, start.Add(time.Duration(i)*time.Minute).UTC().Format(time.RFC3339), text) + "\n"
	}

	// Thirteen marathons that say "branch" all day long.
	for s := 0; s < 13; s++ {
		sid := fmt.Sprintf("marathon-%d", s)
		body := ""
		for i := 0; i < 200; i++ {
			body += say(sid, fmt.Sprintf("branch work, run %d", i), s*300+i)
		}
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Sixteen sessions mention "pgbouncer" three times each — more sessions
	// than "branch" lives in, a fraction of the messages.
	for s := 0; s < 16; s++ {
		sid := fmt.Sprintf("touched-%d", s)
		body := ""
		for i := 0; i < 3; i++ {
			body += say(sid, fmt.Sprintf("pgbouncer pool question %d", i), 5000+s*10+i)
		}
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	_, _, _, idfOf, err := ProjectRelevant(dir, []string{"work/app"}, []string{"pgbouncer", "branch"}, 8)
	if err != nil {
		t.Fatal(err)
	}
	subject, ordinary := idfOf["pgbouncer"], idfOf["branch"]
	if subject <= ordinary {
		t.Fatalf("the subject scores %.2f and the word filling every marathon scores %.2f — counting whole sessions inverts the signal",
			subject, ordinary)
	}
	// The floor itself is not asserted here: it is set for a store of
	// thousands, and twenty-nine sessions cannot reach it. What this holds is
	// the ordering, which is what was upside down.
	if subject < ordinary*1.5 {
		t.Errorf("the subject scores %.2f against %.2f — separated, but by a margin a threshold cannot sit in",
			subject, ordinary)
	}
}
