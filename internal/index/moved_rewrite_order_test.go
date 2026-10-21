package index

import (
	"os"
	"path/filepath"
	"testing"
)

// A transcript rewritten and moved to another project before the next pass is
// no rename: the old file is kept as a deleted transcript and the new one is
// read as a copy of the same session. The pass leaves the kept records where
// they were and appends the new ones; a rebuild wrote the kept file after the
// live one, so the session's tool output came back in another order and its
// friction texts, facts and fixes differed from the pass's.
func TestRewrittenAndMovedKeepsTheOrderARebuildWrites(t *testing.T) {
	h := newTwoWayEnv(t)
	wall := func(port string) string { return "dial tcp 127.0.0.1:" + port + ": connect: connection refused\n" }
	sess := func(p, sid string, day int, q, port string) string {
		return hPrompt(p, sid, day, 0, q) + hBash(p, sid, day, 1, sid+"a", "make migrate", wall(port), true)
	}
	h.twoWays("rewrite then move", func() {
		h.put("svc", "s1", sess("svc", "s1", 2, "why does the pool fail", "5432"))
		h.put("app", "s2", sess("app", "s2", 3, "why does the queue fail", "5432"))
		h.put("app", "s3", sess("app", "s3", 4, "why does the cache fail", "5432"))
	}, func() {
		h.put("svc", "s1", sess("svc", "s1", 5, "again: why does the ledger fail", "6432"))
		if err := os.MkdirAll(filepath.Dir(h.path("infra", "s1")), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(h.path("svc", "s1"), h.path("infra", "s1")); err != nil {
			t.Fatal(err)
		}
	})
}

// A session's commands do not always come in time order: a kept file can hold
// turns older than the copy on disk, and a transcript can hold them out of
// order itself. A pass takes its facts from them sorted by time; the rebuild
// took them as they came, and named another command the most recent.
func TestSessionFactsTakeCommandsInTimeOrder(t *testing.T) {
	h := newTwoWayEnv(t)
	h.twoWays("commands out of time order", func() {
		h.put("app", "s1", hPrompt("app", "s1", 2, 0, "why does the pool fail"))
	}, func() {
		h.put("svc", "s2", hPrompt("svc", "s2", 3, 0, "why does the queue fail")+
			hBash("svc", "s2", 3, 5, "a", "make migrate", "ok", false)+
			hBash("svc", "s2", 3, 1, "b", "go test ./queue/...", "ok", false))
	})
}
