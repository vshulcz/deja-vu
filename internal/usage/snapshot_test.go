package usage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// The rotation keeps a fixed count, so the size of one record is what decides
// whether the file stays under its threshold — and the budgets that set that
// size live in packages this one cannot import. `RecordRoom` is the line
// between them; `cmd/deja` holds the budgets to it.
//
// Here: twenty records at the largest budget today fit, which is what the line
// is for. Above it the file sits over the threshold for good and half of two
// concurrent injections is rewritten away (#1971).
func TestTwentyOfTheLargestAnswerStillFit(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("b", 8192)
	for range snapshotsToKeep {
		RecordDigestPolicy(dir, KindBlame, big, 1, 0, "local-only")
	}
	if got := len(Snapshots(dir, 0)); got != snapshotsToKeep {
		t.Errorf("the log holds %d of %d records at the largest budget", got, snapshotsToKeep)
	}
}

// The reader caps a line at 4 MB and nothing capped the write, so a digest past
// that was written and then unreadable: `deja log` showed nothing, and the next
// rotation rewrote the file without it. A record deja cannot read is worse than
// a record it clipped (#2222).
func TestADigestTooLargeToReadBackIsClippedRatherThanLost(t *testing.T) {
	for _, size := range []int{8 << 10, 100 << 10, snapshotDigestMax, snapshotDigestMax + 1, 5 << 20, 16 << 20} {
		dir := t.TempDir()
		digest := strings.Repeat("z", size)
		RecordDigestTerms(dir, KindResource, digest, 1, 0, nil, "s1")

		got := snapshotsFrom(SnapshotPath(dir), 0)
		if len(got) != 1 {
			fi, _ := os.Stat(SnapshotPath(dir))
			t.Errorf("a %d B digest wrote %d bytes and read back as %d records", size, fi.Size(), len(got))
			continue
		}
		// What survives is the head of what was served, so the log still says
		// what the agent was given.
		if !strings.HasPrefix(got[0].Digest, strings.Repeat("z", 4096)) {
			t.Errorf("a %d B digest came back as something else: %.80q", size, got[0].Digest)
		}
		if size <= snapshotDigestMax {
			// Anything inside the budget is kept whole, and everything deja
			// actually injects is far inside it.
			if got[0].Digest != digest {
				t.Errorf("a %d B digest was clipped to %d, though it is within the budget", size, len(got[0].Digest))
			}
			continue
		}
		// The kept head is inside the budget. The record itself is a little
		// longer than the head, because it says it was clipped.
		head := got[0].Digest[:strings.LastIndex(got[0].Digest, "\n… (clipped")]
		if len(head) > snapshotDigestMax {
			t.Errorf("a %d B digest kept %d bytes, over the %d budget", size, len(head), snapshotDigestMax)
		}
		if len(head) >= size {
			t.Errorf("a %d B digest was not clipped: %d bytes kept", size, len(head))
		}
		// And the clipping says so, rather than ending mid-sentence.
		if !strings.Contains(got[0].Digest, "clipped") {
			t.Errorf("a clipped digest does not say it was clipped: %.120q", got[0].Digest[len(got[0].Digest)-120:])
		}
		// Bytes is what was served, not what was kept: the log is about the
		// injection, and clipping is this file's business.
		if got[0].Bytes != size {
			t.Errorf("bytes = %d, want the %d that were served", got[0].Bytes, size)
		}
	}
}

// The shapes that make escaping expensive, and the cut itself. A digest of
// newlines doubles in JSON, invalid bytes triple, and a cut in the middle of a
// rune is a mojibake tail on the last line someone reads (#2222).
func TestClippingSurvivesTheShapesThatCostMost(t *testing.T) {
	for _, c := range []struct {
		name   string
		digest string
	}{
		{"newlines, which double", strings.Repeat("\n", 6<<20)},
		{"quotes, which double", strings.Repeat(`"`, 6<<20)},
		{"invalid bytes, which triple", strings.Repeat(string([]byte{0xff}), 6<<20)},
		{"multi-byte runes", strings.Repeat("é", 3<<20)},
		{"one long rune run", strings.Repeat("🙂", 2<<20)},
		// One byte in front, so the budget falls inside a rune rather than
		// between two: this is what backs the cut up.
		{"a rune the cut lands inside", "a" + strings.Repeat("日", 1<<20)},
	} {
		dir := t.TempDir()
		RecordDigestTerms(dir, KindResource, c.digest, 1, 0, nil, "s1")
		got := snapshotsFrom(SnapshotPath(dir), 0)
		if len(got) != 1 {
			fi, _ := os.Stat(SnapshotPath(dir))
			t.Errorf("%s: wrote %d bytes and read back %d records", c.name, fi.Size(), len(got))
			continue
		}
		if !strings.Contains(got[0].Digest, "clipped") {
			t.Errorf("%s: not clipped, %d bytes came back", c.name, len(got[0].Digest))
		}
		// The kept head is still the text that was served, decodable as it was
		// written rather than ending in half a character.
		head := strings.TrimSuffix(got[0].Digest, got[0].Digest[strings.LastIndex(got[0].Digest, "\n… (clipped"):])
		if !utf8.ValidString(head) && utf8.ValidString(c.digest) {
			t.Errorf("%s: the kept head is not valid UTF-8 though the digest was", c.name)
		}
	}
}

