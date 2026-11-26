package index

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/vshulcz/deja-vu/internal/vault"
)

// recordRoleIn reads only what a record's prefix says: whose session it is and
// what kind of record it is. Nothing is inflated, which is the point.
func recordRoleIn(b []byte, t *recordTables) (key, role string, ok bool) {
	kid, n := binary.Uvarint(b)
	if n <= 0 {
		return "", "", false
	}
	b = b[n:]
	_, n = binary.Uvarint(b)
	if n <= 0 {
		return "", "", false
	}
	b = b[n:]
	rid, n := binary.Uvarint(b)
	if n <= 0 {
		return "", "", false
	}
	return t.lookup(kid), t.lookup(rid), true
}

// RoleStubOthers instructs role-filtered session loaders to emit a zero-text
// stub Record{Key, Role} for records whose role is not wanted, preserving
// exact message slice indices without inflating unwanted record payloads.
const RoleStubOthers = "\x00stub"

// RoleStubToolOutput instructs record scans and loaders to decode only the
// header and 8-byte timestamp of tool-output records without inflating or
// allocating the message text.
const RoleStubToolOutput = "\x00stub-tool-output"

func roleWanted(roles map[string]bool, role string) bool {
	if roles == nil || role == "" {
		return true
	}
	if v, ok := roles[role]; ok {
		return v
	}
	return roles["*"]
}

func decodeRecordHeader(b []byte, t *recordTables) (Record, byte, []byte, error) {
	var rec Record
	rc, rcErr := t.cipherOrInit()
	if rcErr != nil {
		return rec, 0, nil, rcErr
	}
	kid, n := binary.Uvarint(b)
	if n <= 0 {
		return rec, 0, nil, errShortRecord
	}
	b = b[n:]
	pid, n := binary.Uvarint(b)
	if n <= 0 {
		return rec, 0, nil, errShortRecord
	}
	b = b[n:]
	rid, n := binary.Uvarint(b)
	if n <= 0 {
		return rec, 0, nil, errShortRecord
	}
	b = b[n:]
	rec.Key = t.lookup(kid)
	rec.SourcePath = t.lookup(pid)
	rec.Role = t.lookup(rid)
	if len(b) < 1 {
		return rec, 0, nil, errShortRecord
	}
	flag := b[0]
	payload := b[1:]
	if flag == recordSealed|recordRaw || flag == recordSealed|recordDeflated {
		baseFlag := flag &^ recordSealed
		if rc == nil {
			return rec, 0, nil, vault.ErrKeyUnavailable
		}
		plain, err := rc.Open(baseFlag, payload)
		if err != nil {
			return rec, 0, nil, fmt.Errorf("%w: %w", errCorruptIndex, err)
		}
		return rec, baseFlag, plain, nil
	}
	if rc != nil {
		if flag != recordRaw || slices.ContainsFunc(payload, func(c byte) bool { return c != 0 }) {
			return rec, 0, nil, fmt.Errorf("%w: unsealed record in encrypted store", errCorruptIndex)
		}
	}
	return rec, flag, payload, nil
}

const stubDeflatePrefix = 384

func inflateRecordTimestamp(b []byte, ts *[8]byte) error {
	zr := inflateReaders.Get().(io.ReadCloser)                                 //nolint:forcetypeassert // the pool holds only flate readers
	if err := zr.(flate.Resetter).Reset(bytes.NewReader(b), nil); err != nil { //nolint:forcetypeassert // a flate reader is a Resetter
		inflateReaders.Put(zr)
		return err
	}
	_, err := io.ReadFull(zr, ts[:])
	inflateReaders.Put(zr)
	return err
}

// decodeRecordStubRole decodes a record's key, source path, role, and 8-byte
// timestamp without reading or allocating its text body.
func decodeRecordStubRole(b []byte, t *recordTables) (Record, error) {
	rec, flag, b, err := decodeRecordHeader(b, t)
	if err != nil {
		return rec, err
	}
	var ts [8]byte
	switch flag {
	case recordDeflated:
		if err := inflateRecordTimestamp(b[:min(len(b), stubDeflatePrefix)], &ts); err != nil {
			if len(b) <= stubDeflatePrefix {
				return rec, err
			}
			if err := inflateRecordTimestamp(b, &ts); err != nil {
				return rec, err
			}
		}
	case recordRaw:
		if len(b) < 8 {
			return rec, errShortRecord
		}
		copy(ts[:], b[:8])
	default:
		return rec, fmt.Errorf("%w: unknown record encoding %d", errCorruptIndex, flag)
	}
	if n := int64(binary.LittleEndian.Uint64(ts[:])); n == zeroTimeUnixNano { //nolint:gosec // a signed time's bits, written as uint64
		rec.Time = time.Time{}
	} else {
		rec.Time = time.Unix(0, n)
	}
	return rec, nil
}

