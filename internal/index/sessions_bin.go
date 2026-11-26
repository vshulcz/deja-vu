package index

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/vshulcz/deja-vu/internal/vault"
)

const (
	sessionsBinMagic        = "DJSESS01"
	sessionsBinVersion      = uint32(1)
	sessionsBinHeaderSize   = 32
	sessionsBinDirEntrySize = 24
	sessionsBinHashSize     = sha256.Size
	sessionsBinMinSize      = sessionsBinHeaderSize + sessionsBinHashSize
	sessionsBinMaxEntry     = 32 << 20
	sessionsBinMaxSliceLen  = 1 << 20
)

type dirEntry struct {
	keyOff uint64
	valOff uint64
	keyLen uint32
	valLen uint32
}

func statSessionsFile(dir string) (os.FileInfo, error) {
	fi, err := os.Stat(filepath.Join(dir, "sessions.bin"))
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		return fi, err
	}
	return os.Stat(filepath.Join(dir, "sessions.gob"))
}

func readSessionsCatalog(dir string) (map[string]SessionMeta, error) {
	sessions, err := readSessionsBin(dir)
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		return sessions, err
	}
	var legacy map[string]SessionMeta
	if gobErr := readGob(filepath.Join(dir, "sessions.gob"), &legacy); gobErr != nil {
		return nil, gobErr
	}
	if legacy == nil {
		legacy = map[string]SessionMeta{}
	}
	return legacy, nil
}

func writeSessionsBinAtomicStat(p string, sessions map[string]SessionMeta) (os.FileInfo, error) {
	data, err := encodeSessionsBin(sessions)
	if err != nil {
		return nil, err
	}
	fi, err := writeSealedAtomicStat(p, data, vault.DomainIndexFile)
	if err != nil {
		return nil, err
	}
	if fi != nil {
		_ = os.SameFile(fi, fi)
	}
	return fi, nil
}

func encodeSessionsBin(sessions map[string]SessionMeta) ([]byte, error) {
	keys := slices.Sorted(maps.Keys(sessions))
	cntU32, ok := intToU32(len(keys))
	if !ok {
		return nil, fmt.Errorf("sessions catalog count overflows uint32: %d", len(keys))
	}

	buf := make([]byte, sessionsBinHeaderSize, sessionsBinHeaderSize+len(keys)*96+sessionsBinHashSize)
	entries := make([]dirEntry, 0, len(keys))

	for _, k := range keys {
		keyOff, kOk := intToU64(len(buf))
		keyLen, klOk := intToU32(len(k))
		if !kOk || !klOk {
			return nil, fmt.Errorf("sessions catalog key exceeds offset bounds: %d", len(k))
		}
		buf = append(buf, k...)

		valStart := len(buf)
		valOff, vOk := intToU64(valStart)
		if !vOk {
			return nil, errors.New("sessions catalog payload exceeds offset bounds")
		}
		var err error
		buf, err = appendSessionMeta(buf, sessions[k])
		if err != nil {
			return nil, err
		}
		valLen, vlOk := intToU32(len(buf) - valStart)
		if !vlOk {
			return nil, errors.New("sessions catalog entry exceeds uint32 length")
		}
		entries = append(entries, dirEntry{keyOff: keyOff, valOff: valOff, keyLen: keyLen, valLen: valLen})
	}

	dirOffset, ok := intToU64(len(buf))
	if !ok {
		return nil, errors.New("sessions catalog directory offset overflows uint64")
	}

	copy(buf[0:8], sessionsBinMagic)
	binary.LittleEndian.PutUint32(buf[8:12], sessionsBinVersion)
	binary.LittleEndian.PutUint32(buf[12:16], cntU32)
	binary.LittleEndian.PutUint64(buf[16:24], dirOffset)
	binary.LittleEndian.PutUint64(buf[24:32], 0)

	var entBuf [sessionsBinDirEntrySize]byte
	for _, e := range entries {
		binary.LittleEndian.PutUint64(entBuf[0:8], e.keyOff)
		binary.LittleEndian.PutUint64(entBuf[8:16], e.valOff)
		binary.LittleEndian.PutUint32(entBuf[16:20], e.keyLen)
		binary.LittleEndian.PutUint32(entBuf[20:24], e.valLen)
		buf = append(buf, entBuf[:]...)
	}

	sum := sha256.Sum256(buf)
	buf = append(buf, sum[:]...)
	return buf, nil
}

