package index

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/vshulcz/deja-vu/internal/vault"
)

func init() {
	vault.RegisterMigrator("index", migrateIndexStore)
}

func canonicalVaultDir(dir string) string {
	if dir == "" {
		return ""
	}
	d := filepath.Clean(dir)
	for {
		switch {
		case len(d) > len(".compactions.gob") && strings.HasSuffix(d, ".compactions.gob"):
			d = strings.TrimSuffix(d, ".compactions.gob")
		case len(d) > len(".vaultmig") && strings.HasSuffix(d, ".vaultmig"):
			d = strings.TrimSuffix(d, ".vaultmig")
		case len(d) > len(".part") && strings.HasSuffix(d, ".part"):
			d = strings.TrimSuffix(d, ".part")
		case len(d) > len(".tmp") && strings.HasSuffix(d, ".tmp"):
			d = strings.TrimSuffix(d, ".tmp")
		case len(d) > len(".old") && strings.HasSuffix(d, ".old"):
			d = strings.TrimSuffix(d, ".old")
		default:
			if base, ok := trimParkedOldSuffix(d); ok {
				d = base
				continue
			}
			return d
		}
	}
}

func trimParkedOldSuffix(d string) (string, bool) {
	idx := strings.LastIndex(d, ".old.")
	if idx <= 0 {
		return "", false
	}
	tail := d[idx+len(".old."):]
	if tail == "" {
		return "", false
	}
	for i := 0; i < len(tail); i++ {
		if tail[i] < '0' || tail[i] > '9' {
			return "", false
		}
	}
	return d[:idx], true
}

func vaultDirForFile(path string) string {
	if path == "" {
		return ""
	}
	clean := filepath.Clean(path)
	if strings.HasSuffix(clean, ".compactions.gob") || strings.HasSuffix(clean, ".compactions.gob.tmp") {
		return canonicalVaultDir(clean)
	}
	d := filepath.Dir(clean)
	for d != "." && d != "/" && d != filepath.Dir(d) {
		base := filepath.Base(d)
		if base == "buckets" || base == "spill" || base == "deferred" {
			d = filepath.Dir(d)
			continue
		}
		break
	}
	return canonicalVaultDir(d)
}

func indexDEKForDir(dir string) ([]byte, error) {
	canon := canonicalVaultDir(dir)
	dek, err := vault.CheckUnlocked(canon)
	if err != nil {
		invalidateSnapshot(canon)
		return nil, err
	}
	return dek, nil
}

func indexDEKForFile(path string) ([]byte, error) {
	return indexDEKForDir(vaultDirForFile(path))
}

func writeRawFileAtomicStat(p string, data []byte) (os.FileInfo, error) {
	tmp := p + ".tmp"
	f, err := openIndexFileWrite(tmp, os.O_RDWR|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return nil, err
	}
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return nil, err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return nil, err
	}
	if err = f.Close(); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	if err = renameWaiting(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	return fi, nil
}

func writeRawFileAtomic(p string, data []byte) error {
	_, err := writeRawFileAtomicStat(p, data)
	return err
}

func writeSealedAtomicStat(p string, data []byte, domain string) (os.FileInfo, error) {
	dek, err := indexDEKForFile(p)
	if err != nil {
		return nil, err
	}
	if len(dek) > 0 {
		sealed, err := vault.SealBytes(dek, domain, data)
		if err != nil {
			return nil, err
		}
		data = sealed
	}
	return writeRawFileAtomicStat(p, data)
}

func writeSealedFileAtomic(p string, data []byte, domain string) error {
	_, err := writeSealedAtomicStat(p, data, domain)
	return err
}

func writeSealedIndexFile(p string, data []byte, domain string) error {
	dek, err := indexDEKForFile(p)
	if err != nil {
		return err
	}
	if len(dek) > 0 {
		sealed, err := vault.SealBytes(dek, domain, data)
		if err != nil {
			return err
		}
		data = sealed
	}
	return writeIndexFile(p, data)
}

func readIndexBytes(p, domain string) ([]byte, error) {
	dek, err := indexDEKForFile(p)
	if err != nil {
		return nil, err
	}
	f, err := openIndexFile(p)
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil {
		return nil, err
	}
	if vault.IsSealedBytes(raw) {
		if len(dek) == 0 {
			return nil, vault.ErrKeyUnavailable
		}
		return vault.OpenBytes(dek, domain, raw)
	}
	if len(dek) > 0 {
		return nil, fmt.Errorf("vault: unsealed index file %s", filepath.Base(p))
	}
	return raw, nil
}

