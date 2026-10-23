package embed

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// #5053: records are redacted before they are embedded, the query was not, so
// a token pasted into a search went to the endpoint verbatim. A recall ranks
// the same query twice and sent it twice.
func TestQueryIsRedactedAndSentOnce(t *testing.T) {
	t.Setenv("DEJA_NO_REDACT", "")
	var mu sync.Mutex
	var bodies []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		_, _ = w.Write([]byte(`{"data":[{"embedding":[1,0]}]}`))
	}))
	defer s.Close()
	token := "ghp_" + strings.Repeat("a1B2", 9)
	query := "pool exhausted secret " + token
	hits := []search.Hit{{Session: session("a"), Score: 10}, {Session: session("b"), Score: 0}}
	sidecar := Sidecar{Vectors: []Vector{{Key: "claude:a", Values: []float32{1, 0}}, {Key: "claude:b", Values: []float32{0, 1}}}}
	client := &Client{URL: s.URL, Model: "m"}
	for range 2 {
		if _, err := Rerank(context.Background(), hits, query, sidecar, client); err != nil {
			t.Fatal(err)
		}
	}
	// A second client for the same endpoint, as each search builds its own.
	if _, err := Rerank(context.Background(), hits, query, sidecar, &Client{URL: s.URL, Model: "m"}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("the same query went out %d times, want once", len(bodies))
	}
	if strings.Contains(bodies[0], token) {
		t.Fatalf("the token reached the endpoint: %s", bodies[0])
	}
	if !strings.Contains(bodies[0], "pool exhausted") {
		t.Fatalf("the query itself is missing: %s", bodies[0])
	}
}
