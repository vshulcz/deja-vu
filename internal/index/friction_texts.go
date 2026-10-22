package index

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
)

// frictionTextsFile holds, for every session that carries a wall, the first
// line of its tool output behind each wall's hash. TopFriction ranks walls
// from the manifest and then had to read those sessions back and run every
// line of their output through FrictionLine to learn what a hash stood for:
// on a real store 145 sessions and 137 MB of output, most of the session-start
// block's time.
//
// Like the offset table it is a cache, never the authority. The header names
// the log it was written against; a reader whose log differs, or that wants a
// wall the file was not written for, recovers the text as before. A session
// with no entry is read back as before too.
const frictionTextsFile = "frictext.bin"

var frictionTextsMagic = [8]byte{'D', 'J', 'F', 'T', 'X', 'T', '0', '1'}

// frictionTextsOff turns the file off for readers, so a test can compare.
var frictionTextsOff bool

// frictionEntry is one session's part: a fingerprint of its tool output as
// frictionReader reads it, and the first line behind each wall it holds.
type frictionEntry struct {
	stamp uint64
	lines map[uint64]string
}

type frictionTextFile struct {
	size    int64
	fp      [sha256.Size]byte
	walls   map[uint64]bool
	entries map[string]frictionEntry
}

// frictionWalls is every hash enough sessions share to be a wall, before any
// caller's project gate: what TopFriction can ever ask for.
func frictionWalls(m Manifest) map[uint64]bool {
	n := map[uint64]int{}
	for _, meta := range m.Sessions {
		for _, h := range meta.Hit {
			n[h]++
		}
	}
	out := map[uint64]bool{}
	for h, c := range n {
		if c >= FrictionMinSessions {
			out[h] = true
		}
	}
	return out
}

// frictionCarriers is the sessions holding one of walls, by record key.
func frictionCarriers(m Manifest, walls map[uint64]bool) map[string]SessionMeta {
	out := map[string]SessionMeta{}
	for _, meta := range m.Sessions {
		for _, h := range meta.Hit {
			if walls[h] {
				out[meta.Harness+":"+meta.ID] = meta
				break
			}
		}
	}
	return out
}

const fnvOffset64, fnvPrime64 = 14695981039346656037, 1099511628211

// stampText folds one tool output into a session's stamp: FNV-1a over its
// length and bytes, so the stamp of a session extended by an append is the
// old stamp carried on over the new outputs.
func stampText(h uint64, text string) uint64 {
	var n [8]byte
	binary.LittleEndian.PutUint64(n[:], uint64(len(text)))
	for _, c := range n {
		h = (h ^ uint64(c)) * fnvPrime64
	}
	for i := 0; i < len(text); i++ {
		h = (h ^ uint64(text[i])) * fnvPrime64
	}
	return h
}

// firstFrictionLines adds to lines the first line of text behind each of
// walls not already there. It is frictionTexts' loop with the wanted set
// fixed to every wall.
func firstFrictionLines(text string, walls map[uint64]bool, lines map[uint64]string) {
	for line := range strings.SplitSeq(text, "\n") {
		line, ok := FrictionLine(line)
		if !ok || line == "" {
			continue
		}
		h := frictionHash(line)
		if walls[h] && lines[h] == "" {
			lines[h] = line
		}
	}
}

