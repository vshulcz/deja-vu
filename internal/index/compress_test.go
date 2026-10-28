package index

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	search "github.com/vshulcz/deja-vu/internal/query"
)

// Large payloads are deflated; everything else is stored as it is. Whatever
// the encoding, the text must come back byte-identical and stay findable.
func TestCompressedRecordsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "records.bin")
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	tbl := newRecordTables()
	rw, err := newRecordWriter(f, tbl)
	if err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("func encodeRecord(r Record) []byte { return nil } // padding line\n", 400)
	random := make([]byte, compressFloor*2)
	for i := range random {
		random[i] = byte(i*7 + i/251) // poorly compressible
	}
	want := []Record{
		{Key: "claude:s1", SourcePath: "/tmp/s1.jsonl", Role: "user", Text: "short one", Time: time.Unix(10, 20)},
		{Key: "claude:s1", SourcePath: "/tmp/s1.jsonl", Role: "assistant", Text: big, Time: time.Unix(30, 40)},
		{Key: "claude:s2", SourcePath: "/tmp/s2.jsonl", Role: "user", Text: string(random), Time: time.Unix(50, 60)},
		{Key: "claude:s2", SourcePath: "/tmp/s2.jsonl", Role: "user", Text: "", Time: time.Unix(70, 80)},
	}
	for _, r := range want {
		if _, err := rw.write(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := rw.Close(); err != nil {
		t.Fatal(err)
	}
	var got []Record
	if err := eachRecord(p, tbl, func(r Record) { got = append(got, r) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("read %d records, wrote %d", len(got), len(want))
	}
	for i, w := range want {
		g := got[i]
		if g.Text != w.Text || g.Key != w.Key || g.SourcePath != w.SourcePath || g.Role != w.Role || !g.Time.Equal(w.Time) {
			t.Errorf("record %d round-tripped wrong: key=%q role=%q len(text)=%d want len=%d", i, g.Key, g.Role, len(g.Text), len(w.Text))
		}
	}
}

// An ordinary message of a few hundred bytes is deflated now, and a record a
// build before the floor dropped stored raw at the same size still reads: the
// per-record flag decides, not the size, so a log mixing both needs no
// rebuild.
func TestMidSizeRecordsDeflateAndOldRawOnesStillRead(t *testing.T) {
	tbl := newRecordTables()
	text := strings.Repeat("the gravel pit pump stalls when the float sticks; ", 12)
	r := Record{Key: "claude:s1", SourcePath: "/tmp/s1.jsonl", Role: "assistant", Text: text, Time: time.Unix(10, 20)}
	enc := encodeRecord(r, tbl)
	_, _, _, rest, ok := recordPrefix(enc)
	if !ok || rest[0] != recordDeflated {
		t.Fatalf("a %d-byte message was not deflated (ok=%v)", len(text), ok)
	}
	if len(enc) >= len(text) {
		t.Errorf("deflated record is %d bytes for %d of text", len(enc), len(text))
	}
	short := encodeRecord(Record{Key: "claude:s1", Role: "user", Text: "tiny", Time: time.Unix(1, 0)}, tbl)
	if _, _, _, rest, _ := recordPrefix(short); rest[0] != recordRaw {
		t.Errorf("a record under the floor was deflated")
	}

	// The same record as the 8 KB floor wrote it.
	body := binary.LittleEndian.AppendUint64(nil, uint64(r.Time.UnixNano()))
	body = appendField(body, r.Text)
	old := binary.AppendUvarint(nil, tbl.intern(r.Key))
	old = binary.AppendUvarint(old, tbl.intern(r.SourcePath))
	old = binary.AppendUvarint(old, tbl.intern(r.Role))
	old = append(append(old, recordRaw), body...)
	for name, b := range map[string][]byte{"deflated": enc, "raw": old} {
		got, err := decodeRecord(b, tbl)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.Text != text || got.Key != r.Key || got.Role != r.Role || !got.Time.Equal(r.Time) {
			t.Errorf("%s record read back wrong: %+v", name, got)
		}
	}
}

// A big tool dump is exactly what gets compressed, so it must still be
// searchable and still come back in full.
func TestCompressedDumpsStaySearchable(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	dump := strings.Repeat("internal/index/store_io.go:63 encodeRecord ok padding padding ", 300) + " quetzalcoatlmarker tail"
	line := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + dump + `"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if len(dump) < compressFloor {
		t.Fatalf("fixture is %d bytes, below the %d floor — it would never be compressed", len(dump), compressFloor)
	}
	ss, err := Search(dir, search.Options{Query: "quetzalcoatlmarker", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("compressed dump not found: %d sessions", len(ss))
	}
	var full string
	var fullSb92 strings.Builder
	for _, m := range ss[0].Messages {
		fullSb92.WriteString(m.Text)
	}
	full += fullSb92.String()
	if !strings.Contains(full, "quetzalcoatlmarker") || len(full) < len(dump) {
		t.Fatalf("compressed dump came back truncated: %d bytes, want >= %d", len(full), len(dump))
	}
}

// A bucket directory no longer stores each token's offset; it is the running
// sum of the block lengths. Every token must still land on its own postings.
func TestBucketOffsetsAreDerivedCorrectly(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ab.bin")
	want := map[string][]posting{
		"talpha": {{Off: 1, Sid: 1}, {Off: 900, Sid: 2}},
		"tbeta":  {{Off: 5, Sid: 3}},
		"tgamma": {},
		"tdelta": {{Off: 7, Sid: 4}, {Off: 8, Sid: 5}, {Off: 9000000, Sid: 6}},
	}
	if err := writeBucket(p, want); err != nil {
		t.Fatal(err)
	}
	got, err := readBucket(p)
	if err != nil {
		t.Fatal(err)
	}
	for tok, w := range want {
		g := got[tok]
		if len(g) != len(w) {
			t.Errorf("token %q: %d postings, want %d", tok, len(g), len(w))
			continue
		}
		for i := range w {
			if g[i] != w[i] {
				t.Errorf("token %q posting %d = %+v, want %+v", tok, i, g[i], w[i])
			}
		}
	}
	// And reading one token directly must land on the same block.
	for tok, w := range want {
		g, err := readBucketToken(p, tok, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(g) != len(w) {
			t.Errorf("readBucketToken(%q) = %d postings, want %d", tok, len(g), len(w))
		}
	}
}
