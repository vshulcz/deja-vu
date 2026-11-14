package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const clawCodeCall = `{"type":"message","message":{"role":"assistant","blocks":[{"type":"tool_use","id":"call-cc-1","name":"bash","input":"{\"command\":\"go test ./...\"}"}]}}` + "\n"

func clawCodeResult(outputJSON string, isError bool) string {
	b, err := json.Marshal(outputJSON)
	if err != nil {
		panic(err)
	}
	errFlag := "false"
	if isError {
		errFlag = "true"
	}
	return `{"type":"message","message":{"role":"tool","blocks":[{"type":"tool_result","tool_use_id":"call-cc-1","tool_name":"bash","output":` + string(b) + `,"is_error":` + errFlag + `}]}}` + "\n"
}

func clawCodeExitStore(outputJSON string, isError bool) func(*testing.T, string) string {
	return func(t *testing.T, dir string) string {
		t.Helper()
		return writeExitFixture(t, filepath.Join(dir, "cc-exit.jsonl"),
			`{"type":"session_meta","session_id":"cc-exit","workspace_root":"/workspace/claw-demo","created_at_ms":1759305600000}`+"\n"+
				`{"type":"message","message":{"role":"user","blocks":[{"type":"text","text":"run tests"}]}}`+"\n"+
				clawCodeCall+
				clawCodeResult(outputJSON, isError))
	}
}

