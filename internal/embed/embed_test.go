package embed

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

func TestClientShapesAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want float32
	}{
		{"ollama", `{"embeddings":[[1,2]]}`, 1},
		{"openai", `{"data":[{"embedding":[3,4]}]}`, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path == "" {
					t.Error("bad request")
				}
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request["model"] == nil {
					t.Error("bad body")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer s.Close()
			c := &Client{URL: s.URL, Model: "test"}
			v, err := c.Embed(context.Background(), []string{"hello"})
			if err != nil || len(v) != 1 || v[0][0] != tc.want {
				t.Fatalf("vectors=%v err=%v", v, err)
			}
		})
	}
	s := httptest.NewServer(http.NotFoundHandler())
	defer s.Close()
	if _, err := (&Client{URL: s.URL, Model: "test"}).Embed(context.Background(), []string{"x"}); err == nil {
		t.Fatal("expected status error")
	}
	for name, body := range map[string]string{"bad json": "{", "wrong count": `{"data":[]}`} {
		t.Run(name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer ts.Close()
			if _, err := (&Client{URL: ts.URL, Model: "test"}).Embed(context.Background(), []string{"x"}); err == nil {
				t.Fatal("expected response error")
			}
		})
	}
	if _, err := (&Client{URL: "://bad"}).Embed(context.Background(), []string{"x"}); err == nil {
		t.Fatal("expected request creation error")
	}
	if _, err := (&Client{URL: "http://127.0.0.1:1", HTTP: &http.Client{}}).Embed(context.Background(), []string{"x"}); err == nil {
		t.Fatal("expected transport error")
	}
	if got, err := (&Client{}).Embed(context.Background(), nil); err != nil || got != nil {
		t.Fatalf("empty embed = %v, %v", got, err)
	}
}

func TestNewExplicitAndProbeOrder(t *testing.T) {
	t.Setenv("DEJA_EMBED_MODEL", "probe-model")
	t.Setenv("DEJA_EMBED_URL", "http://explicit.invalid/api/embed")
	c, err := New()
	if err != nil || c.URL != os.Getenv("DEJA_EMBED_URL") || c.Model != "probe-model" {
		t.Fatalf("client=%v err=%v", c, err)
	}
	if !IsOllama("http://x/api/embed/") || IsOllama("http://x/v1/embeddings") {
		t.Fatal("IsOllama classified endpoint incorrectly")
	}
	for body, wantErr := range map[string]bool{`{"embeddings":[]}`: true, `{"embeddings":[[]]}`: true, `{"embeddings":[[1]]}`: false} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		c := &Client{URL: ts.URL, Model: "m"}
		err := c.probe()
		if (err != nil) != wantErr {
			t.Fatalf("probe body %s err=%v", body, err)
		}
		ts.Close()
	}
	first := httptest.NewServer(http.NotFoundHandler())
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"embeddings":[[1]]}`)) }))
	defer first.Close()
	defer second.Close()
	t.Setenv("DEJA_EMBED_URL", "")
	oldURLs := probeURLs
	probeURLs = []string{first.URL, second.URL}
	c, err = New()
	probeURLs = oldURLs
	if err != nil || c.URL != second.URL {
		t.Fatalf("probe fallback client=%v err=%v", c, err)
	}
	probeURLs = []string{first.URL}
	if _, err := New(); err == nil {
		t.Fatal("unavailable probe endpoints should fail")
	}
	probeURLs = oldURLs
}

func TestSidecarRoundTripCorruptAndRerank(t *testing.T) {
	d := t.TempDir()
	dir := filepath.Join(d, "index.db")
	s := Sidecar{Model: "m", Generation: "g", Dim: 2, Covered: 2, Vectors: []Vector{{Offset: 4, Key: "claude:a", Values: []float32{1, 0}}, {Offset: 8, Key: "claude:b", Values: []float32{0, 1}}}}
	if err := write(dir, s); err != nil {
		t.Fatal(err)
	}
	got, err := Read(dir)
	if err != nil || len(got.Vectors) != 2 || got.Vectors[1].Values[1] != 1 {
		t.Fatalf("sidecar=%+v err=%v", got, err)
	}
	hits := []search.Hit{{Session: session("a"), Score: 2}, {Session: session("b"), Score: 1}}
	client := &Client{URL: "", Model: "m", HTTP: http.DefaultClient}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"data":[{"embedding":[1,0]}]}`)) }))
	defer server.Close()
	client.URL = server.URL
	ranked, err := Rerank(context.Background(), hits, "q", got, client)
	if err != nil || ranked[0].Session.ID != "a" {
		t.Fatalf("ranked=%v err=%v", ranked, err)
	}
	if err := os.WriteFile(Path(dir), []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(dir); err == nil {
		t.Fatal("expected corrupt error")
	}
	if got := truncate(strings.Repeat("x", 2100)); len([]rune(got)) != 2000 || !strings.HasSuffix(got, "[truncated]") {
		t.Fatalf("truncate = %q", got)
	}
	if Cosine([]float32{1}, []float32{1, 0}) != 0 || Cosine([]float32{0, 0}, []float32{1, 0}) != 0 {
		t.Fatal("invalid cosine should be zero")
	}
}

