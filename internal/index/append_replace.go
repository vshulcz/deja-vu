package index

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// A pass that replaces a few sessions wrote the whole store again: on 25,000
// sessions, 9 changed transcripts cost 570 MB of records.bin, 208 MB of
// postings, 2.5 GB of memory and every search's disk for the length of it
// (#5387). It stages its index in a new directory as before, but the new
// directory starts as hard links to the old files: the replaced sessions'
// records are appended to the log both directories share, the records they
// replace are listed as dead in the new manifest, and the new postings go into
// overlays. Each session the pass writes is filed under a key id new to the
// string table, so a reader still holding the old manifest resolves the
// appended tail to no session and passes over it; the swap is the commit, as
// it always was.
//
// The log is written whole again, without its dead records, when they come to
// a quarter of it, and on any pass where a hard link is refused.

// appendPendingFile marks a live index whose log a pass is appending to. It
// holds the size the log had before, which an interrupted pass leaves it
// longer than; the next lock taker cuts it back rather than rebuilding.
const appendPendingFile = "append.pending"

// deadShareCompact is the share of the log in dead records past which the next
// replacing pass writes the log again.
const deadShareCompact = 4

// appendReplacing turns the append off, for a test's control.
var appendReplacing = true

// beforeAppendSwap, when set, runs before an appending pass swaps its index
// in; a test fails the pass there.
var beforeAppendSwap func() error

// appendsInPlace stages tmp from dir's files and reports whether the pass may
// append to the log. False leaves tmp empty, and the pass writes the log anew.
func appendsInPlace(dir, tmp string, old Manifest) bool {
	if !appendReplacing || old.RecordsSize <= 0 || old.DeadBytes*deadShareCompact > old.RecordsSize {
		return false
	}
	fi, err := os.Stat(filepath.Join(dir, "records.bin"))
	if err != nil || fi.Size() != old.RecordsSize {
		return false
	}
	undo := func() bool {
		_ = os.RemoveAll(tmp)
		_ = os.MkdirAll(filepath.Join(tmp, "buckets"), 0o700)
		return false
	}
	if os.Link(filepath.Join(dir, "records.bin"), filepath.Join(tmp, "records.bin")) != nil {
		return undo()
	}
	// Before the first byte is appended: a pass killed after it leaves the
	// mark behind.
	if writeFileAtomic(filepath.Join(dir, appendPendingFile), []byte(strconv.FormatInt(old.RecordsSize, 10))) != nil {
		return undo()
	}
	return true
}

