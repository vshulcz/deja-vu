package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// An id is also a searchable string, so a transcript that mentions one matches
// it lexically and the search answered before the id route ever ran: over the
// 120 most recent sessions on a real store, asking for each by its own id
// prefix brought the right session back 24 times and a different session, with
// the same confidence, the other 96 (#3717).
func TestRecallContextResolvesAnIDBeforeSearching(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	const want = "hhhh0002-2222-4000-8000-a1b2c3d4e5f6"
	const talker = "hhhh0003-3333-4000-8000-b2c3d4e5f6a7"
	write := func(id, text string) {
		line := `{"type":"user","message":{"role":"user","content":` + quoteJSON(text) +
			`},"timestamp":"2026-07-01T10:00:00Z","sessionId":"` + id + `","cwd":"/proj"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(want, "the connection pool exhausted again")
	// The session that talks about the other one's id — a note, a handover, a
	// reviewer quoting a session. This is the ordinary case on a store deja
	// has been used on for a while, not a contrived one.
	write(talker, "picked the fix up from session hhhh0002 and finished it")

	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	text, n, _, ids, err := recallContextResult(dir, "hhhh0002", "")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || len(ids) != 1 || ids[0] != want {
		t.Fatalf("asking for an id answered from %v (n=%d), not the session it names\n%s", ids, n, text)
	}

	// The controls. Words still search, and a word carrying a dash is still a
	// query rather than a selector.
	if _, n, _, ids, err := recallContextResult(dir, "pool exhausted", ""); err != nil || n != 1 || ids[0] != want {
		t.Errorf("words: n=%d ids=%v err=%v", n, ids, err)
	}
	if !looksLikeSessionID("hhhh0002") || !looksLikeSessionID("ses_01cbcf4b") || !looksLikeSessionID("arev-cline-json2") {
		t.Error("a store's own id shapes must read as ids")
	}
	for _, q := range []string{"wiring", "timeout", "retry budget", "short", "поиск-ошибки"} {
		if looksLikeSessionID(q) {
			t.Errorf("%q is a query, not an id", q)
		}
	}
}
