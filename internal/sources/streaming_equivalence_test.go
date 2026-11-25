package sources

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/model"
)

func assertValidSessionUTF8(t *testing.T, ctx string, s model.Session) {
	t.Helper()
	fields := []struct {
		name string
		val  string
	}{
		{"ID", s.ID},
		{"Harness", s.Harness},
		{"Project", s.Project},
		{"Path", s.Path},
		{"Title", s.Title},
		{"OrigID", s.OrigID},
		{"RemoteID", s.RemoteID},
		{"FormerID", s.FormerID},
		{"From", s.From},
		{"Lifecycle", s.Lifecycle},
		{"LifecycleNote", s.LifecycleNote},
		{"LifecycleAt", s.LifecycleAt},
		{"Kind", s.Kind},
		{"Parent", s.Parent},
		{"Agent", s.Agent},
	}
	if s.Model != nil {
		fields = append(fields,
			struct{ name, val string }{"Model.Provider", s.Model.Provider},
			struct{ name, val string }{"Model.ID", s.Model.ID},
		)
	}
	for _, f := range fields {
		if !utf8.ValidString(f.val) {
			t.Fatalf("%s: invalid UTF-8 in %s: %q", ctx, f.name, f.val)
		}
	}
	for k := range s.ToolCalls {
		if !utf8.ValidString(k) {
			t.Fatalf("%s: invalid UTF-8 in ToolCalls key: %q", ctx, k)
		}
	}
	for i, v := range s.Typed {
		if !utf8.ValidString(v) {
			t.Fatalf("%s: invalid UTF-8 in Typed[%d]", ctx, i)
		}
	}
	for i, v := range s.Planted {
		if !utf8.ValidString(v) {
			t.Fatalf("%s: invalid UTF-8 in Planted[%d]", ctx, i)
		}
	}
	for i, m := range s.Messages {
		if !utf8.ValidString(m.Role) {
			t.Fatalf("%s: invalid UTF-8 in Messages[%d].Role: %q", ctx, i, m.Role)
		}
		if !utf8.ValidString(m.Text) {
			t.Fatalf("%s: invalid UTF-8 in Messages[%d].Text (len=%d)", ctx, i, len(m.Text))
		}
	}
}

func criticalCuts(raw []byte) []int {
	seen := map[int]bool{
		0:                true,
		1:                true,
		2:                true,
		3:                true,
		4:                true,
		len(raw) / 4:     true,
		len(raw) / 2:     true,
		3 * len(raw) / 4: true,
		len(raw) - 3:     true,
		len(raw) - 2:     true,
		len(raw) - 1:     true,
		len(raw):         true,
	}
	for i, b := range raw {
		if b == '\n' {
			seen[i-1] = true
			seen[i] = true
			seen[i+1] = true
		}
	}
	cuts := make([]int, 0, len(seen))
	for c := range seen {
		if c >= 0 && c <= len(raw) {
			cuts = append(cuts, c)
		}
	}
	slices.Sort(cuts)
	return cuts
}

