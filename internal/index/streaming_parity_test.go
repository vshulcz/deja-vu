package index

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func metaByKey(t *testing.T, dir string) map[string]SessionMeta {
	t.Helper()
	rows, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := make(map[string]SessionMeta, len(rows))
	for _, r := range rows {
		m[r.Harness+":"+r.ID] = r
	}
	return m
}

func TestStreamingByteChunkedEnsureMatchesColdRebuild(t *testing.T) {
	tmp := parityEnv(t, map[string]string{
		"DEJA_CLAUDE_ROOT":      "claude",
		"DEJA_CODEX_ROOT":       "codex",
		"DEJA_ANTIGRAVITY_ROOT": "agy",
	})
	indexDir := filepath.Join(tmp, "idx")

	// Seed an initial unrelated session so Pass 1 of cl-stream-1 and cx-stream-1
	// runs through the incremental append path too, matching live hook passes.
	parityWrite(t, filepath.Join(tmp, "claude", "-work-app", "cl-anchor.jsonl"),
		`{"type":"user","sessionId":"cl-anchor","cwd":"/work/app","timestamp":"2026-10-01T06:00:00Z","message":{"role":"user","content":"anchor session"}}`+"\n"+
			`{"type":"assistant","sessionId":"cl-anchor","requestId":"req_anchor","timestamp":"2026-10-01T06:00:01Z","message":{"id":"msg_anchor","role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"anchor ready"}]}}`+"\n",
		false,
	)
	parityPass(t, indexDir, false)

	// 1. Claude: preamble-only initial flush (queue-operation + bridge_status + forkedFrom),
	// clean first turn (exercising ingest_append preamble re-read), then torn UTF-8 cut,
	// multi-block req_01 with CRLF, failed Bash command, and trailing newline split.
	clPath := filepath.Join(tmp, "claude", "-work-app", "cl-stream-1.jsonl")
	parityWrite(t, clPath,
		`{"type":"queue-operation","operation":"dequeue","timestamp":"2026-10-01T09:00:00Z","sessionId":"cl-stream-1"}`+"\n"+
			`{"type":"system","subtype":"bridge_status","url":"https://claude.ai/code/session_01streamtest","sessionId":"cl-stream-1"}`+"\n"+
			`{"type":"system","sessionId":"cl-stream-1","forkedFrom":{"sessionId":"cl-parent-0"}}`+"\n",
		false,
	)
	parityPass(t, indexDir, false)

	parityWrite(t, clPath,
		`{"type":"user","sessionId":"cl-stream-1","cwd":"/work/app","timestamp":"2026-10-01T09:00:01Z","message":{"role":"user","content":"Fix parser 日本語 🦊"}}`+"\n",
		true,
	)
	parityPass(t, indexDir, false)
	if clPre := metaByKey(t, indexDir)["claude:cl-stream-1"]; clPre.RemoteID != "session_01streamtest" || clPre.Kind != "fork" || clPre.Parent != "cl-parent-0" {
		t.Errorf("Pass 2 claude:cl-stream-1 lost preamble metadata: remote=%q kind=%q parent=%q", clPre.RemoteID, clPre.Kind, clPre.Parent)
	}

	clReq1Line1 := `{"type":"assistant","sessionId":"cl-stream-1","requestId":"req_01","timestamp":"2026-10-01T09:00:02Z","message":{"id":"msg_01","role":"assistant","model":"claude-sonnet-4-6","usage":{"input_tokens":1500,"output_tokens":40,"cache_read_input_tokens":300,"output_tokens_details":{"thinking_tokens":50}},"content":[{"type":"thinking","thinking":"Inspecting 日本語 🦊 UTF-8 path"}]}}` + "\n"
	clCut := strings.Index(clReq1Line1, "🦊") + 2
	parityWrite(t, clPath, clReq1Line1[:clCut], true)
	parityPass(t, indexDir, false)

	clReq1Line2CRLF := `{"type":"assistant","sessionId":"cl-stream-1","requestId":"req_01","timestamp":"2026-10-01T09:00:03Z","message":{"id":"msg_01","role":"assistant","model":"claude-sonnet-4-6","usage":{"input_tokens":1500,"output_tokens":110,"cache_read_input_tokens":300,"output_tokens_details":{"thinking_tokens":50}},"content":[{"type":"text","text":"Checking files"}]}}` + "\r\n"
	clReq1Line3 := `{"type":"assistant","sessionId":"cl-stream-1","requestId":"req_01","timestamp":"2026-10-01T09:00:04Z","message":{"id":"msg_01","role":"assistant","model":"claude-sonnet-4-6","usage":{"input_tokens":1500,"output_tokens":180,"cache_read_input_tokens":300,"output_tokens_details":{"thinking_tokens":50}},"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./..."}}]}}` + "\n"
	parityWrite(t, clPath, clReq1Line1[clCut:]+clReq1Line2CRLF+clReq1Line3, true)
	parityPass(t, indexDir, false)

	parityWrite(t, clPath,
		`{"type":"user","sessionId":"cl-stream-1","cwd":"/work/app","timestamp":"2026-10-01T09:00:05Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"Exit code 1\nFAIL\texample/pkg\t0.12s"}]}}`+"\n"+
			`{"type":"assistant","sessionId":"cl-stream-1","requestId":"req_02","timestamp":"2026-10-01T09:00:06Z","message":{"id":"msg_02","role":"assistant","model":"claude-sonnet-4-6","usage":{"input_tokens":1700,"output_tokens":60},"content":[{"type":"text","text":"Fixed failing test"}]}}`,
		true,
	)
	parityPass(t, indexDir, false)
	parityWrite(t, clPath, "\n", true)
	parityPass(t, indexDir, false)

	// 2. Codex: BOM + session_meta + turn_context preamble-only initial flush,
	// clean first turn (verifies turn_context model is kept before any torn line),
	// then torn UTF-8 user line, duplicate token_count, and relative apply_patch.
	cxID := "019fbcca-3333-7000-8000-000000000001"
	cxPath := filepath.Join(tmp, "codex", "sessions", "2026", "10", "01", "rollout-2026-10-01T08-00-00-"+cxID+".jsonl")
	parityWrite(t, cxPath,
		"\xef\xbb\xbf"+
			fmt.Sprintf(`{"timestamp":"2026-10-01T08:00:00Z","type":"session_meta","payload":{"id":%q,"cwd":"/work/app","model_provider":"custom-gw"}}`, cxID)+"\n"+
			`{"timestamp":"2026-10-01T08:00:00Z","type":"turn_context","payload":{"cwd":"/work/app","model":"gpt-5-codex"}}`+"\n",
		false,
	)
	parityPass(t, indexDir, false)

	parityWrite(t, cxPath,
		`{"timestamp":"2026-10-01T08:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":"Run suite Привет 🦊"}}`+"\n"+
			fmt.Sprintf(`{"timestamp":"2026-10-01T08:00:02Z","type":"response_item","payload":{"type":"custom_tool_call","name":"exec","call_id":"c1","input":%q}}`, `exec_command({cmd: "go test ./..."})`)+"\n",
		true,
	)
	parityPass(t, indexDir, false)
	if cxPre := metaByKey(t, indexDir)["codex:"+cxID]; cxPre.Model == nil || cxPre.Model.Provider != "custom-gw" || cxPre.Model.ID != "gpt-5-codex" {
		t.Errorf("Pass 2 codex:%s lost preamble Model: %+v", cxID, cxPre.Model)
	}

	cxUser2 := `{"timestamp":"2026-10-01T08:00:03Z","type":"response_item","payload":{"type":"message","role":"user","content":"Check тест 🦊"}}` + "\n"
	cxCut := strings.Index(cxUser2, "🦊") + 2
	parityWrite(t, cxPath, cxUser2[:cxCut], true)
	parityPass(t, indexDir, false)

	parityWrite(t, cxPath,
		cxUser2[cxCut:]+
			`{"timestamp":"2026-10-01T08:00:04Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":1000},"last_token_usage":{"input_tokens":900,"cached_input_tokens":200,"output_tokens":80,"reasoning_output_tokens":20}}}}`+"\n"+
			`{"timestamp":"2026-10-01T08:00:05Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"c1","output":"ok\nProcess exited with code 0"}}`+"\n"+
			`{"timestamp":"2026-10-01T08:00:06Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":1000},"last_token_usage":{"input_tokens":900,"cached_input_tokens":200,"output_tokens":80,"reasoning_output_tokens":20}}}}`+"\n"+
			fmt.Sprintf(`{"timestamp":"2026-10-01T08:00:07Z","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","call_id":"c2","input":%q}}`, "*** Begin Patch\n*** Add File: sub/x.go\n+package sub\n*** End Patch")+"\n"+
			`{"timestamp":"2026-10-01T08:00:08Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"c2","output":"Success."}}`+"\n"+
			`{"timestamp":"2026-10-01T08:00:09Z","type":"response_item","payload":{"type":"message","role":"assistant","content":"All green."}}`+"\n",
		true,
	)
	parityPass(t, indexDir, false)

	// 3. Antigravity: EPHEMERAL_MESSAGE preamble-only initial flush, torn VIEW_FILE line,
	// and workspace -> repo-b project convergence across offset.
	repoB := filepath.Join(tmp, "work", "repo-b")
	if err := os.MkdirAll(filepath.Join(repoB, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.go", "b.go", "c.go"} {
		parityWrite(t, filepath.Join(repoB, name), "package b\n", false)
	}
	wsA := filepath.Join(tmp, "work", "ws-a")
	parityWrite(t, filepath.Join(tmp, "agy", "cache", "conversation_metadata.json"),
		fmt.Sprintf(`{"conversations":{"ag-stream-1":{"summary":{"WorkspaceURIs":["file://%s"]}}}}`, wsA),
		false,
	)
	agPath := filepath.Join(tmp, "agy", "brain", "ag-stream-1", ".system_generated", "logs", "transcript.jsonl")
	parityWrite(t, agPath,
		`{"step_index":0,"source":"SYSTEM","type":"EPHEMERAL_MESSAGE","status":"DONE","created_at":"2026-10-01T07:00:00Z","content":"init"}`+"\n",
		false,
	)
	parityPass(t, indexDir, false)

	parityWrite(t, agPath,
		`{"step_index":1,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-10-01T07:00:01Z","content":"Inspect repo-b"}`+"\n"+
			`{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-10-01T07:00:02Z","content":"Reading files"}`+"\n",
		true,
	)
	parityPass(t, indexDir, false)

	agView1 := fmt.Sprintf(`{"step_index":3,"source":"MODEL","type":"VIEW_FILE","status":"DONE","created_at":"2026-10-01T07:00:03Z","content":"File Path: file://%s\npackage b 🦊"}`, filepath.Join(repoB, "a.go")) + "\n"
	agCut := strings.Index(agView1, "🦊") + 2
	parityWrite(t, agPath, agView1[:agCut], true)
	parityPass(t, indexDir, false)

	parityWrite(t, agPath,
		agView1[agCut:]+
			fmt.Sprintf(`{"step_index":4,"source":"MODEL","type":"VIEW_FILE","status":"DONE","created_at":"2026-10-01T07:00:04Z","content":"File Path: file://%s\npackage b"}`, filepath.Join(repoB, "b.go"))+"\n"+
			fmt.Sprintf(`{"step_index":5,"source":"MODEL","type":"VIEW_FILE","status":"DONE","created_at":"2026-10-01T07:00:05Z","content":"File Path: file://%s\npackage b"}`, filepath.Join(repoB, "c.go"))+"\n"+
			`{"step_index":6,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-10-01T07:00:06Z","content":"Done"}`+"\n",
		true,
	)
	parityPass(t, indexDir, false)

	// 4. Split batch (prepareSplit): one file rewritten (rest) alongside a preamble-only
	// file receiving its first user+assistant turn (appendable).
	clSplitRW := filepath.Join(tmp, "claude", "-work-app", "cl-split-rw.jsonl")
	clSplitPre := filepath.Join(tmp, "claude", "-work-app", "cl-split-pre.jsonl")
	parityWrite(t, clSplitRW,
		`{"type":"user","sessionId":"cl-split-rw","cwd":"/work/app","timestamp":"2026-10-01T09:10:00Z","message":{"role":"user","content":"Initial prompt"}}`+"\n"+
			`{"type":"assistant","sessionId":"cl-split-rw","requestId":"req_rw1","timestamp":"2026-10-01T09:10:01Z","message":{"id":"msg_rw1","role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"Initial answer"}]}}`+"\n",
		false,
	)
	parityWrite(t, clSplitPre,
		`{"type":"system","subtype":"bridge_status","url":"https://claude.ai/code/session_01splitpre","sessionId":"cl-split-pre"}`+"\n",
		false,
	)
	parityPass(t, indexDir, false)

	// Rewrite clSplitRW (triggers rest) while appending first turn to clSplitPre (appendable).
	parityWrite(t, clSplitRW,
		`{"type":"user","sessionId":"cl-split-rw","cwd":"/work/app","timestamp":"2026-10-01T09:10:00Z","message":{"role":"user","content":"Rewritten prompt with extra details"}}`+"\n"+
			`{"type":"assistant","sessionId":"cl-split-rw","requestId":"req_rw1","timestamp":"2026-10-01T09:10:01Z","message":{"id":"msg_rw1","role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"Rewritten answer"}]}}`+"\n",
		false,
	)
	parityWrite(t, clSplitPre,
		`{"type":"user","sessionId":"cl-split-pre","cwd":"/work/app","timestamp":"2026-10-01T09:11:00Z","message":{"role":"user","content":"Split preamble question"}}`+"\n"+
			`{"type":"assistant","sessionId":"cl-split-pre","requestId":"req_sp1","timestamp":"2026-10-01T09:11:01Z","message":{"id":"msg_sp1","role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"Split preamble answer"}]}}`+"\n",
		true,
	)
	parityPass(t, indexDir, false)

	sameAsRebuild(t, indexDir)

	// Verify metadata fields not checked by paritySnapshot (Started, Updated, Model, Tokens,
	// ToolCalls, ToolErrors, RemoteID, Kind, Parent).
	freshDir := indexDir + "-fresh"
	incMap, freshMap := metaByKey(t, indexDir), metaByKey(t, freshDir)
	keys := make([]string, 0, len(freshMap))
	for k := range freshMap {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		got, ok := incMap[k]
		want := freshMap[k]
		if !ok {
			t.Fatalf("missing session %s in incremental index", k)
		}
		if got.Title != want.Title || got.Project != want.Project ||
			!got.Started.Equal(want.Started) || !got.Updated.Equal(want.Updated) ||
			got.RemoteID != want.RemoteID || got.Kind != want.Kind || got.Parent != want.Parent ||
			!reflect.DeepEqual(got.Model, want.Model) || !reflect.DeepEqual(got.Tokens, want.Tokens) ||
			!reflect.DeepEqual(got.ToolCalls, want.ToolCalls) || got.ToolErrors != want.ToolErrors {
			t.Errorf("%s metadata mismatch:\n  inc:   title=%q project=%q remote=%q kind=%q parent=%q started=%s updated=%s model=%+v tokens=%+v tools=%v err=%d\n  fresh: title=%q project=%q remote=%q kind=%q parent=%q started=%s updated=%s model=%+v tokens=%+v tools=%v err=%d",
				k,
				got.Title, got.Project, got.RemoteID, got.Kind, got.Parent, got.Started, got.Updated, got.Model, got.Tokens, got.ToolCalls, got.ToolErrors,
				want.Title, want.Project, want.RemoteID, want.Kind, want.Parent, want.Started, want.Updated, want.Model, want.Tokens, want.ToolCalls, want.ToolErrors,
			)
		}
	}

	clGot := incMap["claude:cl-stream-1"]
	if clGot.RemoteID != "session_01streamtest" || clGot.Kind != "fork" || clGot.Parent != "cl-parent-0" {
		t.Errorf("claude:cl-stream-1 preamble metadata lost: remote=%q kind=%q parent=%q", clGot.RemoteID, clGot.Kind, clGot.Parent)
	}
	if got := incMap["claude:cl-split-pre"].RemoteID; got != "session_01splitpre" {
		t.Errorf("claude:cl-split-pre RemoteID = %q, want %q", got, "session_01splitpre")
	}
	if got := incMap["codex:"+cxID]; got.Model == nil || got.Model.Provider != "custom-gw" || got.Model.ID != "gpt-5-codex" {
		t.Errorf("codex:%s Model = %+v, want Provider custom-gw / ID gpt-5-codex", cxID, got.Model)
	}
	if got := incMap["antigravity:ag-stream-1"]; !strings.HasSuffix(got.Project, "work/repo-b") {
		t.Errorf("antigravity:ag-stream-1 Project = %q, want suffix work/repo-b", got.Project)
	} else if wantStart := time.Date(2026, 10, 1, 7, 0, 1, 0, time.UTC); !got.Started.Equal(wantStart) {
		t.Errorf("antigravity:ag-stream-1 Started = %v, want %v", got.Started, wantStart)
	}
}
