package embed

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Ollama refuses a whole batch over one empty input, so a record cleaning left
// empty stopped `deja embed` half an hour in (#4975). It is not sent.
func TestARecordWithNoTextIsNotSent(t *testing.T) {
	recs := []index.OffsetRecord{
		{Offset: 0, Record: index.Record{Key: "claude:a", Text: "the pool is not closed"}},
		{Offset: 10, Record: index.Record{Key: "claude:a", Text: ""}},
		{Offset: 20, Record: index.Record{Key: "claude:a", Text: " \n\t"}},
		{Offset: 30, Record: index.Record{Key: "claude:a", Text: "already embedded"}},
		{Offset: 40, Record: index.Record{Key: "claude:a", Text: "raise the timeout"}},
		{Offset: 50, Record: index.Record{Key: "claude:a", Text: "raise the timeout"}},
	}
	got := make([]int64, 0, len(recs))
	covered := map[uint64]bool{RecordIdent(recs[3].Record): true}
	for _, r := range toEmbed(recs, covered) {
		got = append(got, r.Offset)
	}
	if len(got) != 2 || got[0] != 0 || got[1] != 40 {
		t.Fatalf("records to embed = %v, want [0 40]", got)
	}
}

// The status alone sent people hunting; the endpoint's own words say why.
func TestAnEndpointErrorCarriesItsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"empty content"}` + strings.Repeat(" pad", 400)))
	}))
	defer srv.Close()
	_, err := (&Client{URL: srv.URL, Model: "test"}).Embed(context.Background(), []string{"a"})
	if err == nil || !strings.Contains(err.Error(), "400 Bad Request") || !strings.Contains(err.Error(), "empty content") {
		t.Fatalf("err = %v, want the status and the body's reason", err)
	}
	if len(err.Error()) > 700 {
		t.Errorf("the error carries %d bytes of body; it is capped at 512", len(err.Error()))
	}
}

// A failure between checkpoints kept nothing of what the run had embedded since
// the last one: 182 vectors past the 35th checkpoint in #4975. The vectors are
// written before the error is returned.
func TestAFailedRequestKeepsWhatTheRunEmbedded(t *testing.T) {
	dir, _ := embedStore(t)
	defer func(b, c int) { embedBatch, checkpointEvery = b, c }(embedBatch, checkpointEvery)
	embedBatch, checkpointEvery = 2, 50

	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) > 2 {
			http.Error(w, `{"error":"empty content"}`, http.StatusBadRequest)
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
	defer srv.Close()
	if _, err := EmbedIndex(dir, &Client{URL: srv.URL, Model: "test"}, nil); err == nil {
		t.Fatal("the third request failed, so the run should have too")
	}
	kept, err := Read(dir)
	if err != nil {
		t.Fatalf("no sidecar after the failed run: %v", err)
	}
	if len(kept.Vectors) != 4 || kept.Covered != 4 {
		t.Fatalf("the failed run kept %d vectors, covered %d; want 4 and 4", len(kept.Vectors), kept.Covered)
	}
}
