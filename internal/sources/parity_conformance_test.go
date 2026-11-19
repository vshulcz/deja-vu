package sources

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestParityInlineBlockUnpack(t *testing.T) {
	blk, ok := parseInlineBlock("  [thinking] verify retry cap first  ")
	if !ok || blk.typ != "thinking" || blk.text != "verify retry cap first" {
		t.Fatalf("parseInlineBlock(thinking) = (%+v, %v)", blk, ok)
	}

	blk, ok = parseInlineBlock(`[tool_use Bash] {"command":"go test ./..."}`)
	if !ok || blk.typ != "tool_use" || blk.name != "Bash" {
		t.Fatalf("parseInlineBlock(tool_use) = (%+v, %v)", blk, ok)
	}

	blk, ok = parseInlineBlock("[tool_result] Exit code 2\nFAIL pkg/foo")
	if !ok || blk.typ != "tool_result" || !blk.isErr || !strings.Contains(blk.text, "FAIL pkg/foo") {
		t.Fatalf("parseInlineBlock(tool_result) = (%+v, %v)", blk, ok)
	}

	if _, ok := parseInlineBlock("plain user speech"); ok {
		t.Fatal("parseInlineBlock accepted plain speech")
	}

	blocks, results, speech, onlyTool := unpackContentBlocks([]any{
		"[thinking] internal thought",
		"visible explanation",
		`[tool_use Edit] {"file_path":"/Users/alex/repo/main.go","old_string":"a","new_string":"b"}`,
		"[tool_result] Exit code 1\nbad syntax",
	})
	if speech != "visible explanation" {
		t.Fatalf("speech = %q, want visible explanation", speech)
	}
	if onlyTool {
		t.Fatal("onlyTool should be false when human/assistant speech is present")
	}
	if len(blocks) != 2 || len(results) != 1 || !strings.Contains(results[0], "bad syntax") {
		t.Fatalf("unpackContentBlocks got blocks=%d results=%v", len(blocks), results)
	}
}

func TestParityFlatRoleAndZCodeWorkRecords(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_ZCODE_ROOT", root)

	projDir := filepath.Join(root, "-Users-alex-repo")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	huge := strings.Repeat("x", maxParsedMessage+512) + "TAIL_END"
	path := filepath.Join(projDir, "zc-parity-1.jsonl")
	lines := strings.Join([]string{
		`{"role":"user","sessionId":"zc-parity-1","timestamp":"2026-10-01T10:00:00Z","content":"update retry backoff"}`,
		fmt.Sprintf(`{"role":"assistant","sessionId":"zc-parity-1","timestamp":"2026-10-01T10:00:01Z","content":["[thinking] inspect retry.go",%q,%q,%q,{"type":"tool_use","id":"call-sh-1","name":"Bash","input":{"command":"go test ./..."}}]}`,
			`[tool_use Read] {"file_path":"/Users/alex/repo/retry.go"}`,
			`[tool_use Write] {"file_path":"/Users/alex/repo/backoff.go","content":"func ComputeBackoffDelay(attempt int, baseMillis int64) int64 {\n\treturn baseMillis * (1 << attempt)\n}\n"}`,
			`[tool_use Edit] {"file_path":"/Users/alex/repo/retry.go","old_string":"max := 3","new_string":"max := 5"}`),
		fmt.Sprintf(`{"role":"tool","sessionId":"zc-parity-1","tool_call_id":"call-sh-1","exit_code":2,"timestamp":"2026-10-01T10:00:02Z","content":%q}`, huge),
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	ss, err := ParseZCodeFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("ParseZCodeFile = (%d, %v), want 1 session", len(ss), err)
	}
	byRole := map[string][]string{}
	for _, m := range ss[0].Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
	}
	for _, role := range []string{RoleFiles, RoleWrote, RoleEdit, RoleCommand, RoleToolOutput} {
		if len(byRole[role]) == 0 {
			t.Fatalf("missing role %q in ZCode messages: %#v", role, byRole)
		}
	}
	if !strings.Contains(byRole[RoleCommand][0], "go test ./...") || !strings.Contains(byRole[RoleCommand][0], "2") {
		t.Fatalf("RoleCommand did not join exit code: %q", byRole[RoleCommand][0])
	}
	if len(byRole[RoleToolOutput][0]) > maxParsedMessage {
		t.Fatalf("RoleToolOutput exceeded cap: %d > %d", len(byRole[RoleToolOutput][0]), maxParsedMessage)
	}
}