func TestEmbedIndexRoundTripAndWatermark(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "claude", "-tmp-project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "session.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"user","sessionId":"s","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"a semantic needle"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "aider"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "antigravity"))
	t.Setenv("DEJA_GROK_ROOT", filepath.Join(tmp, "grok"))
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var request struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Input) != 1 || !strings.Contains(request.Input[0], "needle") {
			t.Errorf("request = %#v, err=%v", request, err)
		}
		if calls > 1 {
			_, _ = w.Write([]byte(`{"embeddings":[[1]]}`))
		} else {
			_, _ = w.Write([]byte(`{"embeddings":[[1,0]]}`))
		}
	}))
	defer ts.Close()
	s, err := EmbedIndex(dir, &Client{URL: ts.URL, Model: "m"})
	if err != nil || calls != 1 || s.Covered != 1 || len(s.Vectors) != 1 {
		t.Fatalf("first embed sidecar=%+v calls=%d err=%v", s, calls, err)
	}
	if _, err = EmbedIndex(dir, &Client{URL: ts.URL, Model: "m"}); err != nil || calls != 1 {
		t.Fatalf("watermark did not reuse vector calls=%d err=%v", calls, err)
	}
	if err := write(dir, Sidecar{Model: "m", Generation: s.Generation, Dim: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := EmbedIndex(dir, &Client{URL: ts.URL, Model: "m"}); err == nil || !strings.Contains(err.Error(), "dimension changed") {
		t.Fatalf("dimension change err=%v", err)
	}
	bad := httptest.NewServer(http.NotFoundHandler())
	defer bad.Close()
	if _, err := EmbedIndex(dir, &Client{URL: bad.URL, Model: "other"}); err == nil {
		t.Fatal("embedding endpoint error was ignored")
	}
	if err := write(filepath.Join(tmp, "no", "missing"), Sidecar{}); err == nil {
		t.Fatal("write into missing directory succeeded")
	}
	renamed := filepath.Join(tmp, "rename-target")
	if err := os.MkdirAll(Path(renamed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := write(renamed, Sidecar{}); err == nil {
		t.Fatal("write over directory succeeded")
	}
}

func TestRerankUsesBestDuplicateAndRejectsEmptyQueryVector(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"data":[{"embedding":[1,0]}]}`)) }))
	defer s.Close()
	hits := []search.Hit{{Session: session("a"), Score: 10}, {Session: session("b"), Score: 0}}
	sidecar := Sidecar{Vectors: []Vector{{Key: "claude:a", Values: []float32{-1, 0}}, {Key: "claude:a", Values: []float32{1, 0}}, {Key: "claude:b", Values: []float32{0, 1}}}}
	got, err := Rerank(context.Background(), hits, "q", sidecar, &Client{URL: s.URL})
	if err != nil || got[0].Session.ID != "a" || got[0].Score != 1 {
		t.Fatalf("rerank = %#v, %v", got, err)
	}
	var many []search.Hit
	for i := 0; i < 65; i++ {
		many = append(many, search.Hit{Session: session("a"), Score: float64(i)})
	}
	if _, err := Rerank(context.Background(), many, "q", sidecar, &Client{URL: s.URL}); err != nil {
		t.Fatal(err)
	}
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"data":[]}`)) }))
	defer empty.Close()
	if _, err := Rerank(context.Background(), hits, "q", sidecar, &Client{URL: empty.URL}); err == nil {
		t.Fatal("expected empty query vector error")
	}
}

