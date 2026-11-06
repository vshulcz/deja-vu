package embed

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

func TestSemanticFloorEnv(t *testing.T) {
	for _, c := range []struct {
		env  string
		want float64
	}{
		{"", defaultSemanticFloor},
		{"0.3", 0.3},
		{" 0.42 ", 0.42},
		{"0", 0},
		{"-1", -1},
		{"1", 1},
		{"abc", defaultSemanticFloor},
		{"1.5", defaultSemanticFloor},
		{"-2", defaultSemanticFloor},
		{"NaN", defaultSemanticFloor},
		{"Inf", defaultSemanticFloor},
	} {
		t.Setenv("DEJA_EMBED_FLOOR", c.env)
		if got := semanticFloor(); got != c.want {
			t.Errorf("DEJA_EMBED_FLOOR=%q: got %v, want %v", c.env, got, c.want)
		}
	}
}

// The floor has to reach the search, not only the helper: a vector at a cosine
// of 0.45 stays out at the default and is served once the floor is below it.
func TestDejaEmbedFloorDecidesWhatIsServed(t *testing.T) {
	dir, _ := embedStore(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []string `json:"input"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		out := make([][]float32, len(body.Input))
		for i := range out {
			out[i] = []float32{0.45, 0.893029}
		}
		payload, _ := json.Marshal(map[string]any{"embeddings": out})
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	sidecar, err := EmbedIndex(dir, &Client{URL: srv.URL, Model: "test"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	hitsAt := func(floor string) []search.Hit {
		t.Helper()
		t.Setenv("DEJA_EMBED_FLOOR", floor)
		hits, err := semanticHitsForQuery(dir, search.Options{Query: "anything"}, sidecar, []float32{1, 0})
		if err != nil {
			t.Fatal(err)
		}
		return hits
	}
	if got := hitsAt(""); len(got) != 0 {
		t.Fatalf("default floor served %d hits at cosine 0.45, want 0", len(got))
	}
	if got := hitsAt("0.40"); len(got) == 0 {
		t.Fatal("DEJA_EMBED_FLOOR=0.40 served nothing at cosine 0.45")
	}
}
