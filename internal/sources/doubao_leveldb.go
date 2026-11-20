package sources

// Read-only LevelDB history reader for Chromium IndexedDB backing stores,
// written for the Doubao Desktop reader (doubao.go). IndexedDB keeps its
// records in a LevelDB directory (`<origin>_0.indexeddb.leveldb`): SSTables
// (`.ldb`) plus a write-ahead log (`.log`). Only the shapes a Doubao store
// holds are handled, and nothing here writes.
//
// Two quirks matter and both bite silently when missed:
//
//   - SSTable keys are *internal* keys: user key plus an 8-byte trailer,
//     little-endian `(sequence << 8) | type` (1 = put, 0 = delete). WriteBatch
//     keys in the log have no trailer; stripping it there cuts two different
//     keys into one.
//   - Doubao rewrites the whole protocol record on every streaming increment,
//     so a useful store holds many *versions* of one key across files and
//     compaction. readLevelDbHistory therefore hands out every version of
//     every key in write order, not just the live one.
//
// Only block compression snappy is handled, which is what Chromium writes.
// Corrupt log frames are counted, not fatal: one torn frame must not cost the
// rest of the store.
//
// Format attested by Nwflower/dsh-chat-import (lib/sources/chromium-indexeddb.mjs,
// drift adapted in PR #123) and checked against a real 2.37.8 store.

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"sort"
)

const (
	leveldbBlockSize    = 32768
	snappyMaxOutputSize = 64 * 1024 * 1024
)

// leveldbVersion is one write of one key. value is nil for a deletion seen in
// the write-ahead log; a tombstone in an SSTable carries an empty value
// instead, which no decoder accepts, and is dropped by the value layer.
type leveldbVersion struct {
	key   []byte
	value []byte
	seq   uint64
}

// crc32cTable holds the Castagnoli lookup table LevelDB frames are checksummed with.
var crc32cTable = func() (t [256]uint32) {
	for i := range 256 {
		c := uint32(i)
		for range 8 {
			if c&1 != 0 {
				c = 0x82f63b78 ^ (c >> 1)
			} else {
				c >>= 1
			}
		}
		t[i] = c
	}
	return t
}()

func crc32c(b []byte) uint32 {
	c := ^uint32(0)
	for _, v := range b {
		c = crc32cTable[(c^uint32(v))&0xff] ^ (c >> 8)
	}
	return ^c
}

// unmaskCrc reverses LevelDB's frame checksum masking, which rotates right by
// 15 and adds a fixed delta.
func unmaskCrc(masked uint32) uint32 {
	rot := masked - 0xa282ead8
	return (rot >> 17) | (rot << 15)
}

// snappyUncompress decodes a raw snappy block (no framing). Malformed input
// errors; the caller counts that and moves on.
func snappyUncompress(input []byte) ([]byte, error) {
	pos := 0
	outLen, shift := uint64(0), uint(0)
	for {
		if pos >= len(input) {
			return nil, errors.New("snappy: truncated length prefix")
		}
		b := input[pos]
		pos++
		outLen += uint64(b&0x7f) << shift
		if b < 0x80 {
			break
		}
		shift += 7
		if shift > 35 {
			return nil, errors.New("snappy: length prefix too long")
		}
	}
	if outLen > snappyMaxOutputSize {
		return nil, errors.New("snappy: implausible output length")
	}
	out := make([]byte, outLen)
	opos := 0
	for pos < len(input) && opos < len(out) {
		tag := input[pos]
		pos++
		switch tag & 3 {
		case 0:
			// The length code spans 60..63, so the whole length fits int many
			// times over; decoding it in the int domain needs no narrowing.
			length := int(tag >> 2)
			if length >= 60 {
				extra := length - 59
				if extra > len(input)-pos {
					return nil, errors.New("snappy: truncated literal length")
				}
				length = 0
				for i := range extra {
					length |= int(input[pos+i]) << (8 * i)
				}
				pos += extra
			}
			length++
			if pos+length > len(input) || opos+length > len(out) {
				return nil, errors.New("snappy: literal out of bounds")
			}
			copy(out[opos:], input[pos:pos+length])
			pos += length
			opos += length
		case 1:
			if pos >= len(input) {
				return nil, errors.New("snappy: truncated copy1")
			}
			length := int((tag>>2)&0x7) + 4
			offset := int(tag>>5)<<8 | int(input[pos])
			pos++
			if offset == 0 || offset > opos || opos+length > len(out) {
				return nil, errors.New("snappy: copy out of bounds")
			}
			for range length {
				out[opos] = out[opos-offset]
				opos++
			}
		case 2:
			if pos+1 >= len(input) {
				return nil, errors.New("snappy: truncated copy2")
			}
			length := int(tag>>2) + 1
			offset := int(input[pos]) | int(input[pos+1])<<8
			pos += 2
			if offset == 0 || offset > opos || opos+length > len(out) {
				return nil, errors.New("snappy: copy out of bounds")
			}
			for range length {
				out[opos] = out[opos-offset]
				opos++
			}
		default:
			if pos+4 > len(input) {
				return nil, errors.New("snappy: truncated copy4")
			}
			length := int(tag>>2) + 1
			off32 := binary.LittleEndian.Uint32(input[pos : pos+4])
			pos += 4
			if off32 == 0 || uint64(off32) > uint64(opos) || opos+length > len(out) {
				return nil, errors.New("snappy: copy out of bounds")
			}
			offset := int(off32)
			for range length {
				out[opos] = out[opos-offset]
				opos++
			}
		}
	}
	if opos != len(out) {
		return nil, errors.New("snappy: decoded length mismatch")
	}
	return out, nil
}

