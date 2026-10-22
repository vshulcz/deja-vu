package embed

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// An e5 model reads "query: " and "passage: " in front of the text; without
// them it still answers, only worse. Both default to empty, so a client that
// sets nothing sends exactly what it always sent.
func TestNewReadsThePrefixes(t *testing.T) {
	t.Setenv("DEJA_EMBED_URL", "http://127.0.0.1:1/v1/embeddings")
	t.Setenv("DEJA_EMBED_QUERY_PREFIX", "query: ")
	t.Setenv("DEJA_EMBED_PASSAGE_PREFIX", "passage: ")
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if c.QueryPrefix != "query: " || c.PassagePrefix != "passage: " {
		t.Fatalf("prefixes = %q, %q", c.QueryPrefix, c.PassagePrefix)
	}
	t.Setenv("DEJA_EMBED_QUERY_PREFIX", "")
	t.Setenv("DEJA_EMBED_PASSAGE_PREFIX", "")
	c, _ = New()
	if c.QueryPrefix != "" || c.PassagePrefix != "" {
		t.Fatalf("unset prefixes must stay empty, got %q, %q", c.QueryPrefix, c.PassagePrefix)
	}
}

func TestQueryCarriesItsPrefixAndIsMemoisedPerPrefix(t *testing.T) {
	var mu sync.Mutex
	var inputs []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []string `json:"input"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		inputs = append(inputs, body.Input...)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"data":[{"embedding":[1,0]}]}`))
	}))
	defer s.Close()
	plain := &Client{URL: s.URL, Model: "m-prefix"}
	e5 := &Client{URL: s.URL, Model: "m-prefix", QueryPrefix: "query: "}
	for _, c := range []*Client{plain, e5, e5, plain} {
		if _, err := c.EmbedQuery(context.Background(), "лимит запросов"); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(inputs) != 2 || inputs[0] != "лимит запросов" || inputs[1] != "query: лимит запросов" {
		t.Fatalf("texts sent = %q, want the bare query once and the prefixed query once", inputs)
	}
}

// The passage prefix changes every vector, so a sidecar made without it must
// not be carried over to a client that adds it.
func TestAChangedPassagePrefixEmbedsEverythingAgain(t *testing.T) {
	dir, _ := embedStore(t)
	var seen sync.Map
	var embedded atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []string `json:"input"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		out := make([][]float32, len(body.Input))
		for i, in := range body.Input {
			embedded.Add(1)
			seen.Store(in, true)
			out[i] = []float32{1, 0}
		}
		payload, _ := json.Marshal(map[string]any{"embeddings": out})
		_, _ = w.Write(payload)
	}))
	defer ts.Close()
	run := func(prefix string) int64 {
		embedded.Store(0)
		c := &Client{URL: ts.URL, Model: "m", PassagePrefix: prefix}
		if _, err := EmbedIndex(dir, c, nil); err != nil {
			t.Fatal(err)
		}
		return embedded.Load()
	}
	first := run("")
	if first == 0 {
		t.Fatal("nothing was embedded")
	}
	if again := run(""); again != 0 {
		t.Fatalf("same settings embedded %d records again", again)
	}
	if got := run("passage: "); got != first {
		t.Fatalf("a new passage prefix embedded %d records, want all %d", got, first)
	}
	if again := run("passage: "); again != 0 {
		t.Fatalf("same prefix embedded %d records again", again)
	}
	prefixed := false
	seen.Range(func(k, _ any) bool {
		text, ok := k.(string)
		if !ok {
			t.Fatalf("seen key %T is not a string", k)
		}
		if strings.HasPrefix(text, "passage: ") {
			prefixed = true
		}
		return true
	})
	if !prefixed {
		t.Fatal("no text went out with the passage prefix")
	}
	// The sidecar still names the plain model for display.
	sc, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sc.Model, "m") {
		t.Fatalf("sidecar model = %q", sc.Model)
	}
}

// A client found by probing the local endpoints is given the prefixes too: the
// same environment that configures an explicit URL configures it.
func TestAProbedClientCarriesThePrefixes(t *testing.T) {
	var mu sync.Mutex
	var inputs []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []string `json:"input"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		inputs = append(inputs, body.Input...)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"data":[{"embedding":[1,0]}]}`))
	}))
	defer ts.Close()
	old := probeURLs
	probeURLs = []string{ts.URL}
	t.Cleanup(func() { probeURLs = old })
	t.Setenv("DEJA_EMBED_URL", "")
	t.Setenv("DEJA_EMBED_OFF", "")
	t.Setenv("DEJA_EMBED_MODEL", "m-probed")
	t.Setenv("DEJA_EMBED_QUERY_PREFIX", "query: ")
	t.Setenv("DEJA_EMBED_PASSAGE_PREFIX", "passage: ")
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if c.URL != ts.URL {
		t.Fatalf("New did not take the probed endpoint: %q", c.URL)
	}
	if c.QueryPrefix != "query: " || c.PassagePrefix != "passage: " {
		t.Fatalf("prefixes of the probed client = %q, %q", c.QueryPrefix, c.PassagePrefix)
	}
	if _, err := c.EmbedQuery(context.Background(), "лимит зондирования"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(inputs) == 0 || inputs[len(inputs)-1] != "query: лимит зондирования" {
		t.Fatalf("texts sent = %q, want the query with its prefix last", inputs)
	}
}
