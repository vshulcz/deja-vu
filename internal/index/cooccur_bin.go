package index

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"hash/fnv"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
)

const (
	cooccurBinFile       = "cooccur.bin"
	cooccurBinMagic      = "DJCOOC01"
	cooccurBinHeaderSize = 56
	cooccurStampWindow   = 4096
	maxCooccurBuckets    = 1 << 28
)

func cooccurBinPath(dir string) string {
	return filepath.Join(dir, cooccurBinFile)
}

func cooccurHash(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

func cooccurGobStamp(dir string) (uint64, [sha256.Size]byte, error) {
	var zero [sha256.Size]byte
	f, err := openIndexBlobFile(cooccurPath(dir))
	if err != nil {
		return 0, zero, err
	}
	defer func() { _ = f.Close() }()

	size, err := f.PlainSize()
	if err != nil {
		return 0, zero, err
	}
	if size < 0 {
		return 0, zero, os.ErrInvalid
	}

	h := sha256.New()
	var lenBuf [8]byte
	uSize := uint64(size)
	binary.LittleEndian.PutUint64(lenBuf[:], uSize)
	_, _ = h.Write(lenBuf[:])

	if size <= 2*cooccurStampWindow {
		buf := make([]byte, size)
		if _, err := io.ReadFull(f, buf); err != nil {
			return 0, zero, err
		}
		_, _ = h.Write(buf)
	} else {
		var win [cooccurStampWindow]byte
		if _, err := io.ReadFull(io.NewSectionReader(f, 0, cooccurStampWindow), win[:]); err != nil {
			return 0, zero, err
		}
		_, _ = h.Write(win[:])
		if _, err := io.ReadFull(io.NewSectionReader(f, size-cooccurStampWindow, cooccurStampWindow), win[:]); err != nil {
			return 0, zero, err
		}
		_, _ = h.Write(win[:])
	}

	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))
	return uSize, sum, nil
}

func cooccurBucketCount(nKeys int) uint32 {
	if nKeys <= 0 {
		return 0
	}
	n := uint32(16)
	for int(n) < nKeys && n < (1<<30) {
		n <<= 1
	}
	return n
}