// indexBlobFile wraps either a raw *os.File (zero-copy when the vault is
// disabled) or a decrypted in-memory buffer paired with the underlying *os.File
// so os.SameFile inode identity checks continue to match.
type indexBlobFile struct {
	f      *os.File
	r      *bytes.Reader
	plain  []byte
	sealed bool
}

func openIndexBlobFile(p, domain string) (*indexBlobFile, error) {
	dek, err := indexDEKForFile(p)
	if err != nil {
		return nil, err
	}
	f, err := openIndexFile(p)
	if err != nil {
		return nil, err
	}
	if len(dek) == 0 {
		var head [5]byte
		n, _ := f.ReadAt(head[:], 0)
		if n == len(head) && vault.IsSealedBytes(head[:]) {
			_ = f.Close()
			return nil, vault.ErrKeyUnavailable
		}
		return &indexBlobFile{f: f}, nil
	}
	raw, err := io.ReadAll(f)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !vault.IsSealedBytes(raw) {
		_ = f.Close()
		return nil, fmt.Errorf("vault: unsealed index file %s", filepath.Base(p))
	}
	plain, err := vault.OpenBytes(dek, domain, raw)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &indexBlobFile{
		f:      f,
		r:      bytes.NewReader(plain),
		plain:  plain,
		sealed: true,
	}, nil
}

func (b *indexBlobFile) Read(p []byte) (int, error) {
	if b.sealed {
		return b.r.Read(p)
	}
	return b.f.Read(p)
}

func (b *indexBlobFile) ReadAt(p []byte, off int64) (int, error) {
	if b.sealed {
		return b.r.ReadAt(p, off)
	}
	return b.f.ReadAt(p, off)
}

func (b *indexBlobFile) Seek(offset int64, whence int) (int64, error) {
	if b.sealed {
		return b.r.Seek(offset, whence)
	}
	return b.f.Seek(offset, whence)
}

func (b *indexBlobFile) Stat() (os.FileInfo, error) {
	return b.f.Stat()
}