// zeroTimeUnixNano is what the zero time serializes to; records.bin has
// always stored it, so it stays the on-disk marker for "never stamped".
var zeroTimeUnixNano = time.Time{}.UnixNano()

// UnixNano is only defined for 1678-2262: outside that window the nanosecond
// count wraps, and what comes back is a plausible date rather than an obvious
// one. A transcript stamped 2999 read back out of records.bin as 1829, so
// `deja stats` reported a store that began 200 years before its oldest
// session. Clamping keeps a nonsense stamp at the edge of the range instead of
// moving it to the other end of history.
var (
	minRecordTime = time.Unix(0, math.MinInt64)
	maxRecordTime = time.Unix(0, math.MaxInt64)
)

func recordNanos(t time.Time) int64 {
	switch {
	case t.IsZero():
		return zeroTimeUnixNano
	case t.Before(minRecordTime):
		return math.MinInt64
	case t.After(maxRecordTime):
		return math.MaxInt64
	}
	return t.UnixNano()
}

// recordTables interns the two fields every record used to repeat verbatim.
// A corpus of 57k messages held only 90 distinct source paths and 1073
// distinct keys, so writing them per record cost 7.3 MB — 15% of the record
// log — to store 1163 strings.
//
// The table is its own thing, deliberately not the session Ord: reusing Ord
// would make a record's identity depend on the manifest's Ord bookkeeping
// being right, turning an Ord bug from "postings merged" into "this message
// belongs to a different session". The table is written by the same pass that
// writes the records and swapped with them.
type recordTables struct {
	strs []string
	id   map[string]uint64
	// dead is the manifest's DeadRecords: records a walk of the log skips.
	dead []int64

	cipherMu      sync.RWMutex
	dir           string
	cipher        *vault.RecordCipher
	cipherChecked bool
}

func (t *recordTables) cipherState() (*vault.RecordCipher, string, bool) {
	if t == nil {
		return nil, "", false
	}
	t.cipherMu.RLock()
	rc, dir, checked := t.cipher, t.dir, t.cipherChecked
	t.cipherMu.RUnlock()
	return rc, dir, checked
}

func (t *recordTables) recordCipher() (*vault.RecordCipher, string) {
	rc, dir, _ := t.cipherState()
	return rc, dir
}

func (t *recordTables) ensureCipher(dir string) error {
	canon := canonicalVaultDir(dir)
	if canon == "" {
		return nil
	}
	if _, curDir, checked := t.cipherState(); checked && curDir == canon {
		return nil
	}
	rc, _, err := vault.ActiveRecordCipher(canon)
	if err != nil {
		invalidateSnapshot(canon)
		return err
	}
	if t != nil {
		t.cipherMu.Lock()
		t.dir = canon
		t.cipher = rc
		t.cipherChecked = true
		t.cipherMu.Unlock()
	}
	return nil
}

func (t *recordTables) cipherOrInit() (*vault.RecordCipher, error) {
	rc, dir, checked := t.cipherState()
	if rc == nil && !checked && dir != "" {
		if err := t.ensureCipher(dir); err != nil {
			return nil, err
		}
		rc, _, _ = t.cipherState()
	}
	return rc, nil
}

func (t *recordTables) ensureCipherForFile(path string) error {
	if dir := vaultDirForFile(path); dir != "" {
		return t.ensureCipher(dir)
	}
	if _, dir, checked := t.cipherState(); dir != "" && !checked {
		return t.ensureCipher(dir)
	}
	return nil
}

type deadCursor struct{ d []int64 }

// at reports whether the record at off is dead. Offsets must be asked in
// ascending order.
func (c *deadCursor) at(off int64) bool {
	for len(c.d) > 0 && c.d[0] < off {
		c.d = c.d[1:]
	}
	return len(c.d) > 0 && c.d[0] == off
}

