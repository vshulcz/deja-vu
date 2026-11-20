package sources

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func anteWriteFile(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAnteRootsAndDiscovery(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("ANTE_HOME", "")
	t.Setenv("DEJA_ANTE_ROOT", "")
	t.Setenv("DEJA_ANTE_ROOTS", "")

	wantDefault := filepath.Join(home, ".ante")
	if got := AnteConfigDir(); got != wantDefault {
		t.Fatalf("AnteConfigDir() = %q, want %q", got, wantDefault)
	}
	if got := AnteRoot(); got != filepath.Join(wantDefault, "sessions") {
		t.Fatalf("AnteRoot() = %q", got)
	}

	customHome := filepath.Join(t.TempDir(), "custom-ante")
	t.Setenv("ANTE_HOME", customHome)
	if got := AnteConfigDir(); got != customHome {
		t.Fatalf("AnteConfigDir() with ANTE_HOME = %q, want %q", got, customHome)
	}

	t.Setenv("ANTE_HOME", "")
	extraDirect := filepath.Join(t.TempDir(), "direct-store")
	extraWrapped := t.TempDir()
	if err := os.MkdirAll(filepath.Join(extraWrapped, ".ante", "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(extraDirect, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_ANTE_ROOTS", strings.Join([]string{extraDirect, extraWrapped}, string(os.PathListSeparator)))
	roots := AnteRoots()
	if !slices.Contains(roots, extraDirect) || !slices.Contains(roots, filepath.Join(extraWrapped, ".ante", "sessions")) {
		t.Fatalf("AnteRoots() = %v, missing extra roots", roots)
	}

	root := t.TempDir()
	t.Setenv("DEJA_ANTE_ROOT", root)
	sesA := filepath.Join(root, "ses_01A")
	sesB := filepath.Join(root, "ses_01B")
	sesSub := filepath.Join(sesA, "subagents", "sub_1")
	for _, d := range []string{sesA, sesB, sesSub} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	eventsA := anteWriteFile(t, filepath.Join(sesA, "events.jsonl"), `{"timestamp":"2026-08-14T10:00:00Z","id":"e1","event":{"UserInput":"hello"}}`+"\n")
	dialogA := anteWriteFile(t, filepath.Join(sesA, "dialog.json"), `{}`)
	metaA := anteWriteFile(t, filepath.Join(sesA, "meta.json"), `{}`)
	dialogB := anteWriteFile(t, filepath.Join(sesB, "dialog.json"), `{}`)
	subEvents := anteWriteFile(t, filepath.Join(sesSub, "events.jsonl"), `{"timestamp":"2026-08-14T10:01:00Z","id":"e1","event":{"UserInput":"subtask"}}`+"\n")

	files := AnteSessionFiles()
	if !slices.Contains(files, eventsA) || !slices.Contains(files, dialogB) || !slices.Contains(files, subEvents) {
		t.Fatalf("AnteSessionFiles() = %v, missing expected transcripts", files)
	}
	if slices.Contains(files, dialogA) || slices.Contains(files, metaA) {
		t.Fatalf("AnteSessionFiles() = %v, should exclude dialog beside events.jsonl and meta.json", files)
	}

	sidecars := AnteSidecarFiles()
	if !slices.Contains(sidecars, metaA) || !slices.Contains(sidecars, dialogA) {
		t.Fatalf("AnteSidecarFiles() = %v, want meta.json and shadowed dialog.json", sidecars)
	}
	if slices.Contains(sidecars, dialogB) || slices.Contains(sidecars, eventsA) {
		t.Fatalf("AnteSidecarFiles() = %v, should not mark fallback dialog.json or events.jsonl as sidecar", sidecars)
	}

	if !AnteUnderRoot(eventsA) || !AnteUnderRoot(dialogB) || AnteUnderRoot(dialogA) || AnteUnderRoot(metaA) {
		t.Fatalf("AnteUnderRoot classification mismatch")
	}

	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "0")
	noSub := AnteSessionFiles()
	if slices.Contains(noSub, subEvents) {
		t.Fatalf("AnteSessionFiles() with DEJA_INCLUDE_SUBAGENTS=0 included %q", subEvents)
	}
}

func TestParseAnteEventsAndExitsAndTokens(t *testing.T) {
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "1")
	dir := filepath.Join(t.TempDir(), "ses_01MAIN")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	anteWriteFile(t, filepath.Join(dir, "meta.json"), `{
  "id": "ses_01MAIN",
  "provider": "anthropic",
  "model": "claude-sonnet-4-6",
  "dir": "/Users/alex/payments-api",
  "first_user_message": "Fix retry loop\nand timeouts",
  "usage": {"input_tokens": 9999, "output_tokens": 9999},
  "started_time": "2026-08-14T10:00:00Z",
  "last_active_time": "2026-08-14T10:05:00Z",
  "forked_from": "ses_00ORIGIN"
}`)

	body := strings.Join([]string{
		`{"timestamp":"2026-08-14T10:00:01Z","id":"e1","event":{"UserInput":"Fix retry loop\nand timeouts"}}`,
		`{"timestamp":"2026-08-14T10:00:02Z","id":"e2","event":{"ToolStart":{"id":"c_write","name":"Write","args":{"file_path":"retry.go","content":"package p\n\nconst defaultRetryBackoffSeconds = 15\n"}}}}`,
		`{"timestamp":"2026-08-14T10:00:03Z","id":"e3","event":{"ToolEnd":{"tool_use_id":"c_write","tool_name":"Write","status":"Success","result_json":{}}}}`,
		`{"timestamp":"2026-08-14T10:00:04Z","id":"e4","event":{"ToolStart":{"id":"c_edit","name":"Edit","args":{"file_path":"/Users/alex/payments-api/backoff.go","old_string":"30s","new_string":"15s"}}}}`,
		`{"timestamp":"2026-08-14T10:00:05Z","id":"e5","event":{"ToolEnd":{"tool_use_id":"c_edit","tool_name":"Edit","status":"Cancelled","result_json":{}}}}`,
		`{"timestamp":"2026-08-14T10:00:06Z","id":"e6","event":{"ToolStart":{"id":"c_bash","name":"Bash","args":{"command":"go test ./..."}}}}`,
		`{"timestamp":"2026-08-14T10:00:07Z","id":"e7","event":{"ToolEnd":{"tool_use_id":"c_bash","tool_name":"Bash","status":"Failed","result_json":{"Completed":{"exit_code":2,"stderr":"FAIL"}}}}}`,
		`{"timestamp":"2026-08-14T10:00:08Z","id":"e8","event":{"ToolStart":{"id":"c_bash2","name":"Bash","args":{"command":"make lint"}}}}`,
		`{"timestamp":"2026-08-14T10:00:09Z","id":"e9","event":{"ToolEnd":{"tool_use_id":"c_bash2","tool_name":"Bash","status":"Denied","result_json":{}}}}`,
		`{"timestamp":"2026-08-14T10:00:10Z","id":"e10","event":{"ShellOutput":{"command":"git status","output":"clean","exit_code":0}}}`,
		`{"timestamp":"2026-08-14T10:00:11Z","id":"e11","event":{"CompactEnd":{"summary":"Adjusted backoff to 15s."}}}`,
		`{"timestamp":"2026-08-14T10:00:12Z","id":"e12","event":{"AgentMessage":"Done."}}`,
		`{"timestamp":"2026-08-14T10:00:13Z","id":"e13","event":{"UsageUpdate":{"usage":{"input_tokens":120,"output_tokens":45}}}}`,
		`{"timestamp":"2026-08-14T10:00:14Z","id":"e14","event":{"SessionEnd":{"turns":1,"usage":{"input_tokens":120,"output_tokens":45}}}}`,
	}, "\n") + "\n"
	p := anteWriteFile(t, filepath.Join(dir, "events.jsonl"), body)

	sessions, err := ParseAnteFile(p)
	if err != nil {
		t.Fatalf("ParseAnteFile error: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want 1", len(sessions))
	}
	s := sessions[0]
	if s.ID != "ses_01MAIN" || s.Harness != "ante" || s.Project != projectName("/Users/alex/payments-api") {
		t.Fatalf("unexpected identity: %+v", s)
	}
	if s.Kind != model.KindFork || s.Parent != "ses_00ORIGIN" {
		t.Fatalf("fork lineage = (%q, %q), want (fork, ses_00ORIGIN)", s.Kind, s.Parent)
	}
	if s.Title != "Fix retry loop" {
		t.Fatalf("Title = %q, want first line", s.Title)
	}
	if s.Model == nil || s.Model.Provider != "anthropic" || s.Model.ID != "claude-sonnet-4-6" {
		t.Fatalf("Model = %+v", s.Model)
	}
	if s.Tokens == nil || s.Tokens.Input != 120 || s.Tokens.Output != 45 {
		t.Fatalf("Tokens = %+v, want non-double-counted 120/45", s.Tokens)
	}
	if s.ToolErrors != 1 {
		t.Fatalf("ToolErrors = %d, want 1 (only Failed status)", s.ToolErrors)
	}

	var hasWrote, hasResolvedFile, hasEdit, hasFailedCmd, hasUnstampedDenied, hasSummary, hasOutput bool
	for _, m := range s.Messages {
		switch m.Role {
		case model.RoleFiles:
			if strings.Contains(m.Text, "/Users/alex/payments-api/retry.go") {
				hasResolvedFile = true
			}
		case model.RoleWrote:
			if strings.Contains(m.Text, "/Users/alex/payments-api/retry.go") {
				hasWrote = true
			}
		case model.RoleEdit:
			if strings.Contains(m.Text, "backoff.go") {
				hasEdit = true
			}
		case model.RoleCommand:
			if strings.Contains(m.Text, "go test ./...") && strings.Contains(m.Text, "exit 2") {
				hasFailedCmd = true
			}
			if strings.TrimSpace(m.Text) == "$ make lint" {
				hasUnstampedDenied = true
			}
		case model.RoleSummary:
			if strings.Contains(m.Text, "Adjusted backoff to 15s.") {
				hasSummary = true
			}
		case model.RoleToolOutput:
			if strings.Contains(m.Text, "FAIL") || strings.Contains(m.Text, "clean") {
				hasOutput = true
			}
		}
	}
	if !hasWrote || !hasResolvedFile || !hasEdit || !hasFailedCmd || !hasUnstampedDenied || !hasSummary || !hasOutput {
		t.Fatalf("missing expected messages: wrote=%v file=%v edit=%v failedCmd=%v deniedCmd=%v summary=%v output=%v\nmsgs=%+v",
			hasWrote, hasResolvedFile, hasEdit, hasFailedCmd, hasUnstampedDenied, hasSummary, hasOutput, s.Messages)
	}
}