// varintLen narrows a decoded varint length to int once it is proven to fit
// the bytes available. The bound check runs in the unsigned domain the varint
// came from, so an over-long length is rejected here rather than wrapped by
// the conversion; the mask documents the surviving domain for the checker.
func varintLen(n uint64, avail int) (int, bool) {
	if avail < 0 || n > uint64(avail) {
		return 0, false
	}
	return int(n & 0x7fffffffffffffff), true
}

// readVarint reads a protobuf-style unsigned varint.
func readVarint(b []byte, pos int) (uint64, int, error) {
	var result uint64
	shift := uint(0)
	for {
		if pos >= len(b) {
			return 0, pos, errors.New("varint out of bounds")
		}
		c := b[pos]
		pos++
		result |= uint64(c&0x7f) << shift
		if c < 0x80 {
			return result, pos, nil
		}
		shift += 7
		if shift > 63 {
			return 0, pos, errors.New("varint too long")
		}
	}
}

// leveldbBlock reads one block body: type 0 is plain, 1 snappy-compressed.
// An unreadable block yields nil and the caller drops it.
func leveldbBlock(buf []byte, offset, size int) []byte {
	if offset < 0 || size < 0 || offset+size+1 > len(buf) {
		return nil
	}
	raw := buf[offset : offset+size]
	switch buf[offset+size] {
	case 0:
		return raw
	case 1:
		out, err := snappyUncompress(raw)
		if err != nil {
			return nil
		}
		return out
	default:
		return nil
	}
}

type leveldbEntry struct {
	key   []byte
	value []byte
}

// sstBlockEntries decodes prefix-compressed entries of one block. A triple of
// zero varints is the restart array, not an entry.
func sstBlockEntries(block []byte) []leveldbEntry {
	var out []leveldbEntry
	pos := 0
	var key []byte
	for pos < len(block) {
		shared, p, err := readVarint(block, pos)
		if err != nil {
			break
		}
		nonShared, p2, err := readVarint(block, p)
		if err != nil {
			break
		}
		vlen, p3, err := readVarint(block, p2)
		if err != nil {
			break
		}
		pos = p3
		if shared == 0 && nonShared == 0 && vlen == 0 {
			break
		}
		ns, ok := varintLen(nonShared, len(block)-pos)
		if !ok {
			break
		}
		vl, ok := varintLen(vlen, len(block)-pos-ns)
		if !ok {
			break
		}
		sh, ok := varintLen(shared, len(key))
		if !ok {
			break
		}
		key = append(key[:sh], block[pos:pos+ns]...)
		pos += ns
		out = append(out, leveldbEntry{
			key:   append([]byte(nil), key...),
			value: block[pos : pos+vl],
		})
		pos += vl
	}
	return out
}

// sstEntries decodes every data entry of one SSTable through its index block.
// The footer's magic is not checked: a truncated file yields nothing rather
// than an error, matching how the reader treats a store mid-compaction.
func sstEntries(buf []byte) []leveldbEntry {
	if len(buf) < 48 {
		return nil
	}
	footer := buf[len(buf)-48:]
	metaOff, p, err := readVarint(footer, 0)
	if err != nil {
		return nil
	}
	_, p2, err := readVarint(footer, p)
	if err != nil {
		return nil
	}
	indexOff, p3, err := readVarint(footer, p2)
	if err != nil {
		return nil
	}
	indexSize, _, err := readVarint(footer, p3)
	if err != nil {
		return nil
	}
	_ = metaOff
	io, ok := varintLen(indexOff, len(buf))
	if !ok {
		return nil
	}
	isz, ok := varintLen(indexSize, len(buf)-io)
	if !ok {
		return nil
	}
	indexBlock := leveldbBlock(buf, io, isz)
	if indexBlock == nil {
		return nil
	}
	var out []leveldbEntry
	for _, handle := range sstBlockEntries(indexBlock) {
		dataOff, r, err := readVarint(handle.value, 0)
		if err != nil {
			continue
		}
		dataSize, _, err := readVarint(handle.value, r)
		if err != nil {
			continue
		}
		do, ok := varintLen(dataOff, len(buf))
		if !ok {
			continue
		}
		ds, ok := varintLen(dataSize, len(buf)-do)
		if !ok {
			continue
		}
		if dataBlock := leveldbBlock(buf, do, ds); dataBlock != nil {
			out = append(out, sstBlockEntries(dataBlock)...)
		}
	}
	return out
}

