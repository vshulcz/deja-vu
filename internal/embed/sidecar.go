package embed

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
)

// sidecarVersion 2 adds each vector's Ident; 3 drops the record's time from
// it. A version 1 file is still read, and its vectors are rekeyed on the next
// `deja embed` while the layout they were made for holds; a version 2 file is
// rekeyed by its own identity, wherever its records now sit.
const sidecarVersion uint16 = 3

var magic = [4]byte{'D', 'J', 'V', '1'}

// Vector is one record's embedding and where to find the record.
type Vector struct {
	Offset int64
	// Ident is the record's identity (RecordIdent), what finds the record
	// again once a rewrite of records.bin has moved every offset. Zero in a
	// vector from a version 1 sidecar.
	Ident  uint64
	Key    string
	Values []float32
}

// RecordIdent names a record by what it holds rather than where it sits: its
// session, role and text. Any pass that rewrites records.bin moves the
// offsets, and a sidecar keyed by offset alone was thrown away and embedded
// again from zero, 45 minutes on a 112k-record store, often within the hour
// it was built (#4976). The text is hashed by its length and its first and
// last 256 bytes, which with the session is enough to tell records apart and
// keeps a lookup over every record cheap.
//
// The time stays out: a vector is made from the text alone, and a harness that
// writes no time per turn (Cursor without its <timestamp> block, Continue,
// Cline's fallback) dates its turns from the file's modification time, so
// every pass that grew the session gave each of its records a new identity
// and the next embed sent the whole session again.
func RecordIdent(r index.Record) uint64 {
	h := identHash()
	h.str(r.Key)
	h.str(r.Role)
	h.num(uint64(len(r.Text)))
	head, tail := r.Text, ""
	if len(head) > 256 {
		head = r.Text[:256]
		tail = r.Text[max(256, len(r.Text)-256):]
	}
	h.str(head)
	h.str(tail)
	return h.sum()
}

// recordIdentV2 is the identity a version 2 sidecar holds: the time in place
// of the tail. It is how those vectors find their records once, to be rekeyed.
func recordIdentV2(r index.Record) uint64 {
	h := identHash()
	text := r.Text
	if len(text) > 256 {
		text = text[:256]
	}
	h.str(r.Key)
	h.str(r.Role)
	h.num(uint64(r.Time.UnixNano()))
	h.num(uint64(len(r.Text)))
	h.str(text)
	return h.sum()
}

// fnv is FNV-1a by hand, without the allocations of hash/fnv and fmt.
type fnv uint64

func identHash() *fnv { h := fnv(14695981039346656037); return &h }

const fnvPrime = 1099511628211

func (h *fnv) str(s string) {
	for i := range len(s) {
		*h = (*h ^ fnv(s[i])) * fnvPrime
	}
	*h *= fnvPrime
}

func (h *fnv) num(n uint64) {
	for range 8 {
		*h = (*h ^ fnv(n&0xff)) * fnvPrime
		n >>= 8
	}
}

func (h *fnv) sum() uint64 {
	if *h != 0 {
		return uint64(*h)
	}
	return 1
}

// identOf is the identity function the vectors of a sidecar written as version
// were keyed with.
func identOf(version uint16) func(index.Record) uint64 {
	if version == 2 {
		return recordIdentV2
	}
	return RecordIdent
}

// keyed reports whether every vector carries its record's identity, so none
// depends on the offsets staying put.
func keyed(s Sidecar) bool {
	for _, v := range s.Vectors {
		if v.Ident == 0 {
			return false
		}
	}
	return true
}

// recordLookup finds the record a vector was made from: at its offset when
// the record there is still the one it names, and by identity once a rewrite
// has moved it. A version 1 vector names nothing and is trusted at its offset
// only while the index keeps the layout the sidecar was written for. Hashing
// every record costs about 30 ms over 100k, so the identity map is built only
// when a vector has moved, once per search.
type recordLookup struct {
	records  []index.OffsetRecord
	byOffset map[int64]index.OffsetRecord
	byIdent  map[uint64]index.OffsetRecord
	ident    func(index.Record) uint64
	layout   bool
}

func newRecordLookup(records []index.OffsetRecord, layout bool, ident func(index.Record) uint64) *recordLookup {
	l := &recordLookup{records: records, byOffset: make(map[int64]index.OffsetRecord, len(records)), layout: layout, ident: ident}
	for _, r := range records {
		l.byOffset[r.Offset] = r
	}
	return l
}