func newRecordTables() *recordTables {
	return &recordTables{id: map[string]uint64{}}
}

// tablesFromStrings is the read path: resolving an id is a slice index, so
// the string->id map is left nil and built lazily only if something interns.
// Building it eagerly here cost a map of every string in the corpus on every
// search.
func tablesFromStrings(strs []string) *recordTables {
	return &recordTables{strs: strs}
}

func tablesFromManifest(m Manifest) *recordTables {
	t := tablesFromStrings(m.RecordStrings)
	t.dead = m.DeadRecords
	t.dir = canonicalVaultDir(m.vaultDir)
	return t
}

func loadRecordTables(dir string) (*recordTables, error) {
	// Cached: this sits on the search path, and re-decoding the whole
	// manifest per query is what the interning was meant to avoid paying for.
	m, err := readManifestCached(dir)
	if err != nil {
		return nil, err
	}
	t := tablesFromManifest(m)
	if err := t.ensureCipher(dir); err != nil {
		return nil, err
	}
	return t, nil
}

// fresh gives s an id of its own from here on, past every id the table had.
// Records written under it resolve to nothing in a table from before, which
// is how a reader of the previous manifest skips a tail appended for it.
func (t *recordTables) fresh(s string) {
	if before := len(t.strs); t.intern(s) >= uint64(before) {
		return // new to the table: its id is past every old one already
	}
	id := uint64(len(t.strs))
	t.strs = append(t.strs, s)
	t.id[s] = id
}

// intern returns the id for s, appending it on first sight. Ids are stable:
// the table only ever grows, so an appended record log keeps resolving.
func (t *recordTables) intern(s string) uint64 {
	if t.id == nil {
		// A zero-value table is usable; callers that build a writer by hand
		// should not have to know about the map.
		t.id = make(map[string]uint64, len(t.strs)+8)
		for i, v := range t.strs {
			if _, ok := t.id[v]; !ok {
				t.id[v] = uint64(i)
			}
		}
	}
	if id, ok := t.id[s]; ok {
		return id
	}
	id := uint64(len(t.strs))
	t.strs = append(t.strs, s)
	t.id[s] = id
	return id
}

func (t *recordTables) lookup(id uint64) string {
	if id < uint64(len(t.strs)) {
		return t.strs[id]
	}
	return ""
}

// compressFloor used to be 8 KB, from a run that compressed every message at
// the default level: the record log halved, but index builds doubled and a
// query that materializes many records tripled. That left 99.9% of records
// stored as plain text and records.bin bigger than the transcripts. At the
// fastest level and a 256-byte floor the day-zero pile's index went from 277
// to 194 MB for 3-5% more build CPU and no measurable query cost. Below 256
// bytes deflate rarely wins anything. Readers go by the per-record flag, so
// an index mixing both encodings needs no version bump.
const compressFloor = 256

const (
	recordRaw      = 0
	recordDeflated = 1
	recordSealed   = 0x80
)

var deflateWriters = sync.Pool{New: func() any { w, _ := flate.NewWriter(nil, flate.BestSpeed); return w }}
var inflateReaders = sync.Pool{New: func() any { return flate.NewReader(nil) }}

// A record is [keyID][pathID][flag][payload]. The ids stay outside the
// payload so a walk can peek the key and skip the record without inflating
// anything — that path never touches a compressed byte.
func encodeRecordChecked(r Record, t *recordTables) ([]byte, error) {
	rc, rcErr := t.cipherOrInit()
	if rcErr != nil {
		return nil, rcErr
	}
	body := make([]byte, 0, len(r.Text)+24)
	body = binary.LittleEndian.AppendUint64(body, uint64(recordNanos(r.Time))) //nolint:gosec // a signed time's bits, read back as int64
	body = appendField(body, r.Text)

	b := make([]byte, 0, len(body)+16)
	b = binary.AppendUvarint(b, t.intern(r.Key))
	b = binary.AppendUvarint(b, t.intern(r.SourcePath))
	// The role sits outside the compressed body so a scan for one kind of
	// record can skip the rest without inflating them. It used to be the first
	// field inside the body, which made "every command in the store" — what
	// `deja how` asks — decompress all 280,000 records to read 20,000 of them:
	// 53% of that command's time was flate.
	b = binary.AppendUvarint(b, t.intern(r.Role))

	var baseFlag byte = recordRaw
	payload := body
	if len(body) >= compressFloor {
		var buf bytes.Buffer
		w := deflateWriters.Get().(*flate.Writer) //nolint:forcetypeassert // the pool holds only *flate.Writer
		w.Reset(&buf)
		if _, err := w.Write(body); err != nil {
			deflateWriters.Put(w)
		} else {
			err := w.Close()
			deflateWriters.Put(w)
			// Incompressible payloads (already-compressed blobs, random ids) are
			// stored as they are rather than grown.
			if err == nil && buf.Len() < len(body) {
				baseFlag = recordDeflated
				payload = buf.Bytes()
			}
		}
	}
	if rc != nil {
		sealed, err := rc.Seal(baseFlag, payload)
		if err != nil {
			return nil, err
		}
		return append(append(b, baseFlag|recordSealed), sealed...), nil
	}
	return append(append(b, baseFlag), payload...), nil
}

