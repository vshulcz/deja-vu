package index

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	steps := []func(){func() {
		h.put("svc", "s8", day("svc", 8, "start: why does the build fail", "The ledger needs a fix."))
	}, func() {
		h.put("svc", "s8", day("svc", 9, "again: why does the build fail", "The worker needs a fix."))
		move("svc", "app")
	}, func() {
		h.put("app", "s8", day("app", 10, "again: why does the parser fail", "The mutex needs a fix."))
	}, func() {
		move("app", "svc")
	}}
	settled := func(dir string) string {
		t.Helper()
		m, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		return m.Sessions["claude:s8"].Settled
	}
	h.threeWays("moved back", steps...)
	// The live copy written again: the pass takes the row back from the
	// deleted file's records, and kept the conclusion it read off it.
	h2 := newTwoWayEnv(t)
	h = h2
	h.threeWays("moved back, then the live copy rewritten", append(steps, func() {
		h.put("svc", "s8", day("svc", 11, "again: why does the outbox fail", "Nothing settled yet."))
	})...)
	if got := settled(filepath.Join(h.tmp, "a.db")); got != "The mutex needs a fix." {
		t.Errorf("Settled after the live copy is rewritten = %q, want the deleted file's", got)
	}
}

// A version bump rebuilds what was read off a deleted transcript from its
// records, as it always did: a bump can change what is redacted from the
// conclusion, or how friction and questions are read.
func TestAVersionBumpRereadsADeletedTranscriptsConclusion(t *testing.T) {
	h := newTwoWayEnv(t)
	h.put("app", "s1", richSession("app", "s1", 2, "pool"))
	h.put("app", "s2", richSession("app", "s2", 3, "queue"))
	a := filepath.Join(h.tmp, "a.db")
	if err := Ensure(a, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(h.path("app", "s2")); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(a, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(a)
	if err != nil {
		t.Fatal(err)
	}
	row := m.Sessions["claude:s2"]
	if row.Settled == "" {
		t.Fatal("control: the kept session settled nothing")
	}
	row.Settled, row.Hit = "stale", []uint64{42}
	m.Sessions["claude:s2"] = row
	m.Version = version - 1
	if err := writeManifestMeta(a, m); err != nil {
		t.Fatal(err)
	}
	invalidateManifestCache(a)
	if err := Ensure(a, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err = readManifest(a)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Sessions["claude:s2"]; got.Settled == "stale" || slices.Equal(got.Hit, []uint64{42}) {
		t.Errorf("the bump kept the old row's text: settled %q, hit %v", got.Settled, got.Hit)
	}
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

// `deja forget` of a session whose transcript is deleted takes the file out
// of the kept ones as it goes, as the next pass and a rebuild do: kept, it
// stayed in the "no longer on disk" count until a pass had something to read.
func TestForgetDropsTheDeletedTranscriptItTakes(t *testing.T) {
	h := newTwoWayEnv(t)
	// Forget writes its tombstones beside the config.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(h.tmp, "config"))
	a := filepath.Join(h.tmp, "a.db")
	h.put("app", "s1", richSession("app", "s1", 2, "pool"))
	h.put("svc", "s2", richSession("svc", "s2", 3, "queue"))
	if err := Ensure(a, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(h.path("svc", "s2")); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(a, "", false, nil); err != nil {
		t.Fatal(err)
	}
	kept := func() []string {
		t.Helper()
		m, err := readManifest(a)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for p, f := range m.Files {
			if f.Kept {
				out = append(out, filepath.Base(filepath.Dir(p))+"/"+filepath.Base(p))
			}
		}
		return out
	}
	if got := kept(); !slices.Equal(got, []string{"-Users-me-svc/s2.jsonl"}) {
		t.Fatalf("control: kept files before forget = %v", got)
	}
	if _, err := Forget(a, ForgetOptions{Session: "s2"}); err != nil {
		t.Fatal(err)
	}
	if got := kept(); len(got) != 0 {
		t.Errorf("kept files after forget = %v, want none", got)
	}
}

// A line the live copy repeats, whose first copy is filed under the deleted
// transcript the rewrite and move left behind: the pass took the repeat for a
// new turn, a rebuild of the file counts it once. A line the live copy takes
// up for the first time, twice in one append, still counts once, and so does
// a line with a secret in it, which the records hold redacted.
func TestARepeatedLineOfAKeptCopysTurnCountsOnce(t *testing.T) {
	for _, tc := range []struct {
		name, text    string
		heldByRewrite bool
		appendTimes   int
	}{
		{"held before", "The queue needs a fix.", true, 1},
		{"new to the file, twice in one append", "The queue needs a fix.", false, 2},
		{"redacted", "The queue needs GITHUB_TOKEN=ghp_16C7e42F292c6912E7710c838347Ae178B4a", true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newTwoWayEnv(t)
			said := hSay("svc", "s1", 2, 2, tc.text)
			rewrite := hPrompt("svc", "s1", 3, 0, "again: why does the ledger fail") + hSay("svc", "s1", 3, 1, "The ledger needs a fix.")
			if tc.heldByRewrite {
				rewrite = hPrompt("svc", "s1", 3, 0, "again: why does the ledger fail") + said + hSay("svc", "s1", 3, 1, "The ledger needs a fix.")
			}
			h.threeWays(tc.name, func() {
				h.put("svc", "s1", hPrompt("svc", "s1", 2, 0, "why does the pool fail")+said)
			}, func() {
				h.put("svc", "s1", rewrite)
				if err := os.MkdirAll(filepath.Dir(h.path("infra", "s1")), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(h.path("svc", "s1"), h.path("infra", "s1")); err != nil {
					t.Fatal(err)
				}
			}, func() {
				h.add("infra", "s1", strings.Repeat(said, tc.appendTimes)+hSay("infra", "s1", 3, 3, "The outbox needs a fix."))
			})
		})
	}
}
