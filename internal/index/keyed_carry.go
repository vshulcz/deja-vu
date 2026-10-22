package index

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
)

// A replacing pass that appends in place leaves every record it carries where
// it is: what it needs of one is that it is still live and where it sits. The
// pass walked the whole log anyway, to find the few records it does something
// else with, and on 40,000 sessions that walk was a second of a pass that
// replaced 20 of them. The offset table says which records each session holds,
// so the pass reads only the sessions it may change and takes the rest of the
// table as it stands.

// keyedHoldMax bounds the payload bytes a keyed read holds before handing
// any on.
var keyedHoldMax int64 = 64 << 20

// storedRecordsWalked counts the records a replacing pass handed to its
// carry, for the tests that keep a small pass from reading the whole log.
var storedRecordsWalked atomic.Int64

// checkKeyedCarry, set by the tests, walks the whole log after a keyed carry
// and fails the pass when a record it took as it stands was one the walk
// would have done something else with.
var checkKeyedCarry bool

// carrySuspects is the sessions whose records a replacing pass may drop,
// move or read, when that set can be named from the manifest: every file the
// pass read again or lost is a transcript of its own, whose records name it
// and are filed under the sessions that list it. A shared store the pass read
// again drops records only by the keys it handed back, which the caller names
// in sets (#5476). A store that went, a prompt log or an import names other
// paths, and the pass walks the log for those.
func carrySuspects(old Manifest, changed map[string]FileState, removed map[string]bool, sets ...map[string]bool) (map[string]bool, bool) {
	touched := make(map[string]bool, len(changed)+len(removed))
	for p := range changed {
		if storeHarness(p) != "" {
			continue
		}
		if !copyable(p) {
			return nil, false
		}
		touched[p] = true
	}
	for p := range removed {
		if !copyable(p) {
			return nil, false
		}
		touched[p] = true
	}
	out := map[string]bool{}
	for key, meta := range old.Sessions {
		if touched[meta.Path] {
			out[key] = true
			continue
		}
		for _, q := range meta.Copies {
			if touched[q] {
				out[key] = true
				break
			}
		}
	}
	for _, set := range sets {
		for key := range set {
			out[key] = true
		}
	}
	return out, true
}

// eachStoredRecordOfKeys is eachStoredRecord, up to size, for the records of
// the keys want accepts, read through the offset table, and for every record
// past the span the table covers. The live records of every other key in that
// span go to rest, by key, in log order. A caller that names its keys has
// only theirs read from the table, and rest hears of no other; with roles,
// fn hears only of records in them. ok is false, before fn or rest has
// been called, when the table cannot answer for the log.
func eachStoredRecordOfKeys(dir string, size int64, t *recordTables, keys map[string]bool, want func(key string) bool, roles map[string]bool, fn func(off int64, payload []byte) error, rest func(key string, offs []int64)) (bool, error) {
	f, err := openIndexFile(filepath.Join(dir, "records.bin"))
	if err != nil {
		return false, nil
	}
	defer func() { _ = f.Close() }()
	ro, err := readRecordOffsetsFor(dir, f, keys)
	if err != nil || ro.covered > size {
		return false, nil
	}
	var offs []int64
	kept := map[string][]int64{}
	for key, ko := range ro.byKey {
		ko = slices.DeleteFunc(ko, t.isDead)
		if len(ko) == 0 {
			continue
		}
		if want(key) {
			offs = append(offs, ko...)
			continue
		}
		kept[key] = ko
	}
	// Past a share of the log, the records would be held in memory to read
	// what a walk streams.
	if ro.records > 0 && int64(len(offs)) > ro.records/offsetsWalkShare {
		return false, nil
	}
	slices.Sort(offs)
	// Read before any is handed on, so a table that does not match the log
	// is found while the walk can still take over.
	type stored struct {
		off     int64
		payload []byte
	}
	recs := make([]stored, 0, len(offs))
	var held int64
	var buf []byte
	for i := 0; i < len(offs); {
		j := i + 1
		for j < len(offs) && offs[j]-offs[j-1] <= spanGapMax && offs[j]-offs[i] < spanSizeMax {
			j++
		}
		start := offs[i]
		n := min(int64(offs[j-1]-start)+readAheadSize, ro.covered-start)
		if int64(cap(buf)) < n {
			buf = make([]byte, n)
		}
		b := buf[:n]
		if _, err := f.ReadAt(b, start); err != nil && !errors.Is(err, io.EOF) {
			return false, nil
		}
		for _, off := range offs[i:j] {
			payload, ok := storedAt(f, b, off-start, off, ro.covered)
			if !ok {
				return false, nil
			}
			kid, _, rid, _, ok := recordPrefix(payload)
			if !ok || !want(t.lookup(kid)) {
				return false, nil
			}
			if roles != nil && !roles[t.lookup(rid)] {
				continue
			}
			// Held until the table is known to be right, so a pass whose
			// records are most of the log's bytes walks it instead.
			if held += int64(len(payload)); held > keyedHoldMax {
				return false, nil
			}
			recs = append(recs, stored{off, payload})
		}
		i = j
	}
	for key, ko := range kept {
		rest(key, ko)
	}
	for _, r := range recs {
		if err := fn(r.off, r.payload); err != nil {
			return true, err
		}
	}
	if ro.covered == size {
		return true, nil
	}
	if _, err := f.Seek(ro.covered, io.SeekStart); err != nil {
		return true, err
	}
	return true, walkStored(io.LimitReader(f, size-ro.covered), t, ro.covered, fn)
}