func encodeRecord(r Record, t *recordTables) []byte {
	b, err := encodeRecordChecked(r, t)
	if err != nil {
		panic(fmt.Sprintf("encodeRecord: %v", err))
	}
	return b
}

func appendField(b []byte, s string) []byte {
	b = binary.AppendUvarint(b, uint64(len(s)))
	return append(b, s...)
}

// errShortRecord is a framed record whose payload does not hold what every
// record holds. The frame was read whole, so this is not a torn tail: it is a
// hole in the log, such as a frame a crash left zeroed, and it is corruption.
// It used to be io.ErrUnexpectedEOF, which every walker reads as the end of
// the file, so a hole mid-log silently ended the walk there.
var errShortRecord = fmt.Errorf("%w: a record ends before its fields do", errCorruptIndex)

func decodeRecord(b []byte, t *recordTables) (Record, error) {
	rec, flag, b, err := decodeRecordHeader(b, t)
	if err != nil {
		return rec, err
	}
	var ok bool
	if flag == recordDeflated {
		zr := inflateReaders.Get().(io.ReadCloser)                                 //nolint:forcetypeassert // the pool holds only flate readers
		if err := zr.(flate.Resetter).Reset(bytes.NewReader(b), nil); err != nil { //nolint:forcetypeassert // a flate reader is a Resetter
			inflateReaders.Put(zr)
			return rec, err
		}
		body, err := io.ReadAll(zr)
		inflateReaders.Put(zr)
		if err != nil {
			return rec, err
		}
		b = body
	} else if flag != recordRaw {
		return rec, fmt.Errorf("%w: unknown record encoding %d", errCorruptIndex, flag)
	}
	if len(b) < 8 {
		return rec, errShortRecord
	}
	// time.Time{}.UnixNano() is a large negative number that time.Unix turns
	// into the year 1754, so an unstamped message never satisfied IsZero()
	// again. Sync then read it as older than any watermark and skipped it on
	// every push — the message could never reach another machine, silently.
	if n := int64(binary.LittleEndian.Uint64(b[:8])); n == zeroTimeUnixNano { //nolint:gosec // a signed time's bits, written as uint64
		rec.Time = time.Time{}
	} else {
		rec.Time = time.Unix(0, n)
	}
	b = b[8:]
	if rec.Text, ok = consumeField(b); !ok {
		return rec, errShortRecord
	}
	return rec, nil
}

func consumeField(b []byte) (string, bool) {
	n, used := binary.Uvarint(b)
	if used <= 0 || uint64(len(b)-used) < n { //nolint:gosec // used <= len(b)
		return "", false
	}
	start := used
	end := start + int(n) //nolint:gosec // n is checked against len(b) above
	return string(b[start:end]), true
}

// decodeRecordIn decodes the record at rel inside an already-read span, and
// reports whether the span actually held all of it.
func decodeRecordIn(b []byte, rel int, t *recordTables) (Record, bool) {
	if rel < 0 || rel+4 > len(b) {
		return Record{}, false
	}
	size := binary.LittleEndian.Uint32(b[rel : rel+4])
	if size > maxRecordSize || rel+4+int(size) > len(b) {
		return Record{}, false
	}
	r, err := decodeRecord(b[rel+4:rel+4+int(size)], t)
	return r, err == nil
}
