package index

import (
	"encoding/binary"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/query"
)

// threeWordStore builds three sessions, one distinctive word each, and
// returns the env and the index dir.
func threeWordStore(t *testing.T) (*twoWayEnv, string) {
	t.Helper()
	h := newTwoWayEnv(t)
	h.put("app", "s1", hPrompt("app", "s1", 2, 0, "alphaword question about caching")+hSay("app", "s1", 2, 1, "alphaword answer"))
	h.put("app", "s2", hPrompt("app", "s2", 3, 0, "bravoword question about pools")+hSay("app", "s2", 3, 1, "bravoword answer"))
	h.put("app", "s3", hPrompt("app", "s3", 4, 0, "charlieword question about locks")+hSay("app", "s3", 4, 1, "charlieword answer"))
	dir := filepath.Join(h.tmp, "idx.db")
	if err := Ensure(dir, "claude", true, nil); err != nil {
		t.Fatal(err)
	}
	return h, dir
}

// zeroRecordContaining zeroes the whole frame (header + payload) of the first
// record whose text contains word: the hole a crash on a writeback filesystem
// leaves. File length is unchanged, so the manifest size check passes.
func zeroRecordContaining(t *testing.T, dir, word string) {
	t.Helper()
	recs, err := ReadRecords(dir)
	if err != nil {
		t.Fatal(err)
	}
	rp := filepath.Join(dir, "records.bin")
	b, err := os.ReadFile(rp)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if strings.Contains(r.Record.Text, word) {
			n := int(binary.LittleEndian.Uint32(b[r.Offset:]))
			for i := r.Offset; i < r.Offset+4+int64(n); i++ {
				b[i] = 0
			}
			if err := os.WriteFile(rp, b, 0o600); err != nil {
				t.Fatal(err)
			}
			invalidateManifestCache(dir)
			return
		}
	}
	t.Fatalf("no record with %q", word)
}

// A zeroed frame in the middle of records.bin is corruption: the search for a
// word in that record says so, so the recovery path rebuilds, instead of
// answering nothing.
func TestAZeroedRecordFailsTheSearchInsteadOfAnsweringNothing(t *testing.T) {
	_, dir := threeWordStore(t)
	zeroRecordContaining(t, dir, "bravoword question")
	if _, err := Search(dir, query.Options{Query: "pools", Limit: 10}); !IsCorrupt(err) {
		t.Fatalf("search over a zeroed record answered err=%v; want a corrupt-index error", err)
	}
}

// A zeroed frame is not the end of the log: the walkers (deep verify counts,
// the rewrite pass's carry-over, secrets, sync push) used to stop there and
// skip every record after it without a word.
func TestARecordWalkDoesNotEndAtAHoleInTheLog(t *testing.T) {
	_, dir := threeWordStore(t)
	zeroRecordContaining(t, dir, "bravoword question")
	tbl, err := loadRecordTables(dir)
	if err != nil {
		t.Fatal(err)
	}
	var sawCharlie bool
	err = eachRecord(filepath.Join(dir, "records.bin"), tbl, func(r Record) {
		if strings.Contains(r.Text, "charlieword") {
			sawCharlie = true
		}
	})
	if !IsCorrupt(err) || sawCharlie {
		t.Fatalf("eachRecord over a mid-log zeroed frame: err=%v, read past it %v; want corruption at the hole", err, sawCharlie)
	}
}

// The rewrite pass carries old records through eachRecord. Over a hole it
// used to keep what came before and commit the loss of the rest; it rebuilds
// instead.
func TestAPassOverAHoleKeepsTheSessionsAfterIt(t *testing.T) {
	h, dir := threeWordStore(t)
	zeroRecordContaining(t, dir, "bravoword question")
	// Rewrite s1's file in place: not an append, so the log is rewritten.
	h.put("app", "s1", hPrompt("app", "s1", 2, 0, "deltaword replaced content entirely")+hSay("app", "s1", 2, 1, "deltaword"))
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Sessions) != 3 {
		t.Errorf("the pass over the hole left %d sessions, want 3", len(m.Sessions))
	}
	for _, word := range []string{"bravoword", "charlieword", "deltaword"} {
		got, err := Search(dir, query.Options{Query: word, Limit: 10})
		if err != nil || len(got) == 0 {
			t.Errorf("%s after the pass: %d hits, err=%v", word, len(got), err)
		}
	}
}