// createLog creates a record log at p that is a file of its own. Truncating
// whatever is there would write through a hard link a pass that never finished
// left in its staging directory, onto the live log.
func createLog(p string) (*os.File, error) {
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return os.OpenFile(p, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
}

// undoAppend cuts the log in dir back to what its manifest committed, after a
// pass that appended to it did not swap its index in.
func undoAppend(dir string, size int64) {
	p := filepath.Join(dir, "records.bin")
	if fi, err := os.Stat(p); err == nil && fi.Size() > size {
		_ = os.Truncate(p, size)
	}
	_ = os.Remove(filepath.Join(dir, appendPendingFile))
}

// recoverAppendedTail undoes what a pass killed while appending left in dir.
// Only a lock holder calls it, and the mark means no swap followed the
// append, so the bytes past the mark's size are that pass's and nobody's
// else: any other writer takes the lock first and runs this before it.
func recoverAppendedTail(dir string) {
	b, err := os.ReadFile(filepath.Join(dir, appendPendingFile))
	if err != nil {
		return
	}
	size, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || size <= 0 {
		_ = os.Remove(filepath.Join(dir, appendPendingFile))
		return
	}
	undoAppend(dir, size)
}

// mergeDead joins two ascending offset lists that share none.
func mergeDead(a, b []int64) []int64 {
	if len(b) == 0 {
		return a
	}
	if len(a) == 0 {
		return b
	}
	out := make([]int64, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if a[i] < b[j] {
			out = append(out, a[i])
			i++
		} else {
			out = append(out, b[j])
			j++
		}
	}
	out = append(out, a[i:]...)
	return append(out, b[j:]...)
}

// A record a pass marks dead is still in the log until the log is written
// again, and two things read it there. An older deja that meets this index
// rebuilds it and carries the sessions whose transcripts are gone out of the
// log, knowing nothing of DeadRecords, so the replaced turns came back live
// and stayed (#5428). And text a user took out of a transcript sat on disk
// until a quarter of the log was dead (#5429). So the pass that kills a
// record overwrites it once its index is swapped in: the frame keeps its
// length, the three ids all name the record's role, which is no session and
// no file, and the time and text are zero. Every walk of every version
// decodes that as an empty record of no session and passes over it.

// scrubPendingFile marks a live index whose dead records a pass has not yet
// overwritten; the next lock taker finishes the job.
const scrubPendingFile = "scrub.pending"

// scrubRecords overwrites the records at offs in the log at path, as above,
// and syncs the log.
func scrubRecords(path string, offs []int64) error {
	if len(offs) == 0 {
		return nil
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	var hdr [4]byte
	var buf []byte
	for _, off := range offs {
		if _, err := f.ReadAt(hdr[:], off); err != nil {
			return err
		}
		n := int(binary.LittleEndian.Uint32(hdr[:]))
		if n > maxRecordSize {
			return fmt.Errorf("%w: record length %d exceeds cap", errCorruptIndex, n)
		}
		if cap(buf) < n {
			buf = make([]byte, n)
		}
		b := buf[:n]
		if _, err := f.ReadAt(b, off+4); err != nil {
			return err
		}
		if !scrubbedPayload(b) {
			continue
		}
		if _, err := f.WriteAt(b, off+4); err != nil {
			return err
		}
	}
	return f.Sync()
}

// scrubbedPayload rewrites b, one record's payload, as an empty record of no
// session, and reports whether it changed anything. A raw record body is a
// flag, eight bytes of time and a length, so ten bytes past the ids always
// fit; when the role's id is longer than the key's and path's together (a
// one-letter message under a role first seen late), the record keeps its own
// ids and only its text and time go.
func scrubbedPayload(b []byte) bool {
	kid, pid, rid, rest, ok := recordPrefix(b)
	if !ok {
		return false
	}
	// A real record's time is never eight zero bytes: the zero time is
	// stored as its own sentinel.
	if !slices.ContainsFunc(rest, func(c byte) bool { return c != 0 }) {
		return false
	}
	prefix := binary.AppendUvarint(nil, rid)
	prefix = binary.AppendUvarint(prefix, rid)
	prefix = binary.AppendUvarint(prefix, rid)
	if len(prefix)+10 > len(b) {
		prefix = binary.AppendUvarint(prefix[:0], kid)
		prefix = binary.AppendUvarint(prefix, pid)
		prefix = binary.AppendUvarint(prefix, rid)
	}
	clear(b[copy(b, prefix):])
	return true
}

// scrubAfterSwap clears what the records at offs, which a swapped-in pass
// killed, leave on disk, and then the mark. First the words only they held,
// out of the postings files (see blankDeadWords), read from the records
// themselves, so it goes before the records are overwritten: a pass killed in
// between finds them still to read. Then the records.
//
// The sidecars that say which log they describe do it by a fingerprint of
// sampled bytes, which an overwrite can land in. What they hold is about the
// live records, which the overwrite leaves alone, so the fingerprint they
// carry is moved to the log's new one rather than the sidecars written again.
func scrubAfterSwap(dir string, offs []int64) {
	m, err := readManifest(dir)
	if err != nil {
		return
	}
	scrubSwapped(dir, m, offs)
}

// scrubSwapped is scrubAfterSwap for the pass that just committed m to dir,
// which has it in hand: reading it back cost a decode of every session row.
func scrubSwapped(dir string, m Manifest, offs []int64) {
	log := filepath.Join(dir, "records.bin")
	neighbours := readCooccur(dir)
	words, unindexed, err := deadWords(log, tablesFromStrings(m.RecordStrings), offs, neighbours != nil)
	if err != nil {
		return
	}
	// The neighbour map takes its words from the whole text of a record, and
	// the postings from part of it. A map word the dead records held only
	// where the postings do not look is asked of the postings too: one no
	// live record has a posting for goes, as nothing can say another session
	// still holds it, and one with a posting stays.
	vocab := cooccurVocabulary(neighbours, len(unindexed) > 0)
	for w := range unindexed {
		if !vocab[w] {
			continue
		}
		k := "t" + w
		if words[bucket(k)] == nil {
			words[bucket(k)] = map[string][]posting{}
		}
		if _, ok := words[bucket(k)][k]; !ok {
			words[bucket(k)][k] = nil
		}
	}
	gone, err := blankDeadWords(dir, words, m.DeadRecords)
	if err != nil {
		return
	}
	// Best effort: a map a reader holds open on Windows is not a reason to
	// leave the records unzeroed.
	if neighbours != nil {
		_ = writeCooccurWithout(dir, neighbours, func(w string) bool { return gone["t"+w] })
	}
	size, before, err := scrubMarked(dir)
	if err != nil {
		return
	}
	if scrubRecords(log, offs) != nil {
		return
	}
	if f, err := os.Open(log); err == nil {
		after, err := recordLogFingerprint(f, size)
		_ = f.Close()
		if err == nil && after != before {
			for _, name := range logStampedFiles {
				restamp(filepath.Join(dir, name), before, after)
			}
		}
	}
	_ = os.Remove(filepath.Join(dir, scrubPendingFile))
}

// markScrubPending writes the mark into dir, carrying the size and
// fingerprint of its log before any record is zeroed. Those are what the
// sidecars carry until the scrub moves them, and a pass killed after zeroing
// the records leaves a log that no longer has them to read back (#5451).
func markScrubPending(dir string) error {
	size, fp, err := logFingerprint(filepath.Join(dir, "records.bin"))
	if err != nil {
		return err
	}
	b := binary.LittleEndian.AppendUint64(nil, uint64(size)) //nolint:gosec // file sizes are non-negative
	return os.WriteFile(filepath.Join(dir, scrubPendingFile), append(b, fp[:]...), 0o600)
}

// scrubMarked is the log size and fingerprint the mark in dir carries, or
// the log's own when the mark has none: one written before it carried them.
func scrubMarked(dir string) (int64, [sha256.Size]byte, error) {
	var fp [sha256.Size]byte
	b, err := os.ReadFile(filepath.Join(dir, scrubPendingFile))
	if err == nil && len(b) == 8+sha256.Size {
		copy(fp[:], b[8:])
		return int64(binary.LittleEndian.Uint64(b)), fp, nil //nolint:gosec // written from a non-negative size
	}
	return logFingerprint(filepath.Join(dir, "records.bin"))
}

// deadWords is the words the records at offs in the log at path were filed
// under, by bucket, for those not yet overwritten. Only the words count: the
// postings files are asked whether anything live still holds them. With
// unindexed it also gives the neighbour map's words of the part of each
// record the postings leave out (see tokenizedPart).
//
// On as many cores as there are, in runs of the log: a pass that rewrote a
// fifth of a 40,000-session store tokenized 200,000 dead records here on one,
// 17 s of a pass the rewrite it replaced did in 63.
func deadWords(path string, t *recordTables, offs []int64, unindexed bool) (bucketPostings, map[string]bool, error) {
	workers := max(1, min(runtime.NumCPU(), (len(offs)+1023)/1024))
	parts := make([]bucketPostings, workers)
	rests := make([]map[string]bool, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for w := range workers {
		lo, hi := len(offs)*w/workers, len(offs)*(w+1)/workers
		wg.Go(func() {
			parts[w], rests[w], errs[w] = deadWordsOf(path, t, offs[lo:hi], unindexed)
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, nil, err
	}
	words, rest := parts[0], rests[0]
	for i, part := range parts[1:] {
		for b, toks := range part {
			if words[b] == nil {
				words[b] = toks
				continue
			}
			for tok := range toks {
				words[b][tok] = nil
			}
		}
		for w := range rests[i+1] {
			rest[w] = true
		}
	}
	return words, rest, nil
}

func deadWordsOf(path string, t *recordTables, offs []int64, unindexed bool) (bucketPostings, map[string]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = f.Close() }()
	words := bucketPostings{}
	unposted := map[string]bool{}
	var hdr [4]byte
	var b []byte
	for _, off := range offs {
		if _, err := f.ReadAt(hdr[:], off); err != nil {
			return nil, nil, err
		}
		n := binary.LittleEndian.Uint32(hdr[:])
		if n > maxRecordSize {
			return nil, nil, fmt.Errorf("%w: record length %d exceeds cap", errCorruptIndex, n)
		}
		if cap(b) < int(n) {
			b = make([]byte, n)
		}
		b = b[:n]
		if _, err := f.ReadAt(b, off+4); err != nil {
			return nil, nil, err
		}
		if _, _, _, rest, ok := recordPrefix(b); !ok || !slices.ContainsFunc(rest, func(c byte) bool { return c != 0 }) {
			continue
		}
		r, err := decodeRecord(b, t)
		if err != nil {
			continue
		}
		part := tokenizedPart(r.Role, r.Text)
		eachIndexKey(part, r.Time, func(tok string) {
			bk := bucket(tok)
			if words[bk] == nil {
				words[bk] = map[string][]posting{}
			}
			words[bk][tok] = nil
		})
		if unindexed && len(part) != len(r.Text) {
			for _, tok := range tokens(r.Text) {
				unposted[tok] = true
			}
		}
	}
	return words, unposted, nil
}

// logStampedFiles are the sidecars that carry the record log's fingerprint.
var logStampedFiles = []string{recordOffsetsFile, frictionTextsFile}

// restamp replaces the fingerprint before with after in the file at p, if it
// carries it.
func restamp(p string, before, after [sha256.Size]byte) {
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	i := bytes.Index(b, before[:])
	if i < 0 {
		return
	}
	copy(b[i:], after[:])
	_ = writeFileAtomic(p, b)
}

// recoverScrub finishes what a pass killed between its swap and its scrub
// left: every dead record the manifest lists, the ones already overwritten
// passing through unchanged. Only a lock holder calls it.
func recoverScrub(dir string) {
	if _, err := os.Stat(filepath.Join(dir, scrubPendingFile)); err != nil {
		return
	}
	m, err := readManifest(dir)
	if err != nil {
		return
	}
	scrubAfterSwap(dir, m.DeadRecords)
}
