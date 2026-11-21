package index

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"
)

const (
	lineageStubWindow = 1024
	lineageSpanGap    = readAheadSize
	lineageTailChunk  = 16
)

type roleTimeKey struct {
	role string
	nano int64
}

type recordStub struct {
	off  int64
	role string
	at   time.Time
	text string
}

type sessionStubs struct {
	list []recordStub
	byRT map[roleTimeKey][]int
}

type rawRecordOffsets struct {
	b         []byte
	covered   int64
	logSize   int64
	nkeys     int
	dirStart  int
	dataStart int
}

// lineageReader caches open files, offset tables, record stubs, and on-demand
// full record decodes within one lineage resolution pass.
type lineageReader struct {
	dir         string
	recordsPath string
	manifest    Manifest

	tablesOnce sync.Once
	tables     *recordTables

	openOnce sync.Once
	file     *os.File
	ro       rawRecordOffsets
	roOK     bool

	tailOnce  sync.Once
	tailByKey map[string][]int64
	tailErr   bool

	offsByKey     map[string][]int64
	stubsByKey    map[string]*sessionStubs
	fullByOff     map[int64]Record
	fallbackByKey map[string][]Record
}

func newLineageReader(dir string, m Manifest) *lineageReader {
	return &lineageReader{
		dir:           dir,
		recordsPath:   filepath.Join(dir, "records.bin"),
		manifest:      m,
		offsByKey:     map[string][]int64{},
		stubsByKey:    map[string]*sessionStubs{},
		fullByOff:     map[int64]Record{},
		fallbackByKey: map[string][]Record{},
	}
}

func (lr *lineageReader) close() {
	if lr.file != nil {
		_ = lr.file.Close()
		lr.file = nil
	}
}

func (lr *lineageReader) getTables() *recordTables {
	lr.tablesOnce.Do(func() {
		lr.tables = tablesFromManifest(lr.manifest)
	})
	return lr.tables
}

func (lr *lineageReader) ensureOpen() {
	lr.openOnce.Do(func() {
		f, err := openIndexFile(lr.recordsPath)
		if err != nil {
			return
		}
		lr.file = f
		if ro, ok := readRawRecordOffsets(lr.dir, f); ok {
			lr.ro = ro
			lr.roOK = true
		}
	})
}

func readRawRecordOffsets(dir string, f *os.File) (rawRecordOffsets, bool) {
	b, err := os.ReadFile(filepath.Join(dir, recordOffsetsFile))
	if err != nil || len(b) < recordOffsetsHeaderSize || !bytes.Equal(b[:8], recordOffsetsMagic[:]) {
		return rawRecordOffsets{}, false
	}
	covered := int64(binary.LittleEndian.Uint64(b[8:])) //nolint:gosec // written from non-negative size
	var fp [sha256.Size]byte
	copy(fp[:], b[24:24+sha256.Size])
	nkeys := int(binary.LittleEndian.Uint32(b[24+sha256.Size:]))
	dirStart := recordOffsetsHeaderSize
	dataStart := dirStart + 16*nkeys
	if nkeys < 0 || dataStart > len(b) {
		return rawRecordOffsets{}, false
	}
	fi, err := f.Stat()
	if err != nil || fi.Size() < covered {
		return rawRecordOffsets{}, false
	}
	got, err := recordLogFingerprint(f, covered)
	if err != nil || got != fp {
		return rawRecordOffsets{}, false
	}
	return rawRecordOffsets{
		b:         b,
		covered:   covered,
		logSize:   fi.Size(),
		nkeys:     nkeys,
		dirStart:  dirStart,
		dataStart: dataStart,
	}, true
}

func (ro rawRecordOffsets) entryAt(i int) (uint64, int) {
	p := ro.dirStart + 16*i
	return binary.LittleEndian.Uint64(ro.b[p:]), int(binary.LittleEndian.Uint64(ro.b[p+8:])) //nolint:gosec // written from non-negative offset
}

