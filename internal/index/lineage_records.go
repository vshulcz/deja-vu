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
	lineageStubWindow       = 1024
	lineageSpanGap          = readAheadSize
	lineageTailChunk        = 16
	lineagePrefixMinRecords = 256
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

type prefixResult struct {
	length    int
	bound     time.Time
	monotonic bool
	ok        bool
}

type timeProbe struct {
	idx int
	at  time.Time
}

var errSpanIncomplete = errors.New("span incomplete")

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

	offsByKey      map[string][]int64
	stubByOff      map[int64]recordStub
	roleStubsByKey map[string]map[string][]recordStub
	prefixByPair   map[[2]string]prefixResult
	fullByOff      map[int64]Record
	fallbackByKey  map[string][]Record
}

func newLineageReader(dir string, m Manifest) *lineageReader {
	return &lineageReader{
		dir:            dir,
		recordsPath:    filepath.Join(dir, "records.bin"),
		manifest:       m,
		offsByKey:      map[string][]int64{},
		stubByOff:      map[int64]recordStub{},
		roleStubsByKey: map[string]map[string][]recordStub{},
		prefixByPair:   map[[2]string]prefixResult{},
		fullByOff:      map[int64]Record{},
		fallbackByKey:  map[string][]Record{},
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
		lineageStubDecodes.Add(1)
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
	rec, derr := decodeRecordStubRole(full, t)
	if derr == nil {
		lineageStubDecodes.Add(1)
	}
	return rec, derr
}

func sameStubMeta(a, b recordStub) bool {
	return a.role == b.role && a.at.UnixNano() == b.at.UnixNano() && a.at.IsZero() == b.at.IsZero()
}

func (lr *lineageReader) stubAt(key string, off int64) (recordStub, error) {
	if st, ok := lr.stubByOff[off]; ok {
		return st, nil
	}
	if rec, ok := lr.fullByOff[off]; ok {
		st := recordStub{off: off, role: rec.Role, at: rec.Time}
		lr.stubByOff[off] = st
		return st, nil
	}
	rec, err := lr.readStubAt(key, off)
	if err != nil {
		return recordStub{}, err
	}
	st := recordStub{off: off, role: rec.Role, at: rec.Time}
	lr.stubByOff[off] = st
	return st, nil
}

func (lr *lineageReader) findVerifiedBound(aKey string, aOffs []int64, oKey string, oOffs []int64, lo int) (time.Time, int, bool, error) {
	minCheck := max(0, lo-64)
	for i := lo - 1; i >= minCheck; i-- {
		aSt, err := lr.stubAt(aKey, aOffs[i])
		if err != nil {
			return time.Time{}, -1, false, err
		}
		oSt, err := lr.stubAt(oKey, oOffs[i])
		if err != nil {
			return time.Time{}, -1, false, err
		}
		if !sameStubMeta(aSt, oSt) {
			return time.Time{}, -1, true, nil
		}
		if aSt.at.IsZero() {
			continue
		}
		aRec, err := lr.fullRecordAt(aKey, aOffs[i])
		if err != nil {
			return time.Time{}, -1, false, err
		}
		oRec, err := lr.fullRecordAt(oKey, oOffs[i])
		if err != nil {
			return time.Time{}, -1, false, err
		}
		if aRec.Text != oRec.Text {
			return time.Time{}, -1, true, nil
		}
		return aSt.at, i, false, nil
	}
	return time.Time{}, -1, true, nil
}

func (lr *lineageReader) prefixProbesMonotonic(aKey string, aOffs []int64, oKey string, oOffs []int64, boundIdx int, bound time.Time, probes []timeProbe) (bool, bool, error) {
	var pts []timeProbe
	if boundIdx > 0 {
		a0, err := lr.stubAt(aKey, aOffs[0])
		if err != nil {
			return false, false, err
		}
		o0, err := lr.stubAt(oKey, oOffs[0])
		if err != nil {
			return false, false, err
		}
		if !sameStubMeta(a0, o0) {
			return false, true, nil
		}
		if !a0.at.IsZero() {
			pts = append(pts, timeProbe{idx: 0, at: a0.at})
		}
	}
	for _, p := range probes {
		if p.idx < boundIdx {
			pts = append(pts, p)
		}
	}
	pts = append(pts, timeProbe{idx: boundIdx, at: bound})
	slices.SortFunc(pts, func(a, b timeProbe) int {
		if a.idx < b.idx {
			return -1
		}
		if a.idx > b.idx {
			return 1
		}
		return 0
	})
	for i := 1; i < len(pts); i++ {
		if pts[i-1].at.After(pts[i].at) {
			return false, false, nil
		}
	}
	return true, false, nil
}

func (lr *lineageReader) sharedPrefix(aKey string, aOffs []int64, oKey string, oOffs []int64) prefixResult {
	pair := [2]string{aKey, oKey}
	if pair[0] > pair[1] {
		pair[0], pair[1] = pair[1], pair[0]
	}
	if pref, ok := lr.prefixByPair[pair]; ok {
		return pref
	}
	n := min(len(aOffs), len(oOffs))
	if n == 0 {
		pref := prefixResult{monotonic: true, ok: true}
		lr.prefixByPair[pair] = pref
		return pref
	}
	lo, hi := 0, n
	var probes []timeProbe
	for lo < hi {
		mid := lo + (hi-lo)/2
		aSt, err := lr.stubAt(aKey, aOffs[mid])
		if err != nil {
			if errors.Is(err, errOffsetsMismatch) {
				lr.roOK = false
			}
			return prefixResult{}
		}
		oSt, err := lr.stubAt(oKey, oOffs[mid])
		if err != nil {
			if errors.Is(err, errOffsetsMismatch) {
				lr.roOK = false
			}
			return prefixResult{}
		}
		if sameStubMeta(aSt, oSt) {
			if !aSt.at.IsZero() {
				probes = append(probes, timeProbe{idx: mid, at: aSt.at})
			}
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		pref := prefixResult{monotonic: true, ok: true}
		lr.prefixByPair[pair] = pref
		return pref
	}
	bound, boundIdx, diverged, err := lr.findVerifiedBound(aKey, aOffs, oKey, oOffs, lo)
	if err != nil {
		if errors.Is(err, errOffsetsMismatch) {
			lr.roOK = false
		}
		return prefixResult{}
	}
	if diverged {
		pref := prefixResult{ok: true}
		lr.prefixByPair[pair] = pref
		return pref
	}
	monotonic, headDiverged, err := lr.prefixProbesMonotonic(aKey, aOffs, oKey, oOffs, boundIdx, bound, probes)
	if err != nil {
		if errors.Is(err, errOffsetsMismatch) {
			lr.roOK = false
		}
		return prefixResult{}
	}
	if headDiverged {
		pref := prefixResult{ok: true}
		lr.prefixByPair[pair] = pref
		return pref
	}
	pref := prefixResult{length: lo, bound: bound, monotonic: monotonic, ok: true}
	lr.prefixByPair[pair] = pref
	return pref
}

func (lr *lineageReader) tryRoleStubFromSpan(b []byte, rel int, key string, off int64, wantRoles map[string]bool) (recordStub, bool, error) {
	if rel < 0 || rel+4 > len(b) {
		return recordStub{}, false, errSpanIncomplete
	}
	size := int(binary.LittleEndian.Uint32(b[rel : rel+4]))
	if size > maxRecordSize {
		return recordStub{}, false, errOffsetsMismatch
	}
	if rel+4 >= len(b) {
		return recordStub{}, false, errSpanIncomplete
	}
	avail := b[rel+4 : min(len(b), rel+4+size)]
	t := lr.getTables()
	recKey, role, hdrOK := recordRoleIn(avail, t)
	if !hdrOK {
		return recordStub{}, false, errSpanIncomplete
	}
	if recKey != key {
		return recordStub{}, false, errOffsetsMismatch
	}
	if wantRoles != nil && !wantRoles[role] {
		return recordStub{}, false, nil
	}
	rec, derr := decodeRecordStubRole(avail, t)
	if derr != nil {
		return recordStub{}, false, errSpanIncomplete
	}
	lineageStubDecodes.Add(1)
	st := recordStub{off: off, role: rec.Role, at: rec.Time}
	lr.stubByOff[off] = st
	return st, true, nil
}

func (lr *lineageReader) readRoleStubPoint(key string, off int64, wantRoles map[string]bool) (recordStub, bool, error) {
	role, hdrOK, herr := lr.peekHeaderAt(off, key)
	if herr != nil {
		return recordStub{}, false, herr
	}
	if hdrOK && wantRoles != nil && !wantRoles[role] {
		return recordStub{}, false, nil
	}
	st, err := lr.stubAt(key, off)
	if err != nil {
		return recordStub{}, false, err
	}
	if wantRoles != nil && !wantRoles[st.role] {
		return recordStub{}, false, nil
	}
	return st, true, nil
}

func (lr *lineageReader) roleStubAtOffset(b []byte, spanStart int64, key string, off int64, wantRoles map[string]bool) (recordStub, bool, error) {
	if st, ok := lr.stubByOff[off]; ok {
		return st, wantRoles == nil || wantRoles[st.role], nil
	}
	if rec, ok := lr.fullByOff[off]; ok {
		st := recordStub{off: off, role: rec.Role, at: rec.Time}
		lr.stubByOff[off] = st
		return st, wantRoles == nil || wantRoles[st.role], nil
	}
	st, keep, serr := lr.tryRoleStubFromSpan(b, int(off-spanStart), key, off, wantRoles)
	if !errors.Is(serr, errSpanIncomplete) {
		return st, keep, serr
	}
	return lr.readRoleStubPoint(key, off, wantRoles)
}

func (lr *lineageReader) roleStubsFrom(key string, offs []int64, startIdx int, wantRoles map[string]bool) ([]recordStub, bool) {
	if wantRoles != nil && len(wantRoles) == 0 {
		return nil, true
	}
	if startIdx == 0 && len(wantRoles) == 1 {
		for r := range wantRoles {
			if byRole, ok := lr.roleStubsByKey[key]; ok {
				if cached, hit := byRole[r]; hit {
					return cached, true
				}
			}
		}
	}
	sliceOffs := offs[startIdx:]
	if len(sliceOffs) == 0 {
		return nil, true
	}
	var out []recordStub
	var buf []byte
	for i := 0; i < len(sliceOffs); {
		j := i + 1
		for j < len(sliceOffs) && sliceOffs[j]-sliceOffs[j-1] <= lineageSpanGap && sliceOffs[j]-sliceOffs[i] < spanSizeMax {
			j++
		}
		start := sliceOffs[i]
		want := int(sliceOffs[j-1]-start) + lineageStubWindow
		if cap(buf) < want {
			buf = make([]byte, want)
		}
		b := buf[:want]
		n, err := lr.file.ReadAt(b, start)
		if err != nil && !errors.Is(err, io.EOF) {
			n = 0
		}
		b = b[:n]
		for _, off := range sliceOffs[i:j] {
			st, keep, serr := lr.roleStubAtOffset(b, start, key, off, wantRoles)
			if serr != nil {
				if errors.Is(serr, errOffsetsMismatch) {
					lr.roOK = false
				}
				return nil, false
			}
			if keep {
				out = append(out, st)
			}
		}
		i = j
	}
	if startIdx == 0 && len(wantRoles) == 1 {
		for r := range wantRoles {
			byRole := lr.roleStubsByKey[key]
			if byRole == nil {
				byRole = map[string][]recordStub{}
				lr.roleStubsByKey[key] = byRole
			}
			byRole[r] = out
		}
	}
	return out, true
}

func (lr *lineageReader) fallbackRoleStubs(key string, wantRoles map[string]bool) ([]recordStub, bool) {
	if wantRoles != nil && len(wantRoles) == 0 {
		return nil, true
	}
	recs, ok := lr.fallbackRecords(key)
	if !ok {
		return nil, false
	}
	out := make([]recordStub, 0, len(recs))
	for _, r := range recs {
		if wantRoles != nil && !wantRoles[r.Role] {
			continue
		}
		out = append(out, recordStub{off: -1, role: r.Role, at: r.Time, text: r.Text})
	}
	return out, true
}

func (lr *lineageReader) sessionRoleStubs(key string, startIdx int, wantRoles map[string]bool) ([]recordStub, bool) {
	lr.ensureOpen()
	if lr.file == nil {
		return nil, false
	}
	if !lr.roOK {
		return lr.fallbackRoleStubs(key, wantRoles)
	}
	offs, ok := lr.offsetsFor(key)
	if !ok {
		return lr.fallbackRoleStubs(key, wantRoles)
	}
	stubs, ok := lr.roleStubsFrom(key, offs, startIdx, wantRoles)
	if !ok && !lr.roOK {
		return lr.fallbackRoleStubs(key, wantRoles)
	}
	return stubs, ok
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

func (lr *lineageReader) lastMatchFallback(key string, skipRec func(Record) bool) (Record, int, bool) {
	recs, ok := lr.fallbackRecords(key)
	if !ok {
		return Record{}, 0, false
	}
	for i, rec := range slices.Backward(recs) {
		if !skipRec(rec) {
			return rec, i, true
		}
	}
	return Record{}, 0, false
}

func (lr *lineageReader) lastMatchWithIndex(key string, skipRole func(string) bool, skipRec func(Record) bool) (Record, int, bool) {
	lr.ensureOpen()
	if lr.file == nil {
		return Record{}, 0, false
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
					return rec, i, true
				}
				continue
			}
			role, hdrOK, herr := lr.peekHeaderAt(off, key)
			if herr != nil {
				if errors.Is(herr, errOffsetsMismatch) {
					lr.roOK = false
					return lr.lastMatchFallback(key, skipRec)
				}
				return Record{}, 0, false
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
				return Record{}, 0, false
			}
			if !skipRec(rec) {
				return rec, i, true
			}
		}
	}
	return Record{}, 0, false
}

