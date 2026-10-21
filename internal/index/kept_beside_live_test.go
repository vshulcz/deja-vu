package index

import (
	"os"
	"path/filepath"
	"testing"
)

// threeWays brings index A up to date pass by pass through each step and
// rebuilds a copy of it after the last one.
func (h *twoWayEnv) threeWays(label string, steps ...func()) {
	t := h.t
	t.Helper()
	a := filepath.Join(h.tmp, "a.db")
	for _, s := range steps {
		s()
		if err := Ensure(a, "claude", false, nil); err != nil {
			t.Fatal(err)
		}
	}
	b := filepath.Join(h.tmp, "b.db")
	copyDir(t, a, b)
	if err := Ensure(b, "claude", true, nil); err != nil {
		t.Fatal(err)
	}
	diffSnaps(t, label, takeSnap(t, a), takeSnap(t, b))
}

func keptBesideLive(t *testing.T, h *twoWayEnv) (seed, rewriteMove func()) {
	wall := func(port string) string { return "dial tcp 127.0.0.1:" + port + ": connect: connection refused\n" }
	sess := func(p, sid string, day int, q, port string) string {
		return hPrompt(p, sid, day, 0, q) + hBash(p, sid, day, 1, sid+"a", "make migrate", wall(port), true)
	}
	seed = func() {
		h.put("svc", "s1", sess("svc", "s1", 2, "why does the pool fail", "5432"))
		h.put("app", "s2", sess("app", "s2", 3, "why does the queue fail", "5432"))
	}
	rewriteMove = func() {
		h.put("svc", "s1", sess("svc", "s1", 5, "again: why does the ledger fail", "6432"))
		if err := os.MkdirAll(filepath.Dir(h.path("infra", "s1")), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(h.path("svc", "s1"), h.path("infra", "s1")); err != nil {
			t.Fatal(err)
		}
	}
	return seed, rewriteMove
}

func TestKeptBesideALiveCopyRewritten(t *testing.T) {
	h := newTwoWayEnv(t)
	seed, rm := keptBesideLive(t, h)
	h.threeWays("kept beside a live copy, rewritten", seed, rm, func() {
		h.put("infra", "s1", hPrompt("infra", "s1", 7, 0, "third: why does the outbox fail")+
			hBash("infra", "s1", 7, 1, "s1z", "go build ./...", "ok", false))
	})
}

func TestKeptBesideALiveCopyRenamed(t *testing.T) {
	h := newTwoWayEnv(t)
	seed, rm := keptBesideLive(t, h)
	h.threeWays("kept beside a live copy, renamed", seed, rm, func() {
		if err := os.MkdirAll(filepath.Dir(h.path("app", "s1")), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(h.path("infra", "s1"), h.path("app", "s1")); err != nil {
			t.Fatal(err)
		}
	})
}