func (ro rawRecordOffsets) decodeKeyOffsets(at int, want string) ([]int64, bool, bool) {
	p := ro.dataStart + at
	if p < ro.dataStart || p >= len(ro.b) {
		return nil, false, false
	}
	kl, n := binary.Uvarint(ro.b[p:])
	if n <= 0 || kl > uint64(len(ro.b)-p-n) { //nolint:gosec // n bytes were read from ro.b[p:]
		return nil, false, false
	}
	p += n
	key := string(ro.b[p : p+int(kl)]) //nolint:gosec // kl is bounded above
	p += int(kl)                       //nolint:gosec // kl is bounded above
	if key != want {
		return nil, false, true
	}
	cnt, n := binary.Uvarint(ro.b[p:])
	if n <= 0 {
		return nil, false, false
	}
	p += n
	offs := make([]int64, 0, cnt)
	prev := int64(0)
	for range cnt {
		d, n := binary.Uvarint(ro.b[p:])
		if n <= 0 {
			return nil, false, false
		}
		p += n
		prev += int64(d) //nolint:gosec // deltas of ascending offsets
		offs = append(offs, prev)
	}
	return offs, true, true
}

func (ro rawRecordOffsets) lookup(key string) ([]int64, bool) {
	h := keyHash(key)
	i := sort.Search(ro.nkeys, func(i int) bool { e, _ := ro.entryAt(i); return e >= h })
	for ; i < ro.nkeys; i++ {
		e, at := ro.entryAt(i)
		if e != h {
			break
		}
		offs, matched, ok := ro.decodeKeyOffsets(at, key)
		if !ok {
			return nil, false
		}
		if matched {
			return offs, true
		}
	}
	return nil, true
}

func (lr *lineageReader) ensureTail() {
	lr.tailOnce.Do(func() {
		if lr.ro.covered >= lr.ro.logSize {
			return
		}
		lr.tailByKey = map[string][]int64{}
		t := lr.getTables()
		dead := t.deadFrom(lr.ro.covered)
		sr := io.NewSectionReader(lr.file, lr.ro.covered, lr.ro.logSize-lr.ro.covered)
		r := bufio.NewReaderSize(sr, 256*1024)
		off := lr.ro.covered
		var hdr [4]byte
		for off < lr.ro.logSize {
			if _, err := io.ReadFull(r, hdr[:]); err != nil {
				if err == io.EOF || errors.Is(err, io.ErrUnexpectedEOF) {
					return
				}
				lr.tailErr = true
				return
			}
			n := int64(binary.LittleEndian.Uint32(hdr[:]))
			if n > maxRecordSize {
				lr.tailErr = true
				return
			}
			head, perr := r.Peek(int(min(n, binary.MaxVarintLen64)))
			if perr != nil && len(head) == 0 {
				if perr == io.EOF || errors.Is(perr, io.ErrUnexpectedEOF) {
					return
				}
				lr.tailErr = true
				return
			}
			kid, un := binary.Uvarint(head)
			if un <= 0 {
				lr.tailErr = true
				return
			}
			if !dead.at(off) {
				k := t.lookup(kid)
				lr.tailByKey[k] = append(lr.tailByKey[k], off)
			}
			if _, err := r.Discard(int(n)); err != nil {
				if err == io.EOF || errors.Is(err, io.ErrUnexpectedEOF) {
					return
				}
				lr.tailErr = true
				return
			}
			off += 4 + n
		}
	})
}

func (lr *lineageReader) offsetsFor(key string) ([]int64, bool) {
	if cached, ok := lr.offsByKey[key]; ok {
		return cached, true
	}
	if !lr.roOK || lr.file == nil {
		return nil, false
	}
	offs, ok := lr.ro.lookup(key)
	if !ok {
		lr.roOK = false
		return nil, false
	}
	t := lr.getTables()
	offs = slices.DeleteFunc(slices.Clone(offs), t.isDead)
	if lr.ro.covered < lr.ro.logSize {
		lr.ensureTail()
		if lr.tailErr {
			lr.roOK = false
			return nil, false
		}
		if tail := lr.tailByKey[key]; len(tail) > 0 {
			offs = append(offs, tail...)
		}
	}
	lr.offsByKey[key] = offs
	return offs, true
}

func (lr *lineageReader) fallbackRecords(key string) ([]Record, bool) {
	if recs, ok := lr.fallbackByKey[key]; ok {
		return recs, true
	}
	if lr.file == nil {
		return nil, false
	}
	recs, err := recordsForKey(lr.recordsPath, lr.getTables(), key)
	if err != nil {
		return nil, false
	}
	lineageBodyDecodes.Add(int64(len(recs)))
	lr.fallbackByKey[key] = recs
	return recs, true
}

