package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func junieFixtureRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "registry", "junie", "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_JUNIE_ROOT", root)
	return root
}

func TestJunieReadsTheEventLog(t *testing.T) {
	junieFixtureRoot(t)
	files := JunieSessionFiles()
	if len(files) != 1 || !isJunieSession(files[0]) {
		t.Fatalf("session files = %v", files)
	}
	ss, err := ParseJunieFile(files[0])
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	s := ss[0]
	if s.ID != "session-260901-101500-k3m9" || s.Title != "Fix the retry loop off-by-one" || s.Project != projectName("/work/retry-demo") {
		t.Fatalf("session = %q %q %q", s.ID, s.Title, s.Project)
	}
	var got []string
	for _, m := range s.Messages {
		got = append(got, m.Role+": "+strings.SplitN(m.Text, "\n", 2)[0])
	}
	want := []string{
		"user: why does the retry loop drop the last attempt",
		"command: $ go test ./...  → exit 1",
		"tool-output: --- FAIL: TestRetryRunsEveryAttempt (0.00s)",
		"files: /work/retry-demo/retry.go",
		"wrote: /work/retry-demo/retry.go",
		"edit: /work/retry-demo/retry.go",
		"command: $ go test ./...",
		"tool-output: ok  \tretry-demo\t0.003s",
		"assistant: ### Summary",
		"user: does the backoff still double between attempts",
		"assistant: Yes: the delay doubles after each failed attempt and stops at the cap.",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("records:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !s.Messages[0].Time.Before(s.Messages[len(s.Messages)-1].Time) {
		t.Fatalf("times not in order: %v .. %v", s.Messages[0].Time, s.Messages[len(s.Messages)-1].Time)
	}
	if got := JunieProjectDir(s.ID); got != "/work/retry-demo" {
		t.Fatalf("project dir = %q", got)
	}
	if JunieSessionEvents("../"+s.ID) != "" || JunieSessionEvents("..") != "" {
		t.Fatal("a session id with a separator reached outside the root")
	}
}

func TestJunieTurnAndCompaction(t *testing.T) {
	junieFixtureRoot(t)
	st := JunieTurn("session-260901-101500-k3m9")
	// The failed run was followed by one that passed; the replay of the
	// failure at the task's end is not a new command.
	if st.FailureID != "" {
		t.Fatalf("newest command passed, failure = %q", st.FailureID)
	}
	if st.CompactionID != "5f0c1d2e-0006-4000-8000-000000000006" {
		t.Fatalf("compaction = %q", st.CompactionID)
	}
	tr, found, err := ReadJunieCompaction("session-260901-101500-k3m9")
	if err != nil || !found {
		t.Fatalf("compaction: %v %v", found, err)
	}
	if tr.Workspace != "/work/retry-demo" || tr.Harness != "junie" || tr.Fingerprint == "" {
		t.Fatalf("transcript = %+v", tr)
	}
	// The prompt that started the task is logged before the compaction it
	// set off; the answer after it is not part of the capture.
	for _, m := range tr.Session.Messages {
		if strings.Contains(m.Text, "delay doubles") {
			t.Fatalf("capture kept a turn from after the compaction: %q", m.Text)
		}
	}
}

func TestJunieTurnSeesALiveFailure(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_JUNIE_ROOT", root)
	dir := filepath.Join(root, "session-261001-090000-ab12")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	log := `{"kind":"UserPromptEvent","prompt":"run the tests","timestampMs":1790000000000}
{"kind":"SessionA2uxEvent","event":{"state":"IN_PROGRESS","agentEvent":{"kind":"TerminalBlockUpdatedEvent","stepId":"s1","status":"IN_PROGRESS","command":"make test"}},"timestampMs":1790000001000}
{"kind":"SessionA2uxEvent","event":{"state":"IN_PROGRESS","agentEvent":{"kind":"TerminalBlockUpdatedEvent","stepId":"s1","status":"FAILED","command":"make test","output":"undefined: retryBudget","exitCode":2}},"timestampMs":1790000002000}
`
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	st := JunieTurn("session-261001-090000-ab12")
	if st.FailureID != "s1" || st.FailureCommand != "make test" || st.Failure != "undefined: retryBudget" {
		t.Fatalf("turn = %+v", st)
	}
	if _, found, _ := ReadJunieCompaction("session-261001-090000-ab12"); found {
		t.Fatal("found a compaction in a log with none")
	}
}

func TestJunieHeadlessTaskComesFromState(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_JUNIE_ROOT", root)
	dir := filepath.Join(root, "session-261001-100000-cd34")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	log := `{"kind":"SessionA2uxEvent","event":{"state":"IN_PROGRESS","agentEvent":{"kind":"MarkdownBlockUpdatedEvent","stepId":"m1","text":"The cache key ignores the locale."}},"timestampMs":1790000005000}
`
	state := `{"event":{"agentEvent":{"blob":"{\"lastAgentState\":{\"issue\":{\"description\":\"<additional_context>\\nrecalled\\n</additional_context>\\nwhy is the cache stale\"}}}"}}}`
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseJunieFile(filepath.Join(dir, "events.jsonl"))
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	if m := ss[0].Messages[0]; m.Role != "user" || m.Text != "why is the cache stale" {
		t.Fatalf("first record = %+v", m)
	}
}
