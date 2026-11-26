package evidence

import (
	"bytes"
	"encoding/binary"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/vault"
)

// receipts.bin is sorted FNV-64 keys with offsets, then the text. The hook
// opens it and binary-searches with ReadAt rather than loading the table:
// on a copy of a real store (2318 sessions) it held 17,525 receipts in 5.4 MB,
// and the per-prompt p50 stayed at 76 ms.
const (
	fileName  = "receipts.bin"
	magic     = "DJRC1\n"
	entrySize = 16
)

// Build writes the receipts of every session to dir. A full build does; the
// incremental passes Update the file instead. Failures are swallowed: a
// missing receipt is the old output, never a reason to fail a build.
func Build(dir string, ss []model.Session, clean func(string) string) {
	all := map[uint64]string{}
	collect(ss, clean, func(r map[uint64]string, _ []uint64) { maps.Copy(all, r) })
	_ = write(dir, all)
}

// Update writes to dir the receipts in from with those of ss made anew: the
// sessions an incremental pass added or changed, whole. Every key of their
// assistant messages is dropped first, so what a key holds is what a full
// build would write for it. With no receipts in from it writes nothing:
// written for ss alone, the file would claim to cover every other session
// too (Reader.Covers). It reports whether dir holds receipts that cover ss.
func Update(from, dir string, ss []model.Session, clean func(string) string) bool {
	all, ok := load(from)
	if !ok {
		return false
	}
	collect(ss, clean, func(r map[uint64]string, keys []uint64) {
		for _, k := range keys {
			delete(all, k)
		}
		maps.Copy(all, r)
	})
	return write(dir, all) == nil
}

