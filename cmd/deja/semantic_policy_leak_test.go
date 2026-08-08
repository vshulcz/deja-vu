package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
)

// The lexical tier scopes hits by the trust policy, but the semantic fallback
// reaches the whole sidecar. Without the same scoping it handed an imported
// peer's content — withheld from every other read path — straight to the
// caller whenever an embedding endpoint was configured.
func TestSemanticSearchHonoursTheTrustPolicy(t *testing.T) {
	tmp := hermeticEnv(t)

	// An imported session, the way sync import records one.
	batch := filepath.Join(tmp, "batch")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"harness":"claude","session_id":"peer1","project":"proj/p","role":"user","text":"peeronlymarker widget rendering pipeline","time":"2026-05-01T10:00:00+00:00"}` + "\n"
	if err := os.WriteFile(filepath.Join(batch, "deja-sync-aaaa-1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "sync", "import", batch); err != nil {
		t.Fatal(err)
	}

	// A constant-vector endpoint: every query is cosine-1 to the stored record,
	// so a lexical miss always falls through to a semantic match.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"embedding":[1,0]}]}`))
	}))
	defer ts.Close()
	t.Setenv("DEJA_EMBED_URL", ts.URL)
	if err := runEmbed(index.DefaultDir(), nil); err != nil {
		t.Fatal(err)
	}

	// Deny imported for search, then a lexical miss that only the semantic tier
	// can answer. The imported content must not come back.
	writePolicyFile(t, `{"activations":{"search":{"imported":false}}}`)
	out, _ := captureRun(t, "zzzlexicalmiss")
	if strings.Contains(out, "peeronlymarker") {
		t.Fatalf("semantic search leaked imported content under deny:\n%s", out)
	}

	// The MCP recall path (recallTextResult) has its own semantic fallback and
	// must scope it the same way — an agent hitting recall must not receive a
	// withheld imported session either.
	writePolicyFile(t, `{"activations":{"mcp":{"imported":false},"search":{"imported":false}}}`)
	if text, _, _, _, err := recallTextResult(index.DefaultDir(), "zzzlexicalmiss", "", 5, 0, 4096); err != nil {
		t.Fatal(err)
	} else if strings.Contains(text, "peeronlymarker") {
		t.Fatalf("MCP recall leaked imported content via semantic under deny:\n%s", text)
	}

	// Allowed, the same query surfaces it — proving the query does reach the
	// semantic tier and the deny above is what withheld it.
	writePolicyFile(t, `{"activations":{"search":{"imported":true}}}`)
	out, _ = captureRun(t, "zzzlexicalmiss")
	if !strings.Contains(out, "peeronlymarker") {
		t.Fatalf("semantic search did not return imported content when allowed:\n%s", out)
	}
}

func writePolicyFile(t *testing.T, body string) {
	t.Helper()
	p := policy.Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