func TestSemanticSearchFiltersRanksAndCaps(t *testing.T) {
	if errBadQueryVector.Error() != "embedding endpoint returned no query vector" {
		t.Fatal("unexpected query vector error")
	}
	tmp := t.TempDir()
	root := filepath.Join(tmp, "claude")
	if err := os.MkdirAll(filepath.Join(root, "project"), 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(id, text string) string {
		return fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":%q}}`+"\n", id, text)
	}
	if err := os.WriteFile(filepath.Join(root, "project", "a.jsonl"), []byte(line("a", "semantic answer")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "project", "b.jsonl"), []byte(line("b", "weak answer")), 0o600); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"HOME": tmp, "USERPROFILE": tmp, "DEJA_CLAUDE_ROOT": root,
		"DEJA_CODEX_ROOT": filepath.Join(tmp, "codex"), "DEJA_OPENCODE_DB": filepath.Join(tmp, "open.db"),
		"DEJA_AIDER_ROOTS": filepath.Join(tmp, "aider"), "DEJA_GEMINI_ROOT": filepath.Join(tmp, "gemini"),
		"DEJA_CURSOR_ROOT": filepath.Join(tmp, "cursor"), "DEJA_CURSOR_CLI_ROOT": filepath.Join(tmp, "cursor-cli"),
		"DEJA_ANTIGRAVITY_ROOT": filepath.Join(tmp, "antigravity"), "DEJA_GROK_ROOT": filepath.Join(tmp, "grok"),
		"DEJA_QWEN_ROOT": filepath.Join(tmp, "qwen"),
	} {
		t.Setenv(key, value)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	records, err := index.ReadRecords(dir)
	if err != nil {
		t.Fatal(err)
	}
	gen, err := index.Generation(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := write(dir, Sidecar{Generation: gen, Dim: 2, Vectors: []Vector{
		{Offset: records[0].Offset, Key: records[0].Record.Key, Values: []float32{1, 0}},
		{Offset: records[1].Offset, Key: records[1].Record.Key, Values: []float32{0.8, 0.6}},
	}}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"embeddings":[[1,0]]}`))
	}))
	defer ts.Close()
	hits, err := SemanticSearch(context.Background(), dir, search.Options{Query: "rephrased", All: true}, Sidecar{Generation: gen, Dim: 2, Vectors: []Vector{
		{Offset: records[0].Offset, Key: records[0].Record.Key, Values: []float32{1, 0}},
		{Offset: records[1].Offset, Key: records[1].Record.Key, Values: []float32{0, 1}},
	}}, &Client{URL: ts.URL})
	if err != nil || len(hits) != 1 || hits[0].Session.ID != "a" || hits[0].Count != 0 || hits[0].Score != 1 || !strings.Contains(hits[0].Snippets[0], "semantic answer") {
		t.Fatalf("semantic hits=%#v err=%v", hits, err)
	}
	if _, err := SemanticSearch(context.Background(), dir, search.Options{Query: "q", All: true}, Sidecar{}, &Client{URL: "http://127.0.0.1:1", HTTP: &http.Client{}}); err == nil {
		t.Fatal("endpoint failure was ignored")
	}
	badVector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"embeddings":[[1],[2]]}`))
	}))
	defer badVector.Close()
	if _, err := SemanticSearch(context.Background(), dir, search.Options{Query: "q", All: true}, Sidecar{}, &Client{URL: badVector.URL}); err == nil {
		t.Fatal("multiple query vectors were accepted")
	}
	if _, err := SemanticSearch(context.Background(), filepath.Join(tmp, "missing"), search.Options{Query: "q", All: true}, Sidecar{}, &Client{URL: ts.URL}); err == nil {
		t.Fatal("missing records were accepted")
	}
	if got, err := SemanticSearch(context.Background(), dir, search.Options{Query: "q", Role: "assistant"}, Sidecar{Vectors: []Vector{
		{Offset: 999, Key: records[0].Record.Key, Values: []float32{1, 0}},
		{Offset: records[0].Offset, Key: "claude:missing", Values: []float32{1, 0}},
	}}, &Client{URL: ts.URL}); err != nil || len(got) != 0 {
		t.Fatalf("filtered semantic results=%#v err=%v", got, err)
	}
	noManifest := filepath.Join(tmp, "no-manifest")
	if err := os.MkdirAll(noManifest, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(noManifest, "records.bin"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SemanticSearch(context.Background(), noManifest, search.Options{Query: "q", All: true}, Sidecar{}, &Client{URL: ts.URL}); err == nil {
		t.Fatal("missing manifest was accepted")
	}
}

func TestSidecarRejectsMalformedHeadersAndRerankShortcuts(t *testing.T) {
	dir := t.TempDir()
	cases := [][]byte{
		[]byte("bad"),
		append(append([]byte{}, magic[:]...), 2, 0, 0, 0),
	}
	for i, data := range cases {
		if err := os.WriteFile(Path(filepath.Join(dir, string(rune('a'+i)))), data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Read(filepath.Join(dir, string(rune('a'+i)))); err == nil {
			t.Fatal("malformed sidecar was accepted")
		}
	}
	badDim := filepath.Join(dir, "dim")
	data := append([]byte{}, magic[:]...)
	data = append(data, 1, 0)
	var dim [2]byte
	binary.LittleEndian.PutUint16(dim[:], 16_385)
	data = append(data, dim[:]...)
	if err := os.WriteFile(Path(badDim), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(badDim); err == nil {
		t.Fatal("oversized dimension was accepted")
	}
	base := func() *bytes.Buffer {
		b := bytes.NewBuffer(append([]byte{}, magic[:]...))
		_ = binary.Write(b, binary.LittleEndian, sidecarVersion)
		_ = binary.Write(b, binary.LittleEndian, uint16(1))
		return b
	}
	for name, makeData := range map[string]func() []byte{
		"missing model":      func() []byte { return base().Bytes() },
		"short model":        func() []byte { b := base(); _ = binary.Write(b, binary.LittleEndian, uint32(1)); return b.Bytes() },
		"missing generation": func() []byte { b := base(); _ = writeString(b, "m"); return b.Bytes() },
		"missing count":      func() []byte { b := base(); _ = writeString(b, "m"); _ = writeString(b, "g"); return b.Bytes() },
		"huge count": func() []byte {
			b := base()
			_ = writeString(b, "m")
			_ = writeString(b, "g")
			_ = binary.Write(b, binary.LittleEndian, uint64(10_000_001))
			return b.Bytes()
		},
		"missing covered": func() []byte {
			b := base()
			_ = writeString(b, "m")
			_ = writeString(b, "g")
			_ = binary.Write(b, binary.LittleEndian, uint64(0))
			return b.Bytes()
		},
		"missing vector offset": func() []byte {
			b := base()
			_ = writeString(b, "m")
			_ = writeString(b, "g")
			_ = binary.Write(b, binary.LittleEndian, uint64(1))
			_ = binary.Write(b, binary.LittleEndian, uint64(1))
			return b.Bytes()
		},
		"missing vector key": func() []byte {
			b := base()
			_ = writeString(b, "m")
			_ = writeString(b, "g")
			_ = binary.Write(b, binary.LittleEndian, uint64(1))
			_ = binary.Write(b, binary.LittleEndian, uint64(1))
			_ = binary.Write(b, binary.LittleEndian, int64(2))
			return b.Bytes()
		},
		"missing vector values": func() []byte {
			b := base()
			_ = writeString(b, "m")
			_ = writeString(b, "g")
			_ = binary.Write(b, binary.LittleEndian, uint64(1))
			_ = binary.Write(b, binary.LittleEndian, uint64(1))
			_ = binary.Write(b, binary.LittleEndian, int64(2))
			_ = writeString(b, "k")
			return b.Bytes()
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(dir, name)
			if err := os.WriteFile(Path(p), makeData(), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Read(p); err == nil {
				t.Fatal("truncated sidecar was accepted")
			}
		})
	}
	if err := writeString(failingWriter{}, "x"); err == nil || writeString(&headerWriter{}, "x") == nil {
		t.Fatal("writeString failures were ignored")
	}
	if got, err := Rerank(context.Background(), nil, "q", Sidecar{}, &Client{}); err != nil || got != nil {
		t.Fatalf("empty rerank=%v err=%v", got, err)
	}
	if got, err := Rerank(context.Background(), []search.Hit{{Session: session("a")}}, "q", Sidecar{}, &Client{}); err != nil || len(got) != 1 {
		t.Fatalf("empty sidecar rerank=%v err=%v", got, err)
	}
	if _, err := Rerank(context.Background(), []search.Hit{{Session: session("a")}}, "q", Sidecar{Vectors: []Vector{{Key: "claude:a", Values: []float32{1}}}}, &Client{URL: "http://127.0.0.1:1", HTTP: &http.Client{}}); err == nil {
		t.Fatal("rerank transport error was ignored")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, os.ErrPermission }

type headerWriter struct{ n int }

func (w *headerWriter) Write(p []byte) (int, error) {
	if w.n == 0 {
		w.n++
		return len(p), nil
	}
	return 0, os.ErrPermission
}

func session(id string) model.Session { return model.Session{ID: id, Harness: "claude"} }
