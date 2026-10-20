package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/embed"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
)

// A rebuild of records.bin retired a sidecar whose vectors address records by
// offset alone (#1355), which every version 1 sidecar does. Search dropped the
// semantic tier without a word, while the unreachable endpoint beside it has
// always said so (#2208).
func TestAStaleSidecarIsNamedOnTheSearchPath(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"the scheduler retries twice"},"timestamp":"2026-08-02T10:00:00Z","sessionId":"aaa","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []string `json:"input"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		out := make([][]float32, len(body.Input))
		for i := range out {
			out[i] = []float32{1, 0}
		}
		payload, _ := json.Marshal(map[string]any{"embeddings": out})
		_, _ = w.Write(payload)
	}))
	defer ts.Close()
	if _, err := embed.EmbedIndex(dir, &embed.Client{URL: ts.URL, Model: "test"}, nil); err != nil {
		t.Fatal(err)
	}
	asVersion1Sidecar(t, dir)

	said := func(run func(notice *os.File)) string {
		t.Helper()
		f, err := os.CreateTemp(t.TempDir(), "notice")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		run(f)
		b, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	reset := func() { saidSidecarStale = false }
	t.Cleanup(reset)

	// A sidecar search will use is not announced. It may still say the endpoint
	// is unreachable, which is a different sentence about a different thing.
	reset()
	if quiet := said(func(notice *os.File) {
		maybeRerank(dir, []search.Hit{{}}, search.Options{Query: "scheduler"}, notice)
	}); strings.Contains(quiet, "sidecar") {
		t.Fatalf("a usable sidecar was announced: %q", quiet)
	}

	// The transcript rewritten under the index and the log written again, and
	// the offsets the vectors point at are gone. An ordinary pass leaves them
	// valid: a new file is appended since #3500, a rewritten one since #5387.
	rewound := `{"type":"user","message":{"role":"user","content":"the cache eviction wiped sessions"},"timestamp":"2026-08-03T10:00:00Z","sessionId":"aaa","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(rewound), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	s, err := embed.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !embed.Stale(dir, s) {
		t.Fatal("the rebuild left the sidecar usable, so this measures nothing")
	}

	reset()
	rerank := said(func(notice *os.File) {
		maybeRerank(dir, []search.Hit{{}}, search.Options{Query: "scheduler"}, notice)
	})
	if !strings.Contains(rerank, "sidecar") || !strings.Contains(rerank, "deja embed") {
		t.Errorf("a rerank that gave up on a stale sidecar says %q", rerank)
	}
	reset()
	semantic := said(func(notice *os.File) {
		maybeSemantic(dir, nil, search.Options{Query: "scheduler"}, notice)
	})
	if !strings.Contains(semantic, "sidecar") || !strings.Contains(semantic, "deja embed") {
		t.Errorf("a semantic search that gave up on a stale sidecar says %q", semantic)
	}
	// One search runs both, and one retired file is one fact.
	reset()
	both := said(func(notice *os.File) {
		maybeRerank(dir, nil, search.Options{Query: "scheduler"}, notice)
		maybeSemantic(dir, nil, search.Options{Query: "scheduler"}, notice)
	})
	if n := strings.Count(both, "deja embed"); n != 1 {
		t.Errorf("one search said it %d times:\n%s", n, both)
	}
}

// asVersion1Sidecar rewrites the sidecar in dir the way deja wrote it before
// vectors carried their record's identity, so a test can reach the stale path
// a version 2 sidecar no longer takes.
func asVersion1Sidecar(t *testing.T, dir string) {
	t.Helper()
	s, err := embed.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	str := func(v string) {
		_ = binary.Write(&b, binary.LittleEndian, uint32(len(v)))
		b.WriteString(v)
	}
	b.WriteString("DJV1")
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))
	_ = binary.Write(&b, binary.LittleEndian, uint16(s.Dim))
	str(s.Model)
	str(s.Generation)
	_ = binary.Write(&b, binary.LittleEndian, uint64(len(s.Vectors)))
	_ = binary.Write(&b, binary.LittleEndian, uint64(s.Covered))
	for _, v := range s.Vectors {
		_ = binary.Write(&b, binary.LittleEndian, v.Offset)
		str(v.Key)
		_ = binary.Write(&b, binary.LittleEndian, v.Values)
	}
	if err := os.WriteFile(embed.Path(dir), b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if back, err := embed.Read(dir); err != nil || len(back.Vectors) != len(s.Vectors) || (len(back.Vectors) > 0 && back.Vectors[0].Ident != 0) {
		t.Fatalf("the version 1 sidecar does not read back: %v", err)
	}
}