func TestRegistryFixturesTornWriteAndUTF8Safety(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	reg := readFormatRegistry(t, filepath.Join(root, "docs", "registry", "registry.json"))
	kindByHarness := map[string][]FileKind{}
	for _, h := range Registry() {
		kindByHarness[h.Name] = h.Kinds
	}

	tmp := t.TempDir()
	for _, entry := range reg.Harnesses {
		kinds := kindByHarness[entry.ID]
		if len(kinds) == 0 {
			continue
		}
		for _, rel := range entry.FixturePaths {
			srcPath := filepath.Join(root, filepath.FromSlash(rel))
			fi, err := os.Stat(srcPath)
			if err != nil || fi.IsDir() {
				continue
			}
			ext := filepath.Ext(srcPath)
			if ext == ".sql" || ext == ".db" || ext == ".vscdb" || ext == ".sqlite" {
				continue
			}
			raw, err := os.ReadFile(srcPath)
			if err != nil || len(raw) == 0 {
				continue
			}
			for _, cut := range criticalCuts(raw) {
				tornPath := filepath.Join(tmp, fmt.Sprintf("%s-%d%s", entry.ID, cut, ext))
				if err := os.WriteFile(tornPath, raw[:cut], 0o600); err != nil {
					t.Fatal(err)
				}
				for _, k := range kinds {
					if k.dbBacked {
						continue
					}
					sessions, _ := k.Parse(tornPath, 0)
					for si, s := range sessions {
						assertValidSessionUTF8(t, fmt.Sprintf("%s/%s cut=%d session=%d Parse", entry.ID, k.Name, cut, si), s)
					}
					if k.ParseFrom != nil {
						offs := []int64{0, 1, int64(cut / 2), int64(cut)}
						for _, off := range offs {
							if off < 0 {
								continue
							}
							part, _ := k.ParseFrom(tornPath, off, 0)
							for si, s := range part {
								assertValidSessionUTF8(t, fmt.Sprintf("%s/%s cut=%d off=%d session=%d ParseFrom", entry.ID, k.Name, cut, off, si), s)
							}
							if k.Resumes != nil {
								_ = k.Resumes(tornPath, off)
							}
						}
					}
				}
			}
		}
	}

	t.Run("SyntheticUTF8EditSpanBOMCRLFAndLongLines", func(t *testing.T) {
		dir := t.TempDir()
		// 3-byte runes (32768%3 != 0) and odd-prefixed 2-byte runes straddle
		// editSpanMax (32768) whether or not a path prefix is prepended.
		cjk := strings.Repeat("中", editSpanMax)
		oddCyr := "a" + strings.Repeat("Я", editSpanMax)
		longText := strings.Repeat("x", 70*1024) + "🦊"

		// Claude: Edit old_string/new_string > editSpanMax + BOM + CRLF + long line
		claudePath := filepath.Join(dir, "claude-utf8.jsonl")
		claudeBody := "\xef\xbb\xbf" +
			fmt.Sprintf(`{"type":"user","sessionId":"cl-utf8","timestamp":"2026-10-01T09:00:00Z","message":{"role":"user","content":%q}}`, longText) + "\r\n" +
			fmt.Sprintf(`{"type":"assistant","sessionId":"cl-utf8","requestId":"req-1","timestamp":"2026-10-01T09:00:01Z","message":{"id":"m1","role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/work/app/a.go","old_string":%q,"new_string":%q}}]}}`, cjk, oddCyr) + "\r\n" +
			fmt.Sprintf(`{"type":"assistant","sessionId":"cl-utf8","requestId":"req-2","timestamp":"2026-10-01T09:00:02Z","message":{"id":"m2","role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Edit","input":{"file_path":"/work/app/a.go","old_string":%q,"new_string":%q}}]}}`, oddCyr, cjk) + "\r\n"
		if err := os.WriteFile(claudePath, []byte(claudeBody[:len(claudeBody)-5]), 0o600); err != nil {
			t.Fatal(err)
		}
		if ss, _ := ParseClaudeFile(claudePath); len(ss) > 0 {
			for i, s := range ss {
				assertValidSessionUTF8(t, fmt.Sprintf("claude torn %d", i), s)
			}
		}
		if err := os.WriteFile(claudePath, []byte(claudeBody), 0o600); err != nil {
			t.Fatal(err)
		}
		ss, err := ParseClaudeFile(claudePath)
		if err != nil {
			t.Fatal(err)
		}
		claudeEdits := 0
		for i, s := range ss {
			assertValidSessionUTF8(t, fmt.Sprintf("claude full %d", i), s)
			for _, m := range s.Messages {
				if m.Role == RoleEdit {
					claudeEdits++
				}
			}
		}
		if claudeEdits < 2 {
			t.Fatalf("claude emitted %d edit records, want >= 2", claudeEdits)
		}

		// Codex: apply_patch > editSpanMax with multibyte runes + BOM + CRLF
		codexPath := filepath.Join(dir, "rollout-2026-10-01T09-00-00-019fbcca-0000-7000-8000-000000000099.jsonl")
		patch := fmt.Sprintf("*** Begin Patch\n*** Update File: /work/app/b.go\n@@\n-%s\n-%s\n+%s\n*** End Patch", cjk, oddCyr, cjk)
		codexBody := "\xef\xbb\xbf" +
			`{"timestamp":"2026-10-01T09:00:00Z","type":"session_meta","payload":{"id":"019fbcca-0000-7000-8000-000000000099","cwd":"/work/app"}}` + "\r\n" +
			fmt.Sprintf(`{"timestamp":"2026-10-01T09:00:01Z","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","call_id":"c1","input":%q}}`, patch) + "\r\n"
		if err = os.WriteFile(codexPath, []byte(codexBody), 0o600); err != nil {
			t.Fatal(err)
		}
		codexSS, err := ParseCodexRollout(codexPath)
		if err != nil {
			t.Fatal(err)
		}
		codexEdits := 0
		for i, s := range codexSS {
			assertValidSessionUTF8(t, fmt.Sprintf("codex utf8 %d", i), s)
			for _, m := range s.Messages {
				if m.Role == RoleEdit {
					codexEdits++
				}
			}
		}
		if codexEdits == 0 {
			t.Fatalf("codex emitted 0 edit records")
		}

		// Antigravity: CODE_ACTION replace_file_content > editSpanMax with multibyte runes
		agMsgs := antigravityStep("CODE_ACTION",
			fmt.Sprintf("The following changes were made by the replace_file_content tool to: /work/app/c.go. If relevant, run it.\n[diff_block_start]\n@@ -1,2 +1,1 @@\n-%s\n-%s\n+short\n[diff_block_end]\n", cjk, oddCyr),
			time.Time{},
		)
		agEdits := 0
		for i, m := range agMsgs {
			if !utf8.ValidString(m.Text) {
				t.Fatalf("antigravity[%d] produced invalid UTF-8", i)
			}
			if m.Role == RoleEdit {
				agEdits++
			}
		}
		if agEdits == 0 {
			t.Fatalf("antigravity emitted 0 edit records")
		}

		// OpenCode unifiedDiffSpans + Roo rooEditRecords
		diff := fmt.Sprintf("--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-%s\n-%s\n+short\n", cjk, oddCyr)
		ocSpans := unifiedDiffSpans("a/x.go", diff)
		if len(ocSpans) == 0 {
			t.Fatalf("unifiedDiffSpans emitted 0 edit records")
		}
		for i, sp := range ocSpans {
			if !utf8.ValidString(sp) {
				t.Fatalf("unifiedDiffSpans[%d] produced invalid UTF-8", i)
			}
		}
		spans, wrote := rooEditRecords([]any{map[string]any{
			"type": "tool_use",
			"name": "apply_diff",
			"input": map[string]any{
				"path": "a/b.go",
				"diff": fmt.Sprintf("<<<<<<< SEARCH\n%s\n=======\n%s\n>>>>>>> REPLACE", cjk, oddCyr),
			},
		}}, "")
		if len(spans) == 0 {
			t.Fatalf("rooEditRecords emitted 0 edit spans")
		}
		for i, sp := range append(spans, wrote...) {
			if !utf8.ValidString(sp) {
				t.Fatalf("rooEditRecords[%d] produced invalid UTF-8", i)
			}
		}
	})
}