// DeepVerify (deja doctor --deep) must notice a zeroed frame.
func TestDeepVerifyFindsAZeroedRecord(t *testing.T) {
	_, dir := threeWordStore(t)
	zeroRecordContaining(t, dir, "bravoword question")
	rep, err := DeepVerify(dir)
	if err != nil {
		return
	}
	if rep.Clean() {
		t.Fatalf("deep verify reports a store with a zeroed mid-log record as clean: %+v", rep)
	}
}

// A pass killed after it appended records (and postings) but before it
// committed the manifest leaves records.bin longer than RecordsSize. A writer
// that only rewrites the manifest (sync push watermarks, forget's compaction
// removal, AdoptWriteBack) used to re-stamp RecordsSize from stat and adopt
// that tail, and the next pass appended the same turns again.
func TestAManifestOnlyWriteKeepsAnUncommittedTailUncommitted(t *testing.T) {
	h, dir := threeWordStore(t)
	saved := map[string][]byte{}
	for _, n := range []string{"manifest.gob", "sessions.gob"} {
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			t.Fatal(err)
		}
		saved[n] = b
	}
	h.add("app", "s3", hSay("app", "s3", 4, 2, "echoword appended turn"))
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	// Simulate kill -9 between the record/bucket writes and writeManifest.
	for n, b := range saved {
		if err := os.WriteFile(filepath.Join(dir, n), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	invalidateManifestCache(dir)
	// Any manifest-only writer under the lock, e.g. AdoptWriteBack(kept==path).
	if _, err := AdoptWriteBack(dir, h.path("app", "s1"), h.path("app", "s1")); err != nil {
		t.Fatal(err)
	}
	invalidateManifestCache(dir)
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	recs, err := ReadRecords(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range recs {
		if strings.Contains(r.Record.Text, "echoword") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("appended turn stored %d times after crash + manifest-only write + pass; want 1", n)
	}
}

// An index written by a newer deja (binary rolled back, or two installs side
// by side) answers as it is and is left alone. It used to be rebuilt down to
// this build's version, with a line saying this build was the newer one.
func TestANewerIndexIsLeftAlone(t *testing.T) {
	_, dir := threeWordStore(t)
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.Version = version + 1
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	invalidateManifestCache(dir)
	if ReadStateOf(dir) != ReadStateNewer {
		t.Fatalf("setup: want ReadStateNewer")
	}
	var out strings.Builder
	if err := Ensure(dir, "claude", false, &out); err != nil {
		t.Fatal(err)
	}
	after, _ := readManifest(dir)
	if strings.Contains(out.String(), "this build reads a newer index than the one on disk") {
		t.Errorf("message claims this build is newer, but the index on disk (%d) is newer than the build (%d)", version+1, version)
	}
	if !strings.Contains(out.String(), "written by a newer deja") {
		t.Errorf("the pass did not say why it left the index alone: %q", out.String())
	}
	if after.Version != version+1 {
		t.Errorf("a newer index was rebuilt down to version %d without being asked", after.Version)
	}
	if got, err := Search(dir, query.Options{Query: "alphaword", Limit: 10}); err != nil || len(got) == 0 {
		t.Errorf("the newer index does not answer: %d hits, err=%v", len(got), err)
	}
	// Asked for, the rebuild still happens.
	if err := Ensure(dir, "claude", true, nil); err != nil {
		t.Fatal(err)
	}
	if after, _ := readManifest(dir); after.Version != version {
		t.Errorf("a forced rebuild left version %d, want %d", after.Version, version)
	}
}

// A torn fixes.gob (crash on a filesystem without ordered data, a partial
// copy) used to read as "no pairs", and the next pass wrote back only the pairs
// it had just mined. It is mined again from the records.
func TestATornFixTableIsMinedAgain(t *testing.T) {
	h := newTwoWayEnv(t)
	h.put("app", "s1", richSession("app", "s1", 2, "pool"))
	h.put("app", "s2", hPrompt("app", "s2", 3, 0, "first question about caching"))
	dir := filepath.Join(h.tmp, "idx.db")
	if err := Ensure(dir, "claude", true, nil); err != nil {
		t.Fatal(err)
	}
	before := len(ReadFixes(dir))
	if before == 0 {
		t.Fatal("setup: no fix pairs mined")
	}
	fp := filepath.Join(dir, fixesFile)
	b, err := os.ReadFile(fp)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fp, b[:len(b)/2], 0o600); err != nil {
		t.Fatal(err)
	}
	h.add("app", "s2", hSay("app", "s2", 3, 1, "caching answer"))
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	if after := len(ReadFixes(dir)); after < before {
		t.Fatalf("a torn fixes.gob lost %d fix pairs for good", before-after)
	}
}

// A posting block whose bytes were zeroed (same length, directory intact)
// decodes to offset 0 again and again. That resolved to another session's
// record and was dropped, so the search answered "no matches"; it is
// corruption.
func TestAZeroedPostingBlockIsCorruption(t *testing.T) {
	_, dir := threeWordStore(t)
	files, _ := filepath.Glob(filepath.Join(dir, "buckets", "*.bin"))
	hit := false
	for _, p := range files {
		entries, f, err := openBucketDir(p)
		if err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
		for _, e := range entries {
			if !strings.Contains(e.tok, "charlieword") {
				continue
			}
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			for i := e.off; i < e.off+uint64(e.n); i++ {
				b[i] = 0
			}
			if err := os.WriteFile(p, b, 0o600); err != nil {
				t.Fatal(err)
			}
			hit = true
		}
	}
	if !hit {
		t.Fatal("setup: no charlieword token")
	}
	got, err := Search(dir, query.Options{Query: "charlieword", Limit: 10})
	if IsCorrupt(err) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range got {
		for _, m := range s.Messages {
			if !strings.Contains(m.Text, "charlieword") {
				t.Errorf("search for charlieword answered with session %s message %q and no error", s.ID, m.Text)
			}
		}
	}
	if len(got) == 0 {
		t.Errorf("search for charlieword returned nothing and no error over a zeroed posting block")
	}
}

// Lock-free searches racing forced rebuilds whose session set changes: every
// hit must be the session that holds the word. A reader that takes the
// manifest of one generation and the records/postings of the next resolves
// interned ids through the wrong table.
func TestASearchRacingRebuildsKeepsEveryHitInItsSession(t *testing.T) {
	h := newTwoWayEnv(t)
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("s%02d", i)
		h.put("app", id, hPrompt("app", id, 2+i%20, 0, fmt.Sprintf("sessword%02d lives only here", i)))
	}
	dir := filepath.Join(h.tmp, "idx.db")
	if err := Ensure(dir, "claude", true, nil); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for k := 0; ; k++ {
			select {
			case <-stop:
				return
			default:
			}
			// a new session that sorts first shifts every interned id
			id := fmt.Sprintf("a%04d", k)
			h.put("app", id, hPrompt("app", id, 1, 0, fmt.Sprintf("newcomer%04d text", k)))
			_ = Ensure(dir, "claude", true, nil)
		}
	}()
	bad, errs, empty, n := 0, 0, 0, 0
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		i := n % 20
		n++
		want := fmt.Sprintf("s%02d", i)
		got, err := Search(dir, query.Options{Query: fmt.Sprintf("sessword%02d", i), Limit: 5})
		if err != nil {
			errs++
			if errs < 3 {
				t.Logf("search error: %v", err)
			}
			continue
		}
		for _, s := range got {
			ok := s.ID == want
			for _, m := range s.Messages {
				if !strings.Contains(m.Text, fmt.Sprintf("sessword%02d", i)) {
					ok = false
				}
			}
			if !ok {
				bad++
				if bad < 5 {
					t.Errorf("query sessword%02d answered session %s with %d messages: %+v", i, s.ID, len(s.Messages), s.Messages)
				}
			}
		}
		if len(got) == 0 {
			empty++
		}
	}
	close(stop)
	<-done
	t.Logf("%d searches, %d misattributed, %d errors, %d empty without error", n, bad, errs, empty)
}