func (b *indexBlobFile) PlainSize() (int64, error) {
	if b.sealed {
		return int64(len(b.plain)), nil
	}
	fi, err := b.f.Stat()
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

func (b *indexBlobFile) Close() error {
	clear(b.plain)
	b.plain = nil
	b.r = nil
	if b.f != nil {
		return b.f.Close()
	}
	return nil
}

func isVaultInfraFile(name string) bool {
	if strings.HasSuffix(name, ".lock") || strings.HasSuffix(name, ".tmp") {
		return true
	}
	switch name {
	case "vault.json", ".usage.jsonl", "vault.usage.jsonl", "tombstones", appendPendingFile, scrubPendingFile:
		return true
	}
	return false
}

func migrateIndexStore(dir string, dek []byte) error {
	canon := canonicalVaultDir(dir)
	if canon == "" {
		return nil
	}
	sc := compactionSidecarPath(canon)
	if raw, err := os.ReadFile(sc); err == nil && !vault.IsSealedBytes(raw) {
		sealed, sErr := vault.SealBytes(dek, vault.DomainIndexFile, raw)
		if sErr != nil {
			return sErr
		}
		if wErr := writeRawFileAtomic(sc, sealed); wErr != nil {
			return wErr
		}
	}

	fi, err := os.Stat(canon)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("vault: index path %s is not a directory", canon)
	}

	unlock, err := lockDir(canon)
	if err != nil {
		return err
	}
	defer unlock()

	manifestPath := filepath.Join(canon, "manifest.gob")
	mRaw, mErr := os.ReadFile(manifestPath)
	if mErr == nil && vault.IsSealedBytes(mRaw) {
		return sweepUnsealedDirFiles(canon, dek)
	}
	if errors.Is(mErr, fs.ErrNotExist) {
		if _, rErr := os.Stat(recordsPath(canon)); rErr == nil {
			return errors.New("vault: cannot migrate records.bin without manifest.gob")
		}
		return sweepUnsealedDirFiles(canon, dek)
	}
	if mErr != nil {
		return mErr
	}

	var core manifestCore
	if err := gob.NewDecoder(bytes.NewReader(mRaw)).Decode(&core); err != nil {
		return fmt.Errorf("vault: decode manifest for migration: %w", err)
	}

	stageDir := canon + ".vaultmig"
	_ = os.RemoveAll(stageDir)
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(stageDir)
		}
	}()
	if err := mkdirIndexAll(stageDir); err != nil {
		return err
	}

	rc, err := vault.NewRecordCipher(dek)
	if err != nil {
		return err
	}

	offsetMap, oldLogSize, newLogSize, deadDelta, err := migrateRecordsLog(
		recordsPath(canon),
		recordsPath(stageDir),
		rc,
		core.DeadRecords,
	)
	if err != nil {
		return err
	}

	if len(core.DeadRecords) > 0 {
		remappedDead := make([]int64, 0, len(core.DeadRecords))
		for _, oldOff := range core.DeadRecords {
			if newOff, exists := offsetMap[oldOff]; exists {
				remappedDead = append(remappedDead, newOff)
			}
		}
		core.DeadRecords = remappedDead
	}
	if core.DeadBytes+deadDelta >= 0 {
		core.DeadBytes += deadDelta
	}
	if core.RecordsSize > 0 {
		if core.RecordsSize < oldLogSize {
			if newOff, exists := offsetMap[core.RecordsSize]; exists {
				core.RecordsSize = newOff
			} else {
				core.RecordsSize = newLogSize
			}
		} else {
			core.RecordsSize = newLogSize
		}
	}

	if err := migrateBucketsDir(filepath.Join(canon, "buckets"), filepath.Join(stageDir, "buckets"), dek, offsetMap); err != nil {
		return err
	}

	entries, err := os.ReadDir(canon)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if name == "manifest.gob" || name == "records.bin" || name == "buckets" ||
			name == recordOffsetsFile || name == frictionTextsFile || strings.HasSuffix(name, ".tmp") {
			continue
		}
		src := filepath.Join(canon, name)
		dst := filepath.Join(stageDir, name)
		if e.IsDir() {
			switch name {
			case "deferred":
				if err := migrateSealedTree(src, dst, dek, vault.DomainSidecar); err != nil {
					return err
				}
			case "spill":
				if err := migrateSpillTree(src, dst, dek); err != nil {
					return err
				}
			default:
				if err := migrateSealedTree(src, dst, dek, vault.DomainIndexFile); err != nil {
					return err
				}
			}
			continue
		}
		raw, rErr := os.ReadFile(src)
		if rErr != nil {
			return rErr
		}
		out := raw
		if !isVaultInfraFile(name) && !vault.IsSealedBytes(raw) {
			sealed, sErr := vault.SealBytes(dek, vault.DomainIndexFile, raw)
			if sErr != nil {
				return sErr
			}
			out = sealed
		}
		if err := writeRawFileAtomic(dst, out); err != nil {
			return err
		}
	}

	var coreBuf bytes.Buffer
	if err := gob.NewEncoder(&coreBuf).Encode(core); err != nil {
		return err
	}
	sealedManifest, err := vault.SealBytes(dek, vault.DomainIndexFile, coreBuf.Bytes())
	if err != nil {
		return err
	}
	if err := writeRawFileAtomic(filepath.Join(stageDir, "manifest.gob"), sealedManifest); err != nil {
		return err
	}

	ensureSharedIndexPerms(stageDir)
	if err := swapIndexDir(canon, stageDir); err != nil {
		return err
	}
	ok = true
	ensureSharedIndexPerms(canon)
	invalidateSnapshot(canon)

	if m, rErr := readManifest(canon); rErr == nil {
		_ = writeRecordOffsets(canon, tablesFromManifest(m), m.RecordsSize)
		_ = writeFrictionTexts(canon, m, m.RecordsSize)
	}
	return nil
}