func (l *recordLookup) find(v Vector) (index.OffsetRecord, bool) {
	if r, ok := l.byOffset[v.Offset]; ok && r.Record.Key == v.Key {
		if (v.Ident == 0 && l.layout) || (v.Ident != 0 && l.ident(r.Record) == v.Ident) {
			return r, true
		}
	}
	if v.Ident == 0 {
		return index.OffsetRecord{}, false
	}
	if l.byIdent == nil {
		l.byIdent = make(map[uint64]index.OffsetRecord, len(l.records))
		for _, x := range l.records {
			id := l.ident(x.Record)
			if _, dup := l.byIdent[id]; !dup {
				l.byIdent[id] = x
			}
		}
	}
	r, ok := l.byIdent[v.Ident]
	return r, ok && r.Record.Key == v.Key
}

// Sidecar is the vectors file kept beside an index.
type Sidecar struct {
	Model, Generation string
	Dim               int
	Vectors           []Vector
	Covered           int
	// Version is the format the file was read in, which names the identity
	// its vectors hold. Zero means current.
	Version uint16
}

// Stale reports whether a sidecar was built for a different index than the one
// in dir. Vectors point at records by byte offset, and a rebuild moves them: a
// `deja forget` that dropped one session left every later offset shifted, so a
// vector still keyed to a session that survived resolved to a record belonging
// to another one and semantic search quoted text the named session never said
// (#1355). The generation was recorded for exactly this and only checked when
// re-embedding.
//
// A sidecar whose vectors all carry their record's identity is never stale:
// each vector finds its record by what it holds, wherever it now sits.
func Stale(dir string, s Sidecar) bool {
	if len(s.Vectors) == 0 || keyed(s) {
		return false
	}
	gen, err := index.Generation(dir)
	if err != nil {
		return true
	}
	return !sameLayout(s.Generation, gen)
}

// sameLayout reports whether two generations describe an index where the
// offsets a vector holds still point at the same records.
//
// A generation is the build's stamp and the size records.bin had then. The
// stamp alone cannot answer this — the incremental path carries it forward on
// purpose, and two rebuilds inside one tick of a coarse clock share one — but
// the size alone is too strict: appending a session grows the file without
// moving a single existing record, and treating that as a new index threw away
// every vector and re-embedded the whole store on each `deja index` (#1357).
// So: same stamp and a file that only grew is the same layout.
func sameLayout(was, now string) bool {
	if was == now {
		return true
	}
	wasStamp, wasSize, ok := splitGeneration(was)
	nowStamp, nowSize, ok2 := splitGeneration(now)
	if !ok || !ok2 || wasStamp != nowStamp {
		return false
	}
	return nowSize >= wasSize
}

func splitGeneration(gen string) (stamp string, size int64, ok bool) {
	i := strings.LastIndexByte(gen, '+')
	if i < 0 {
		return "", 0, false
	}
	n, err := strconv.ParseInt(gen[i+1:], 10, 64)
	if err != nil || i == 0 || n < 0 {
		return "", 0, false
	}
	return gen[:i], n, true
}

// Path is where the sidecar for the index in dir lives.
func Path(dir string) string {
	return strings.TrimSuffix(dir, string(os.PathSeparator)) + ".vectors.bin"
}

// EmbedIndex embeds the index's records through client and writes the sidecar.
// keep decides which records are sent: it is how the caller keeps content the
// trust policy withholds off the wire to an external endpoint. A nil keep
// embeds every record.
func EmbedIndex(dir string, client *Client, keep func(index.Record) bool) (Sidecar, error) { //nolint:revive // the name is used from cmd/deja
	recs, err := index.ReadRecords(dir)
	if err != nil {
		return Sidecar{}, fmt.Errorf("read index records: %w", err)
	}
	if keep != nil {
		kept := recs[:0:0]
		for _, r := range recs {
			if keep(r.Record) {
				kept = append(kept, r)
			}
		}
		recs = kept
	}
	gen, err := index.Generation(dir)
	if err != nil {
		return Sidecar{}, err
	}
	old, _ := readFile(Path(dir))
	if old.Model != client.storedModel() || old.Dim == 0 {
		old = Sidecar{}
	}
	// A vector is carried to the record it was made from, found by identity
	// and given that record's offset now. Only the records the caller allows
	// are looked in, so vectors the caller no longer allows go rather than
	// being carried forward: a policy that tightens has to reach the sidecar
	// too, since SemanticSearch reads this file directly and kept vectors bring
	// the withheld sessions back as answers (#1311). A version 1 vector has no
	// identity and is carried only while the layout it was made for holds; a
	// version 2 one is found by the identity it was written with.
	lookup := newRecordLookup(recs, sameLayout(old.Generation, gen), identOf(old.Version))
	covered := make(map[uint64]bool, len(old.Vectors))
	kept := old.Vectors[:0:0]
	for _, v := range old.Vectors {
		r, ok := lookup.find(v)
		if !ok {
			continue
		}
		id := RecordIdent(r.Record)
		if covered[id] {
			continue
		}
		covered[id] = true
		kept = append(kept, Vector{Offset: r.Offset, Ident: id, Key: v.Key, Values: v.Values})
	}
	old.Version = sidecarVersion
	old.Vectors = kept
	pending := toEmbed(recs, covered)
	batches := 0
	for len(pending) > 0 {
		n := min(len(pending), embedBatch)
		texts := make([]string, n)
		for i := range pending[:n] {
			texts[i] = client.PassagePrefix + truncate(pending[i].Record.Text)
		}
		vectors, err := client.Embed(context.Background(), texts)
		if err != nil {
			// Keep what this run embedded since the last checkpoint: a run
			// that failed half an hour in lost all of it (#4975).
			if len(old.Vectors) > 0 {
				old.Model, old.Generation, old.Covered = client.storedModel(), gen, len(old.Vectors)
				_ = write(dir, old)
			}
			return Sidecar{}, err
		}
		for i, values := range vectors {
			if old.Dim == 0 {
				old.Dim = len(values)
			}
			if len(values) != old.Dim {
				return Sidecar{}, fmt.Errorf("embedding dimension changed from %d to %d", old.Dim, len(values))
			}
			old.Vectors = append(old.Vectors, Vector{Offset: pending[i].Offset, Ident: RecordIdent(pending[i].Record), Key: pending[i].Record.Key, Values: values})
		}
		pending = pending[n:]
		batches++
		if batches%checkpointEvery == 0 {
			// Covered is what the sidecar holds so far: doctor reads it as
			// the share embedded, and the next run embeds only the rest.
			old.Model, old.Generation, old.Covered = client.storedModel(), gen, len(old.Vectors)
			if err := write(dir, old); err != nil {
				return Sidecar{}, err
			}
		}
	}
	old.Model, old.Generation, old.Covered = client.storedModel(), gen, len(recs)
	if err := write(dir, old); err != nil {
		return Sidecar{}, err
	}
	return old, nil
}

