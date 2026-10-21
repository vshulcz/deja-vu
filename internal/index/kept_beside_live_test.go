package index

import (
	"os"
	"path/filepath"
	"slices"
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

// A transcript rewritten and moved from svc to app, rewritten again and moved
// back leaves the app file deleted and owning the row, its path sorting
// first. The pass took the row's conclusion off the file when it was on disk;
// a rebuild took it off the deleted file's records in the order passes wrote
// them, and named the second day's "worker" where the pass had the third
// day's "mutex".
func TestADeletedCopyOwningTheRowKeepsWhatThePassReadOffIt(t *testing.T) {
	h := newTwoWayEnv(t)
	day := func(p string, d int, q, said string) string {
		return hPrompt(p, "s8", d, 0, q) + hBash(p, "s8", d, 1, "b", "go build ./...", "ok", false) +
			hBash(p, "s8", d, 2, "c", "make migrate", "dial tcp 127.0.0.1:5432: connect: connection refused\n", true) + hSay(p, "s8", d, 6, said)
	}
	move := func(from, to string) {
		if err := os.MkdirAll(filepath.Dir(h.path(to, "s8")), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(h.path(from, "s8"), h.path(to, "s8")); err != nil {
			t.Fatal(err)
		}
	}
	h.threeWays("moved back", func() {
		h.put("svc", "s8", day("svc", 8, "start: why does the build fail", "The ledger needs a fix."))
	}, func() {
		h.put("svc", "s8", day("svc", 9, "again: why does the build fail", "The worker needs a fix."))
		move("svc", "app")
	}, func() {
		h.put("app", "s8", day("app", 10, "again: why does the parser fail", "The mutex needs a fix."))
	}, func() {
		move("app", "svc")
	})
}

// A pass that carries some of a session's records and writes others gathers
// their offsets out of log order; the table it writes lists them in the
// order a walk finds them.
func TestAPassWritesOffsetsInLogOrder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "records.bin"), make([]byte, 64), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &passOffsets{byKey: map[string][]int64{}}
	for _, off := range []int64{40, 52, 0, 12} {
		p.add("claude:s5", off)
	}
	if err := p.write(dir); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(dir, "records.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ro, err := readRecordOffsetsFor(dir, f, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := ro.byKey["claude:s5"]; !slices.Equal(got, []int64{0, 12, 40, 52}) {
		t.Errorf("offsets = %v, want them in log order", got)
	}
}