// collect hands put the receipts of each session, one at a time.
func collect(ss []model.Session, clean func(string) string, put func(map[uint64]string, []uint64)) {
	var mu sync.Mutex
	var wg sync.WaitGroup
	jobs := make(chan int)
	for range max(1, runtime.GOMAXPROCS(0)) {
		wg.Go(func() {
			for i := range jobs {
				r, keys := receipts(ss[i], clean)
				for k, v := range r {
					// The texts were redacted on the way in; this is the
					// one file that does not go through the index's own
					// writer, so it is not left to that.
					r[k], _ = redact.Text(v)
				}
				mu.Lock()
				put(r, keys)
				mu.Unlock()
			}
		})
	}
	for i := range ss {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
}

func canonicalVaultDir(dir string) string {
	if dir == "" {
		return ""
	}
	d := filepath.Clean(dir)
	for {
		switch {
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

// load reads every receipt in dir.
func load(dir string) (map[uint64]string, bool) {
	canon := canonicalVaultDir(dir)
	dek, vErr := vault.CheckUnlocked(canon)
	if vErr != nil {
		readersMu.Lock()
		delete(readers, dir)
		readersMu.Unlock()
		return nil, false
	}
	b, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		return nil, false
	}
	if len(dek) > 0 {
		b, err = vault.OpenBytes(dek, vault.DomainIndexFile, b)
		if err != nil {
			return nil, false
		}
	} else if vault.IsSealedBytes(b) {
		return nil, false
	}
	if len(b) < len(magic)+4 || string(b[:len(magic)]) != magic {
		return nil, false
	}
	n := int(binary.LittleEndian.Uint32(b[len(magic):]))
	base := len(magic) + 4
	blob := base + n*entrySize
	if blob > len(b) {
		return nil, false
	}
	all := make(map[uint64]string, n)
	for i := range n {
		e := b[base+i*entrySize:]
		off, size := int(binary.LittleEndian.Uint32(e[8:])), int(binary.LittleEndian.Uint32(e[12:]))
		if blob+off+size > len(b) {
			return nil, false
		}
		all[binary.LittleEndian.Uint64(e)] = string(b[blob+off : blob+off+size])
	}
	return all, true
}

func write(dir string, all map[uint64]string) error {
	dek, err := vault.CheckUnlocked(canonicalVaultDir(dir))
	if err != nil {
		return err
	}
	keys := make([]uint64, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	head := make([]byte, len(magic)+4+len(keys)*entrySize)
	copy(head, magic)
	binary.LittleEndian.PutUint32(head[len(magic):], uint32(len(keys))) //nolint:gosec // a session count, far below 2^32
	var blob []byte
	for i, k := range keys {
		e := head[len(magic)+4+i*entrySize:]
		binary.LittleEndian.PutUint64(e, k)
		binary.LittleEndian.PutUint32(e[8:], uint32(len(blob)))    //nolint:gosec // bounded by the file size
		binary.LittleEndian.PutUint32(e[12:], uint32(len(all[k]))) //nolint:gosec // one receipt, a few hundred bytes
		blob = append(blob, all[k]...)
	}
	payload := slices.Concat(head, blob)
	if len(dek) > 0 {
		payload, err = vault.SealBytes(dek, vault.DomainIndexFile, payload)
		if err != nil {
			return err
		}
	}
	tmp := filepath.Join(dir, fileName+".tmp")
	shared := atomicfile.SharedGroup(dir)
	perm := os.FileMode(0o600)
	if shared {
		perm = 0o660
	}
	if err := os.WriteFile(tmp, payload, perm); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if shared {
		_ = os.Chmod(tmp, 0o660)
	}
	dst := filepath.Join(dir, fileName)
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	if shared {
		_ = os.Chmod(dst, 0o660)
	}
	return nil
}

// Drop removes the receipts in dir, for a pass that changed sessions and
// could not update them: kept, they would cover those sessions with receipts
// from before the change.
func Drop(dir string) { _ = os.Remove(filepath.Join(dir, fileName)) }

// Reader looks receipts up without loading the table: a binary search over
// the sorted keys with ReadAt. It holds the path, not an open file: on Windows
// a handle kept for the life of the process stops a rebuild from renaming the
// index directory ("Access is denied").
type Reader struct {
	path     string
	canonDir string
	plain    []byte
	n        int
	built    time.Time
}

type openReader struct {
	r   *Reader
	fi  os.FileInfo
	enc bool
}

var (
	readersMu sync.Mutex
	readers   = map[string]openReader{}
)

// Open returns the reader for dir, or nil when the index holds no receipts.
// A rebuild swaps the file in under the same name, so a long-lived process
// reopens it when it changed.
func Open(dir string) *Reader {
	canon := canonicalVaultDir(dir)
	dek, vErr := vault.CheckUnlocked(canon)
	if vErr != nil {
		readersMu.Lock()
		delete(readers, dir)
		readersMu.Unlock()
		return nil
	}
	useEnc := len(dek) > 0
	path := filepath.Join(dir, fileName)
	fi, statErr := os.Stat(path)
	readersMu.Lock()
	defer readersMu.Unlock()
	if o, ok := readers[dir]; ok && o.enc == useEnc && (statErr != nil && o.fi == nil || statErr == nil && o.fi != nil && os.SameFile(o.fi, fi) && o.fi.ModTime().Equal(fi.ModTime())) {
		return o.r
	}
	var r *Reader
	if statErr == nil {
		if useEnc {
			if raw, err := os.ReadFile(path); err == nil {
				if plain, err := vault.OpenBytes(dek, vault.DomainIndexFile, raw); err == nil && len(plain) >= len(magic)+4 && string(plain[:len(magic)]) == magic {
					r = &Reader{path: path, canonDir: canon, plain: plain, n: int(binary.LittleEndian.Uint32(plain[len(magic):])), built: fi.ModTime()}
				}
			}
		} else {
			if f, err := os.Open(path); err == nil {
				h := make([]byte, len(magic)+4)
				if _, err := f.ReadAt(h, 0); err == nil && string(h[:len(magic)]) == magic {
					r = &Reader{path: path, n: int(binary.LittleEndian.Uint32(h[len(magic):])), built: fi.ModTime()}
				}
				_ = f.Close()
			}
		}
	} else {
		fi = nil
	}
	readers[dir] = openReader{r: r, fi: fi, enc: useEnc}
	return r
}

// Covers reports whether the receipts were written after the session last
// changed. A session the build never saw has none, and labelling its lines
// "no record backs it" would be a claim nobody checked.
func (r *Reader) Covers(updated time.Time) bool {
	return r != nil && !updated.IsZero() && !updated.After(r.built)
}

// Lookup returns the receipt lines of one assistant message.
func (r *Reader) Lookup(harness, id, text string) []string {
	if r == nil || r.n == 0 {
		return nil
	}
	var ra io.ReaderAt
	if r.plain != nil {
		if _, err := vault.CheckUnlocked(r.canonDir); err != nil {
			return nil
		}
		ra = bytes.NewReader(r.plain)
	} else {
		f, err := os.Open(r.path)
		if err != nil {
			return nil
		}
		defer func() { _ = f.Close() }()
		ra = f
	}
	k := Key(harness, id, text)
	e := make([]byte, entrySize)
	base := int64(len(magic) + 4)
	lo, hi := 0, r.n
	for lo < hi {
		mid := (lo + hi) / 2
		if _, err := ra.ReadAt(e, base+int64(mid)*entrySize); err != nil {
			return nil
		}
		v := binary.LittleEndian.Uint64(e)
		if v == k {
			off, n := binary.LittleEndian.Uint32(e[8:]), binary.LittleEndian.Uint32(e[12:])
			b := make([]byte, n)
			if _, err := ra.ReadAt(b, base+int64(r.n)*entrySize+int64(off)); err != nil {
				return nil
			}
			return strings.Split(string(b), "\n")
		}
		if v < k {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return nil
}
