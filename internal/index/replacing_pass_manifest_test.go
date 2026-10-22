package index

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// What `deja index` does on a pass that replaces one session: ask whether the
// index is fresh, run the pass, then the lines it prints after it. Each of
// those decoded the manifest again, 49 MB of sessions on a 40,000-session
// store, and the check and the pass each walked every store (#5471).
func indexCommandPass(t *testing.T, dir string, handOver bool) (decodes, walks int64) {
	t.Helper()
	invalidateManifestCache(dir) // as a new process finds it
	d, w := manifestDecodes.Load(), storeWalks.Load()
	fresh, _, check := CheckUpToDate(dir, "")
	if fresh || check == nil {
		t.Fatal("the index is up to date after a session was rewritten")
	}
	if handOver {
		if err := EnsureChecked(check, false, io.Discard); err != nil {
			t.Fatal(err)
		}
	} else {
		parityPass(t, dir, false)
	}
	_ = IngestHealth(dir)
	_ = ExclusionsChanged(dir)
	return manifestDecodes.Load() - d, storeWalks.Load() - w
}

func TestAReplacingPassDecodesTheManifestOnce(t *testing.T) {
	proj, inc := replaceStore(t, 40)
	rewriteSession(t, proj, "s007")
	// Written a while ago: a directory that changed just before the walk is
	// taken for one that changed after it (#5503).
	ago := time.Now().Add(-time.Minute)
	for _, d := range []string{proj, filepath.Dir(proj)} {
		if err := os.Chtimes(d, ago, ago); err != nil {
			t.Fatal(err)
		}
	}
	decodes, walks := indexCommandPass(t, inc, true)
	// The freshness check's, which the pass then changes.
	if decodes != 1 {
		t.Errorf("a replacing pass and the checks after it decoded the manifest %d times, want 1", decodes)
	}
	if walks != 1 {
		t.Errorf("the freshness check and the pass walked the stores %d times, want 1", walks)
	}
	sameAsRebuild(t, inc)

	// The control: a pass that is not handed the check decodes and walks
	// again for itself.
	rewriteSession(t, proj, "s011")
	if decodes, walks := indexCommandPass(t, inc, false); decodes < 2 || walks < 2 {
		t.Errorf("a pass on its own decoded the manifest %d times and walked %d, so the counts miss its own", decodes, walks)
	}
	sameAsRebuild(t, inc)
}

// The pass starts from the freshness check's walk only while the index is the
// one that walk was held against: one another process wrote in between holds
// files as they were after it.
func TestTheCheckedWalkIsNotReusedAcrossAnotherWrite(t *testing.T) {
	proj, inc := replaceStore(t, 8)
	rewriteSession(t, proj, "s003")
	fresh, _, check := CheckUpToDate(inc, "")
	if fresh || check == nil {
		t.Fatal("the index is up to date after a session was rewritten")
	}
	// Another process: the same change and one more, indexed under the lock.
	parityWrite(t, filepath.Join(proj, "s005.jsonl"), replaceBody("s005", "wombat "), false)
	later := time.Now().Add(3 * time.Second)
	_ = os.Chtimes(filepath.Join(proj, "s005.jsonl"), later, later)
	parityPass(t, inc, false)
	w := storeWalks.Load()
	if err := EnsureChecked(check, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if storeWalks.Load() == w {
		t.Error("the pass took a walk from before another process wrote the index")
	}
	if fresh, _ := UpToDate(inc, ""); !fresh {
		t.Error("the index is not up to date after the pass")
	}
	sameAsRebuild(t, inc)
}