// storedAt is a copy of the payload of the record at off, rel bytes into the
// span b, or read on its own when the span ends inside it.
func storedAt(f *os.File, b []byte, rel, off, end int64) ([]byte, bool) {
	if rel+4 > int64(len(b)) {
		var hdr [4]byte
		if _, err := f.ReadAt(hdr[:], off); err != nil {
			return nil, false
		}
		b, rel = hdr[:], 0
	}
	n := int64(binary.LittleEndian.Uint32(b[rel:]))
	if n > maxRecordSize || off+4+n > end {
		return nil, false
	}
	out := make([]byte, n)
	if rel+4+n <= int64(len(b)) {
		copy(out, b[rel+4:rel+4+n])
		return out, true
	}
	if _, err := f.ReadAt(out, off+4); err != nil {
		return nil, false
	}
	return out, true
}

// walkStored hands fn every live record r holds, r standing at offset from.
func walkStored(r io.Reader, t *recordTables, from int64, fn func(off int64, payload []byte) error) error {
	w := newLogWalk(r, t, from)
	for {
		off, payload, err := w.next()
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil
			}
			return err
		}
		if err := fn(off, payload); err != nil {
			return err
		}
	}
}

// verifyKeyedCarry walks the log up to size and fails when a live record was
// handed neither to the carry nor to rest, or when one rest took as it stands
// is one the carry would have done something else with.
func verifyKeyedCarry(dir string, size int64, t *recordTables, handled, rested map[int64]bool, plain func(key, path string) bool) error {
	f, err := openIndexFile(filepath.Join(dir, "records.bin"))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	w := newLogWalk(io.LimitReader(f, size), t, 0)
	for {
		off, payload, err := w.next()
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil
			}
			return err
		}
		kid, pid, _, _, ok := recordPrefix(payload)
		if !ok {
			return errShortRecord
		}
		key, path := t.lookup(kid), t.lookup(pid)
		if !handled[off] {
			return fmt.Errorf("keyed carry missed %s's record at %d", key, off)
		}
		if rested[off] && !plain(key, path) {
			return fmt.Errorf("keyed carry took %s's record at %d from %q as it stands", key, off, path)
		}
	}
}

// carryStoredRecords hands a replacing pass's carry the records it has to
// look at: through the offset table when the pass appends in place and its
// changes can be named by session, the whole log otherwise. keep takes the
// rest, which an appending pass leaves where they are.
func carryStoredRecords(dir string, old Manifest, t *recordTables, appending bool, changed map[string]FileState, removed map[string]bool, carry func(off int64, payload []byte) error, keep func(key string, offs []int64), sets ...map[string]bool) error {
	log := filepath.Join(dir, "records.bin")
	counted := carry
	carry = func(off int64, payload []byte) error {
		storedRecordsWalked.Add(1)
		return counted(off, payload)
	}
	if !appending {
		return eachStoredRecord(log, t, carry)
	}
	suspects, ok := carrySuspects(old, changed, removed, sets...)
	if !ok {
		return eachStoredRecord(log, t, carry)
	}
	want := func(key string) bool {
		if suspects[key] {
			return true
		}
		_, ok := old.Sessions[key]
		return !ok
	}
	var handled, rested map[int64]bool
	fn, rest := carry, keep
	if checkKeyedCarry {
		handled, rested = map[int64]bool{}, map[int64]bool{}
		fn = func(off int64, payload []byte) error {
			handled[off] = true
			return carry(off, payload)
		}
		rest = func(key string, offs []int64) {
			for _, off := range offs {
				handled[off], rested[off] = true, true
			}
			keep(key, offs)
		}
	}
	done, err := eachStoredRecordOfKeys(dir, old.RecordsSize, t, nil, want, nil, fn, rest)
	if err != nil {
		return err
	}
	if !done {
		return eachStoredRecord(log, t, carry)
	}
	if checkKeyedCarry {
		plain := func(key, path string) bool {
			_, ok := old.Sessions[key]
			return ok && path != "" && !removed[path] && (changed[path].Path == "" || storeHarness(path) != "")
		}
		return verifyKeyedCarry(dir, old.RecordsSize, t, handled, rested, plain)
	}
	return nil
}
