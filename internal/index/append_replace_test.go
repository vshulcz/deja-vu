package index

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// replaceStore writes n Claude transcripts of a few turns each, indexes them
// into inc and returns the root, the project dir and inc.
func replaceStore(t *testing.T, n int) (string, string) {
	t.Helper()
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude"})
	proj := filepath.Join(tmp, "claude", "-tmp-proj")
	for i := range n {
		id := fmt.Sprintf("s%03d", i)
		parityWrite(t, filepath.Join(proj, id+".jsonl"), replaceBody(id, ""), false)
	}
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, false)
	return proj, inc
}

func replaceBody(id, first string) string {
	var b strings.Builder
	for i := range 6 {
		role, text := "user", fmt.Sprintf(`"%swhy does the %s retry loop spin on ledger%d, take %d"`, first, id, i, i)
		if i%2 == 1 {
			role, text = "assistant", fmt.Sprintf(`"the backoff in %s never resets after pgbouncer%d drops, %s"`, id, i, strings.Repeat("cursor wal fsync ", 20))
		}
		first = ""
		b.WriteString(parityClaudeLine(id, fmt.Sprintf("%02d:00Z", i), role, text))
	}
	return b.String()
}

// rewriteSession replaces one transcript with a body that no append follows.
func rewriteSession(t *testing.T, proj, id string) {
	t.Helper()
	p := filepath.Join(proj, id+".jsonl")
	parityWrite(t, p, replaceBody(id, "quokka "), false)
	later := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(p, later, later)
}

// indexFiles is every file under dir as stat saw it, by path below dir.
func indexFiles(t *testing.T, dir string) map[string]os.FileInfo {
	t.Helper()
	out := map[string]os.FileInfo{}
	err := filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		// Windows reads a file's identity from its path when first asked;
		// asked later, after a swap, it would be the new file's.
		_ = os.SameFile(fi, fi)
		out[rel] = fi
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// bytesWritten is what a pass wrote into dir, per file: all of a file that is
// not the one that was there, the growth of one that is.
func bytesWritten(before, after map[string]os.FileInfo) map[string]int64 {
	out := map[string]int64{}
	for rel, fi := range after {
		if was, ok := before[rel]; ok && os.SameFile(was, fi) {
			if d := fi.Size() - was.Size(); d > 0 {
				out[rel] = d
			}
			continue
		}
		out[rel] = fi.Size()
	}
	return out
}

// logAndPostings sums what went to records.bin and the postings, leaving out
// the manifest and the offset table: every pass writes those whole, an append
// of one turn included.
func logAndPostings(w map[string]int64) int64 {
	var n int64
	for rel, b := range w {
		if rel == "records.bin" || strings.HasPrefix(rel, "buckets"+string(filepath.Separator)) {
			n += b
		}
	}
	return n
}

// A pass that replaces one session of a store writes that session's records
// and postings, not the store's: records.bin stays the file it was and grows
// by the session, and the postings land in overlays (#5387). Measured against
// a store of 40 and of 160 sessions, so a cost that scales with the store
// cannot pass for the change.
func TestReplacingPassWritesTheChangeNotTheStore(t *testing.T) {
	var written [2]int64
	for i, n := range []int{40, 160} {
		proj, inc := replaceStore(t, n)
		log := filepath.Join(inc, "records.bin")
		before := indexFiles(t, inc)
		rewriteSession(t, proj, "s007")
		parityPass(t, inc, false)
		after := indexFiles(t, inc)
		if !os.SameFile(before["records.bin"], after["records.bin"]) {
			t.Fatalf("%d sessions: records.bin was written anew", n)
		}
		w := bytesWritten(before, after)
		written[i] = logAndPostings(w)
		// The bound: the session's records once, its postings a few times
		// over, and the overlay headers.
		grew := w["records.bin"]
		t.Logf("%d sessions: %d bytes of log and postings for a %d-byte session, log %d bytes", n, written[i], grew, after["records.bin"].Size())
		if limit := 4*grew + 16<<10; written[i] > limit {
			t.Errorf("%d sessions: the pass wrote %d bytes of log and postings, want at most %d for a %d-byte session (%v)", n, written[i], limit, grew, w)
		}
		if fi, err := os.Stat(log); err != nil || 10*grew > fi.Size() {
			t.Fatalf("%d sessions: the replaced session is %d of a %d-byte log, too much of it to tell the two apart", n, grew, fi.Size())
		}
		s, ok, err := FindByIdentity(inc, "claude", "s007")
		if err != nil || !ok || !strings.HasPrefix(s.Messages[0].Text, "quokka") || len(s.Messages) != 6 {
			t.Fatalf("%d sessions: s007 after the pass = %+v ok=%v err=%v", n, s.Messages, ok, err)
		}
		sameAsRebuild(t, inc)
		offsetTableMatchesAWalk(t, inc, true)
	}
	if written[1] > 2*written[0] {
		t.Errorf("the pass wrote %d bytes on 40 sessions and %d on 160: it grows with the store", written[0], written[1])
	}

	// The control: with the log written anew, the same pass writes the store.
	proj, inc := replaceStore(t, 40)
	appendReplacing = false
	defer func() { appendReplacing = true }()
	before := indexFiles(t, inc)
	rewriteSession(t, proj, "s007")
	parityPass(t, inc, false)
	w := bytesWritten(before, indexFiles(t, inc))
	if got := logAndPostings(w); got < before["records.bin"].Size() {
		t.Errorf("rewriting the log wrote %d bytes of a %d-byte log: the measure does not see a rewrite", got, before["records.bin"].Size())
	}
}

// Replaced records stay in the log until they are a quarter of it, and then
// the next replacing pass writes the log without them.
func TestDeadRecordsAreWrittenOutPastAQuarter(t *testing.T) {
	proj, inc := replaceStore(t, 8)
	inode := indexFiles(t, inc)["records.bin"]
	compacted := false
	for i := 0; i < 8 && !compacted; i++ {
		rewriteSession(t, proj, fmt.Sprintf("s%03d", i))
		parityPass(t, inc, false)
		m, err := readManifest(inc)
		if err != nil {
			t.Fatal(err)
		}
		now := indexFiles(t, inc)["records.bin"]
		if !os.SameFile(inode, now) {
			compacted = true
			if len(m.DeadRecords) != 0 || m.DeadBytes != 0 {
				t.Errorf("a written-out log still lists %d dead records", len(m.DeadRecords))
			}
			if hasOverlays(t, inc) {
				t.Error("a written-out log kept postings overlays")
			}
			break
		}
		if len(m.DeadRecords) == 0 || m.DeadBytes <= 0 {
			t.Fatalf("pass %d appended but lists %d dead records", i, len(m.DeadRecords))
		}
	}
	if !compacted {
		t.Fatal("eight replaced sessions of eight never wrote the log out")
	}
	sameAsRebuild(t, inc)
	offsetTableMatchesAWalk(t, inc, true)
}

func hasOverlays(t *testing.T, dir string) bool {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "buckets"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if isOverlay(e.Name()) {
			return true
		}
	}
	return false
}