// toEmbed is the records still to embed: not embedded yet, and with text to
// send. A record that cleaning left empty has nothing to embed, and Ollama
// refuses the whole batch over one empty input ("empty content"), as OpenAI's
// API does (#4975).
// A record with the same identity as one already covered or queued shares its
// vector, so it is sent once. covered is updated with what is queued.
func toEmbed(recs []index.OffsetRecord, covered map[uint64]bool) []index.OffsetRecord {
	var pending []index.OffsetRecord
	for _, r := range recs {
		if strings.TrimSpace(r.Record.Text) == "" {
			continue
		}
		id := RecordIdent(r.Record)
		if covered[id] {
			continue
		}
		covered[id] = true
		pending = append(pending, r)
	}
	return pending
}

// embedBatch is how many texts go in one request; checkpointEvery is how many
// requests pass between writes of the sidecar, so a run killed partway keeps
// what it embedded.
var embedBatch, checkpointEvery = 32, 50

func truncate(s string) string {
	const limit = 2000
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit-12]) + "\n[truncated]"
}

// readMemo keeps the last sidecar parsed by this process. One search reads the
// file for the rerank and again for the semantic tier, and parsing a few hundred
// megabytes twice is seconds the second time buys nothing for. It is valid for
// the same file only: path, size and modification time must all still match.
// A long-lived process (deja mcp) would otherwise hold the vectors, hundreds of
// megabytes, between questions asked minutes apart, so the memo is dropped after
// memoIdle without a read.
var readMemo struct {
	sync.Mutex
	path  string
	size  int64
	mod   time.Time
	car   Sidecar
	timer *time.Timer
}

var memoIdle = 2 * time.Minute

// touchMemo restarts the idle clock; readMemo must be locked.
func touchMemo() {
	if readMemo.timer != nil {
		readMemo.timer.Stop()
	}
	readMemo.timer = time.AfterFunc(memoIdle, func() {
		readMemo.Lock()
		readMemo.path, readMemo.car = "", Sidecar{}
		readMemo.Unlock()
	})
}

// Read parses the sidecar of dir. The result is shared between callers of one
// process and must not be modified; EmbedIndex, which does, uses readFile.
func Read(dir string) (Sidecar, error) {
	path := Path(dir)
	st, err := os.Stat(path)
	if err != nil {
		return Sidecar{}, err
	}
	readMemo.Lock()
	if readMemo.path == path && readMemo.size == st.Size() && readMemo.mod.Equal(st.ModTime()) && readMemo.car.Vectors != nil {
		car := readMemo.car
		touchMemo()
		readMemo.Unlock()
		return car, nil
	}
	readMemo.Unlock()
	car, err := readFile(path)
	if err != nil {
		return car, err
	}
	readMemo.Lock()
	readMemo.path, readMemo.size, readMemo.mod, readMemo.car = path, st.Size(), st.ModTime(), car
	touchMemo()
	readMemo.Unlock()
	return car, nil
}