func stubsFromRecords(recs []Record) *sessionStubs {
	st := &sessionStubs{
		list: make([]recordStub, len(recs)),
		byRT: make(map[roleTimeKey][]int, len(recs)),
	}
	for i, r := range recs {
		st.list[i] = recordStub{off: -1, role: r.Role, at: r.Time, text: r.Text}
		rt := roleTimeKey{role: r.Role, nano: r.Time.UnixNano()}
		st.byRT[rt] = append(st.byRT[rt], i)
	}
	return st
}

func (lr *lineageReader) readStubAt(key string, off int64) (Record, error) {
	t := lr.getTables()
	var head [lineageStubWindow]byte
	n, err := lr.file.ReadAt(head[:], off)
	if err != nil && !errors.Is(err, io.EOF) {
		return Record{}, err
	}
	if n < 4 {
		return Record{}, errOffsetsMismatch
	}
	size := int(binary.LittleEndian.Uint32(head[:4]))
	if size > maxRecordSize {
		return Record{}, errOffsetsMismatch
	}
	payload := head[4:n]
	if recKey, _, ok := recordRoleIn(payload, t); !ok || recKey != key {
		return Record{}, errOffsetsMismatch
	}
	if rec, derr := decodeRecordStubRole(payload[:min(size, len(payload))], t); derr == nil {
		return rec, nil
	}
	if size <= len(payload) {
		return Record{}, errOffsetsMismatch
	}
	full := make([]byte, size)
	m, readErr := lr.file.ReadAt(full, off+4)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return Record{}, readErr
	}
	if m < size {
		return Record{}, errOffsetsMismatch
	}
	return decodeRecordStubRole(full, t)
}

func (lr *lineageReader) readStubFromSpan(b []byte, rel int, key string) (Record, error) {
	if rel < 0 || rel+4 > len(b) {
		return Record{}, errOffsetsMismatch
	}
	size := int(binary.LittleEndian.Uint32(b[rel : rel+4]))
	if size > maxRecordSize {
		return Record{}, errOffsetsMismatch
	}
	t := lr.getTables()
	if rel+4 < len(b) {
		end := min(len(b), rel+4+size)
		avail := b[rel+4 : end]
		if recKey, _, ok := recordRoleIn(avail, t); ok {
			if recKey != key {
				return Record{}, errOffsetsMismatch
			}
			if rec, derr := decodeRecordStubRole(avail, t); derr == nil {
				return rec, nil
			}
		}
	}
	return Record{}, errOffsetsMismatch
}

func (lr *lineageReader) stubsFor(key string) (*sessionStubs, bool) {
	if st, ok := lr.stubsByKey[key]; ok {
		return st, true
	}
	lr.ensureOpen()
	if lr.file == nil {
		return nil, false
	}
	if !lr.roOK {
		recs, ok := lr.fallbackRecords(key)
		if !ok {
			return nil, false
		}
		st := stubsFromRecords(recs)
		lr.stubsByKey[key] = st
		return st, true
	}
	offs, ok := lr.offsetsFor(key)
	if !ok {
		return lr.fallbackStubs(key)
	}
	st := &sessionStubs{
		list: make([]recordStub, 0, len(offs)),
		byRT: make(map[roleTimeKey][]int, len(offs)),
	}
	var buf []byte
	for i := 0; i < len(offs); {
		j := i + 1
		for j < len(offs) && offs[j]-offs[j-1] <= lineageSpanGap && offs[j]-offs[i] < spanSizeMax {
			j++
		}
		start := offs[i]
		want := int(offs[j-1]-start) + lineageStubWindow
		if cap(buf) < want {
			buf = make([]byte, want)
		}
		b := buf[:want]
		n, err := lr.file.ReadAt(b, start)
		if err != nil && !errors.Is(err, io.EOF) {
			n = 0
		}
		b = b[:n]
		for _, off := range offs[i:j] {
			rec, serr := lr.readStubFromSpan(b, int(off-start), key)
			if serr != nil {
				rec, serr = lr.readStubAt(key, off)
			}
			if serr != nil {
				if errors.Is(serr, errOffsetsMismatch) {
					lr.roOK = false
					return lr.fallbackStubs(key)
				}
				return nil, false
			}
			idx := len(st.list)
			st.list = append(st.list, recordStub{off: off, role: rec.Role, at: rec.Time})
			rt := roleTimeKey{role: rec.Role, nano: rec.Time.UnixNano()}
			st.byRT[rt] = append(st.byRT[rt], idx)
		}
		i = j
	}
	lr.stubsByKey[key] = st
	return st, true
}