// A pass that fails after appending leaves the previous index as it was:
// its log cut back, its manifest untouched, and the next pass appends again
// rather than rebuilding.
func TestFailedAppendLeavesThePreviousIndex(t *testing.T) {
	proj, inc := replaceStore(t, 12)
	was := indexFiles(t, inc)["records.bin"].Size()
	rewriteSession(t, proj, "s003")
	beforeAppendSwap = func() error { return errors.New("killed") }
	err := Ensure(inc, "", false, io.Discard)
	beforeAppendSwap = nil
	if err == nil {
		t.Fatal("the pass did not fail")
	}
	if got := indexFiles(t, inc)["records.bin"].Size(); got != was {
		t.Fatalf("records.bin is %d bytes after the failed pass, was %d", got, was)
	}
	if _, statErr := os.Stat(filepath.Join(inc, appendPendingFile)); statErr == nil {
		t.Error("the pending mark outlived the pass")
	}
	if Damaged(inc) {
		t.Fatalf("the previous index reads as damaged: %s", DamageReason(inc))
	}
	s, ok, err := FindByIdentity(inc, "claude", "s003")
	if err != nil || !ok || strings.HasPrefix(s.Messages[0].Text, "quokka") {
		t.Fatalf("s003 after the failed pass = %+v ok=%v err=%v, want the old one", s.Messages, ok, err)
	}
	inode := indexFiles(t, inc)["records.bin"]
	parityPass(t, inc, false)
	if !os.SameFile(inode, indexFiles(t, inc)["records.bin"]) {
		t.Error("the pass after a failed one wrote the log anew")
	}
	sameAsRebuild(t, inc)
}

