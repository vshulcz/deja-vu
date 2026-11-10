package index

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestSessionStatsSurviveFullBuildAndIncrementalAppend(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	claude := filepath.Join(tmp, "claude")
	proj := filepath.Join(claude, "-w-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir := filepath.Join(tmp, "index.db")
	transcript := filepath.Join(proj, "s1.jsonl")

	head := strings.Join([]string{
		`{"type":"user","sessionId":"s1","isSidechain":true,"agentId":"child1","attributionAgent":"general-purpose","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"check build"}}`,
		`{"type":"assistant","sessionId":"s1","isSidechain":true,"agentId":"child1","attributionAgent":"general-purpose","requestId":"r1","timestamp":"2026-01-02T03:04:06Z","message":{"id":"m1","model":"claude-opus-4-6","role":"assistant","usage":{"input_tokens":100,"output_tokens":30,"cache_read_input_tokens":400,"output_tokens_details":{"thinking_tokens":10}},"content":[{"type":"tool_use","id":"u1","name":"Bash","input":{"command":"go test ./..."}}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(transcript, []byte(head), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	tail := strings.Join([]string{
		`{"type":"user","sessionId":"s1","isSidechain":true,"agentId":"child1","attributionAgent":"general-purpose","timestamp":"2026-01-02T03:04:07Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"u1","is_error":true,"content":"Exit code 1\nFAIL"}]}}`,
		`{"type":"assistant","sessionId":"s1","isSidechain":true,"agentId":"child1","attributionAgent":"general-purpose","requestId":"r2","timestamp":"2026-01-02T03:04:08Z","message":{"id":"m2","model":"claude-opus-4-6","role":"assistant","usage":{"input_tokens":60,"output_tokens":20,"cache_read_input_tokens":200,"output_tokens_details":{"thinking_tokens":5}},"content":[{"type":"tool_use","id":"u2","name":"Edit","input":{"file_path":"/w/app/main.go","old_string":"a","new_string":"b"}},{"type":"text","text":"fixed main.go"}]}}`,
	}, "\n") + "\n"
	f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString(tail); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if err = Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	ss, err := Recent(dir, 10)
	if err != nil || len(ss) != 1 {
		t.Fatalf("Recent = %+v, %v", ss, err)
	}
	got := ss[0]
	if got.ID != "child1" || got.Kind != "sidechain" || got.Parent != "s1" || got.Agent != "general-purpose" {
		t.Errorf("lineage = (%q, %q, %q, %q)", got.ID, got.Kind, got.Parent, got.Agent)
	}
	wantModel := &model.Model{Provider: "anthropic", ID: "claude-opus-4-6"}
	if !reflect.DeepEqual(got.Model, wantModel) {
		t.Errorf("got.Model = %+v, want %+v", got.Model, wantModel)
	}
	wantTokens := &model.Tokens{Input: 160, Output: 50, Reasoning: 15, CacheRead: 600}
	if !reflect.DeepEqual(got.Tokens, wantTokens) {
		t.Errorf("got.Tokens = %+v, want %+v", got.Tokens, wantTokens)
	}
	wantTools := map[string]int{"Bash": 1, "Edit": 1}
	if !reflect.DeepEqual(got.ToolCalls, wantTools) {
		t.Errorf("got.ToolCalls = %+v, want %+v", got.ToolCalls, wantTools)
	}
	if got.ToolErrors != 1 {
		t.Errorf("got.ToolErrors = %d, want 1", got.ToolErrors)
	}

	// Re-delivering already-counted messages (empty newMessages tail) must be
	// idempotent and never double Tokens, ToolCalls, or ToolErrors; and a full
	// rebuild (`Ensure(..., true)`) must match the incremental SessionMeta.
	meta := textOf(got).meta
	extendOwnedRow(&meta, got, transcript, true, false)
	if !reflect.DeepEqual(meta.Tokens, wantTokens) {
		t.Fatalf("duplicate delivery doubled Tokens: got %+v, want %+v", meta.Tokens, wantTokens)
	}
	if !reflect.DeepEqual(meta.ToolCalls, wantTools) {
		t.Fatalf("duplicate delivery doubled ToolCalls: got %+v, want %+v", meta.ToolCalls, wantTools)
	}
	if meta.ToolErrors != 1 {
		t.Fatalf("duplicate delivery doubled ToolErrors: got %d, want 1", meta.ToolErrors)
	}

	if err = Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := Recent(dir, 10)
	if err != nil || len(rebuilt) != 1 {
		t.Fatalf("Recent after full rebuild = %+v, %v", rebuilt, err)
	}
	if !reflect.DeepEqual(rebuilt[0].Tokens, got.Tokens) ||
		!reflect.DeepEqual(rebuilt[0].ToolCalls, got.ToolCalls) ||
		rebuilt[0].ToolErrors != got.ToolErrors ||
		!reflect.DeepEqual(rebuilt[0].Model, got.Model) {
		t.Fatalf("incremental vs full rebuild drift:\nincremental: %+v\nrebuilt:     %+v", got, rebuilt[0])
	}
}