func sweepUnsealedDirFiles(canon string, dek []byte) error {
	for _, derived := range []string{recordOffsetsFile, frictionTextsFile} {
		p := filepath.Join(canon, derived)
		if raw, err := os.ReadFile(p); err == nil && !vault.IsSealedBytes(raw) {
			_ = os.Remove(p)
		}
	}
	entries, err := os.ReadDir(canon)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if isVaultInfraFile(name) || name == "manifest.gob" || name == "records.bin" ||
			name == recordOffsetsFile || name == frictionTextsFile {
			continue
		}
		p := filepath.Join(canon, name)
		if e.IsDir() {
			switch name {
			case "buckets":
				if err := sealUnsealedFilesInDir(p, dek, vault.DomainIndexFile); err != nil {
					return err
				}
			case "deferred":
				if err := sealUnsealedFilesInDir(p, dek, vault.DomainSidecar); err != nil {
					return err
				}
			case "spill":
				if err := sealUnsealedSpillsInDir(p, dek); err != nil {
					return err
				}
			default:
				if err := sealUnsealedFilesInDir(p, dek, vault.DomainIndexFile); err != nil {
					return err
				}
			}
			continue
		}
		if err := sealFileInPlace(p, dek, vault.DomainIndexFile); err != nil {
			return err
		}
	}
	return nil
}

func sealFileInPlace(p string, dek []byte, domain string) error {
	raw, err := os.ReadFile(p)
	if err != nil || vault.IsSealedBytes(raw) {
		return err
	}
	sealed, err := vault.SealBytes(dek, domain, raw)
	if err != nil {
		return err
	}
	return writeRawFileAtomic(p, sealed)
}

func sealUnsealedFilesInDir(dir string, dek []byte, domain string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasSuffix(name, ".tmp") {
			continue
		}
		if err := sealFileInPlace(filepath.Join(dir, name), dek, domain); err != nil {
			return err
		}
	}
	return nil
}

func sealUnsealedSpillsInDir(dir string, dek []byte) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if err := sealSpillFileAtomic(p, p, dek); err != nil {
			return err
		}
	}
	return nil
}

func migrateSealedTree(srcDir, dstDir string, dek []byte, domain string) error {
	if err := mkdirIndexAll(dstDir); err != nil {
		return err
	}
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".tmp") {
			continue
		}
		src := filepath.Join(srcDir, name)
		dst := filepath.Join(dstDir, name)
		if e.IsDir() {
			if err := migrateSealedTree(src, dst, dek, domain); err != nil {
				return err
			}
			continue
		}
		raw, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if !vault.IsSealedBytes(raw) {
			sealed, err := vault.SealBytes(dek, domain, raw)
			if err != nil {
				return err
			}
			raw = sealed
		}
		if err := writeRawFileAtomic(dst, raw); err != nil {
			return err
		}
	}
	return nil
}

func migrateSpillTree(srcDir, dstDir string, dek []byte) error {
	if err := mkdirIndexAll(dstDir); err != nil {
		return err
	}
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasSuffix(name, ".tmp") {
			continue
		}
		if err := sealSpillFileAtomic(filepath.Join(srcDir, name), filepath.Join(dstDir, name), dek); err != nil {
			return err
		}
	}
	return nil
}