func readSessionsBinCount(dir string) (int, error) {
	f, err := openIndexBlobFile(filepath.Join(dir, "sessions.bin"))
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()

	size, err := f.PlainSize()
	if err != nil {
		return 0, err
	}
	if size < sessionsBinMinSize {
		return 0, fmt.Errorf("%w: sessions.bin truncated (%d bytes)", errCorruptIndex, size)
	}
	var hdr [sessionsBinHeaderSize]byte
	if _, err = io.ReadFull(f, hdr[:]); err != nil {
		return 0, fmt.Errorf("%w: sessions.bin header: %w", errCorruptIndex, err)
	}
	cnt, _, err := parseSessionsBinHeader(hdr[:], size)
	if err != nil {
		return 0, err
	}
	n, ok := u32ToInt(cnt)
	if !ok {
		return 0, fmt.Errorf("%w: sessions.bin count overflows int", errCorruptIndex)
	}
	return n, nil
}

func readSessionsBin(dir string) (map[string]SessionMeta, error) {
	data, err := readIndexBytes(filepath.Join(dir, "sessions.bin"))
	if err != nil {
		return nil, err
	}
	if len(data) < sessionsBinMinSize {
		return nil, fmt.Errorf("%w: sessions.bin truncated (%d bytes)", errCorruptIndex, len(data))
	}
	bodyLen := len(data) - sessionsBinHashSize
	sum := sha256.Sum256(data[:bodyLen])
	if !bytes.Equal(sum[:], data[bodyLen:]) {
		return nil, fmt.Errorf("%w: sessions.bin checksum mismatch", errCorruptIndex)
	}
	cnt, dirOffset, err := parseSessionsBinHeader(data[:sessionsBinHeaderSize], int64(len(data)))
	if err != nil {
		return nil, err
	}
	n, ok := u32ToInt(cnt)
	if !ok {
		return nil, fmt.Errorf("%w: sessions.bin count overflows int", errCorruptIndex)
	}

	dirOffInt, ok := i64ToInt(dirOffset)
	if !ok {
		return nil, fmt.Errorf("%w: sessions.bin dir offset overflows int", errCorruptIndex)
	}

	out := make(map[string]SessionMeta, n)
	var prevKey string
	for i := range n {
		off := dirOffInt + i*sessionsBinDirEntrySize
		kOff, kEnd, vOff, vEnd, bErr := parseDirEntryBounds(data[off:off+sessionsBinDirEntrySize], dirOffset)
		if bErr != nil {
			return nil, bErr
		}
		key := string(data[kOff:kEnd])
		if i > 0 && key <= prevKey {
			return nil, fmt.Errorf("%w: sessions.bin directory unsorted", errCorruptIndex)
		}
		prevKey = key

		meta, dErr := decodeSessionMetaExact(data[vOff:vEnd])
		if dErr != nil {
			return nil, fmt.Errorf("%w: sessions.bin row %q: %w", errCorruptIndex, key, dErr)
		}
		out[key] = meta
	}
	return out, nil
}

