package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

func TestBlameErrorAndEmptyBranches(t *testing.T) {
	tmp := hermeticEnv(t)
	// Index dir squatted by a file: ensure/search must surface the error.
	squat := filepath.Join(tmp, "squatted")
	if err := os.WriteFile(squat, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(squat, "index.db"))
	if err := runBlame([]string{"parser.go"}); err == nil {
		t.Fatal("expected blame error for squatted index dir")
	}
	if _, err := blameTextResult(search.BlameOptions{}, "parser.go", 5); err == nil {
		t.Fatal("expected MCP blame error for squatted index dir")
	}
	// Healthy empty index: the no-mentions message path.
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "empty-index"))
	out, err := captureRun(t, "blame", "never-mentioned.go")
	if err != nil || out != "" {
		t.Fatalf("blame on empty index: %v (out=%q)", err, out)
	}
	if s, err := blameTextResult(search.BlameOptions{}, "never-mentioned.go", 5); err != nil || s == "" {
		t.Fatalf("mcp empty = %q err=%v", s, err)
	}
}

func TestEmbedCommandBranches(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "p", "s.jsonl"), "s", []string{
		`{"type":"user","sessionId":"s","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"embed me"}}`,
	})
	if err := runEmbed([]string{"--bogus"}); err == nil {
		t.Fatal("expected unknown flag error")
	}
	// Dead endpoint with real records: the embed call must surface the error.
	t.Setenv("DEJA_EMBED_URL", "http://127.0.0.1:1/api/embed")
	if err := runEmbed(nil); err == nil {
		t.Fatal("expected embed endpoint error")
	}
	// Squatted index dir: Ensure error path.
	squat := filepath.Join(tmp, "sq")
	if err := os.WriteFile(squat, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(squat, "i"))
	if err := runEmbed(nil); err == nil {
		t.Fatal("expected ensure error")
	}
}

func TestMaybeSemanticGuards(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "idx"))
	hit := []search.Hit{{}}
	// Existing hits pass through untouched.
	if out, used := maybeSemantic(hit, search.Options{}, os.Stderr); used || len(out) != 1 {
		t.Fatal("hits must pass through")
	}
	// NoEmbed and env opt-outs.
	if _, used := maybeSemantic(nil, search.Options{NoEmbed: true}, os.Stderr); used {
		t.Fatal("NoEmbed must skip")
	}
	t.Setenv("DEJA_EMBED", "off")
	if _, used := maybeSemantic(nil, search.Options{}, os.Stderr); used {
		t.Fatal("env off must skip")
	}
	t.Setenv("DEJA_EMBED", "")
	// No sidecar at all.
	if _, used := maybeSemantic(nil, search.Options{}, os.Stderr); used {
		t.Fatal("missing sidecar must skip")
	}
}

func TestMaybeSemanticSidecarBranches(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "idx"))
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "p", "s1.jsonl"), "s1", []string{
		`{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"connection pool exhausted"}}`,
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input any `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		n := 1
		if list, ok := req.Input.([]any); ok {
			n = len(list)
		}
		vecs := make([]string, n)
		for i := range vecs {
			vecs[i] = "[1,0]"
		}
		_, _ = fmt.Fprintf(w, `{"embeddings":[%s]}`, strings.Join(vecs, ","))
	}))
	defer srv.Close()
	t.Setenv("DEJA_EMBED_URL", srv.URL+"/api/embed")
	if err := runEmbed(nil); err != nil {
		t.Fatal(err)
	}
	// Sidecar current: fallback fires.
	out, used := maybeSemantic(nil, search.Options{Query: "totally different words"}, os.Stderr)
	if !used || len(out) == 0 {
		t.Fatalf("semantic fallback did not fire: used=%v out=%d", used, len(out))
	}
	// Dead endpoint with a valid sidecar: query embed fails, silent skip.
	t.Setenv("DEJA_EMBED_URL", "http://127.0.0.1:1/api/embed")
	if _, used := maybeSemantic(nil, search.Options{Query: "anything"}, os.Stderr); used {
		t.Fatal("dead endpoint must skip")
	}
	t.Setenv("DEJA_EMBED_URL", srv.URL+"/api/embed")
	// Generation moves on re-index: stale sidecar skips.
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "p", "s2.jsonl"), "s2", []string{
		`{"type":"user","sessionId":"s2","timestamp":"2026-01-03T03:04:05Z","message":{"role":"user","content":"another session"}}`,
	})
	if err := run([]string{"index", "--rebuild"}); err != nil {
		t.Fatal(err)
	}
	if _, used := maybeSemantic(nil, search.Options{Query: "anything"}, os.Stderr); used {
		t.Fatal("stale sidecar generation must skip")
	}
}