func TestParityDeepSeekToolOutputSwitchAndCap(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_DEEPSEEK_ROOT", root)

	sessDir := filepath.Join(root, "--work-pgbouncer-lab--", "session-ds-cap")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	huge := strings.Repeat("y", maxParsedMessage+512)
	body := strings.Join([]string{
		`{"type":"session","version":0,"id":"session-ds-cap","createdAt":1787320263519,"cwd":"/work/pgbouncer-lab"}`,
		`{"type":"user/message","seq":1,"time":1787320263520,"data":{"content":[{"type":"text","text":"check pool"}],"source":{"kind":"user"},"role":"user"}}`,
		`{"type":"tool/call","seq":2,"time":1787320263530,"data":{"callId":"c1","name":"bash","arguments":"{\"command\":\"cat notes.txt\"}"}}`,
		fmt.Sprintf(`{"type":"tool/result","seq":3,"time":1787320263540,"data":{"message":{"source":{"kind":"tool","callId":"c1"},"content":[{"type":"tool-result","toolCallId":"c1","content":[{"type":"text","text":%q}]}]}}}`, huge),
		`{"type":"assistant/message","seq":4,"time":1787320263550,"data":{"turn":1,"step":2,"message":{"role":"assistant","content":[{"type":"text","text":"pool checked"}],"source":{"kind":"model"}}}}`,
	}, "\n") + "\n"
	path := filepath.Join(sessDir, "session.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	ss, err := ParseDeepSeekFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("ParseDeepSeekFile = (%d, %v), want 1", len(ss), err)
	}
	var toolOutputs []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleToolOutput {
			toolOutputs = append(toolOutputs, m.Text)
		}
	}
	if len(toolOutputs) != 1 {
		t.Fatalf("expected 1 RoleToolOutput message by default, got %d", len(toolOutputs))
	}
	if len(toolOutputs[0]) > maxParsedMessage {
		t.Fatalf("DeepSeek RoleToolOutput length %d > maxParsedMessage %d", len(toolOutputs[0]), maxParsedMessage)
	}

	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "0")
	offSS, err := ParseDeepSeekFile(path)
	if err != nil || len(offSS) != 1 {
		t.Fatalf("ParseDeepSeekFile(off) = (%d, %v)", len(offSS), err)
	}
	for _, m := range offSS[0].Messages {
		if m.Role == RoleToolOutput {
			t.Fatalf("DEJA_INDEX_TOOL_OUTPUT=0 leaked RoleToolOutput: %q", m.Text)
		}
	}
}

func TestParityResolveFallbackProjectAcrossHarnesses(t *testing.T) {
	tmp := t.TempDir()
	mkRepo := func(parent, name string) string {
		repo := filepath.Join(tmp, parent, name)
		if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		return repo
	}
	repo1 := mkRepo("services", "billing-api")
	repo2 := mkRepo("src", "payments-core")
	repo3 := mkRepo("work", "cache-proxy")

	// 1. >= 3 RoleFiles resolves project.
	sFiles := model.Session{
		ID:      "p-files",
		Harness: "qwen",
		Project: "",
		Messages: []model.Message{
			{Role: RoleFiles, Text: strings.Join([]string{
				filepath.Join(repo1, "handler.go"),
				filepath.Join(repo1, "store.go"),
				filepath.Join(repo1, "retry.go"),
			}, "\n")},
		},
	}
	resolveFallbackProject(&sFiles)
	if sFiles.Project != "services/billing-api" {
		t.Fatalf("sFiles.Project = %q, want services/billing-api", sFiles.Project)
	}

	// 2. RoleEdit / RoleWrote resolves home-derived project even with < 3 RoleFiles.
	sEdit := model.Session{
		ID:      "p-edit",
		Harness: "deepseek",
		Project: "Users/alex",
		Messages: []model.Message{
			{Role: RoleEdit, Text: filepath.Join(repo2, "main.go") + "\nfunc main() {}"},
		},
	}
	resolveFallbackProject(&sEdit)
	if sEdit.Project != "src/payments-core" {
		t.Fatalf("sEdit.Project = %q, want src/payments-core", sEdit.Project)
	}

	// 3. `cd <dir>` in RoleCommand resolves empty/- project.
	sCmd := model.Session{
		ID:      "p-cmd",
		Harness: "codearts",
		Project: "-",
		Messages: []model.Message{
			{Role: RoleCommand, Text: "cd " + repo3 + " && go test ./..."},
		},
	}
	resolveFallbackProject(&sCmd)
	if sCmd.Project != "work/cache-proxy" {
		t.Fatalf("sCmd.Project = %q, want work/cache-proxy", sCmd.Project)
	}
}