func (lr *lineageReader) fallbackStubs(key string) (*sessionStubs, bool) {
	recs, ok := lr.fallbackRecords(key)
	if !ok {
		return nil, false
	}
	st := stubsFromRecords(recs)
	lr.stubsByKey[key] = st
	return st, true
}

func (lr *lineageReader) fullRecordAt(key string, off int64) (Record, error) {
	if rec, ok := lr.fullByOff[off]; ok {
		return rec, nil
	}
	rec, keep, err := readRecordAtInRoles(lr.file, off, lr.getTables(), map[string]bool{key: true}, nil)
	if err != nil {
		return Record{}, err
	}
	if !keep {
		return Record{}, errOffsetsMismatch
	}
	lineageBodyDecodes.Add(1)
	lr.fullByOff[off] = rec
	return rec, nil
}

func (lr *lineageReader) stubText(key string, st recordStub) (string, bool) {
	if st.off < 0 {
		return st.text, true
	}
	rec, err := lr.fullRecordAt(key, st.off)
	if err != nil {
		return "", false
	}
	return rec.Text, true
}

func (lr *lineageReader) peekHeaderAt(off int64, key string) (string, bool, error) {
	var hdr [128]byte
	n, err := lr.file.ReadAt(hdr[:], off)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", false, err
	}
	if n < 4 {
		return "", false, errOffsetsMismatch
	}
	size := binary.LittleEndian.Uint32(hdr[:4])
	if size > maxRecordSize {
		return "", false, errOffsetsMismatch
	}
	recKey, role, ok := recordRoleIn(hdr[4:n], lr.getTables())
	if !ok || recKey != key {
		return "", false, errOffsetsMismatch
	}
	return role, true, nil
}

func (lr *lineageReader) lastMatchFallback(key string, skipRec func(Record) bool) (Record, bool) {
	recs, ok := lr.fallbackRecords(key)
	if !ok {
		return Record{}, false
	}
	for _, rec := range slices.Backward(recs) {
		if !skipRec(rec) {
			return rec, true
		}
	}
	return Record{}, false
}

func (lr *lineageReader) lastMatch(key string, skipRole func(string) bool, skipRec func(Record) bool) (Record, bool) {
	lr.ensureOpen()
	if lr.file == nil {
		return Record{}, false
	}
	if !lr.roOK {
		return lr.lastMatchFallback(key, skipRec)
	}
	offs, ok := lr.offsetsFor(key)
	if !ok {
		return lr.lastMatchFallback(key, skipRec)
	}
	for end := len(offs); end > 0; end -= lineageTailChunk {
		start := max(0, end-lineageTailChunk)
		for i := end - 1; i >= start; i-- {
			off := offs[i]
			if rec, cached := lr.fullByOff[off]; cached {
				if !skipRec(rec) {
					return rec, true
				}
				continue
			}
			role, hdrOK, herr := lr.peekHeaderAt(off, key)
			if herr != nil {
				if errors.Is(herr, errOffsetsMismatch) {
					lr.roOK = false
					return lr.lastMatchFallback(key, skipRec)
				}
				return Record{}, false
			}
			if hdrOK && skipRole != nil && skipRole(role) {
				continue
			}
			rec, err := lr.fullRecordAt(key, off)
			if err != nil {
				if errors.Is(err, errOffsetsMismatch) {
					lr.roOK = false
					return lr.lastMatchFallback(key, skipRec)
				}
				return Record{}, false
			}
			if !skipRec(rec) {
				return rec, true
			}
		}
	}
	return Record{}, false
}

func isHoldNoiseRole(role string) bool { return role == roleSummary }

func isEndedNoiseRole(role string) bool {
	return role == roleSummary || role == roleEdit || role == roleWrote || role == roleFiles
}

func isEndedNoise(r Record) bool {
	return afterForkNoise(r) || r.Role == roleEdit || r.Role == roleWrote || r.Role == roleFiles || r.Time.IsZero()
}

func isWholeHoldNoise(r Record) bool {
	return afterForkNoise(r) || r.Role == roleEdit || r.Role == roleWrote || r.Role == roleFiles
}

