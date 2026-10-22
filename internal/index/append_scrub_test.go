package index

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
)

// secretStore is replaceStore with s007's first turn carrying word, rewritten
// without it and indexed again. It returns the index and a copy of the log
// from before the rewrite.
func secretStore(t *testing.T, word string) (inc string, before []byte) {
	t.Helper()
	proj, inc := replaceStore(t, 12)
	p := filepath.Join(proj, "s007.jsonl")
	parityWrite(t, p, replaceBody("s007", word+" "), false)
	later := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(p, later, later)
	parityPass(t, inc, false)
	if !filesHold(t, inc, word) {
		t.Fatalf("control: no index file holds %q before the rewrite", word)
	}
	before, err := os.ReadFile(filepath.Join(inc, "records.bin"))
	if err != nil {
		t.Fatal(err)
	}
	parityWrite(t, p, replaceBody("s007", ""), false)
	later = later.Add(2 * time.Second)
	_ = os.Chtimes(p, later, later)
	inode := indexFiles(t, inc)["records.bin"]
	parityPass(t, inc, false)
	if !os.SameFile(inode, indexFiles(t, inc)["records.bin"]) {
		t.Fatal("the rewrite wrote the log anew: the append path is not under test")
	}
	if m, err := readManifest(inc); err != nil || len(m.DeadRecords) == 0 {
		t.Fatalf("the rewrite listed no dead records (err %v)", err)
	}
	return inc, before
}

// filesHold reports whether any file under dir holds word.
func filesHold(t *testing.T, dir, word string) bool {
	t.Helper()
	found := false
	_ = filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || found {
			return err
		}
		b, err := os.ReadFile(p)
		if err == nil && bytes.Contains(b, []byte(word)) {
			found = true
		}
		return nil
	})
	return found
}

// Text a user took out of a transcript leaves the index's files on the pass
// that replaced it: the record log and the postings both (#5429).
func TestRemovedTextLeavesTheIndexFilesOnTheReplacingPass(t *testing.T) {
	inc, _ := secretStore(t, "hunter2zanzibarpw")
	if filesHold(t, inc, "hunter2zanzibarpw") {
		var where []string
		_ = filepath.Walk(inc, func(p string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() {
				if b, _ := os.ReadFile(p); bytes.Contains(b, []byte("hunter2zanzibarpw")) {
					where = append(where, p)
				}
			}
			return nil
		})
		t.Fatalf("the removed word is still in %v", where)
	}
	sameAsRebuild(t, inc)
	if Damaged(inc) {
		t.Fatalf("damaged: %s", DamageReason(inc))
	}
}