func TestCodexStreamingOffsetEquivalence(t *testing.T) {
	t.Run("MetaSeededModelSwitchAndDuplicateTokenCountWithRelativePatch", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "rollout-2026-10-01T08-00-00-019fbcca-1111-7000-8000-000000000001.jsonl")
		longInstr := strings.Repeat("k", 5000)
		prefix := "\xef\xbb\xbf" +
			fmt.Sprintf(`{"timestamp":"2026-10-01T08:00:00Z","type":"session_meta","payload":{"id":"019fbcca-1111-7000-8000-000000000001","cwd":"/work/app","model_provider":"custom-gw","instructions":%q}}`, longInstr) + "\n" +
			`{"timestamp":"2026-10-01T08:00:01Z","type":"turn_context","payload":{"model":"my-model-v1"}}` + "\n" +
			`{"timestamp":"2026-10-01T08:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":150},"last_token_usage":{"input_tokens":100,"output_tokens":50,"reasoning_output_tokens":10,"cached_input_tokens":20}}}}` + "\n" +
			`{"timestamp":"2026-10-01T08:00:03Z","type":"response_item","payload":{"type":"message","role":"user","content":"start"}}` + "\n"
		offset := int64(len(prefix))
		tail := `{"timestamp":"2026-10-01T08:00:10Z","type":"turn_context","payload":{"model":"my-model-v2"}}` + "\n" +
			`{"timestamp":"2026-10-01T08:00:11Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":150},"last_token_usage":{"input_tokens":100,"output_tokens":50,"reasoning_output_tokens":10,"cached_input_tokens":20}}}}` + "\n" +
			fmt.Sprintf(`{"timestamp":"2026-10-01T08:00:12Z","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","call_id":"cp1","input":%q}}`, "*** Begin Patch\n*** Update File: sub/a.go\n@@\n-old line\n+new line\n*** End Patch") + "\n"

		if err := os.WriteFile(path, []byte(prefix+tail), 0o600); err != nil {
			t.Fatal(err)
		}
		fullSS, err := ParseCodexRollout(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(fullSS) != 1 {
			t.Fatalf("full parse returned %d sessions, want 1", len(fullSS))
		}
		full := fullSS[0]

		// Simulate incremental prefix read on a copy containing only prefix, then tail from offset.
		prefixPath := filepath.Join(dir, "rollout-prefix.jsonl")
		if err = os.WriteFile(prefixPath, []byte(prefix), 0o600); err != nil {
			t.Fatal(err)
		}
		prefixSS, err := ParseCodexRollout(prefixPath)
		if err != nil {
			t.Fatal(err)
		}
		if len(prefixSS) != 1 {
			t.Fatalf("prefix parse returned %d sessions, want 1", len(prefixSS))
		}
		tailSS, err := ParseCodexRolloutFromOffset(path, offset)
		if err != nil {
			t.Fatal(err)
		}
		if len(tailSS) != 1 {
			t.Fatalf("tail parse returned %d sessions, want 1", len(tailSS))
		}
		part := tailSS[0]
		if part.ID != full.ID {
			t.Fatalf("tail ID = %q, want %q", part.ID, full.ID)
		}
		if part.Project != full.Project {
			t.Fatalf("tail Project = %q, want %q", part.Project, full.Project)
		}
		if !part.Started.Equal(full.Started) {
			t.Fatalf("tail Started = %v, want %v", part.Started, full.Started)
		}
		if part.Model == nil || full.Model == nil || *part.Model != *full.Model {
			t.Fatalf("tail Model = %+v, want %+v", part.Model, full.Model)
		}
		mergedTokens := prefixSS[0].Tokens.Add(part.Tokens)
		if mergedTokens == nil || full.Tokens == nil || *mergedTokens != *full.Tokens {
			t.Fatalf("merged Tokens = %+v, want %+v (tail=%+v)", mergedTokens, full.Tokens, part.Tokens)
		}
		sawResolvedPath := false
		for _, m := range part.Messages {
			if strings.Contains(m.Text, "/work/app/sub/a.go") {
				sawResolvedPath = true
				break
			}
		}
		if !sawResolvedPath {
			t.Fatalf("tail messages did not resolve relative patch path to /work/app/sub/a.go: %+v", part.Messages)
		}
	})

	t.Run("CustomToolCallExecResumesFalse", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "rollout-exec.jsonl")
		prefix := `{"timestamp":"2026-10-01T08:00:00Z","type":"session_meta","payload":{"id":"019fbcca-2222-7000-8000-000000000002","cwd":"/work/app"}}` + "\n" +
			fmt.Sprintf(`{"timestamp":"2026-10-01T08:00:01Z","type":"response_item","payload":{"type":"custom_tool_call","name":"exec","call_id":"call_exec_1","input":%q}}`, `exec_command({cmd: "go test ./internal/sources"})`) + "\n"
		offset := int64(len(prefix))
		tail := `{"timestamp":"2026-10-01T08:00:02Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call_exec_1","output":"Process exited with code 0\nok\n"}}` + "\n"
		if err := os.WriteFile(path, []byte(prefix+tail), 0o600); err != nil {
			t.Fatal(err)
		}
		if codexResumes(path, offset) {
			t.Fatalf("codexResumes(custom_tool_call exec + output across offset) = true, want false")
		}
	})

	t.Run("ForkContextThreadSettingsAppliedAfterOffsetResumesFalse", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "rollout-fork.jsonl")
		prefix := `{"timestamp":"2026-10-01T08:00:00Z","type":"session_meta","payload":{"id":"child-1","cwd":"/work/app"}}` + "\n" +
			`{"timestamp":"2026-10-01T08:00:01Z","type":"session_meta","payload":{"id":"parent-0","cwd":"/work/app"}}` + "\n" +
			`{"timestamp":"2026-10-01T08:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":"parent inherited prompt"}}` + "\n"
		offset := int64(len(prefix))
		tail := `{"timestamp":"2026-10-01T08:00:03Z","type":"event_msg","payload":{"type":"thread_settings_applied"}}` + "\n" +
			`{"timestamp":"2026-10-01T08:00:04Z","type":"response_item","payload":{"type":"message","role":"user","content":"child prompt"}}` + "\n"
		if err := os.WriteFile(path, []byte(prefix+tail), 0o600); err != nil {
			t.Fatal(err)
		}
		if codexResumes(path, offset) {
			t.Fatalf("codexResumes(thread_settings_applied after offset) = true, want false")
		}
	})
}