// writeFrictionTexts brings the file in dir up to m and the first size bytes
// of its log. Sessions the log did not touch since the last write keep their
// entry; an append extends the entry of a session it added to; a wall new to
// the store makes the sessions that may hold it be read whole. A file carried
// from another log has each entry checked against its session's stamp.
func writeFrictionTexts(dir string, m Manifest, size int64) error {
	path := filepath.Join(dir, frictionTextsFile)
	walls := frictionWalls(m)
	carriers := frictionCarriers(m, walls)
	f, err := os.Open(filepath.Join(dir, "records.bin"))
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	defer func() { _ = f.Close() }()
	if fi, err := f.Stat(); err != nil {
		return err
	} else if fi.Size() < size {
		size = fi.Size()
	}
	fp, err := recordLogFingerprint(f, size)
	if err != nil {
		return err
	}
	old, haveOld := decodeFrictionTextsFile(path)
	if haveOld && old.size == size && old.fp == fp && sameWalls(old.walls, walls) && sameKeys(old.entries, carriers) {
		return nil
	}
	sameLog := false
	if haveOld && old.size <= size {
		if ofp, err := recordLogFingerprint(f, old.size); err == nil && ofp == old.fp {
			sameLog = true
		}
	}
	// A wall that was not one when the entries were written: a session can
	// hold its line only if the hash is in its Hit, or if Hit was cut short.
	var added []uint64
	for h := range walls {
		if !old.walls[h] {
			added = append(added, h)
		}
	}
	needsWhole := func(meta SessionMeta) bool {
		if len(added) == 0 {
			return false
		}
		if len(meta.Hit) >= frictionSessionCap {
			return true
		}
		for _, h := range meta.Hit {
			if !old.walls[h] && walls[h] {
				return true
			}
		}
		return false
	}
	t := tablesFromManifest(m)
	changed := map[string]bool{}
	if sameLog && old.size < size {
		tail := map[string][]int64{}
		if _, err := scanRecordOffsets(f, t, old.size, size, tail); err != nil {
			_ = os.Remove(path)
			return err
		}
		for k := range tail {
			changed[k] = true
		}
	}
	out := frictionTextFile{size: size, fp: fp, walls: walls, entries: map[string]frictionEntry{}}
	whole := map[string]bool{}  // read whole and parsed
	verify := map[string]bool{} // read whole, parsed only if the stamp moved
	extend := map[string]bool{} // the tail read and parsed onto the old entry
	for k, meta := range carriers {
		e, ok := old.entries[k]
		switch {
		case !ok || needsWhole(meta):
			whole[k] = true
		case !sameLog:
			verify[k] = true
		case !changed[k]:
			out.entries[k] = frictionEntry{stamp: e.stamp, lines: keepWalls(e.lines, walls)}
		case meta.Harness == "deja":
			// A promoted note is reordered on load, so its first lines are
			// not an append's to extend.
			whole[k] = true
		case meta.Shared:
			// The tail may be a copy that took the row, which leaves the old
			// entry the other copy's.
			whole[k] = true
		default:
			extend[k] = true
		}
	}
	read := func(keys, stampOnly map[string]bool, from int64, start func(k string) (uint64, map[uint64]string)) (map[string]frictionEntry, error) {
		return readFrictionEntries(dir, m, f, t, keys, stampOnly, from, walls, start)
	}
	fresh := func(string) (uint64, map[uint64]string) { return fnvOffset64, map[uint64]string{} }
	// What reads whole and what is checked share one pass over the log; an
	// entry whose session reads differently now is parsed on a second.
	all := map[string]bool{}
	for k := range whole {
		all[k] = true
	}
	for k := range verify {
		all[k] = true
	}
	got, err := read(all, verify, 0, fresh)
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	again := map[string]bool{}
	for k, e := range got {
		if verify[k] {
			if e.stamp != old.entries[k].stamp {
				again[k] = true
				continue
			}
			e.lines = keepWalls(old.entries[k].lines, walls)
		}
		out.entries[k] = e
	}
	got, err = read(again, nil, 0, fresh)
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	for k, e := range got {
		out.entries[k] = e
	}
	got, err = read(extend, nil, old.size, func(k string) (uint64, map[uint64]string) {
		e := old.entries[k]
		return e.stamp, keepWalls(e.lines, walls)
	})
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	for k, e := range got {
		out.entries[k] = e
	}
	return writeFileAtomic(path, encodeFrictionTexts(out))
}

func sameWalls(a, b map[uint64]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for h := range a {
		if !b[h] {
			return false
		}
	}
	return true
}

func sameKeys(a map[string]frictionEntry, b map[string]SessionMeta) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

func keepWalls(lines map[uint64]string, walls map[uint64]bool) map[uint64]string {
	out := make(map[uint64]string, len(lines))
	for h, l := range lines {
		if walls[h] {
			out[h] = l
		}
	}
	return out
}

// frictionOutputsRead counts the tool outputs read back to write the file.
var frictionOutputsRead int64