func TestParseClawCodeJSONLAndTools(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess-1.jsonl")

	// prompt_history duplicates the first user prompt text; only its timestamp
	// should carry over without emitting a second user record.
	lines := []string{
		`{"type":"session_meta","session_id":"sess-1","workspace_root":"/workspace/claw-demo","created_at_ms":1759305600000}`,
		`{"type":"prompt_history","prompt":"fix retry backoff","timestamp_ms":1759305601000}`,
		`{"type":"message","message":{"role":"user","blocks":[{"type":"text","text":"fix retry backoff"}]}}`,
		`{"type":"message","message":{"role":"assistant","blocks":[{"type":"text","text":"Checking retry logic"},{"type":"tool_use","id":"t1","name":"read_file","input":"{\"path\":\"/workspace/claw-demo/retry.go\"}"},{"type":"tool_use","id":"t2","name":"write_file","input":"{\"path\":\"/workspace/claw-demo/new.go\",\"content\":\"package demo\\nfunc clawDemoHandler() string { return \\\"ok\\\" }\\n\"}"},{"type":"tool_use","id":"t3","name":"edit_file","input":"{\"path\":\"/workspace/claw-demo/retry.go\",\"old_string\":\"1s\",\"new_string\":\"2s\"}"},{"type":"tool_use","id":"t4","name":"bash","input":"{\"command\":\"go test ./...\"}"}]}}`,
		`{"type":"message","message":{"role":"tool","blocks":[{"type":"tool_result","tool_use_id":"t4","tool_name":"bash","output":"{\"stdout\":\"ok\",\"stderr\":\"\",\"interrupted\":false}","is_error":false}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	sessions, err := ParseClawCodeFile(path)
	if err != nil {
		t.Fatalf("ParseClawCodeFile: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	s := sessions[0]
	if s.Harness != "clawcode" || s.Project != "workspace/claw-demo" || s.ID != "sess-1" {
		t.Fatalf("unexpected metadata: %+v", s)
	}
	if got := ClawCodeSessionDir(path); got != "/workspace/claw-demo" {
		t.Fatalf("ClawCodeSessionDir() = %q, want /workspace/claw-demo", got)
	}

	byRole := map[string][]string{}
	for _, m := range s.Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
		if m.Time.IsZero() {
			t.Fatalf("message role %q missing timestamp", m.Role)
		}
	}
	if got := len(byRole["user"]); got != 1 {
		t.Fatalf("user messages = %d, want 1 (prompt_history must not duplicate)", got)
	}
	if cmds := strings.Join(byRole[RoleCommand], "\n"); !strings.Contains(cmds, "$ go test ./...") {
		t.Fatalf("command records = %v, want $ go test ./...", byRole[RoleCommand])
	}
	if outs := strings.Join(byRole[RoleToolOutput], "\n"); !strings.Contains(outs, "ok") {
		t.Fatalf("tool output = %v, want ok", byRole[RoleToolOutput])
	}
	files := strings.Join(byRole[RoleFiles], "\n")
	for _, wantFile := range []string{"/workspace/claw-demo/retry.go", "/workspace/claw-demo/new.go"} {
		if !strings.Contains(files, wantFile) {
			t.Fatalf("files %q missing %q", files, wantFile)
		}
	}
	if wrote := strings.Join(byRole[RoleWrote], "\n"); !strings.Contains(wrote, "/workspace/claw-demo/new.go") {
		t.Fatalf("wrote = %q, want new.go", wrote)
	}
	if edits := strings.Join(byRole[RoleEdit], "\n"); !strings.Contains(edits, "1s") {
		t.Fatalf("edits = %q, want 1s", edits)
	}
}

func TestParseClawCodeLegacyJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.json")
	payload := `{
		"version": 1,
		"workspace_root": "/workspace/claw-legacy",
		"created_at_ms": 1759305600000,
		"messages": [
			{"role": "user", "blocks": [{"type": "text", "text": "check ip 203.0.113.10"}]},
			{"role": "assistant", "blocks": [{"type": "tool_use", "id": "b1", "name": "bash", "input": "{\"command\":\"go version\"}"}]}
		]
	}`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}

	sessions, err := ParseClawCodeFile(path)
	if err != nil {
		t.Fatalf("ParseClawCodeFile legacy: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Project != "workspace/claw-legacy" {
		t.Fatalf("unexpected legacy sessions: %+v", sessions)
	}
	if got := ClawCodeSessionDir(path); got != "/workspace/claw-legacy" {
		t.Fatalf("ClawCodeSessionDir legacy = %q, want /workspace/claw-legacy", got)
	}
}

func TestClawCodeDiscoveryAndFilters(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DEJA_CLAWCODE_ROOT", "")
	t.Setenv("DEJA_CLAWCODE_ROOTS", "")
	t.Setenv("CLAW_CONFIG_HOME", "")

	cfgHome := filepath.Join(home, "custom-claw")
	t.Setenv("CLAW_CONFIG_HOME", cfgHome)
	if got := ClawCodeConfigDir(); got != cfgHome {
		t.Fatalf("ClawCodeConfigDir() = %q, want %q", got, cfgHome)
	}
	if wantRoot := filepath.Join(cfgHome, "sessions"); ClawCodeRoot() != wantRoot {
		t.Fatalf("ClawCodeRoot() = %q, want %q", ClawCodeRoot(), wantRoot)
	}

	sessDir := filepath.Join(cfgHome, "sessions")
	extraRoot := filepath.Join(home, "extra-claw")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(extraRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAWCODE_ROOTS", extraRoot)

	if roots := ClawCodeRoots(); len(roots) != 2 {
		t.Fatalf("ClawCodeRoots() = %v, want 2 roots", roots)
	}

	good1 := filepath.Join(sessDir, "a.jsonl")
	good2 := filepath.Join(extraRoot, "b.json")
	meta := filepath.Join(sessDir, "a.meta.json")
	deleted := filepath.Join(sessDir, "old.deleted.json")

	validLine := `{"type":"message","message":{"role":"user","blocks":[{"type":"text","text":"hi"}]}}` + "\n"
	legacyBody := `{"messages":[{"role":"user","blocks":[{"type":"text","text":"legacy"}]}]}`
	for path, body := range map[string]string{
		good1:   validLine,
		good2:   legacyBody,
		meta:    `{"session_id":"a"}`,
		deleted: legacyBody,
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if !ClawCodeUnderRoot(good1) || !ClawCodeUnderRoot(good2) {
		t.Fatalf("ClawCodeUnderRoot should match %q and %q", good1, good2)
	}
	if ClawCodeUnderRoot(meta) || ClawCodeUnderRoot(deleted) {
		t.Fatalf("ClawCodeUnderRoot must skip .meta.json and .deleted.json")
	}

	files := ClawCodeSessionFiles()
	joined := strings.Join(files, "\n")
	if !strings.Contains(joined, good1) || !strings.Contains(joined, good2) {
		t.Fatalf("ClawCodeSessionFiles() missing expected files: %v", files)
	}
	if strings.Contains(joined, ".meta.json") || strings.Contains(joined, ".deleted.json") {
		t.Fatalf("ClawCodeSessionFiles() included skipped metadata/deleted file: %v", files)
	}

	loaded := LoadClawCode()
	if len(loaded) != 2 {
		t.Fatalf("LoadClawCode() = %d sessions, want 2", len(loaded))
	}

	t.Setenv("DEJA_CLAWCODE_ROOT", sessDir)
	if got := ClawCodeRoots(); len(got) != 1 || got[0] != sessDir {
		t.Fatalf("DEJA_CLAWCODE_ROOT override = %v, want [%s]", got, sessDir)
	}
}

func TestParseClawCodeFileFromOffsetAndResumes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stream.jsonl")
	head := `{"type":"session_meta","session_id":"stream","workspace_root":"/workspace/claw-demo","created_at_ms":1759305600000}` + "\n" +
		`{"type":"message","message":{"role":"user","blocks":[{"type":"text","text":"first"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(head), 0o600); err != nil {
		t.Fatal(err)
	}
	offset := int64(len(head))

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	tail := `{"type":"message","message":{"role":"assistant","blocks":[{"type":"text","text":"second answer"}]}}` + "\n"
	if _, werr := f.WriteString(tail); werr != nil {
		_ = f.Close()
		t.Fatal(werr)
	}
	if cerr := f.Close(); cerr != nil {
		t.Fatal(cerr)
	}

	if !clawCodeResumes(path, offset) {
		t.Fatalf("clawCodeResumes on plain assistant tail = false, want true")
	}

	sessions, err := ParseClawCodeFileFromOffset(path, offset)
	if err != nil {
		t.Fatalf("ParseClawCodeFileFromOffset: %v", err)
	}
	if len(sessions) != 1 || len(sessions[0].Messages) != 1 || sessions[0].Messages[0].Text != "second answer" {
		t.Fatalf("unexpected incremental sessions: %+v", sessions)
	}
	if sessions[0].ID != "stream" || sessions[0].Project != "workspace/claw-demo" {
		t.Fatalf("incremental session lost header metadata: %+v", sessions[0])
	}
}