func (lr *lineageReader) held(asker, other SessionMeta) bool {
	last, ok := lr.lastMatch(other.Key(), isHoldNoiseRole, afterForkNoise)
	if !ok {
		return false
	}
	st, ok := lr.stubsFor(asker.Key())
	if !ok || len(st.list) == 0 {
		return false
	}
	idxs := st.byRT[roleTimeKey{role: last.Role, nano: last.Time.UnixNano()}]
	for _, idx := range idxs {
		if txt, ok := lr.stubText(asker.Key(), st.list[idx]); ok && txt == last.Text {
			return true
		}
	}
	return false
}

func (lr *lineageReader) endedBy(src SessionMeta, at time.Time) bool {
	if at.IsZero() {
		return true
	}
	rec, ok := lr.lastMatch(src.Key(), isEndedNoiseRole, isEndedNoise)
	if !ok {
		return false
	}
	return !rec.Time.After(at)
}

func (lr *lineageReader) startedAt(meta SessionMeta) time.Time {
	if !meta.Started.IsZero() {
		return meta.Started
	}
	lr.ensureOpen()
	if lr.file == nil {
		return time.Time{}
	}
	if lr.roOK {
		if offs, ok := lr.offsetsFor(meta.Key()); ok {
			for _, off := range offs {
				rec, err := lr.readStubAt(meta.Key(), off)
				if err != nil {
					if errors.Is(err, errOffsetsMismatch) {
						lr.roOK = false
						break
					}
					return time.Time{}
				}
				if !rec.Time.IsZero() {
					return rec.Time
				}
			}
			if lr.roOK {
				return time.Time{}
			}
		}
	}
	recs, ok := lr.fallbackRecords(meta.Key())
	if !ok {
		return time.Time{}
	}
	for _, r := range recs {
		if !r.Time.IsZero() {
			return r.Time
		}
	}
	return time.Time{}
}

func (lr *lineageReader) wholeHolds(wholes []map[string]bool, other SessionMeta) bool {
	rec, ok := lr.lastMatch(other.Key(), isEndedNoiseRole, isWholeHoldNoise)
	if !ok {
		return false
	}
	k := turnKey(rec.Role, rec.Time.UnixMilli())
	for _, w := range wholes {
		if w[k] {
			return true
		}
	}
	return false
}

func (lr *lineageReader) shared(asker, other SessionMeta) time.Time {
	var zero time.Time
	if asker.Harness != other.Harness {
		return zero
	}
	aStubs, ok := lr.stubsFor(asker.Key())
	if !ok || len(aStubs.list) == 0 {
		return zero
	}
	oStubs, ok := lr.stubsFor(other.Key())
	if !ok || len(oStubs.list) == 0 {
		return zero
	}
	var cands []int
	for i, st := range oStubs.list {
		if !st.at.After(zero) {
			continue
		}
		if len(aStubs.byRT[roleTimeKey{role: st.role, nano: st.at.UnixNano()}]) > 0 {
			cands = append(cands, i)
		}
	}
	if len(cands) == 0 {
		return zero
	}
	slices.SortFunc(cands, func(i, j int) int {
		ti, tj := oStubs.list[i].at, oStubs.list[j].at
		if ti.After(tj) {
			return -1
		}
		if tj.After(ti) {
			return 1
		}
		return 0
	})
	aKey, oKey := asker.Key(), other.Key()
	for _, idx := range cands {
		oSt := oStubs.list[idx]
		oTxt, ok := lr.stubText(oKey, oSt)
		if !ok {
			continue
		}
		for _, aIdx := range aStubs.byRT[roleTimeKey{role: oSt.role, nano: oSt.at.UnixNano()}] {
			if aTxt, ok := lr.stubText(aKey, aStubs.list[aIdx]); ok && aTxt == oTxt {
				return oSt.at
			}
		}
	}
	return zero
}

func (lr *lineageReader) wholeShared(turns map[string]bool, other SessionMeta) time.Time {
	var at time.Time
	oStubs, ok := lr.stubsFor(other.Key())
	if !ok {
		return at
	}
	for _, st := range oStubs.list {
		if !st.at.IsZero() && turns[turnKey(st.role, st.at.UnixMilli())] && st.at.After(at) {
			at = st.at
		}
	}
	return at
}