// readFrictionEntries reads the tool output of keys from offset from on and
// folds it onto what start gives each key. A promoted note is loaded the way
// frictionReader loads it.
func readFrictionEntries(dir string, m Manifest, f *os.File, t *recordTables, keys, stampOnly map[string]bool, from int64, walls map[uint64]bool, start func(string) (uint64, map[uint64]string)) (map[string]frictionEntry, error) {
	out := make(map[string]frictionEntry, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	// In the read's own goroutine: the parse is a fraction of a second on a
	// real store, and handing 150k outputs to workers cost more in wakeups.
	send := func(key, text string) {
		atomic.AddInt64(&frictionOutputsRead, 1)
		e, ok := out[key]
		if !ok {
			e.stamp, e.lines = start(key)
		}
		e.stamp = stampText(e.stamp, text)
		if !stampOnly[key] {
			firstFrictionLines(text, walls, e.lines)
		}
		out[key] = e
	}
	plain := map[string]bool{}
	var notes []SessionMeta
	for k := range keys {
		if meta, ok := m.Sessions[k]; ok && meta.Harness == "deja" {
			notes = append(notes, meta)
			continue
		}
		plain[k] = true
	}
	var err error
	if len(plain) > 0 {
		roles := map[string]bool{roleToolOutput: true}
		each := func(r Record) {
			// The row's copy, as the reader takes it (see toolOutput).
			if !foreignPath(r.SourcePath, m.Sessions[r.Key]) {
				send(r.Key, r.Text)
			}
		}
		if from == 0 {
			err = eachRecordForKeysInRoles(filepath.Join(dir, "records.bin"), t, plain, roles, each)
		} else if _, err = f.Seek(from, io.SeekStart); err == nil {
			err = walkRecordsForKeys(bufio.NewReaderSize(f, 256*1024), from, t, plain, roles, each)
		}
	}
	for _, meta := range notes {
		if err != nil {
			break
		}
		// A note that does not load yields nothing to frictionReader either.
		s, ok, lerr := loadSessionMeta(dir, m, meta)
		if lerr != nil || !ok {
			continue
		}
		for _, msg := range s.Messages {
			if msg.Role == roleToolOutput {
				send(meta.Harness+":"+meta.ID, msg.Text)
			}
		}
	}
	if err != nil {
		return nil, err
	}
	// A session with no tool output in what was read keeps what it started
	// with: nothing for a fresh one, its old entry for an extended one.
	for k := range keys {
		if _, ok := out[k]; !ok {
			stamp, lines := start(k)
			out[k] = frictionEntry{stamp: stamp, lines: lines}
		}
	}
	return out, nil
}

func encodeFrictionTexts(ft frictionTextFile) []byte {
	var b bytes.Buffer
	var tmp [binary.MaxVarintLen64]byte
	uv := func(v uint64) { b.Write(tmp[:binary.PutUvarint(tmp[:], v)]) }
	u64 := func(v uint64) { binary.LittleEndian.PutUint64(tmp[:8], v); b.Write(tmp[:8]) }
	str := func(s string) { uv(uint64(len(s))); b.WriteString(s) }
	b.Write(frictionTextsMagic[:])
	u64(uint64(ft.size))
	b.Write(ft.fp[:])
	walls := make([]uint64, 0, len(ft.walls))
	for h := range ft.walls {
		walls = append(walls, h)
	}
	sort.Slice(walls, func(i, j int) bool { return walls[i] < walls[j] })
	uv(uint64(len(walls)))
	for _, h := range walls {
		u64(h)
	}
	keys := make([]string, 0, len(ft.entries))
	for k := range ft.entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	uv(uint64(len(keys)))
	for _, k := range keys {
		e := ft.entries[k]
		str(k)
		u64(e.stamp)
		hs := make([]uint64, 0, len(e.lines))
		for h := range e.lines {
			hs = append(hs, h)
		}
		sort.Slice(hs, func(i, j int) bool { return hs[i] < hs[j] })
		uv(uint64(len(hs)))
		for _, h := range hs {
			u64(h)
			str(e.lines[h])
		}
	}
	return b.Bytes()
}

// decodeFrictionTextsFile reads the file without checking it against a log.
func decodeFrictionTextsFile(path string) (frictionTextFile, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return frictionTextFile{}, false
	}
	return decodeFrictionTexts(b)
}