//nolint:gocritic // one err carries through the fixed field sequence
func readFile(path string) (Sidecar, error) {
	f, err := os.Open(path)
	if err != nil {
		return Sidecar{}, err
	}
	defer func() { _ = f.Close() }()
	// Buffered: decoding straight off the file was a few syscalls per vector,
	// and on windows the file stays open for all of it, which refuses the
	// publish of the next sidecar over it (#5412).
	r := bufio.NewReaderSize(f, 1<<16)
	var got [4]byte
	if _, err = io.ReadFull(r, got[:]); err != nil || got != magic {
		return Sidecar{}, errors.New("invalid vector sidecar")
	}
	var version, dim uint16
	var count, covered uint64
	var model, gen string
	if err = binary.Read(r, binary.LittleEndian, &version); err != nil || version < 1 || version > sidecarVersion {
		return Sidecar{}, errors.New("unsupported vector sidecar")
	}
	if err = binary.Read(r, binary.LittleEndian, &dim); err != nil {
		return Sidecar{}, err
	}
	if model, err = readString(r); err != nil {
		return Sidecar{}, err
	}
	if gen, err = readString(r); err != nil {
		return Sidecar{}, err
	}
	if err = binary.Read(r, binary.LittleEndian, &count); err != nil {
		return Sidecar{}, err
	}
	if count > 10_000_000 || dim > 16_384 {
		return Sidecar{}, errors.New("invalid vector sidecar dimensions")
	}
	if err = binary.Read(r, binary.LittleEndian, &covered); err != nil {
		return Sidecar{}, err
	}
	out := Sidecar{Model: model, Generation: gen, Dim: int(dim), Covered: int(covered), Vectors: make([]Vector, count), Version: version}
	for i := range out.Vectors {
		if err = binary.Read(r, binary.LittleEndian, &out.Vectors[i].Offset); err != nil {
			return Sidecar{}, err
		}
		if version >= 2 {
			if err = binary.Read(r, binary.LittleEndian, &out.Vectors[i].Ident); err != nil {
				return Sidecar{}, err
			}
		}
		if out.Vectors[i].Key, err = readString(r); err != nil {
			return Sidecar{}, err
		}
		out.Vectors[i].Values = make([]float32, out.Dim)
		if err = binary.Read(r, binary.LittleEndian, out.Vectors[i].Values); err != nil {
			return Sidecar{}, err
		}
	}
	return out, nil
}

// write publishes the sidecar in one step. The temp name used to be the
// destination plus ".tmp", and EmbedIndex takes no lock — two embeds at once
// shared that name, so one could rename a file the other was still writing and
// leave a sidecar that decodes as nothing (#1319).
func write(dir string, s Sidecar) error {
	readMemo.Lock()
	readMemo.path = ""
	readMemo.Unlock()
	return atomicfile.WriteStream(Path(dir), 0o600, func(w io.Writer) error {
		// An error-remembering writer rather than a check after every field:
		// the fields are a fixed sequence, the first failure is the one worth
		// reporting, and the checks were branches nothing could reach.
		ew := &errWriter{w: w}
		ew.write(magic[:])
		ew.num(sidecarVersion)
		ew.num(uint16(s.Dim)) //nolint:gosec // model dimensions are far below 65536, and readFile refuses above 16384
		ew.str(s.Model)
		ew.str(s.Generation)
		ew.num(uint64(len(s.Vectors)))
		ew.num(uint64(s.Covered)) //nolint:gosec // a count, never negative
		for _, v := range s.Vectors {
			ew.num(v.Offset)
			ew.num(v.Ident)
			ew.str(v.Key)
			ew.num(v.Values)
		}
		return ew.err
	})
}

// errWriter keeps the first error and ignores everything after it.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) write(b []byte) {
	if e.err == nil {
		_, e.err = e.w.Write(b)
	}
}

func (e *errWriter) num(v any) {
	if e.err == nil {
		e.err = binary.Write(e.w, binary.LittleEndian, v)
	}
}

func (e *errWriter) str(v string) {
	if e.err == nil {
		e.err = writeString(e.w, v)
	}
}

func writeString(w interface{ Write([]byte) (int, error) }, s string) error {
	if err := binary.Write(w, binary.LittleEndian, uint32(len(s))); err != nil { //nolint:gosec // model names and session keys are far below 4 GiB
		return err
	}
	_, err := w.Write([]byte(s))
	return err
}
func readString(r io.Reader) (string, error) {
	var n uint32
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return "", err
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return "", err
	}
	return string(b), nil
}

// Cosine is the cosine similarity of a and b, zero when they differ in length
// or either is empty or all zeros.
func Cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, aa, bb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		aa += x * x
		bb += y * y
	}
	if aa == 0 || bb == 0 {
		return 0
	}
	return dot / math.Sqrt(aa*bb)
}