// An older deja meeting this index rebuilds it and carries kept sessions out
// of the log, walking it without the dead list. The replaced records must not
// read as the session's to that walk (#5428).
func TestADeadRecordIsNoSessionsToAWalkThatIgnoresTheDeadList(t *testing.T) {
	inc, _ := secretStore(t, "staleseven")
	m, err := readManifest(inc)
	if err != nil {
		t.Fatal(err)
	}
	blind := tablesFromStrings(m.RecordStrings) // what a format-2 build reads
	dead := tablesFromManifest(m)
	walked := 0
	err = eachStoredRecord(filepath.Join(inc, "records.bin"), blind, func(off int64, payload []byte) error {
		walked++
		r, err := decodeRecord(payload, blind)
		if err != nil {
			return err
		}
		if !dead.isDead(off) {
			return nil
		}
		// What the older carry asks of a record: a transcript it had on
		// file, and a session it had.
		_, file := m.Files[r.SourcePath]
		_, session := m.Sessions[r.Key]
		if file || session || r.Text != "" {
			t.Errorf("dead record at %d reads as %s of %s: %q", off, r.Key, r.SourcePath, r.Text)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if walked <= len(m.DeadRecords) {
		t.Fatalf("walked %d records of a log with %d dead", walked, len(m.DeadRecords))
	}
}

// A pass killed between its swap and the overwrite leaves the mark; the next
// lock taker overwrites every dead record the manifest lists.
func TestALockTakerFinishesAnInterruptedScrub(t *testing.T) {
	inc, before := secretStore(t, "hunter2zanzibarpw")
	log := filepath.Join(inc, "records.bin")
	f, err := os.OpenFile(log, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The log as it was before the overwrite: the old bytes back in place.
	if _, err := f.WriteAt(before, 0); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := os.WriteFile(filepath.Join(inc, scrubPendingFile), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(log); !bytes.Contains(b, []byte("hunter2zanzibarpw")) {
		t.Fatal("control: the restored log does not hold the word")
	}
	unlock, err := lockDir(inc)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	if b, _ := os.ReadFile(log); bytes.Contains(b, []byte("hunter2zanzibarpw")) {
		t.Fatal("the lock taker left the dead records as they were")
	}
	if _, err := os.Stat(filepath.Join(inc, scrubPendingFile)); err == nil {
		t.Error("the mark outlived the overwrite")
	}
	sameAsRebuild(t, inc)
}

// A postings overlay that goes missing is damage, as a missing bucket is
// (#5426).
func TestALostOverlayReadsAsDamage(t *testing.T) {
	proj, inc := replaceStore(t, 12)
	rewriteSession(t, proj, "s003")
	parityPass(t, inc, false)
	entries, err := os.ReadDir(filepath.Join(inc, "buckets"))
	if err != nil {
		t.Fatal(err)
	}
	var overlay string
	for _, e := range entries {
		if isOverlay(e.Name()) {
			overlay = filepath.Join(inc, "buckets", e.Name())
			break
		}
	}
	if overlay == "" {
		t.Fatal("the replacing pass wrote no overlay")
	}
	if Damaged(inc) {
		t.Fatalf("control: damaged before the loss: %s", DamageReason(inc))
	}
	invalidateManifestCache(inc)
	if err := os.Remove(overlay); err != nil {
		t.Fatal(err)
	}
	if got := DamageReason(inc); got != "some postings files are missing" {
		t.Fatalf("DamageReason after losing %s = %q", filepath.Base(overlay), got)
	}
}

// An in-place merge hands back the overlays it made redundant rather than
// removing them: the caller commits a count without them first, so a reader's
// damage check never finds fewer files than the manifest it read (#5426).
func TestAnInPlaceMergeLeavesItsRedundantOverlaysToTheCaller(t *testing.T) {
	d := t.TempDir()
	base := filepath.Join(d, "ab.bin")
	layer := func(n, from int) map[string][]posting {
		out := map[string][]posting{}
		for i := 0; i < n; i++ {
			out["ab"+strings.Repeat("x", i%7)+string(rune('a'+i%26))+strings.Repeat("y", i/26)] = []posting{{Off: int64(from + i), Sid: 1}}
		}
		return out
	}
	for i, data := range []map[string][]posting{layer(4000, 0), layer(200, 10000), layer(20, 20000), layer(12, 30000)} {
		p := base
		if i > 0 {
			p = overlayPath(base, i)
		}
		if err := writeBucket(p, data); err != nil {
			t.Fatal(err)
		}
	}
	files := countBucketFiles(d)
	stale, err := addOverlays(d, d, bucketPostings{"ab": layer(10, 40000)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if staleCount(stale) == 0 {
		t.Fatal("control: the add merged nothing away")
	}
	if got := countBucketFiles(d); got < files {
		t.Fatalf("the merge removed files itself: %d before, %d after", files, got)
	}
	for _, g := range stale {
		for _, p := range g {
			if _, err := os.Stat(p); err != nil {
				t.Fatalf("%s went before the caller committed: %v", filepath.Base(p), err)
			}
		}
	}
	dropStale(stale)
	if got := countBucketFiles(d); got != files+1-staleCount(stale) && got != files-staleCount(stale) {
		t.Fatalf("after the drop: %d files", got)
	}
	data, err := readBucket(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 4000 {
		t.Fatalf("the bucket and its overlays hold %d words after the merge", len(data))
	}
}

// On Windows a reader holding the live log keeps the parked copy of the last
// index, which shares it, from going. The next swap parks one name further
// rather than failing, and recovery restores the newest copy.
func TestASwapParksPastACopyAReaderHolds(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "index.db")
	for _, d := range []string{dir, dir + ".old", dir + ".next"} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "id"), []byte(filepath.Base(d)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	prevOS, prevRemove := renameOS, removeParked
	renameOS = "windows"
	removeParked = func(p string) error {
		if p == dir+".old" {
			return &os.PathError{Op: "remove", Path: p, Err: syscall.Errno(32)}
		}
		return os.RemoveAll(p)
	}
	defer func() { renameOS, removeParked = prevOS, prevRemove }()
	if err := swapIndexDir(dir, dir+".next"); err != nil {
		t.Fatalf("the swap failed on a held parking spot: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "id")); string(b) != "index.db.next" {
		t.Fatalf("live index is %q", b)
	}
	// The swap's own copy went once it was done with; the held one stays.
	if _, err := os.Stat(dir + ".old.1"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the copy this swap parked is still there: %v", err)
	}
	// A crash between the two renames: the newest copy is the one put back.
	if err := os.Rename(dir, dir+".old.1"); err != nil {
		t.Fatal(err)
	}
	recoverIndexDir(dir)
	if b, _ := os.ReadFile(filepath.Join(dir, "id")); string(b) != "index.db.next" {
		t.Fatalf("recovery restored %q, want the newest copy", b)
	}
	// Not a reader: the message #1009 asked for still comes.
	removeParked = func(p string) error {
		return &os.PathError{Op: "remove", Path: p, Err: syscall.EPERM}
	}
	if err := os.MkdirAll(dir+".next", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := swapIndexDir(dir, dir+".next"); err == nil || !strings.Contains(err.Error(), "remove that directory") {
		t.Fatalf("a parking spot held by permissions: err = %v", err)
	}
}

// A reader that read the manifest before a replacing pass swapped in, and the
// records after the pass zeroed the ones it replaced, gets the session as the
// new manifest has it, not an empty one.
func TestAReaderHoldingTheLastManifestStillGetsAReplacedSession(t *testing.T) {
	proj, inc := replaceStore(t, 12)
	pinned, err := readManifest(inc)
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := pinned.Sessions["claude:s007"]
	if !ok {
		t.Fatal("control: no claude:s007 in the manifest")
	}
	rewriteSession(t, proj, "s007")
	parityPass(t, inc, false)
	if m, err := readManifest(inc); err != nil || len(m.DeadRecords) == 0 {
		t.Fatalf("control: the rewrite listed no dead records (err %v)", err)
	}
	s, ok, err := loadSessionMeta(inc, pinned, meta)
	if err != nil || !ok {
		t.Fatalf("loadSessionMeta: ok=%v err=%v", ok, err)
	}
	if len(s.Messages) != 6 || !strings.Contains(s.Messages[0].Text, "quokka") {
		t.Fatalf("a reader on the last manifest got %d messages, want the 6 of the rewrite", len(s.Messages))
	}
}

// `deja index` on an index that looks up to date still finishes a scrub a
// killed pass left, and clears the parked copy it left: it returns before
// Ensure takes the lock, so nothing else would.
func TestTheUpToDatePathFinishesAnInterruptedScrub(t *testing.T) {
	inc, before := secretStore(t, "hunter2zanzibarpw")
	log := filepath.Join(inc, "records.bin")
	f, err := os.OpenFile(log, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(before, 0); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := os.WriteFile(filepath.Join(inc, scrubPendingFile), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(inc+".old", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inc+".old", "sessions.gob"), []byte("hunter2zanzibarpw"), 0o600); err != nil {
		t.Fatal(err)
	}
	invalidateManifestCache(inc)
	if fresh, _ := UpToDate(inc, ""); !fresh {
		t.Fatal("control: the index does not read as up to date")
	}
	SweepStaleTmp(inc)
	if b, _ := os.ReadFile(log); bytes.Contains(b, []byte("hunter2zanzibarpw")) {
		t.Error("the removed word is still in records.bin")
	}
	if _, err := os.Stat(inc + ".old"); err == nil {
		t.Error("the parked copy is still there")
	}
	if _, err := os.Stat(filepath.Join(inc, scrubPendingFile)); err == nil {
		t.Error("the mark is still there")
	}
}

// A pass killed after it zeroed the records and before it moved the
// sidecars' fingerprint leaves them naming the log as it was. The lock taker
// that finishes the scrub moves them, so they keep answering.
func TestFinishingAScrubRestampsTheSidecarsAKilledPassLeft(t *testing.T) {
	inc, before := secretStore(t, "hunter2zanzibarpw")
	log := filepath.Join(inc, "records.bin")
	_, after, err := logFingerprint(log)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(log, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(before, 0); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	// The state just before the scrub: the log unzeroed, the sidecars
	// stamped for it, the mark as the pass writes it.
	_, pre, err := logFingerprint(log)
	if err != nil {
		t.Fatal(err)
	}
	restamp(filepath.Join(inc, recordOffsetsFile), after, pre)
	if err := markScrubPending(inc); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(inc)
	if err != nil {
		t.Fatal(err)
	}
	// Killed here: the records zeroed, the sidecars not yet moved.
	if err := scrubRecords(log, m.DeadRecords); err != nil {
		t.Fatal(err)
	}
	unlock, err := lockDir(inc)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	lf, err := os.Open(log)
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	if _, err := readRecordOffsetsFor(inc, lf, nil); err != nil {
		t.Fatalf("recoffsets.bin no longer matches the log: %v", err)
	}
}

// A CJK word is posted as bigrams, each of whose bucket is named from a hash
// of the whole key. Removed, none of its bigrams stays readable.
func TestRemovedCJKTextLeavesThePostings(t *testing.T) {
	inc, _ := secretStore(t, "麒麟獬豸")
	for _, pair := range []string{"麒麟", "麟獬", "獬豸"} {
		if filesHold(t, filepath.Join(inc, "buckets"), pair) {
			t.Errorf("the bigram %s of the removed word is still in buckets/", pair)
		}
	}
	sameAsRebuild(t, inc)
}

// A blanked word stays in its bucket and keeps nothing the bucket's name
// does not already say: the two letters an ASCII bucket is named after, and
// none of a word filed under a hash.
func TestBlankedWordKeepsOnlyTheBucketName(t *testing.T) {
	if n := len(hashedFillers()); n != len(hexBuckets) {
		t.Fatalf("fillers for %d of %d hashed buckets", n, len(hexBuckets))
	}
	for _, tok := range []string{"t麒麟", "t麟", "да", "éa", "привет", "zanzibar", "naïve", "я"} {
		b, ok := blankedWord(tok)
		if !ok {
			if len(tok) > 2 {
				t.Errorf("%q: not blanked", tok)
			}
			continue
		}
		if len(b) != len(tok) || bucket(string(b)) != bucket(tok) {
			t.Errorf("%q: blanked to %q, bucket %s, want %s", tok, b, bucket(string(b)), bucket(tok))
		}
		keep := 0
		if bucket(tok) == safe(tok[:2]) {
			keep = 2
		}
		if !bytes.Equal(b[:keep], []byte(tok[:keep])) {
			t.Errorf("%q: blanked to %q, which moved the bucket's letters", tok, b)
		}
		for _, r := range tok[keep:] {
			if r >= utf8.RuneSelf && bytes.Contains(b, []byte(string(r))) || r < utf8.RuneSelf && keep > 0 && bytes.IndexByte(b[keep:], byte(r)) >= 0 {
				t.Errorf("%q: blanked to %q, which still holds %q", tok, b, r)
			}
		}
	}
}