// One opencode session stamped ahead of the clock (a skewed machine, a synced
// store) set the store watermark in the future, and turns written after it at
// the real time sat below it unread.
func TestASessionAheadOfTheClockDoesNotFreezeTheStoreMark(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_GOOSE_DB", filepath.Join(tmp, "none-goose.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	db := filepath.Join(tmp, "opencode.db")
	t.Setenv("DEJA_OPENCODE_DB", db)
	run := func(sql string) {
		t.Helper()
		if out, err := exec.Command("sqlite3", db, sql).CombinedOutput(); err != nil {
			t.Fatalf("sqlite3: %v %s", err, out)
		}
	}
	now := time.Now().UnixMilli()
	fut := now + 24*3600*1000
	run(fmt.Sprintf(`create table session(id text primary key, directory text, title text, time_created integer, time_updated integer);
create table message(id text primary key, session_id text, time_created integer, time_updated integer, data text);
create table part(id text primary key, message_id text, session_id text, time_created integer, time_updated integer, data text);
insert into session values('s1','/w/app','skewed',%[1]d,%[1]d);
insert into message values('m1','s1',%[1]d,%[1]d,'{"role":"user","time":{"created":%[1]d}}');
insert into part values('p1','m1','s1',%[1]d,%[1]d,'{"type":"text","text":"skewword from a machine ahead of time"}');`, fut))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if m, _ := readManifest(dir); m.Files[db].LastUpdated > time.Now().UnixNano() {
		t.Errorf("the store mark sits %v ahead of the clock", time.Until(time.Unix(0, m.Files[db].LastUpdated)))
	}
	n := now + 1000
	run(fmt.Sprintf(`insert into session values('s2','/w/app','fresh',%[1]d,%[1]d);
insert into message values('m2','s2',%[1]d,%[1]d,'{"role":"user","time":{"created":%[1]d}}');
insert into part values('p2','m2','s2',%[1]d,%[1]d,'{"type":"text","text":"punctualword written now"}');`, n))
	later := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(db, later, later)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	hits, err := Search(dir, query.Options{Query: "punctualword", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatalf("a session written now never reached the index: the store watermark sits a day ahead")
	}
	// The session ahead of the clock is above the capped mark on every pass;
	// reading it again must not store it again.
	for i := 0; i < 2; i++ {
		later = later.Add(2 * time.Second)
		_ = os.Chtimes(db, later, later)
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
	}
	recs, rerr := ReadRecords(dir)
	if rerr != nil {
		t.Fatal(rerr)
	}
	stored := 0
	for _, r := range recs {
		if strings.Contains(r.Record.Text, "skewword") {
			stored++
		}
	}
	if stored != 1 {
		t.Errorf("the session stamped ahead is stored %d times after three passes, want 1", stored)
	}
}

// A filesystem mounted read-only answers EROFS, not a permission error, and
// the read-only fallbacks only knew the second: search and MCP recall failed
// outright on a mount a chmod'ed directory would have answered from.
func TestAReadOnlyMountIsUnwritable(t *testing.T) {
	for _, err := range []error{
		&fs.PathError{Op: "open", Path: "index.db.lock", Err: syscall.EROFS},
		&fs.PathError{Op: "open", Path: "index.db.lock", Err: fs.ErrPermission},
	} {
		if !Unwritable(err) {
			t.Errorf("Unwritable(%v) = false", err)
		}
	}
	if Unwritable(&fs.PathError{Op: "open", Path: "index.db.lock", Err: syscall.ENOENT}) {
		t.Error("a missing file reads as an unwritable index")
	}
}