func (lr *lineageReader) lastMatch(key string, skipRole func(string) bool, skipRec func(Record) bool) (Record, bool) {
	rec, _, ok := lr.lastMatchWithIndex(key, skipRole, skipRec)
	return rec, ok
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

func (lr *lineageReader) matchAtOffset(key string, off int64, target Record) (bool, error) {
	if rec, ok := lr.fullByOff[off]; ok {
		return rec.Role == target.Role && rec.Time.UnixNano() == target.Time.UnixNano() && rec.Text == target.Text, nil
	}
	st, err := lr.stubAt(key, off)
	if err != nil {
		return false, err
	}
	if st.role != target.Role || st.at.UnixNano() != target.Time.UnixNano() || st.at.IsZero() != target.Time.IsZero() {
		return false, nil
	}
	rec, err := lr.fullRecordAt(key, off)
	if err != nil {
		return false, err
	}
	return rec.Text == target.Text, nil
}

func (lr *lineageReader) probeHoldCandidates(aKey string, aOffs []int64, target Record, targetIdx int) (matched, fallback bool, err error) {
	candidates := [9]int{
		targetIdx, targetIdx - 1, targetIdx + 1, targetIdx - 2, targetIdx + 2,
		len(aOffs) - 1, len(aOffs) - 2, len(aOffs) - 3, len(aOffs) - 4,
	}
	var seen [9]int
	seenN := 0
	for _, idx := range candidates {
		if idx < 0 || idx >= len(aOffs) || slices.Contains(seen[:seenN], idx) {
			continue
		}
		seen[seenN] = idx
		seenN++
		ok, perr := lr.matchAtOffset(aKey, aOffs[idx], target)
		if perr != nil {
			if errors.Is(perr, errOffsetsMismatch) {
				lr.roOK = false
				return false, true, nil
			}
			return false, false, perr
		}
		if ok {
			return true, false, nil
		}
	}
	return false, false, nil
}

func (lr *lineageReader) held(asker, other SessionMeta) bool {
	last, lastIdx, ok := lr.lastMatchWithIndex(other.Key(), isHoldNoiseRole, afterForkNoise)
	if !ok {
		return false
	}
	aKey := asker.Key()
	startIdx := 0
	if lr.roOK {
		if aOffs, aOK := lr.offsetsFor(aKey); aOK {
			if len(aOffs) == 0 {
				return false
			}
			matched, fallback, err := lr.probeHoldCandidates(aKey, aOffs, last, lastIdx)
			if err != nil {
				return false
			}
			if matched {
				return true
			}
			if !fallback && len(aOffs) >= lineagePrefixMinRecords {
				if oOffs, oOK := lr.offsetsFor(other.Key()); oOK && len(oOffs) >= lineagePrefixMinRecords {
					pref := lr.sharedPrefix(aKey, aOffs, other.Key(), oOffs)
					if pref.monotonic && last.Time.After(pref.bound) {
						startIdx = pref.length
					}
				}
			}
			if !lr.roOK {
				startIdx = 0
			}
		}
	}
	stubs, ok := lr.sessionRoleStubs(aKey, startIdx, map[string]bool{last.Role: true})
	if !ok {
		return false
	}
	wantNano := last.Time.UnixNano()
	for _, st := range stubs {
		if st.at.UnixNano() != wantNano {
			continue
		}
		if txt, ok := lr.stubText(aKey, st); ok && txt == last.Text {
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

func (lr *lineageReader) matchSharedStubs(longKey string, longStubs []recordStub, shortKey string, shortByRT map[roleTimeKey][]recordStub, best time.Time) time.Time {
	var cands []int
	for i, st := range longStubs {
		if !st.at.After(best) {
			continue
		}
		if len(shortByRT[roleTimeKey{role: st.role, nano: st.at.UnixNano()}]) > 0 {
			cands = append(cands, i)
		}
	}
	if len(cands) == 0 {
		return best
	}
	slices.SortFunc(cands, func(i, j int) int {
		ti, tj := longStubs[i].at, longStubs[j].at
		if ti.After(tj) {
			return -1
		}
		if tj.After(ti) {
			return 1
		}
		return 0
	})
	for _, idx := range cands {
		lSt := longStubs[idx]
		lTxt, ok := lr.stubText(longKey, lSt)
		if !ok {
			continue
		}
		for _, sSt := range shortByRT[roleTimeKey{role: lSt.role, nano: lSt.at.UnixNano()}] {
			if sTxt, ok := lr.stubText(shortKey, sSt); ok && sTxt == lTxt {
				return lSt.at
			}
		}
	}
	return best
}

func (lr *lineageReader) sharedPrefixAndBounds(aKey, oKey string) (best time.Time, startIdx int, shortKey, longKey string) {
	shortKey, longKey = aKey, oKey
	if !lr.roOK {
		return best, 0, shortKey, longKey
	}
	aOffs, aOK := lr.offsetsFor(aKey)
	oOffs, oOK := lr.offsetsFor(oKey)
	if !aOK || !oOK {
		return best, 0, shortKey, longKey
	}
	if len(oOffs) < len(aOffs) {
		shortKey, longKey = oKey, aKey
	}
	if len(aOffs) >= lineagePrefixMinRecords && len(oOffs) >= lineagePrefixMinRecords {
		if pref := lr.sharedPrefix(aKey, aOffs, oKey, oOffs); pref.ok {
			best = pref.bound
			if pref.monotonic {
				startIdx = pref.length
			}
		}
	}
	if !lr.roOK {
		return time.Time{}, 0, aKey, oKey
	}
	return best, startIdx, shortKey, longKey
}

func (lr *lineageReader) shared(asker, other SessionMeta) time.Time {
	var zero time.Time
	if asker.Harness != other.Harness {
		return zero
	}
	aKey, oKey := asker.Key(), other.Key()
	best, startIdx, shortKey, longKey := lr.sharedPrefixAndBounds(aKey, oKey)
	shortStubs, ok := lr.sessionRoleStubs(shortKey, startIdx, nil)
	if !ok {
		return zero
	}
	shortByRT := make(map[roleTimeKey][]recordStub, len(shortStubs))
	wantRoles := map[string]bool{}
	for _, st := range shortStubs {
		if !st.at.After(best) {
			continue
		}
		rt := roleTimeKey{role: st.role, nano: st.at.UnixNano()}
		shortByRT[rt] = append(shortByRT[rt], st)
		wantRoles[st.role] = true
	}
	if len(wantRoles) == 0 {
		return best
	}
	longStubs, ok := lr.sessionRoleStubs(longKey, startIdx, wantRoles)
	if !ok {
		return zero
	}
	return lr.matchSharedStubs(longKey, longStubs, shortKey, shortByRT, best)
}

func (lr *lineageReader) wholeShared(turns map[string]bool, other SessionMeta) time.Time {
	var at time.Time
	oStubs, ok := lr.sessionRoleStubs(other.Key(), 0, nil)
	if !ok {
		return at
	}
	for _, st := range oStubs {
		if !st.at.IsZero() && turns[turnKey(st.role, st.at.UnixMilli())] && st.at.After(at) {
			at = st.at
		}
	}
	return at
}