// A digest is kilobytes, not the ~100 bytes a usage event is, and the injection
// log takes the whole record in one Write on an append descriptor with no lock.
// The question is whether two agents starting together can interleave two of
// them into a line that parses as neither — the shape #1319 was about, and the
// one the reader's own filtering would hide rather than report.
//
// They cannot here: one write of a whole record reaches the file whole, on the
// systems this runs on. Not a guarantee — os.File.Write loops on short writes,
// and a network filesystem promises nothing about an append — which is why this
// is measured rather than assumed.
//
// Sizes stay under the rotation threshold on purpose: past it, a rotation
// rewrites the file from the lines the reader accepts and would delete the torn
// line before the assertion ever saw it (#1971).
func TestConcurrentWritersNeverInterleaveASnapshot(t *testing.T) {
	for _, size := range []int{200, 4096, 64 << 10} {
		t.Run(fmt.Sprintf("%d bytes", size), func(t *testing.T) {
			dir := t.TempDir()
			// writers × each × size stays well under snapshotRotateAt, so no
			// rotation can delete the evidence.
			writers, each := 16, 8
			switch {
			case size > 4096:
				writers, each = 4, 2
			case size > 200:
				writers, each = 8, 4
			}
			var wg sync.WaitGroup
			for w := range writers {
				wg.Add(1)
				go func(w int) {
					defer wg.Done()
					body := strings.Repeat(string(rune('a'+w)), size) //nolint:gosec // w is under eight
					for range each {
						RecordDigestPolicy(dir, KindHook, body, 1, 0, "local-only")
					}
				}(w)
			}
			wg.Wait()

			f, err := os.Open(SnapshotPath(dir))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = f.Close() }()
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
			lines := 0
			for sc.Scan() {
				// Two writers can each close the same truncated tail, and each
				// prepends its own newline (#1967): the blank line between them
				// is not a record, and every reader of this file skips it.
				if len(bytes.TrimSpace(sc.Bytes())) == 0 {
					continue
				}
				lines++
				var s Snapshot
				if err := json.Unmarshal(sc.Bytes(), &s); err != nil {
					t.Fatalf("line %d holds no whole record: %v", lines, err)
				}
				// One writer's letter, repeated, and nothing else: a record
				// carrying part of another's would still parse.
				if len(s.Digest) != size || strings.Count(s.Digest, s.Digest[:1]) != size {
					t.Fatalf("line %d holds %d bytes of mixed digest", lines, len(s.Digest))
				}
			}
			if err := sc.Err(); err != nil {
				t.Fatal(err)
			}
			if lines != writers*each {
				t.Fatalf("file holds %d lines, wrote %d — a rotation would hide a torn line by deleting it", lines, writers*each)
			}
		})
	}
}

