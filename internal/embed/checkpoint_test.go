package embed

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// A run killed partway through used to lose every vector it had computed: the
// sidecar was written once, at the end (#4568). With a checkpoint after each
// request here, the next run embeds only what the first never reached.
func TestAnInterruptedEmbedKeepsWhatItEmbedded(t *testing.T) {
	dir, _ := embedStore(t)
	defer func(b, c int) { embedBatch, checkpointEvery = b, c }(embedBatch, checkpointEvery)
	embedBatch, checkpointEvery = 2, 1

	var calls atomic.Int64
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) > 2 {
			http.Error(w, "killed", http.StatusInternalServerError)
			return
		}
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
	defer failing.Close()
	if _, err := EmbedIndex(dir, &Client{URL: failing.URL, Model: "test"}, nil); err == nil {
		t.Fatal("the third request failed, so the run should have too")
	}
	partial, err := Read(dir)
	if err != nil {
		t.Fatalf("no sidecar after an interrupted run: %v", err)
	}
	if len(partial.Vectors) != 4 || partial.Covered != 4 {
		t.Fatalf("interrupted sidecar holds %d vectors, covered %d; want 4 and 4", len(partial.Vectors), partial.Covered)
	}

	var embedded atomic.Int64
	done, err := EmbedIndex(dir, countingClient(t, &embedded), nil)
	if err != nil {
		t.Fatal(err)
	}
	if embedded.Load() != 1 || len(done.Vectors) != 5 {
		t.Fatalf("the second run embedded %d texts into %d vectors; want 1 into 5", embedded.Load(), len(done.Vectors))
	}
}

func TestEmbedBatchEnvOverride(t *testing.T) {
	dir, _ := embedStore(t)
	t.Setenv("DEJA_EMBED_BATCH", "2")

	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var body struct {
			Input []string `json:"input"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		if len(body.Input) > 2 {
			t.Errorf("request batch size = %d, want <= 2", len(body.Input))
		}
		out := make([][]float32, len(body.Input))
		for i := range out {
			out[i] = []float32{1, 0}
		}
		payload, _ := json.Marshal(map[string]any{"embeddings": out})
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	done, err := EmbedIndex(dir, &Client{URL: srv.URL, Model: "test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(done.Vectors) != 5 || requests.Load() != 3 {
		t.Fatalf("embedded %d vectors in %d requests; want 5 vectors in 3 requests (2+2+1)", len(done.Vectors), requests.Load())
	}
}
