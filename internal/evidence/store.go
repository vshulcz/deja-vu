package evidence

import (
	"encoding/binary"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/redact"
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

// load reads every receipt in dir.
func load(dir string) (map[uint64]string, bool) {
	b, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil || len(b) < len(magic)+4 || string(b[:len(magic)]) != magic {
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
	tmp := filepath.Join(dir, fileName+".tmp")
	if err := os.WriteFile(tmp, slices.Concat(head, blob), 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, fileName))
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
	path  string
	n     int
	built time.Time
}

type openReader struct {
	r  *Reader
	fi os.FileInfo
}

var (
	readersMu sync.Mutex
	readers   = map[string]openReader{}
)

// Open returns the reader for dir, or nil when the index holds no receipts.
// A rebuild swaps the file in under the same name, so a long-lived process
// reopens it when it changed.
func Open(dir string) *Reader {
	path := filepath.Join(dir, fileName)
	fi, statErr := os.Stat(path)
	readersMu.Lock()
	defer readersMu.Unlock()
	if o, ok := readers[dir]; ok && (statErr != nil && o.fi == nil || statErr == nil && o.fi != nil && os.SameFile(o.fi, fi) && o.fi.ModTime().Equal(fi.ModTime())) {
		return o.r
	}
	var r *Reader
	if statErr == nil {
		if f, err := os.Open(path); err == nil {
			h := make([]byte, len(magic)+4)
			if _, err := f.ReadAt(h, 0); err == nil && string(h[:len(magic)]) == magic {
				r = &Reader{path: path, n: int(binary.LittleEndian.Uint32(h[len(magic):])), built: fi.ModTime()}
			}
			_ = f.Close()
		}
	} else {
		fi = nil
	}
	readers[dir] = openReader{r: r, fi: fi}
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
	f, err := os.Open(r.path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	k := Key(harness, id, text)
	e := make([]byte, entrySize)
	base := int64(len(magic) + 4)
	lo, hi := 0, r.n
	for lo < hi {
		mid := (lo + hi) / 2
		if _, err := f.ReadAt(e, base+int64(mid)*entrySize); err != nil {
			return nil
		}
		v := binary.LittleEndian.Uint64(e)
		if v == k {
			off, n := binary.LittleEndian.Uint32(e[8:]), binary.LittleEndian.Uint32(e[12:])
			b := make([]byte, n)
			if _, err := f.ReadAt(b, base+int64(r.n)*entrySize+int64(off)); err != nil {
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