func TestAnteResumesAndSubagentAndDialogFallback(t *testing.T) {
	root := t.TempDir()
	parentDir := filepath.Join(root, "ses_PARENT")
	subDir := filepath.Join(parentDir, "subagents", "worker")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	anteWriteFile(t, filepath.Join(parentDir, "meta.json"), `{"id":"ses_PARENT","dir":"/Users/alex/payments-api"}`)
	subPath := anteWriteFile(t, filepath.Join(subDir, "events.jsonl"),
		`{"timestamp":"2026-08-14T11:00:00Z","id":"s1","event":{"UserInput":"check schema"}}`+"\n"+
			`{"timestamp":"2026-08-14T11:00:05Z","id":"s2","event":{"AgentMessage":"looks valid"}}`+"\n")

	subSessions, err := ParseAnteFile(subPath)
	if err != nil || len(subSessions) != 1 {
		t.Fatalf("ParseAnteFile subagent = %v, %v", subSessions, err)
	}
	sub := subSessions[0]
	if sub.Kind != model.KindSubagent || sub.Parent != "ses_PARENT" || sub.ID != "ses_PARENT-worker" || sub.Project != projectName("/Users/alex/payments-api") {
		t.Fatalf("unexpected subagent session: %+v", sub)
	}

	evPath := filepath.Join(parentDir, "events.jsonl")
	head := strings.Join([]string{
		`{"timestamp":"2026-08-14T10:00:00Z","id":"e1","event":{"UserInput":"hello"}}`,
		`{"timestamp":"2026-08-14T10:00:01Z","id":"e2","event":{"ToolStart":{"id":"cmd1","name":"Bash","args":{"command":"go test ./..."}}}}`,
	}, "\n") + "\n"
	anteWriteFile(t, evPath, head)
	offset := int64(len(head))

	f, err := os.OpenFile(evPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"timestamp":"2026-08-14T10:00:02Z","id":"e3","event":{"ToolEnd":{"tool_use_id":"cmd1","tool_name":"Bash","status":"Failed","result_json":{"Completed":{"exit_code":1}}}}}` + "\n")
	_ = f.Close()
	if anteResumes(evPath, offset) {
		t.Fatal("anteResumes returned true when failed Bash ToolEnd settled an earlier ToolStart")
	}

	dialogDir := filepath.Join(root, "ses_DIALOG")
	if err := os.MkdirAll(dialogDir, 0o755); err != nil {
		t.Fatal(err)
	}
	anteWriteFile(t, filepath.Join(dialogDir, "meta.json"), `{"id":"ses_DIALOG","cwd":"/Users/alex/payments-api","usage":{"input_tokens":50,"output_tokens":10},"started_time":"2026-08-14T12:00:00Z","last_active_time":"2026-08-14T12:01:00Z"}`)
	dialogPath := anteWriteFile(t, filepath.Join(dialogDir, "dialog.json"), `{
  "reasoning_model": "o3-mini",
  "messages": [
    {"role": "User", "content": {"Text": "inspect config"}},
    {"role": "Model", "content": {"Parts": [
      {"Text": "checking"},
      {"ToolUse": {"id": "d1", "name": "Bash", "args": {"command": "ls -la"}}},
      {"ToolResult": {"tool_use_id": "d1", "content": "ok", "is_error": true}}
    ]}}
  ]
}`)
	if anteResumes(dialogPath, 10) {
		t.Fatal("anteResumes must return false for dialog.json")
	}
	ds, err := ParseAnteFile(dialogPath)
	if err != nil || len(ds) != 1 {
		t.Fatalf("ParseAnteFile dialog.json = %v, %v", ds, err)
	}
	if ds[0].Model == nil || ds[0].Model.ID != "o3-mini" || ds[0].ToolErrors != 1 || ds[0].Tokens == nil || ds[0].Tokens.Input != 50 {
		t.Fatalf("unexpected dialog session: %+v", ds[0])
	}
}
