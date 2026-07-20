package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/bench"
	"github.com/vshulcz/deja-vu/internal/embed"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/stats"
)

func TestEmbedCommandBuildsSemanticSidecar(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "-tmp-semantic")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileMkdir(t, filepath.Join(root, "s.jsonl"), `{"type":"user","sessionId":"s","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"semantic command"}}`+"\n")
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Input) != 1 {
			t.Errorf("embed request=%#v err=%v", req, err)
		}
		_, _ = w.Write([]byte(`{"data":[{"embedding":[1,0]}]}`))
	}))
	defer ts.Close()
	t.Setenv("DEJA_EMBED_URL", ts.URL)
	if err := runEmbed(index.DefaultDir(), []string{"--bad"}); err == nil {
		t.Fatal("unknown embed flag should fail")
	}
	if err := runEmbed(index.DefaultDir(), nil); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("embedding calls=%d, want one", calls)
	}
	s, err := embed.Read(indexDirForTest())
	if err != nil || s.Covered != 1 || len(s.Vectors) != 1 || s.Vectors[0].Values[0] != 1 {
		t.Fatalf("sidecar=%+v err=%v", s, err)
	}
	report := collectDoctorEmbed(index.DefaultDir())
	if report == nil || report.State != "reachable" || report.Dim != 2 || report.Coverage != 100 {
		t.Fatalf("doctor embedding report=%#v", report)
	}
	var notice bytes.Buffer
	semanticHits, semantic := maybeSemantic(index.DefaultDir(), nil, search.Options{Query: "rephrased"}, os.Stderr)
	if !semantic || len(semanticHits) != 1 || semanticHits[0].Count != 0 {
		t.Fatalf("semantic fallback hits=%#v semantic=%v", semanticHits, semantic)
	}
	var jsonOut bytes.Buffer
	search.Print(&jsonOut, semanticHits, search.Options{JSON: true, Semantic: true})
	if !strings.Contains(jsonOut.String(), `"semantic":true`) {
		t.Fatalf("semantic JSON=%q", jsonOut.String())
	}
	metric, err := measureSemanticOnlyRephrased(indexDirForTest(), []bench.Query{{Text: "ignored"}, {Text: "rephrased", Relevant: []string{"s"}}}, &embed.Client{URL: ts.URL})
	if err != nil || metric != 1 {
		t.Fatalf("semantic-only metric=%v err=%v", metric, err)
	}
	if metric, err := measureSemanticOnlyRephrased(indexDirForTest(), nil, &embed.Client{URL: ts.URL}); err != nil || metric != 0 {
		t.Fatalf("empty semantic-only metric=%v err=%v", metric, err)
	}
	_ = notice
	hits := []search.Hit{{Session: model.Session{ID: "s", Harness: "claude"}, Score: 1}}
	if got := maybeRerank(index.DefaultDir(), hits, search.Options{Query: "semantic"}, os.Stderr); len(got) != 1 || got[0].Score != 1 {
		t.Fatalf("semantic rerank=%#v", got)
	}
	t.Setenv("DEJA_EMBED_URL", "http://127.0.0.1:1")
	if got := maybeRerank(index.DefaultDir(), hits, search.Options{Query: "semantic"}, os.Stderr); len(got) != 1 || got[0].Score != 1 {
		t.Fatalf("failed semantic rerank=%#v", got)
	}
	if err := os.WriteFile(filepath.Join(root, "new.jsonl"), []byte(`{"type":"user","sessionId":"new","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"new semantic"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := maybeRerank(index.DefaultDir(), hits, search.Options{Query: "semantic"}, os.Stderr); len(got) != 1 || got[0].Score != 1 {
		t.Fatalf("stale semantic rerank=%#v", got)
	}
}

func TestStatsJSONOutputIncludesSemanticSize(t *testing.T) {
	tmp := hermeticEnv(t)
	if err := os.MkdirAll(indexDirForTest(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(embed.Path(indexDirForTest()), []byte("sidecar"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	err = runStats(index.DefaultDir(), []string{"--json"})
	_ = w.Close()
	os.Stdout = old
	var out bytes.Buffer
	_, _ = out.ReadFrom(r)
	_ = r.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"sidecar_size": 7`) {
		t.Fatalf("stats JSON=%s", out.String())
	}
	if err := runStats(index.DefaultDir(), []string{"--json", "--card"}); err == nil {
		t.Fatal("stats output modes should be exclusive")
	}
	card := filepath.Join(tmp, "stats.svg")
	if err := runStats(index.DefaultDir(), []string{"--card", card, "--harness", "claude", "--project", "none", "--since", "1h", "--role", "user"}); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(card); err != nil || !strings.Contains(string(b), "<svg") {
		t.Fatalf("stats card err=%v content=%q", err, b)
	}
	_ = tmp
}

func TestMaybeRerankFallsBackWithoutSidecar(t *testing.T) {
	hermeticEnv(t)
	hits := []search.Hit{{Session: model.Session{ID: "s", Harness: "claude"}, Score: 1}}
	var notice bytes.Buffer
	if got := maybeRerank(index.DefaultDir(), hits, search.Options{Query: "q"}, os.Stderr); len(got) != 1 || got[0].Score != 1 {
		t.Fatalf("missing sidecar rerank=%#v", got)
	}
	_ = notice
}

func TestDoctorEmbedAndStatsFilters(t *testing.T) {
	var out bytes.Buffer
	doctorEmbed(&out, doctorEmbedReport{State: "unavailable"})
	doctorEmbed(&out, doctorEmbedReport{State: "reachable", Model: "m", Dim: 2, Coverage: 50})
	if !strings.Contains(out.String(), "endpoint   unavailable") || !strings.Contains(out.String(), "coverage=50.0%") {
		t.Fatalf("embedding report=%q", out.String())
	}
	now := time.Now()
	ss := []model.Session{
		{Harness: "claude", Project: "Alpha", Updated: now, Messages: []model.Message{{Role: "user"}}},
		{Harness: "codex", Project: "Beta", Updated: now.Add(-time.Hour), Messages: []model.Message{{Role: "assistant"}}},
	}
	got := stats.Filter(ss, search.Options{Harness: "claude", Project: "alp", Since: time.Minute, Role: "user"})
	if len(got) != 1 || len(got[0].Messages) != 1 || got[0].Messages[0].Role != "user" {
		t.Fatalf("filtered stats=%#v", got)
	}
	if got := stats.Filter(ss, search.Options{Role: "system"}); len(got) != 0 {
		t.Fatalf("role filter retained sessions=%#v", got)
	}
	for _, args := range [][]string{{"--since"}, {"--since", "bad"}, {"--card", "--card"}} {
		if err := runStats(index.DefaultDir(), args); err == nil {
			t.Fatalf("runStats(index.DefaultDir(), %v) accepted invalid arguments", args)
		}
	}
}

func indexDirForTest() string { return os.Getenv("DEJA_INDEX_DIR") }