func decodeFrictionTexts(b []byte) (frictionTextFile, bool) {
	p := 0
	bad := false
	u64 := func() uint64 {
		if bad || p+8 > len(b) {
			bad = true
			return 0
		}
		v := binary.LittleEndian.Uint64(b[p:])
		p += 8
		return v
	}
	uv := func() uint64 {
		if bad {
			return 0
		}
		v, n := binary.Uvarint(b[p:])
		if n <= 0 {
			bad = true
			return 0
		}
		p += n
		return v
	}
	str := func() string {
		n := uv()
		if bad || n > uint64(len(b)-p) {
			bad = true
			return ""
		}
		s := string(b[p : p+int(n)])
		p += int(n)
		return s
	}
	var ft frictionTextFile
	if len(b) < 8+8+sha256.Size || !bytes.Equal(b[:8], frictionTextsMagic[:]) {
		return frictionTextFile{}, false
	}
	p = 8
	ft.size = int64(u64())
	copy(ft.fp[:], b[p:p+sha256.Size])
	p += sha256.Size
	nw := uv()
	if nw > uint64(len(b)) {
		return frictionTextFile{}, false
	}
	ft.walls = make(map[uint64]bool, nw)
	for i := uint64(0); i < nw && !bad; i++ {
		ft.walls[u64()] = true
	}
	ne := uv()
	if ne > uint64(len(b)) {
		return frictionTextFile{}, false
	}
	ft.entries = make(map[string]frictionEntry, ne)
	for i := uint64(0); i < ne && !bad; i++ {
		k := str()
		e := frictionEntry{stamp: u64(), lines: map[uint64]string{}}
		nl := uv()
		if nl > uint64(len(b)) {
			bad = true
		}
		for j := uint64(0); j < nl && !bad; j++ {
			h := u64()
			e.lines[h] = str()
		}
		ft.entries[k] = e
	}
	if bad || p != len(b) {
		return frictionTextFile{}, false
	}
	return ft, true
}

// loadFrictionTexts reads the file in dir for a reader of m wanting want. It
// answers only for the log m committed, untouched since, and only when every
// wanted hash was a wall when it was written; otherwise nil, and the reader
// reads the sessions back.
func loadFrictionTexts(dir string, m Manifest, want map[uint64]string) map[string]frictionEntry {
	if frictionTextsOff || len(want) == 0 {
		return nil
	}
	ft, ok := decodeFrictionTextsFile(filepath.Join(dir, frictionTextsFile))
	if !ok || ft.size != m.RecordsSize {
		return nil
	}
	for h := range want {
		if !ft.walls[h] {
			return nil
		}
	}
	f, err := os.Open(filepath.Join(dir, "records.bin"))
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	// Records past the committed size are read by a walk, so a file that
	// stops there would not say what the walk says.
	if fi, err := f.Stat(); err != nil || fi.Size() != ft.size {
		return nil
	}
	if fp, err := recordLogFingerprint(f, ft.size); err != nil || fp != ft.fp {
		return nil
	}
	return ft.entries
}

// carryFrictionTexts hands a pass that writes its log again in tmp the
// entries of the log in dir, re-stamped for the new log, less the sessions in
// changed. The rest were copied record for record in their order, so their
// stamps and lines still hold, and the commit reads only the changed ones.
// Without it every entry was checked against the new log by reading every
// session that carries a wall (#5402). A file that does not describe dir's
// committed log is copied as it is, and checked entry by entry as before.
func carryFrictionTexts(dir, tmp string, old Manifest, changed map[string]bool) {
	b, err := os.ReadFile(filepath.Join(dir, frictionTextsFile))
	if err != nil {
		return
	}
	dst := filepath.Join(tmp, frictionTextsFile)
	ft, ok := decodeFrictionTexts(b)
	// A prefix of the log, not the whole of it: a pass that appends its
	// records shares dir's log, which has grown by them already (#5387).
	if !ok || ft.size != old.RecordsSize || !logPrefixIs(filepath.Join(dir, "records.bin"), ft.size, ft.fp) {
		// Not the committed log's. Copied, it would read as covering a log
		// an appending pass leaves the same up to its tail, sessions it
		// replaced included; the commit builds the file instead.
		return
	}
	size, fp, err := logFingerprint(filepath.Join(tmp, "records.bin"))
	if err != nil {
		_ = os.WriteFile(dst, b, 0o600)
		return
	}
	for k := range changed {
		delete(ft.entries, k)
	}
	ft.size, ft.fp = size, fp
	_ = writeFileAtomic(dst, encodeFrictionTexts(ft))
}

// logFingerprint is the size and fingerprint of the whole log at path.
func logFingerprint(path string) (int64, [sha256.Size]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, [sha256.Size]byte{}, err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return 0, [sha256.Size]byte{}, err
	}
	fp, err := recordLogFingerprint(f, fi.Size())
	return fi.Size(), fp, err
}

// logPrefixIs reports whether the log at path starts with size bytes whose
// fingerprint is fp.
func logPrefixIs(path string, size int64, fp [sha256.Size]byte) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	if fi, err := f.Stat(); err != nil || fi.Size() < size {
		return false
	}
	got, err := recordLogFingerprint(f, size)
	return err == nil && got == fp
}