func TestParityWorktreeAndScratchAllHarnesses(t *testing.T) {
	collapseCases := []struct {
		in   string
		want string
	}{
		{"/Users/alex/repo/.codex/worktrees/wt-1/pkg/a.go", "/Users/alex/repo/pkg/a.go"},
		{"/Users/alex/repo/.qoder/worktrees/wt-2/pkg/b.go", "/Users/alex/repo/pkg/b.go"},
		// Home-root Codex Desktop worktrees must not collapse away the checkout root.
		{"/Users/alex/.codex/worktrees/ab12/repo/a.go", "/Users/alex/.codex/worktrees/ab12/repo/a.go"},
		{"/home/alex/.codex/worktrees/ab12/repo/a.go", "/home/alex/.codex/worktrees/ab12/repo/a.go"},
		{"/root/.codex/worktrees/ab12/repo/a.go", "/root/.codex/worktrees/ab12/repo/a.go"},
		{`C:\Users\alex\.codex\worktrees\ab12\repo\a.go`, `C:\Users\alex\.codex\worktrees\ab12\repo\a.go`},
		// Git internal worktree metadata must not collapse into a repo file.
		{"/Users/alex/repo/.git/worktrees/wt-1/HEAD", "/Users/alex/repo/.git/worktrees/wt-1/HEAD"},
	}
	for _, tc := range collapseCases {
		if got := CollapseWorktreePath(tc.in); got != tc.want {
			t.Errorf("CollapseWorktreePath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	if IsAgentScratchPath("/Users/alex/.codex/worktrees/ab12/repo/a.go") {
		t.Fatal("IsAgentScratchPath flagged real file inside ~/.codex/worktrees/<id>/repo as scratch")
	}
	if IsAgentScratchPath("/Users/alex/repo/.qoder/worktrees/wt-2/pkg/b.go") {
		t.Fatal("IsAgentScratchPath flagged file inside .qoder/worktrees checkout as scratch")
	}
	for _, scratch := range []string{
		"/Users/alex/.codex/sessions/2026/11/rollout.jsonl",
		"/Users/alex/.qoder/projects/-Users-alex-repo/s.jsonl",
		"/Users/alex/.cursor/plans/plan.md",
	} {
		if !IsAgentScratchPath(scratch) {
			t.Errorf("IsAgentScratchPath(%q) = false, want true", scratch)
		}
	}
}

func TestParityQoderAndCherryStudioCompactionAndAutoMemory(t *testing.T) {
	// Qoder compaction, head, and auto-memory discovery.
	qoderRoot := t.TempDir()
	t.Setenv("DEJA_QODER_ROOT", qoderRoot)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(t.TempDir(), "no-claude"))

	qProj := filepath.Join(qoderRoot, "projects", "-Users-alex-qproj")
	qMem := filepath.Join(qProj, "memory")
	if err := os.MkdirAll(qMem, 0o755); err != nil {
		t.Fatal(err)
	}
	qTranscript := filepath.Join(qProj, "q-sess-1.jsonl")
	qBody := strings.Join([]string{
		`{"type":"user","sessionId":"q-sess-1","cwd":"/Users/alex/qproj","timestamp":"2026-10-02T10:00:00Z","message":{"role":"user","content":"add retry jitter"}}`,
		`{"type":"assistant","sessionId":"q-sess-1","cwd":"/Users/alex/qproj","timestamp":"2026-10-02T10:00:05Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"q-edit-1","name":"Edit","input":{"file_path":"/Users/alex/qproj/retry.go"}}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(qTranscript, []byte(qBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(qMem, "feedback_rules.md"), []byte("---\nname: Retry jitter\ntype: feedback\n---\nAlways add jitter to retry loops.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	qt, err := ReadCompactionTranscript(qTranscript, "q-sess-1")
	if err != nil {
		t.Fatalf("ReadCompactionTranscript(qoder): %v", err)
	}
	if qt.Harness != "qoder" || qt.Session.Harness != "qoder" || qt.Workspace != "/Users/alex/qproj" || len(qt.ToolCalls) != 1 {
		t.Fatalf("qoder compaction = %#v", qt)
	}
	qh, err := TranscriptHead(qTranscript, "q-sess-1")
	if err != nil || qh.Harness != "qoder" {
		t.Fatalf("TranscriptHead(qoder) = (%q, %v)", qh.Harness, err)
	}
	if entries := LoadClaudeAutoMemory([]string{"/Users/alex/qproj"}); len(entries) != 1 || !strings.Contains(entries[0].Body, "Always add jitter") {
		t.Fatalf("LoadClaudeAutoMemory(qoder) = %#v", entries)
	}

	// Cherry Studio compaction, snapshot collapse, head, and auto-memory discovery.
	cherryProjects := filepath.Join(t.TempDir(), "CherryStudio", "Data", "Agents", ".claude", "projects")
	t.Setenv("DEJA_CHERRYSTUDIO_ROOTS", cherryProjects)

	cProj := filepath.Join(cherryProjects, "-Users-alex-cproj")
	cMem := filepath.Join(cProj, "memory")
	if err = os.MkdirAll(cMem, 0o755); err != nil {
		t.Fatal(err)
	}
	cTranscript := filepath.Join(cProj, "cs-sess-1.jsonl")
	cBody := strings.Join([]string{
		`{"type":"user","sessionId":"cs-sess-1","cwd":"/Users/alex/cproj","timestamp":"2026-10-03T10:00:00Z","message":{"role":"user","content":"check lock timeout"}}`,
		`{"type":"assistant","uuid":"u1","requestId":"req-1","sessionId":"cs-sess-1","cwd":"/Users/alex/cproj","timestamp":"2026-10-03T10:00:01Z","message":{"id":"msg-1","role":"assistant","content":[{"type":"text","text":"advisory"}]}}`,
		`{"type":"assistant","uuid":"u2","requestId":"req-1","sessionId":"cs-sess-1","cwd":"/Users/alex/cproj","timestamp":"2026-10-03T10:00:02Z","message":{"id":"msg-1","role":"assistant","content":[{"type":"text","text":"advisory lock timeout"},{"type":"tool_use","id":"cs-read-1","name":"Read","input":{"file_path":"/Users/alex/cproj/lock.go"}}]}}`,
	}, "\n") + "\n"
	if err = os.WriteFile(cTranscript, []byte(cBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(cMem, "project_conventions.md"), []byte("---\nname: Lock timeout\ntype: project\n---\nUse explicit lock timeout on migrations.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ct, err := ReadCompactionTranscript(cTranscript, "cs-sess-1")
	if err != nil {
		t.Fatalf("ReadCompactionTranscript(cherrystudio): %v", err)
	}
	if ct.Harness != "cherrystudio" || ct.Session.Harness != "cherrystudio" || ct.Workspace != "/Users/alex/cproj" || len(ct.ToolCalls) != 1 {
		t.Fatalf("cherrystudio compaction = %#v", ct)
	}
	assistantSpeech := 0
	for _, m := range ct.Session.Messages {
		if m.Role == "assistant" {
			assistantSpeech++
		}
	}
	if assistantSpeech != 1 {
		t.Fatalf("cherrystudio compaction did not collapse streaming snapshots: got %d assistant turns (%#v)", assistantSpeech, ct.Session.Messages)
	}
	ch, err := TranscriptHead(cTranscript, "cs-sess-1")
	if err != nil || ch.Harness != "cherrystudio" {
		t.Fatalf("TranscriptHead(cherrystudio) = (%q, %v)", ch.Harness, err)
	}
	if entries := LoadClaudeAutoMemory([]string{"/Users/alex/cproj"}); len(entries) != 1 || !strings.Contains(entries[0].Body, "explicit lock timeout") {
		t.Fatalf("LoadClaudeAutoMemory(cherrystudio) = %#v", entries)
	}
}

func TestParityRegistryResumesForCodeArtsQoderZCode(t *testing.T) {
	checked := map[string]bool{}
	for _, h := range Registry() {
		switch h.Name {
		case "codearts", "qoder":
			checked[h.Name] = true
			for _, k := range h.Kinds {
				if k.Resumes == nil {
					t.Errorf("%s kind %q missing Resumes callback", h.Name, k.Name)
				}
			}
		case "zcode":
			checked[h.Name] = true
			for _, k := range h.Kinds {
				if k.Name == "zcode" && k.Resumes == nil {
					t.Errorf("zcode kind %q missing Resumes callback", k.Name)
				}
			}
		}
	}
	for _, want := range []string{"codearts", "qoder", "zcode"} {
		if !checked[want] {
			t.Fatalf("harness %q missing from Registry()", want)
		}
	}
	_ = slices.Contains([]string{"codearts", "qoder", "zcode"}, "qoder")
}

func TestParityHelperBranchesAndQwenHead(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"", true},
		{"-", true},
		{"Users/alex", true},
		{"home/alex", true},
		{"Users/", false},
		{"services/api", false},
	} {
		if got := IsHomeOrEmptyProject(tc.in); got != tc.want {
			t.Errorf("IsHomeOrEmptyProject(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}

	for _, tc := range []struct {
		res  map[string]any
		code int
		ok   bool
	}{
		{map[string]any{"content": "Exit code 3\nFAIL"}, 3, true},
		{map[string]any{"content": "ok\nCommand failed with exit code 4."}, 4, true},
		{map[string]any{"is_error": true}, 1, true},
		{map[string]any{"content": "no status"}, 0, false},
	} {
		code, ok := flatRoleResultExitCode(tc.res)
		if code != tc.code || ok != tc.ok {
			t.Errorf("flatRoleResultExitCode(%#v) = (%d, %v), want (%d, %v)", tc.res, code, ok, tc.code, tc.ok)
		}
	}

	if got := CollapseWorktreePath(""); got != "" {
		t.Errorf("CollapseWorktreePath(\"\") = %q", got)
	}
	if got := CollapseWorktreePath("/Users/alex/repo/.git/worktrees/feat/main.go"); got != "/Users/alex/repo/.git/worktrees/feat/main.go" {
		t.Errorf("CollapseWorktreePath(.git/worktrees) modified path: %q", got)
	}
	if got := CollapseWorktreePath("relative/.codex/worktrees/wt-1/a.go"); got != "relative/a.go" {
		t.Errorf("CollapseWorktreePath(relative) = %q", got)
	}

	for _, tc := range []struct {
		in   string
		want string
		ok   bool
	}{
		{"", "", false},
		{"[thinking] step 1", "", false},
		{"[tool_use Read] {}", "", false},
		{"[tool_result] FAIL unit test", "FAIL unit test", true},
		{"final reply", "final reply", true},
	} {
		got, ok := filterInlineSpeechText(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("filterInlineSpeechText(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}

	qwenDir := filepath.Join(t.TempDir(), "chats")
	if err := os.MkdirAll(qwenDir, 0o755); err != nil {
		t.Fatal(err)
	}
	qwenPath := filepath.Join(qwenDir, "qw-1.jsonl")
	qwenLines := strings.Join([]string{
		`{"type":"user","sessionId":"qw-1","cwd":"/Users/alex/qproj","timestamp":"2026-10-01T10:00:00Z","message":{"role":"user","parts":[{"text":"check timeout"}]}}`,
		`{"type":"assistant","sessionId":"qw-1","cwd":"/Users/alex/qproj","timestamp":"2026-10-01T10:00:01Z","message":{"role":"model","parts":[{"text":"increased timeout to 5s"}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(qwenPath, []byte(qwenLines), 0o644); err != nil {
		t.Fatal(err)
	}
	qh, err := TranscriptHead(qwenPath, "qw-1")
	if err != nil || qh.Harness != "qwen" || qh.ID != "qw-1" {
		t.Fatalf("TranscriptHead(qwen) = (%+v, %v)", qh, err)
	}
}