// Above the cliff every write rotates, and every record appended while a
// rotation rebuilds is rewritten away. Twenty records of 26 kB or more exceed
// the 512 kB threshold, so the file never drops back under it — and a whole
// session read through `deja://session/…` is recorded here unbudgeted, which is
// how a real store gets there (#1971).
func TestARotationLeavesTheFileUnderItsOwnThreshold(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("y", 40<<10)
	for range snapshotsToKeep + 4 {
		RecordDigestPolicy(dir, KindHook, big, 1, 0, "local-only")
	}
	fi, err := os.Stat(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() >= snapshotRotateAt {
		t.Errorf("the log is %d bytes after a rotation, over its own %d threshold: every write from here rotates",
			fi.Size(), snapshotRotateAt)
	}
}

// And the newest digest survives the rotation that shrinks the file, which is
// the record `deja log --last` exists to show.
func TestTheNewestDigestSurvivesARotationOfBigRecords(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("y", 40<<10)
	for range snapshotsToKeep + 4 {
		RecordDigestPolicy(dir, KindHook, big, 1, 0, "local-only")
	}
	RecordDigestPolicy(dir, KindHook, "the one just served", 1, 0, "local-only")

	got := Snapshots(dir, 0)
	if len(got) == 0 {
		t.Fatal("the log is empty")
	}
	if got[0].Digest != "the one just served" {
		t.Errorf("newest digest is %d bytes of the wrong record", len(got[0].Digest))
	}
}

// oneSnapshot writes a snapshots file holding exactly the given line.
func oneSnapshot(t *testing.T, line string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)
	p := SnapshotPath(dir)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The usage log asks one question of a line since #1917 — a stamp and a kind.
// The snapshots file beside it asked a different one, so `deja log --last`
// could print a heading with no kind, or a digest dated the year 1 (#1946).
// Both are things deja cannot write: every writer passes a kind, and the stamp
// is time.Now().
func TestASnapshotNeedsAStampAKindAndADigest(t *testing.T) {
	for _, c := range []struct {
		name, line string
		kept       bool
	}{
		{"whole", `{"t":"2026-08-20T10:00:00Z","kind":"hook","bytes":100,"sessions":1,"digest":"earlier work"}`, true},
		{"unknown kind", `{"t":"2026-08-20T10:00:00Z","kind":"weather","bytes":100,"digest":"earlier work"}`, true},
		{"no digest", `{"t":"2026-08-20T10:00:00Z","kind":"hook","bytes":100,"sessions":1}`, false},
		{"no stamp", `{"kind":"hook","bytes":100,"sessions":1,"digest":"earlier work"}`, false},
		{"no kind", `{"t":"2026-08-20T10:00:00Z","bytes":100,"sessions":1,"digest":"earlier work"}`, false},
		{"blank line", ``, false},
	} {
		got := Snapshots(oneSnapshot(t, c.line), 0)
		want := 0
		if c.kept {
			want = 1
		}
		if len(got) != want {
			t.Errorf("%s: kept %d, want %d", c.name, len(got), want)
		}
	}
}

// And what deja writes is still read back whole — the rule must not tighten
// into "a kind I know".
func TestAWrittenSnapshotSurvivesTheRule(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)
	SnapshotPolicy(dir, KindHook, "a digest of earlier work", 2, "local-only")

	got := Snapshots(dir, 0)
	if len(got) != 1 {
		t.Fatalf("wrote one snapshot, read %d", len(got))
	}
	if got[0].Kind != KindHook || got[0].Digest == "" || got[0].Time.IsZero() {
		t.Errorf("the snapshot came back missing something: %#v", got[0])
	}
}

// A process killed between a snapshot record and its newline costs that record.
// It cost every later one too: the next digest was appended onto the partial
// line, so one line held two objects and parsed as neither, and `deja log
// --last` reported nothing on a machine that had just served two (#1965). The
// usage log closed this in #1901; its sibling had not.
func TestASnapshotAfterAnInterruptedWriteIsStillReadable(t *testing.T) {
	dir := t.TempDir()
	RecordDigestPolicy(dir, KindHook, "the first digest", 2, 4000, "local-only")

	b, err := os.ReadFile(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(SnapshotPath(dir), b[:len(b)-1], 0o600); err != nil {
		t.Fatal(err)
	}

	RecordDigestPolicy(dir, KindHook, "the second digest", 2, 4000, "local-only")

	got := Snapshots(dir, 0)
	if len(got) == 0 {
		t.Fatal("one interrupted write lost every digest in the file")
	}
	if got[0].Digest != "the second digest" {
		t.Errorf("newest digest is %q, want the one just served", got[0].Digest)
	}
	// The interrupted record survives too: what the kill cost it was the
	// newline, not the JSON, and the write after it no longer takes it down.
	if len(got) != 2 {
		t.Errorf("readable snapshots: %d, want both", len(got))
	}
}

// The same file, rotated by the write that follows a partial tail. Rotation
// rewrites from what the reader accepts, so it is the second place a
// half-written line could take the rest of the file with it — and it turns out
// to be the one place that already healed it: the rewrite terminates every
// line, so the write after a rotation never glued anything. A guard on a path
// that was right, not a repeat of the one above.
func TestRotationSurvivesAPartialTail(t *testing.T) {
	dir := t.TempDir()
	big := make([]byte, 4096)
	for i := range big {
		big[i] = 'x'
	}
	// Past the rotation threshold, measured rather than assumed: 300 records of
	// this size sat under it, and the test then rotated nothing.
	for {
		RecordDigestPolicy(dir, KindHook, string(big), 2, 4000, "local-only")
		fi, err := os.Stat(SnapshotPath(dir))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Size() >= 512<<10 {
			break
		}
	}
	b, err := os.ReadFile(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(SnapshotPath(dir), b[:len(b)-1], 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}

	RecordDigestPolicy(dir, KindHook, "after the rotation", 2, 4000, "local-only")

	after, err := os.Stat(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() >= before.Size() {
		t.Fatalf("no rotation happened: %d bytes before, %d after", before.Size(), after.Size())
	}
	got := Snapshots(dir, 0)
	if len(got) == 0 {
		t.Fatal("the rotation emptied the file")
	}
	if got[0].Digest != "after the rotation" {
		t.Errorf("newest digest is %d bytes of the wrong record", len(got[0].Digest))
	}
}