// A pass killed mid-append leaves records.bin longer than its manifest and the
// pending mark beside it. The next lock taker cuts the tail off, so the store
// is not rebuilt for it; the control shows the same tail without the mark is
// what a rebuild is for.
func TestKilledAppendIsCutBackUnderTheLock(t *testing.T) {
	_, inc := replaceStore(t, 6)
	log := filepath.Join(inc, "records.bin")
	was := indexFiles(t, inc)["records.bin"].Size()
	appendGarbage := func() {
		f, err := os.OpenFile(log, os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write([]byte{9, 0, 0, 0, 1, 2, 3})
		_ = f.Close()
	}
	if err := os.WriteFile(filepath.Join(inc, appendPendingFile), []byte(strconv.FormatInt(was, 10)), 0o600); err != nil {
		t.Fatal(err)
	}
	appendGarbage()
	unlock, err := lockDir(inc)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	if got := indexFiles(t, inc)["records.bin"].Size(); got != was {
		t.Fatalf("records.bin is %d bytes after recovery, want %d", got, was)
	}
	if Damaged(inc) {
		t.Fatalf("damaged after recovery: %s", DamageReason(inc))
	}
	// Without the mark the tail is nobody's to cut.
	appendGarbage()
	unlock, err = lockDir(inc)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	if got := indexFiles(t, inc)["records.bin"].Size(); got == was {
		t.Fatal("a tail with no mark was cut")
	}
}

// A staging directory a pass could not clear may still hold a hard link to
// the live log. The next pass that writes the log anew must make its own file
// rather than truncate that one, which is the live log under another name.
func TestALeftoverStagingLinkNeverTruncatesTheLiveLog(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not stop a delete on windows")
	}
	proj, inc := replaceStore(t, 12)
	log := filepath.Join(inc, "records.bin")
	was := indexFiles(t, inc)["records.bin"].Size()
	tmp := inc + ".tmp"
	if err := os.MkdirAll(filepath.Join(tmp, "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(log, filepath.Join(tmp, "records.bin")); err != nil {
		t.Skipf("no hard links here: %v", err)
	}
	// Stands in for a link Windows will not delete while a reader holds the
	// log open.
	if err := os.Chmod(tmp, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(tmp, 0o700) }()
	appendReplacing = false
	defer func() { appendReplacing = true }()
	rewriteSession(t, proj, "s004")
	_ = Ensure(inc, "", false, io.Discard)
	if got := indexFiles(t, inc)["records.bin"].Size(); got < was {
		t.Fatalf("the live log went from %d to %d bytes through the leftover link", was, got)
	}
	if Damaged(inc) {
		t.Fatalf("damaged: %s", DamageReason(inc))
	}
}

// A pass that lands while the per-prompt recall ranks leaves it the answer a
// ranking made after the pass gives: it is read again from the new
// generation, not scored from one bucket of each (#5459).
func TestARankingAcrossAPassAnswersAsOneAfterIt(t *testing.T) {
	proj, inc := replaceStore(t, 40)
	for range 3 {
		rewriteSession(t, proj, "s007")
		parityPass(t, inc, false)
	}
	if m, err := readManifest(inc); err != nil || len(m.DeadRecords) == 0 {
		t.Fatalf("control: the rewrites listed no dead records (err %v)", err)
	}
	// The pass holds the lock, so the ranking reads without it, as it does
	// beside a pass in another process.
	unlock, err := lockDir(inc)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	// Between a bucket and its overlays, on each read of the session's word:
	// the ranking reads it twice, once to see it is there at all.
	landed := 0
	bucketBaseRead = func(tok string) {
		if landed == 2 || tok != "ts007" {
			return
		}
		landed++
		rewriteSession(t, proj, "s007")
		if err := ensureHeld(inc, "", true, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { bucketBaseRead = nil }()
	ranked := func() string {
		ss, matched, strong, _, idf, err := ProjectRelevantNamingCut(inc, nil, []string{"s007", "pgbouncer3"}, 5, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		for i, s := range ss {
			fmt.Fprintf(&b, "%s:%d:%d:%d ", s.ID, len(s.Messages), matched[i], strong[i])
		}
		fmt.Fprintf(&b, "idf %v", idf)
		return b.String()
	}
	got := ranked()
	if landed != 2 {
		t.Fatalf("control: %d passes landed during the ranking, want 2", landed)
	}
	if want := ranked(); got != want {
		t.Fatalf("ranked across the passes:\n %s\nafter them:\n %s", got, want)
	}
}

// A second pass that lands while a reader reads a session again for the
// first one zeroes what that reread reads. The reader goes once more, and
// gets the session as the last pass left it (#5460).
func TestASecondPassDuringTheRereadStillGetsTheSession(t *testing.T) {
	proj, inc := replaceStore(t, 12)
	pinned, err := readManifest(inc)
	if err != nil {
		t.Fatal(err)
	}
	meta := pinned.Sessions["claude:s007"]
	rewriteSession(t, proj, "s007")
	parityPass(t, inc, false)
	landed := 0
	sessionReread = func() {
		if landed > 0 {
			return
		}
		landed++
		p := filepath.Join(proj, "s007.jsonl")
		parityWrite(t, p, replaceBody("s007", "wombat "), false)
		later := time.Now().Add(4 * time.Second)
		_ = os.Chtimes(p, later, later)
		parityPass(t, inc, false)
	}
	defer func() { sessionReread = nil }()
	s, ok, err := loadSessionMeta(inc, pinned, meta)
	if err != nil || !ok {
		t.Fatalf("loadSessionMeta: ok=%v err=%v", ok, err)
	}
	if landed != 1 {
		t.Fatalf("control: %d passes landed during the reread, want 1", landed)
	}
	if len(s.Messages) != 6 || !strings.Contains(s.Messages[0].Text, "wombat") {
		t.Fatalf("got %d messages, want the 6 of the last rewrite", len(s.Messages))
	}
}
