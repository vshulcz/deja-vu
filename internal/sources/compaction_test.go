package sources

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadCompactionTranscriptClaudeUsesNativeCalls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread.jsonl")
	body := strings.Join([]string{
		`{"type":"user","sessionId":"claude-1","timestamp":"2026-09-10T10:00:00Z","message":{"role":"user","content":"fix the retry"}}`,
		`{"type":"assistant","sessionId":"claude-1","timestamp":"2026-09-10T10:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"I will inspect it."},{"type":"tool_use","id":"read-1","name":"Read","input":{"file_path":"retry.go"}},{"type":"tool_use","id":"edit-1","name":"Edit","input":{"file_path":"retry.go"}}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCompactionTranscript(path, "claude-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Harness != "claude" || got.Session.ID != "claude-1" || got.Session.Path != path {
		t.Fatalf("identity=%#v", got)
	}
	if got.Truncated || got.TailOffset != 0 || got.SourceSize != int64(len(body)) || got.Fingerprint == "" || got.HeaderFingerprint == "" {
		t.Fatalf("bad bounded metadata: %#v", got)
	}
	if len(got.ToolCalls) != 2 || got.ToolCalls[0].ID != "read-1" || got.ToolCalls[1].ID != "edit-1" || got.LastToolID != "edit-1" {
		t.Fatalf("native calls=%#v", got.ToolCalls)
	}
	if got.ToolCalls[0].StartOffset >= got.ToolCalls[0].EndOffset {
		t.Fatalf("invalid offsets: %#v", got.ToolCalls[0])
	}
	if !MatchesCompactionBoundary(got, got.SourceSize, got.BoundaryFingerprint) {
		t.Fatal("capture should match its own source boundary")
	}
}

func TestReadCompactionTranscriptClaudeRecognizesFileHistoryPreamble(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread.jsonl")
	body := strings.Join([]string{
		`{"type":"file-history-snapshot","sessionId":"claude-1","cwd":"/work/widget","message":{"content":"snapshot"}}`,
		// A normal user string used to make the raw-call decoder fail before it
		// validated identity or cwd.
		`{"type":"user","sessionId":"claude-1","cwd":"/work/widget","message":{"role":"user","content":"fix it"}}`,
		`{"type":"assistant","sessionId":"claude-1","cwd":"/work/widget","message":{"role":"assistant","content":[{"type":"tool_use","id":"same-call","name":"Read"}]}}`,
		// Claude may replay a record while streaming. It remains one native call.
		`{"type":"assistant","sessionId":"claude-1","cwd":"/work/widget","message":{"role":"assistant","content":[{"type":"tool_use","id":"same-call","name":"Read"}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCompactionTranscript(path, "claude-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Harness != "claude" || got.Workspace != "/work/widget" || got.Session.Project != "widget" {
		t.Fatalf("preamble attribution=%#v", got)
	}
	if len(got.ToolCalls) != 1 || got.ToolCalls[0].ID != "same-call" {
		t.Fatalf("replayed calls=%#v", got.ToolCalls)
	}
}

func TestReadCompactionTranscriptMarksMalformedWindowUnmeasured(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread.jsonl")
	body := strings.Join([]string{
		`{"type":"user","sessionId":"claude-1","message":{"role":"user","content":"start"}}`,
		`not-json`,
		`{"type":"assistant","sessionId":"claude-1","message":{"role":"assistant","content":"finished"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCompactionTranscript(path, "claude-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.MetricComplete || !got.Truncated {
		t.Fatalf("malformed capture must be partial and unmeasured: %#v", got)
	}
}

func TestReadCompactionTranscriptRejectsForeignSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread.jsonl")
	line := `{"type":"user","sessionId":"claude-1","message":{"role":"user","content":"hello"}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ReadCompactionTranscript(path, "someone-else")
	if !errors.Is(err, ErrTranscriptIdentity) {
		t.Fatalf("err=%v, want identity mismatch", err)
	}
}

func TestReadCompactionTranscriptAcceptsStableFinalRecordWithoutNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread.jsonl")
	line := `{"type":"user","sessionId":"claude-1","message":{"role":"user","content":"hello"}}`
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCompactionTranscript(path, "claude-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Session.Messages) != 1 || got.Session.Messages[0].Text != "hello" {
		t.Fatalf("final record was dropped: %#v", got.Session)
	}
}

func TestReadCompactionTranscriptDoesNotNeedTemporaryDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread.jsonl")
	line := `{"type":"user","sessionId":"claude-1","message":{"role":"user","content":"hello"}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "does-not-exist"))
	if _, err := ReadCompactionTranscript(path, "claude-1"); err != nil {
		t.Fatalf("bounded in-memory capture depended on TMPDIR: %v", err)
	}
}

func TestReadCompactionTranscriptRejectsForeignClaudeStringContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread.jsonl")
	body := strings.Join([]string{
		`{"type":"file-history-snapshot","sessionId":"claude-1","cwd":"/work/widget"}`,
		`{"type":"user","sessionId":"someone-else","cwd":"/work/widget","message":{"role":"user","content":"plain text"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ReadCompactionTranscript(path, "claude-1")
	if !errors.Is(err, ErrTranscriptIdentity) {
		t.Fatalf("err=%v, want identity mismatch", err)
	}
}

func TestReadCompactionTranscriptCodexSkipsDuplicatedEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-test.jsonl")
	body := strings.Join([]string{
		`{"timestamp":"2026-09-10T10:00:00Z","type":"session_meta","payload":{"id":"thread-1","session_id":"session-1","cwd":"/work/app"}}`,
		`{"timestamp":"2026-09-10T10:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fix it"}]}}`,
		`{"timestamp":"2026-09-10T10:00:02Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"call-1","arguments":"ignored"}}`,
		`{"timestamp":"2026-09-10T10:00:03Z","type":"event_msg","payload":{"type":"function_call","name":"exec_command","call_id":"call-1"}}`,
		`{"timestamp":"2026-09-10T10:00:04Z","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","call_id":"call-2","input":"*** Begin Patch"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCompactionTranscript(path, "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Harness != "codex" || len(got.ToolCalls) != 2 {
		t.Fatalf("capture=%#v", got)
	}
	if got.ToolCalls[0].ID != "call-1" || got.ToolCalls[1].ID != "call-2" {
		t.Fatalf("calls=%#v", got.ToolCalls)
	}
}

func TestReadCompactionTranscriptBoundsLargeClaudeTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(`{"type":"user","sessionId":"claude-1","message":{"role":"user","content":"old"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	padding := strings.Repeat("x", 700)
	for i := 0; i < 7000; i++ {
		if _, err := fmt.Fprintf(f, `{"type":"assistant","sessionId":"claude-1","message":{"role":"assistant","content":"%s-%d"}}`+"\n", padding, i); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.WriteString(`{"type":"assistant","sessionId":"claude-1","message":{"role":"assistant","content":[{"type":"tool_use","id":"latest","name":"Read"}]}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCompactionTranscript(path, "claude-1")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Truncated || got.TailOffset == 0 || got.SourceSize <= CompactionTailBytes || len(got.ToolCalls) == 0 || got.LastToolID != "latest" {
		t.Fatalf("large capture=%#v", got)
	}
	if got.ToolCalls[len(got.ToolCalls)-1].StartOffset < got.TailOffset {
		t.Fatalf("tool offset before bounded tail: %#v", got.ToolCalls)
	}
}

func TestReadCompactionTranscriptKeepsTailRecordAfterHeaderOverlap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread.jsonl")
	header := `{"type":"user","sessionId":"claude-1","message":{"role":"user","content":"start"}}` + "\n"
	call := `{"type":"assistant","sessionId":"claude-1","message":{"role":"assistant","content":[{"type":"tool_use","id":"overlap","name":"Read"}]}}` + "\n"
	// The tail begins inside the header, then starts exactly at the first
	// assistant record after the overlap is removed.
	total := CompactionTailBytes + 80
	padding := strings.Repeat("\n", int(total)-len(header)-len(call))
	if err := os.WriteFile(path, []byte(header+call+padding), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCompactionTranscript(path, "claude-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ToolCalls) != 1 || got.ToolCalls[0].ID != "overlap" {
		t.Fatalf("tail lost first complete record after header overlap: %#v", got.ToolCalls)
	}
}

func TestReadCompactionTranscriptRejectsOversizedLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "too-wide.jsonl")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", int(CompactionTailBytes)+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ReadCompactionTranscript(path, "claude-1")
	if !errors.Is(err, ErrTranscriptLineTooLarge) {
		t.Fatalf("err=%v, want oversized line", err)
	}
}

func TestCountActionsBeforeEditFailsWhenIntervalFallsOutsideTail(t *testing.T) {
	current := CompactionTranscript{NativeSessionID: "s", SourceSize: 100, TailOffset: 51, MetricComplete: true}
	if got, ok := CountActionsBeforeEdit(current, 50, "prior", "pending"); ok || got != 0 {
		t.Fatalf("got (%d,%t), want unmeasured", got, ok)
	}
	current.TailOffset = 0
	current.ToolCalls = []TranscriptToolCall{
		{ID: "read", Name: "Read", StartOffset: 50},
		{ID: "write", Name: "Write", StartOffset: 70},
	}
	if got, ok := CountActionsBeforeEdit(current, 50, "prior", "pending"); !ok || got != 1 {
		t.Fatalf("got (%d,%t), want one action", got, ok)
	}
}

func TestExplicitEditToolRecognizesNamespacedCodexCalls(t *testing.T) {
	if !IsCompactionEditTool("functions.apply_patch") || !IsCompactionEditTool("functions.Write") || !IsCompactionEditTool("replace_in_file") {
		t.Fatal("namespaced native edit call was not recognized")
	}
}

func TestMatchesCompactionBoundaryRejectsRewrittenTail(t *testing.T) {
	before := CompactionTranscript{SourceSize: 10, TailOffset: 0, tail: []byte("0123456789")}
	before.BoundaryFingerprint = compactionBoundaryFingerprint(before.tail, before.TailOffset, before.SourceSize)
	if !MatchesCompactionBoundary(before, before.SourceSize, before.BoundaryFingerprint) {
		t.Fatal("self boundary did not match")
	}
	after := before
	after.SourceSize = 12
	after.tail = []byte("01234xxxxxZZ")
	if MatchesCompactionBoundary(after, before.SourceSize, before.BoundaryFingerprint) {
		t.Fatal("rewritten boundary was accepted")
	}
}