func TestAntigravityStreamingOffsetEquivalence(t *testing.T) {
	makeTranscript := func(t *testing.T, root, id string) string {
		t.Helper()
		p := filepath.Join(root, "brain", id, ".system_generated", "logs", "transcript.jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}

	t.Run("LongFirstLinePastWindowPreservesStarted", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("DEJA_ANTIGRAVITY_ROOT", root)
		path := makeTranscript(t, root, "ag-long-start")
		wantStart := time.Date(2026, 10, 1, 7, 0, 0, 0, time.UTC)
		longPrompt := strings.Repeat("p", 6500)
		padUser := strings.Repeat("u", callWindow+4096)
		prefix := fmt.Sprintf(`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":%q,"content":%q}`, wantStart.Format(time.RFC3339Nano), longPrompt) + "\n" +
			fmt.Sprintf(`{"step_index":1,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-10-01T07:01:00Z","content":%q}`, padUser) + "\n"
		offset := int64(len(prefix))
		tail := `{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-10-01T07:05:00Z","content":"done after gap"}` + "\n"
		if err := os.WriteFile(path, []byte(prefix+tail), 0o600); err != nil {
			t.Fatal(err)
		}
		ss, err := ParseAntigravityFileFromOffset(path, offset)
		if err != nil {
			t.Fatal(err)
		}
		if len(ss) != 1 {
			t.Fatalf("ParseAntigravityFileFromOffset returned %d sessions, want 1", len(ss))
		}
		if !ss[0].Started.Equal(wantStart) {
			t.Fatalf("tail Started = %v, want %v", ss[0].Started, wantStart)
		}
	})

	t.Run("PlannerBeyondCallWindowResumesFalseWhileInWindowTrue", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("DEJA_ANTIGRAVITY_ROOT", root)

		planner := `{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-10-01T07:00:01Z","content":"editing","tool_calls":[{"name":"write_to_file","args":{"TargetFile":"/work/app/x.go","CodeContent":"package x\n"}}]}` + "\n"
		step := `{"step_index":3,"source":"MODEL","type":"CODE_ACTION","status":"DONE","created_at":"2026-10-01T07:05:00Z","content":"Created file /work/app/x.go"}` + "\n"

		// Beyond callWindow (>4 MiB gap between PLANNER_RESPONSE and step after offset)
		farPath := makeTranscript(t, root, "ag-far-planner")
		pad := strings.Repeat("z", callWindow+4096)
		farPrefix := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-10-01T07:00:00Z","content":"hi"}` + "\n" +
			planner +
			fmt.Sprintf(`{"step_index":2,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-10-01T07:02:00Z","content":%q}`, pad) + "\n"
		farOffset := int64(len(farPrefix))
		if err := os.WriteFile(farPath, []byte(farPrefix+step), 0o600); err != nil {
			t.Fatal(err)
		}
		if antigravityResumes(farPath, farOffset) {
			t.Fatalf("antigravityResumes(>callWindow gap before step) = true, want false")
		}

		// Within callWindow (<4 MiB): still resumable
		nearPath := makeTranscript(t, root, "ag-near-planner")
		nearPrefix := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-10-01T07:00:00Z","content":"hi"}` + "\n" + planner
		nearOffset := int64(len(nearPrefix))
		if err := os.WriteFile(nearPath, []byte(nearPrefix+step), 0o600); err != nil {
			t.Fatal(err)
		}
		if !antigravityResumes(nearPath, nearOffset) {
			t.Fatalf("antigravityResumes(<callWindow) = false, want true")
		}
	})

	t.Run("ProjectFromPathsConvergesOnOffsetParse", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("DEJA_ANTIGRAVITY_ROOT", root)
		cache := filepath.Join(root, "cache")
		if err := os.MkdirAll(cache, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cache, "conversation_metadata.json"), []byte(`{"conversations":{"ag-proj":{"summary":{"WorkspaceURIs":["file:///work/ws-a"]}}}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		repoB := filepath.Join(root, "work", "repo-b")
		if err := os.MkdirAll(filepath.Join(repoB, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		fileA := filepath.Join(repoB, "a.go")
		fileB := filepath.Join(repoB, "b.go")
		fileC := filepath.Join(repoB, "c.go")
		for _, f := range []string{fileA, fileB, fileC} {
			if err := os.WriteFile(f, []byte("package b\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}

		path := makeTranscript(t, root, "ag-proj")
		prefix := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-10-01T07:00:00Z","content":"work in repo b"}` + "\n" +
			`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-10-01T07:00:01Z","content":"reading files"}` + "\n"
		fileURI := func(p string) string {
			return strings.ReplaceAll(filepath.ToSlash(p), " ", "%20")
		}
		offset := int64(len(prefix))
		tail := fmt.Sprintf(`{"step_index":2,"source":"MODEL","type":"VIEW_FILE","status":"DONE","created_at":"2026-10-01T07:00:02Z","content":"File Path: file://%s\npackage b"}`, fileURI(fileA)) + "\n" +
			fmt.Sprintf(`{"step_index":3,"source":"MODEL","type":"VIEW_FILE","status":"DONE","created_at":"2026-10-01T07:00:03Z","content":"File Path: file://%s\npackage b"}`, fileURI(fileB)) + "\n" +
			fmt.Sprintf(`{"step_index":4,"source":"MODEL","type":"VIEW_FILE","status":"DONE","created_at":"2026-10-01T07:00:04Z","content":"File Path: file://%s\npackage b"}`, fileURI(fileC)) + "\n"
		if err := os.WriteFile(path, []byte(prefix+tail), 0o600); err != nil {
			t.Fatal(err)
		}

		fullSS, err := ParseAntigravityFile(path)
		if err != nil {
			t.Fatal(err)
		}
		tailSS, err := ParseAntigravityFileFromOffset(path, offset)
		if err != nil {
			t.Fatal(err)
		}
		if len(fullSS) != 1 || len(tailSS) != 1 {
			t.Fatalf("unexpected session counts full=%d tail=%d", len(fullSS), len(tailSS))
		}
		if fullSS[0].Project != "work/repo-b" {
			t.Fatalf("full Project = %q, want work/repo-b", fullSS[0].Project)
		}
		if tailSS[0].Project != fullSS[0].Project || tailSS[0].ProjectFromDir != fullSS[0].ProjectFromDir {
			t.Fatalf("tail Project=%q (fromDir=%v), want %q (fromDir=%v)", tailSS[0].Project, tailSS[0].ProjectFromDir, fullSS[0].Project, fullSS[0].ProjectFromDir)
		}
	})
}