type leveldbFrameStats struct {
	goodFrames int
	badFrames  int
}

// leveldbLogRecords splits a write-ahead log into complete WriteBatch
// payloads, reassembling fragments across records and block tails. A frame
// whose checksum does not match is counted and still processed: the frame is
// untrusted but the rest of the log can be fine, and the count is what the
// caller reports.
func leveldbLogRecords(buf []byte, stats *leveldbFrameStats) [][]byte {
	var records [][]byte
	var pending []byte
	pos := 0
	for pos+7 <= len(buf) {
		if inBlock := pos % leveldbBlockSize; inBlock > leveldbBlockSize-7 {
			pos += leveldbBlockSize - inBlock
			continue
		}
		masked := binary.LittleEndian.Uint32(buf[pos:])
		length := int(binary.LittleEndian.Uint16(buf[pos+4:]))
		frameType := buf[pos+6]
		if frameType == 0 {
			pos += 7 + length
			continue
		}
		if pos+7+length > len(buf) {
			break
		}
		payload := buf[pos+7 : pos+7+length]
		if unmaskCrc(masked) == crc32c(buf[pos+6:pos+7+length]) {
			stats.goodFrames++
		} else if stats != nil {
			stats.badFrames++
		}
		pos += 7 + length
		switch frameType {
		case 1:
			records = append(records, append([]byte(nil), payload...))
		case 2:
			pending = append([]byte(nil), payload...)
		case 3:
			if pending != nil {
				pending = append(pending, payload...)
			}
		case 4:
			if pending != nil {
				records = append(records, append(pending, payload...))
				pending = nil
			}
		}
	}
	return records
}

// writeBatchEntries decodes a WriteBatch payload: sequence (8) + count (4) +
// per entry a type byte, a varint key length and the key, plus a varint value
// length and the value only for puts. Deletions carry no value; reading one
// would desync the whole batch.
func writeBatchEntries(record []byte) []leveldbEntry {
	var out []leveldbEntry
	if len(record) < 12 {
		return out
	}
	count := binary.LittleEndian.Uint32(record[8:12])
	pos := 12
	for i := uint32(0); i < count && pos < len(record); i++ {
		entryType := record[pos]
		pos++
		klen, p, err := readVarint(record, pos)
		if err != nil {
			break
		}
		kl, ok := varintLen(klen, len(record)-p)
		if !ok {
			break
		}
		key := record[p : p+kl]
		pos = p + kl
		if entryType == 0 {
			out = append(out, leveldbEntry{key: key, value: nil})
			continue
		}
		vlen, p2, err := readVarint(record, pos)
		if err != nil {
			break
		}
		vl, ok := varintLen(vlen, len(record)-p2)
		if !ok {
			break
		}
		out = append(out, leveldbEntry{key: key, value: record[p2 : p2+vl]})
		pos = p2 + vl
	}
	return out
}

// readLevelDbHistory reads a LevelDB directory's full write history: every
// version of every key, oldest first. SSTables load first, then logs; within
// one key versions sort by sequence. Only the relative order inside one key
// is dependable — SSTables rotate as compaction runs — and the value layer
// relies on nothing more than the version set being complete.
func readLevelDbHistory(dir string) map[string][]leveldbVersion {
	names, err := os.ReadDir(dir)
	if err != nil {
		return map[string][]leveldbVersion{}
	}
	var ldbNames, logNames []string
	for _, e := range names {
		if !e.Type().IsRegular() {
			continue
		}
		switch filepath.Ext(e.Name()) {
		case ".ldb":
			ldbNames = append(ldbNames, e.Name())
		case ".log":
			logNames = append(logNames, e.Name())
		}
	}
	sort.Strings(ldbNames)
	sort.Strings(logNames)

	versions := map[string][]leveldbVersion{}
	push := func(key, value []byte, seq uint64) {
		k := string(key)
		versions[k] = append(versions[k], leveldbVersion{key: append([]byte(nil), key...), value: value, seq: seq})
	}
	stats := &leveldbFrameStats{}
	for _, name := range ldbNames {
		buf, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		for _, e := range sstEntries(buf) {
			// Internal keys end with (sequence << 8) | type; the sequence is
			// what orders versions of one key.
			var seq uint64
			if len(e.key) > 8 {
				seq = binary.LittleEndian.Uint64(e.key[len(e.key)-8:]) >> 8
			}
			push(e.key[:len(e.key)-8], e.value, seq)
		}
	}
	for _, name := range logNames {
		buf, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		for _, rec := range leveldbLogRecords(buf, stats) {
			if len(rec) < 12 {
				continue
			}
			base := binary.LittleEndian.Uint64(rec[0:8]) >> 8
			for _, e := range writeBatchEntries(rec) {
				push(e.key, e.value, base)
			}
		}
	}
	for _, list := range versions {
		if len(list) > 1 {
			sort.SliceStable(list, func(i, j int) bool { return list[i].seq < list[j].seq })
		}
	}
	return versions
}