func readSessionsBinKey(dir, target string) (SessionMeta, bool, error) {
	f, err := openIndexBlobFile(filepath.Join(dir, "sessions.bin"))
	if err != nil {
		return SessionMeta{}, false, err
	}
	defer func() { _ = f.Close() }()

	size, err := f.PlainSize()
	if err != nil {
		return SessionMeta{}, false, err
	}
	if size < sessionsBinMinSize {
		return SessionMeta{}, false, fmt.Errorf("%w: sessions.bin truncated (%d bytes)", errCorruptIndex, size)
	}
	var hdr [sessionsBinHeaderSize]byte
	if _, err = f.ReadAt(hdr[:], 0); err != nil {
		return SessionMeta{}, false, fmt.Errorf("%w: sessions.bin header: %w", errCorruptIndex, err)
	}
	cnt, dirOffset, err := parseSessionsBinHeader(hdr[:], size)
	if err != nil {
		return SessionMeta{}, false, err
	}
	n, ok := u32ToInt(cnt)
	if !ok {
		return SessionMeta{}, false, fmt.Errorf("%w: sessions.bin count overflows int", errCorruptIndex)
	}

	lo, hi := 0, n
	var ent [sessionsBinDirEntrySize]byte
	for lo < hi {
		mid := lo + (hi-lo)/2
		if _, err = f.ReadAt(ent[:], dirOffset+int64(mid)*sessionsBinDirEntrySize); err != nil {
			return SessionMeta{}, false, fmt.Errorf("%w: sessions.bin dir read: %w", errCorruptIndex, err)
		}
		kOff, kEnd, vOff, vEnd, bErr := parseDirEntryBounds(ent[:], dirOffset)
		if bErr != nil {
			return SessionMeta{}, false, bErr
		}
		keyBuf := make([]byte, kEnd-kOff)
		if _, err = f.ReadAt(keyBuf, int64(kOff)); err != nil {
			return SessionMeta{}, false, fmt.Errorf("%w: sessions.bin key read: %w", errCorruptIndex, err)
		}
		key := string(keyBuf)
		if key == target {
			valBuf := make([]byte, vEnd-vOff)
			if _, err = f.ReadAt(valBuf, int64(vOff)); err != nil {
				return SessionMeta{}, false, fmt.Errorf("%w: sessions.bin value read: %w", errCorruptIndex, err)
			}
			meta, dErr := decodeSessionMetaExact(valBuf)
			if dErr != nil {
				return SessionMeta{}, false, fmt.Errorf("%w: sessions.bin row %q: %w", errCorruptIndex, key, dErr)
			}
			return meta, true, nil
		}
		if key < target {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return SessionMeta{}, false, nil
}

func parseSessionsBinHeader(hdr []byte, fileSize int64) (uint32, int64, error) {
	if fileSize < sessionsBinMinSize || len(hdr) < sessionsBinHeaderSize {
		return 0, 0, fmt.Errorf("%w: sessions.bin header too small", errCorruptIndex)
	}
	if string(hdr[0:8]) != sessionsBinMagic {
		return 0, 0, fmt.Errorf("%w: sessions.bin bad magic", errCorruptIndex)
	}
	if ver := binary.LittleEndian.Uint32(hdr[8:12]); ver != sessionsBinVersion {
		return 0, 0, fmt.Errorf("%w: sessions.bin version %d", errCorruptIndex, ver)
	}
	if flags := binary.LittleEndian.Uint64(hdr[24:32]); flags != 0 {
		return 0, 0, fmt.Errorf("%w: sessions.bin unsupported flags %d", errCorruptIndex, flags)
	}
	cnt := binary.LittleEndian.Uint32(hdr[12:16])
	rawDirOff := binary.LittleEndian.Uint64(hdr[16:24])
	dirOffset, ok := u64ToInt64(rawDirOff)
	if !ok || dirOffset < sessionsBinHeaderSize || dirOffset > fileSize-sessionsBinHashSize {
		return 0, 0, fmt.Errorf("%w: sessions.bin dir offset %d out of bounds", errCorruptIndex, rawDirOff)
	}
	dirSize := fileSize - sessionsBinHashSize - dirOffset
	wantDirSize := int64(cnt) * sessionsBinDirEntrySize
	if dirSize != wantDirSize {
		return 0, 0, fmt.Errorf("%w: sessions.bin dir size %d != %d", errCorruptIndex, dirSize, wantDirSize)
	}
	return cnt, dirOffset, nil
}

func parseDirEntryBounds(ent []byte, dirOffset int64) (kOff, kEnd, vOff, vEnd int, err error) {
	if len(ent) < sessionsBinDirEntrySize || dirOffset < sessionsBinHeaderSize {
		return 0, 0, 0, 0, fmt.Errorf("%w: sessions.bin invalid dir entry", errCorruptIndex)
	}
	kOff64, ok1 := u64ToInt64(binary.LittleEndian.Uint64(ent[0:8]))
	vOff64, ok2 := u64ToInt64(binary.LittleEndian.Uint64(ent[8:16]))
	kLen64 := int64(binary.LittleEndian.Uint32(ent[16:20]))
	vLen64 := int64(binary.LittleEndian.Uint32(ent[20:24]))
	if !ok1 || !ok2 || kLen64 > sessionsBinMaxEntry || vLen64 > sessionsBinMaxEntry {
		return 0, 0, 0, 0, fmt.Errorf("%w: sessions.bin entry exceeds bounds", errCorruptIndex)
	}
	if kOff64 < sessionsBinHeaderSize || kOff64 > dirOffset || kLen64 > dirOffset-kOff64 {
		return 0, 0, 0, 0, fmt.Errorf("%w: sessions.bin key bounds invalid", errCorruptIndex)
	}
	if vOff64 < sessionsBinHeaderSize || vOff64 > dirOffset || vLen64 > dirOffset-vOff64 {
		return 0, 0, 0, 0, fmt.Errorf("%w: sessions.bin value bounds invalid", errCorruptIndex)
	}
	var ok bool
	if kOff, ok = i64ToInt(kOff64); !ok {
		return 0, 0, 0, 0, fmt.Errorf("%w: sessions.bin key offset overflow", errCorruptIndex)
	}
	if kEnd, ok = i64ToInt(kOff64 + kLen64); !ok {
		return 0, 0, 0, 0, fmt.Errorf("%w: sessions.bin key end overflow", errCorruptIndex)
	}
	if vOff, ok = i64ToInt(vOff64); !ok {
		return 0, 0, 0, 0, fmt.Errorf("%w: sessions.bin value offset overflow", errCorruptIndex)
	}
	if vEnd, ok = i64ToInt(vOff64 + vLen64); !ok {
		return 0, 0, 0, 0, fmt.Errorf("%w: sessions.bin value end overflow", errCorruptIndex)
	}
	return kOff, kEnd, vOff, vEnd, nil
}