func encodeCooccurBin(gobSize uint64, stamp [sha256.Size]byte, m map[string][]string) []byte {
	keys := make([]string, 0, len(m))
	for k, ns := range m {
		if k == "" || k == cooccurDiscourseKey || len(ns) == 0 {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	bucketCount := cooccurBucketCount(len(keys))
	if bucketCount == 0 {
		buf := make([]byte, cooccurBinHeaderSize)
		copy(buf[:8], cooccurBinMagic)
		binary.LittleEndian.PutUint64(buf[8:16], gobSize)
		copy(buf[16:48], stamp[:])
		return buf
	}

	buckets := make([][]string, bucketCount)
	mask := uint64(bucketCount - 1)
	for _, k := range keys {
		b := cooccurHash(k) & mask
		buckets[b] = append(buckets[b], k)
	}

	dirSize := (int(bucketCount) + 1) * 8
	dataStart := cooccurBinHeaderSize + dirSize
	out := make([]byte, dataStart, dataStart+len(keys)*48)
	copy(out[:8], cooccurBinMagic)
	binary.LittleEndian.PutUint64(out[8:16], gobSize)
	copy(out[16:48], stamp[:])
	binary.LittleEndian.PutUint32(out[48:52], bucketCount)

	for b, bKeys := range buckets {
		offPos := cooccurBinHeaderSize + b*8
		binary.LittleEndian.PutUint64(out[offPos:offPos+8], uint64(len(out)))
		for _, k := range bKeys {
			ns := m[k]
			out = binary.AppendUvarint(out, uint64(len(k)))
			out = append(out, k...)
			out = binary.AppendUvarint(out, uint64(len(ns)))
			for _, nb := range ns {
				out = binary.AppendUvarint(out, uint64(len(nb)))
				out = append(out, nb...)
			}
		}
	}
	endPos := cooccurBinHeaderSize + int(bucketCount)*8
	binary.LittleEndian.PutUint64(out[endPos:endPos+8], uint64(len(out)))
	return out
}

func writeCooccurBinBuild(dir string, m map[string][]string) error {
	gobSize, stamp, err := cooccurGobStamp(dir)
	if err != nil {
		return err
	}
	return writeSealedIndexFile(cooccurBinPath(dir), encodeCooccurBin(gobSize, stamp, m))
}

func writeCooccurBinAtomic(dir string, m map[string][]string) error {
	gobSize, stamp, err := cooccurGobStamp(dir)
	if err != nil {
		return err
	}
	return writeFileAtomic(cooccurBinPath(dir), encodeCooccurBin(gobSize, stamp, m))
}

func lookupCooccurNeighbors(dir, tok string) []string {
	if dir == "" || tok == "" || tok == cooccurDiscourseKey {
		return nil
	}
	m, ok := lookupCooccurMany(dir, []string{tok})
	if !ok {
		return nil
	}
	return m[tok]
}

func lookupCooccurMany(dir string, terms []string) (map[string][]string, bool) {
	if dir == "" {
		return nil, false
	}
	gobSize, stamp, err := cooccurGobStamp(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false
		}
		return lookupCooccurFallback(dir, terms)
	}
	if out, ok := lookupCooccurFromBin(dir, gobSize, stamp, terms); ok {
		return out, true
	}
	return lookupCooccurFallback(dir, terms)
}

func lookupCooccurFallback(dir string, terms []string) (map[string][]string, bool) {
	m := readCooccur(dir)
	if m == nil {
		return nil, false
	}
	out := make(map[string][]string, len(terms))
	for _, t := range terms {
		if t == "" || t == cooccurDiscourseKey {
			continue
		}
		if ns := m[t]; len(ns) > 0 {
			out[t] = ns
		}
	}
	return out, true
}

func lookupCooccurFromBin(dir string, gobSize uint64, stamp [sha256.Size]byte, terms []string) (map[string][]string, bool) {
	f, err := openIndexBlobFile(cooccurBinPath(dir))
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }()

	plainSize, err := f.PlainSize()
	if err != nil || plainSize < cooccurBinHeaderSize {
		return nil, false
	}
	var hdr [cooccurBinHeaderSize]byte
	if _, err := io.ReadFull(io.NewSectionReader(f, 0, cooccurBinHeaderSize), hdr[:]); err != nil {
		return nil, false
	}
	if string(hdr[:8]) != cooccurBinMagic {
		return nil, false
	}
	if binary.LittleEndian.Uint64(hdr[8:16]) != gobSize || !bytes.Equal(hdr[16:48], stamp[:]) {
		return nil, false
	}
	bucketCount := binary.LittleEndian.Uint32(hdr[48:52])
	if bucketCount == 0 {
		if plainSize != cooccurBinHeaderSize {
			return nil, false
		}
		return map[string][]string{}, true
	}
	if bucketCount&(bucketCount-1) != 0 || bucketCount > maxCooccurBuckets {
		return nil, false
	}
	dataStart := uint64(cooccurBinHeaderSize) + (uint64(bucketCount)+1)*8
	fileSize := uint64(plainSize)
	if fileSize < dataStart || fileSize > math.MaxInt64 {
		return nil, false
	}

	out := make(map[string][]string, len(terms))
	byBucket := map[uint64][]string{}
	mask := uint64(bucketCount - 1)
	for _, t := range terms {
		if t == "" || t == cooccurDiscourseKey {
			continue
		}
		if _, done := out[t]; done {
			continue
		}
		out[t] = nil
		b := cooccurHash(t) & mask
		byBucket[b] = append(byBucket[b], t)
	}

	var dirEntry [16]byte
	var buf []byte
	for b, wantList := range byBucket {
		if b >= maxCooccurBuckets {
			return nil, false
		}
		dirOff := int64(cooccurBinHeaderSize) + int64(b)*8
		if _, err := io.ReadFull(io.NewSectionReader(f, dirOff, 16), dirEntry[:]); err != nil {
			return nil, false
		}
		start := binary.LittleEndian.Uint64(dirEntry[:8])
		end := binary.LittleEndian.Uint64(dirEntry[8:16])
		if start < dataStart || end < start || end > fileSize || start > math.MaxInt64 {
			return nil, false
		}
		n := int(end - start) //nolint:gosec // bounded by fileSize
		if n == 0 {
			continue
		}
		if cap(buf) < n {
			buf = make([]byte, n)
		}
		bucketBytes := buf[:n]
		if _, err := io.ReadFull(io.NewSectionReader(f, int64(start), int64(n)), bucketBytes); err != nil {
			return nil, false
		}
		if !decodeCooccurBucket(bucketBytes, wantList, out) {
			return nil, false
		}
	}
	for k, v := range out {
		if len(v) == 0 {
			delete(out, k)
		}
	}
	return out, true
}

func decodeCooccurBucket(b []byte, wantList []string, out map[string][]string) bool {
	want := make(map[string]bool, len(wantList))
	for _, t := range wantList {
		want[t] = true
	}
	for len(b) > 0 {
		kLen, n := binary.Uvarint(b)
		if n <= 0 {
			return false
		}
		b = b[n:]
		if kLen == 0 || kLen > uint64(len(b)) {
			return false
		}
		key := b[:int(kLen)] //nolint:gosec // bounded by len(b) above
		b = b[int(kLen):]    //nolint:gosec // bounded by len(b) above

		vCount, n := binary.Uvarint(b)
		if n <= 0 {
			return false
		}
		b = b[n:]
		if vCount == 0 || vCount > uint64(len(b)) {
			return false
		}

		match := want[string(key)]
		var vals []string
		if match {
			vals = make([]string, 0, int(vCount)) //nolint:gosec // bounded by remaining bytes above
		}
		for range vCount {
			vLen, vn := binary.Uvarint(b)
			if vn <= 0 {
				return false
			}
			b = b[vn:]
			if vLen > uint64(len(b)) {
				return false
			}
			val := b[:int(vLen)] //nolint:gosec // bounded by len(b) above
			b = b[int(vLen):]    //nolint:gosec // bounded by len(b) above
			if match {
				vals = append(vals, string(val))
			}
		}
		if match {
			out[string(key)] = vals
		}
	}
	return true
}