func TestClaudeStreamingOffsetEquivalence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cl-stream.jsonl")
	prefixPath := filepath.Join(dir, "cl-stream-prefix.jsonl")

	prefix := `{"type":"user","sessionId":"cl-stream","cwd":"/work/app","timestamp":"2026-10-01T08:00:00Z","message":{"role":"user","content":"update main.go"}}` + "\n" +
		`{"type":"assistant","sessionId":"cl-stream","requestId":"req_00","timestamp":"2026-10-01T08:00:01Z","message":{"id":"msg_00","role":"assistant","model":"claude-sonnet-4-6","usage":{"input_tokens":400,"output_tokens":60,"cache_read_input_tokens":100},"content":[{"type":"text","text":"Working on it."}]}}` + "\n" +
		`{"type":"assistant","sessionId":"cl-stream","requestId":"req_01","timestamp":"2026-10-01T08:00:02Z","message":{"id":"msg_01","role":"assistant","model":"claude-sonnet-4-6","usage":{"input_tokens":1200,"output_tokens":25,"cache_read_input_tokens":300,"output_tokens_details":{"thinking_tokens":80}},"content":[{"type":"thinking","thinking":"plan edit"}]}}` + "\n"
	offset := int64(len(prefix))
	tail := `{"type":"assistant","sessionId":"cl-stream","requestId":"req_01","timestamp":"2026-10-01T08:00:03Z","message":{"id":"msg_01","role":"assistant","model":"claude-sonnet-4-6","usage":{"input_tokens":1200,"output_tokens":95,"cache_read_input_tokens":300,"output_tokens_details":{"thinking_tokens":80}},"content":[{"type":"text","text":"Done updating file."}]}}` + "\n" +
		`{"type":"assistant","sessionId":"cl-stream","requestId":"req_01","timestamp":"2026-10-01T08:00:04Z","message":{"id":"msg_01","role":"assistant","model":"claude-sonnet-4-6","usage":{"input_tokens":1200,"output_tokens":140,"cache_read_input_tokens":300,"output_tokens_details":{"thinking_tokens":80}},"content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/work/app/main.go","old_string":"old","new_string":"new"}}]}}` + "\n" +
		`{"type":"user","sessionId":"cl-stream","timestamp":"2026-10-01T08:00:05Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}` + "\n"

	if err := os.WriteFile(path, []byte(prefix+tail), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prefixPath, []byte(prefix), 0o600); err != nil {
		t.Fatal(err)
	}
	fullSS, err := ParseClaudeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	prefixSS, err := ParseClaudeFile(prefixPath)
	if err != nil {
		t.Fatal(err)
	}
	tailSS, err := ParseClaudeFileFromOffset(path, offset)
	if err != nil {
		t.Fatal(err)
	}
	if len(fullSS) != 1 || len(prefixSS) != 1 || len(tailSS) != 1 {
		t.Fatalf("unexpected session counts full=%d prefix=%d tail=%d", len(fullSS), len(prefixSS), len(tailSS))
	}
	want := model.Tokens{Input: 1600, Output: 200, Reasoning: 80, CacheRead: 400}
	if fullSS[0].Tokens == nil || *fullSS[0].Tokens != want {
		t.Fatalf("full Tokens = %+v, want %+v", fullSS[0].Tokens, want)
	}
	merged := prefixSS[0].Tokens.Add(tailSS[0].Tokens)
	if merged == nil || *merged != *fullSS[0].Tokens {
		t.Fatalf("merged Tokens = %+v, want %+v (tail=%+v)", merged, fullSS[0].Tokens, tailSS[0].Tokens)
	}
}
