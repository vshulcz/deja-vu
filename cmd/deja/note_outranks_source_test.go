package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
)

// noteAndSourceStore is one promoted session, its note, and enough neighbours
// for the lexical scores to spread: the rerank blends a normalised BM25 with
// the cosine, so a store where the note is both the top and the bottom of the
// lexical range cannot express the ordering this is about.
func noteAndSourceStore(t *testing.T) {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "-tmp-proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileMkdir(t, filepath.Join(root, "longs.jsonl"),
		`{"type":"user","sessionId":"longs","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"the goblin pool deadlocks under load"}}`+"\n"+
			`{"type":"assistant","sessionId":"longs","timestamp":"2026-01-01T00:01:00Z","message":{"role":"assistant","content":"we raised the goblin pool size to 32 and the deadlock went away"}}`+"\n")
	writeFileMkdir(t, filepath.Join(root, "other.jsonl"),
		`{"type":"user","sessionId":"other","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"goblin pool notes from an unrelated session"}}`+"\n")
	for _, f := range []string{"f1", "f2", "f3"} {
		writeFileMkdir(t, filepath.Join(root, f+".jsonl"),
			`{"type":"user","sessionId":"`+f+`","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"filler goblin pool goblin pool goblin pool deadlock"}}`+"\n")
	}
	if err := index.Ensure(index.DefaultDir(), "", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := runPromote(index.DefaultDir(), []string{"longs"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	// Again, so the note itself is in the index the search reads.
	if err := index.Ensure(index.DefaultDir(), "", false, nil); err != nil {
		t.Fatal(err)
	}
}

// embedEndpoint stands in for the embedding service and builds the sidecar from
// it. Each record is given a vector by what it says, so a test can decide what
// the machine thinks the query is near: a promoted note's text is the one that
// carries the state marker, which is how it is told apart from the transcript
// it quotes. The query goes through here too, and is the vector the records are
// measured against.
func embedEndpoint(t *testing.T, vector func(text string) string) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		var b strings.Builder
		b.WriteString(`{"data":[`)
		for i, in := range req.Input {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(`{"embedding":[` + vector(in) + `]}`)
		}
		b.WriteString(`]}`)
		_, _ = w.Write([]byte(b.String()))
	}))
	t.Cleanup(ts.Close)
	t.Setenv("DEJA_EMBED_URL", ts.URL)
	if err := runEmbed(index.DefaultDir(), nil); err != nil {
		t.Fatal(err)
	}
}

func hitOrder(hits []search.Hit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Session.Harness+":"+h.Session.ID)
	}
	return out
}

// placeOf is where a session sits in the answer, or -1 when it is not in it.
func placeOf(hits []search.Hit, key string) int {
	for i, h := range hits {
		if h.Session.Harness+":"+h.Session.ID == key {
			return i
		}
	}
	return -1
}

const (
	sourceKey = "claude:longs"
	noteKey   = "deja:deja-note-claude-longs"
)

// `deja promote` tells the reader the note now outranks the raw transcript.
// The ranking arranges that, and then the semantic rerank — which knows nothing
// about the two being a distillation and its source — put the transcript back
// on top: the rerank is half normalised BM25 and half cosine, so the transcript
// takes its own note back whenever the cosine gap beats the lexical one.
// Measured on a real store: four queries, the note below its own session in
// every one (#2083).
func TestARerankKeepsTheNoteAboveItsSource(t *testing.T) {
	noteAndSourceStore(t)
	// The transcript is what the query is nearest, its note is not, and the
	// unrelated session sits between them. That is the machine the issue
	// reports.
	embedEndpoint(t, func(text string) string {
		switch {
		case strings.Contains(text, "[accepted]"):
			return "0,1"
		case strings.Contains(text, "unrelated"):
			return "0.6,0.8"
		case strings.Contains(text, "filler"):
			return "0,1"
		default:
			return "1,0"
		}
	})

	o := search.Options{Query: "goblin pool", Limit: 50}
	result, err := index.SearchWithRecoveryDetailed(index.DefaultDir(), o, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	hits, err := search.Run(search.WithoutIgnored(result.Sessions), o)
	if err != nil {
		t.Fatal(err)
	}
	if placeOf(hits, noteKey) > placeOf(hits, sourceKey) {
		t.Fatalf("the ranking itself did not put the note first: %v", hitOrder(hits))
	}
	ranked := maybeRerank(index.DefaultDir(), hits, o, os.Stderr)

	// The rerank has to have changed something, or this passes on a machine
	// where no reordering happened at all.
	if placeOf(ranked, sourceKey) > placeOf(ranked, "claude:other") {
		t.Fatalf("the rerank did not run: the transcript is still below the unrelated session: %v", hitOrder(ranked))
	}
	note, source := placeOf(ranked, noteKey), placeOf(ranked, sourceKey)
	if note < 0 {
		t.Fatalf("the note is not in the answer at all: %v", hitOrder(ranked))
	}
	if note > source {
		t.Errorf("the rerank put the transcript above its own note: %v", hitOrder(ranked))
	}
}

// The semantic fallback is a ranking of its own — nothing matched lexically, so
// the whole answer is the sidecar's order — and the same rule applies to it.
func TestTheSemanticFallbackKeepsTheNoteAboveItsSource(t *testing.T) {
	noteAndSourceStore(t)
	// The note and its source are both over the floor the fallback holds
	// results to, so both are in the answer and the only question is which of
	// them is first; the neighbours are under it.
	embedEndpoint(t, func(text string) string {
		switch {
		case strings.Contains(text, "[accepted]"):
			return "0.6,0.8"
		case strings.Contains(text, "unrelated"), strings.Contains(text, "filler"):
			return "0,1"
		case strings.Contains(text, "deadlock"):
			return "0.95,0.3122"
		default:
			return "1,0"
		}
	})

	o := search.Options{Query: "zebrafish telemetry", Limit: 50}
	result, err := index.SearchWithRecoveryDetailed(index.DefaultDir(), o, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	hits, err := search.Run(search.WithoutIgnored(result.Sessions), o)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("the words were meant to match nothing, so that the fallback is what answers: %v", hitOrder(hits))
	}
	semantic, used := maybeSemantic(index.DefaultDir(), hits, o, os.Stderr)
	if !used {
		t.Fatalf("the semantic fallback did not answer: %v", hitOrder(semantic))
	}
	note, source := placeOf(semantic, noteKey), placeOf(semantic, sourceKey)
	if note < 0 || source < 0 {
		t.Fatalf("both the note and its source should be in the answer: %v", hitOrder(semantic))
	}
	if note > source {
		t.Errorf("the semantic fallback put the transcript above its own note: %v", hitOrder(semantic))
	}
}