func sealSpillFileAtomic(src, dst string, dek []byte) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if len(raw) >= 4 && vault.IsStreamHeader(raw[:4]) {
		return writeRawFileAtomic(dst, raw)
	}
	tmp := dst + ".tmp"
	f, err := openIndexFileWrite(tmp, os.O_RDWR|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	sw, err := vault.NewStreamWriter(f, dek, vault.DomainSpill)
	if err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if _, err = sw.Write(raw); err != nil {
		_ = sw.Close()
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err = sw.Close(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err = f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err = renameWaiting(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func migrateRecordsLog(srcPath, dstPath string, rc *vault.RecordCipher, dead []int64) (map[int64]int64, int64, int64, int64, error) {
	offsetMap := map[int64]int64{}
	src, err := os.Open(srcPath)
	if errors.Is(err, fs.ErrNotExist) {
		return offsetMap, 0, 0, 0, nil
	}
	if err != nil {
		return nil, 0, 0, 0, err
	}
	defer func() { _ = src.Close() }()

	dst, err := openIndexFileWrite(dstPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = dst.Close()
			_ = os.Remove(dstPath)
		}
	}()

	deadSet := make(map[int64]bool, len(dead))
	for _, off := range dead {
		deadSet[off] = true
	}

	var oldOff, newOff, deadDelta int64
	var hdr [4]byte
	for {
		if _, err := io.ReadFull(src, hdr[:]); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, 0, 0, 0, err
		}
		size := binary.LittleEndian.Uint32(hdr[:])
		if size > maxRecordSize {
			return nil, 0, 0, 0, fmt.Errorf("%w: record length %d exceeds cap", errCorruptIndex, size)
		}
		payload := make([]byte, size)
		if _, err := io.ReadFull(src, payload); err != nil {
			return nil, 0, 0, 0, err
		}
		prefixLen, ok := recordHeaderPrefixLen(payload)
		if !ok || prefixLen >= len(payload) {
			return nil, 0, 0, 0, errShortRecord
		}
		flag := payload[prefixLen]
		body := payload[prefixLen+1:]

		var outPayload []byte
		switch {
		case flag&recordSealed != 0:
			outPayload = payload
		case flag == recordRaw && !slices.ContainsFunc(body, func(c byte) bool { return c != 0 }):
			// Already-scrubbed dead slots carry no data; keep zeroed bytes as-is.
			outPayload = payload
		case flag == recordRaw || flag == recordDeflated:
			sealedBody, sErr := rc.Seal(flag, body)
			if sErr != nil {
				return nil, 0, 0, 0, sErr
			}
			outPayload = make([]byte, prefixLen+1+len(sealedBody))
			copy(outPayload, payload[:prefixLen])
			outPayload[prefixLen] = flag | recordSealed
			copy(outPayload[prefixLen+1:], sealedBody)
		default:
			return nil, 0, 0, 0, fmt.Errorf("vault: unknown record flag %d at offset %d", flag, oldOff)
		}

		if len(outPayload) > 1<<31 {
			return nil, 0, 0, 0, errors.New("record too large")
		}
		var outHdr [4]byte
		binary.LittleEndian.PutUint32(outHdr[:], uint32(len(outPayload))) //nolint:gosec // checked < 1<<31 above
		if _, err := dst.Write(outHdr[:]); err != nil {
			return nil, 0, 0, 0, err
		}
		if _, err := dst.Write(outPayload); err != nil {
			return nil, 0, 0, 0, err
		}

		offsetMap[oldOff] = newOff
		if deadSet[oldOff] {
			deadDelta += int64(4+len(outPayload)) - int64(4+int(size))
		}
		oldOff += int64(4) + int64(size)
		newOff += int64(4) + int64(len(outPayload))
	}

	if err := dst.Sync(); err != nil {
		return nil, 0, 0, 0, err
	}
	if err := dst.Close(); err != nil {
		return nil, 0, 0, 0, err
	}
	cleanup = false
	return offsetMap, oldOff, newOff, deadDelta, nil
}

func recordHeaderPrefixLen(payload []byte) (int, bool) {
	pos := 0
	for range 3 {
		_, n := binary.Uvarint(payload[pos:])
		if n <= 0 {
			return 0, false
		}
		pos += n
	}
	return pos, true
}

func migrateBucketsDir(srcDir, dstDir string, dek []byte, offsetMap map[int64]int64) error {
	entries, err := os.ReadDir(srcDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := mkdirIndexAll(dstDir); err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasSuffix(name, ".tmp") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(srcDir, name))
		if err != nil {
			return err
		}
		dst := filepath.Join(dstDir, name)
		if !isPostingsFile(name) {
			if !vault.IsSealedBytes(raw) {
				sealed, err := vault.SealBytes(dek, vault.DomainIndexFile, raw)
				if err != nil {
					return err
				}
				raw = sealed
			}
			if err := writeRawFileAtomic(dst, raw); err != nil {
				return err
			}
			continue
		}
		plain := raw
		if vault.IsSealedBytes(raw) {
			var oErr error
			plain, oErr = vault.OpenBytes(dek, vault.DomainIndexFile, raw)
			if oErr != nil {
				return oErr
			}
		}
		data, err := decodeBucketBytes(plain)
		if err != nil {
			return err
		}
		for tok, posts := range data {
			for i := range posts {
				if newOff, exists := offsetMap[posts[i].Off]; exists {
					posts[i].Off = newOff
				}
			}
			data[tok] = posts
		}
		reencoded := encodeBucketBytes(data)
		sealed, err := vault.SealBytes(dek, vault.DomainIndexFile, reencoded)
		if err != nil {
			return err
		}
		if err := writeRawFileAtomic(dst, sealed); err != nil {
			return err
		}
	}
	return nil
}
