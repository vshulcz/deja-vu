package hookengine

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type heldPipeReader struct {
	head []byte
	pos  int
	stop chan struct{}
}

func (p *heldPipeReader) Read(dst []byte) (int, error) {
	if p.pos < len(p.head) {
		n := copy(dst, p.head[p.pos:])
		p.pos += n
		return n, nil
	}
	<-p.stop
	return 0, io.EOF
}

type splitChunkReader struct {
	parts [][]byte
	idx   int
	stop  chan struct{}
}

func (p *splitChunkReader) Read(dst []byte) (int, error) {
	if p.idx < len(p.parts) {
		n := copy(dst, p.parts[p.idx])
		p.idx++
		return n, nil
	}
	<-p.stop
	return 0, io.EOF
}

type blockingReader struct {
	prefix []byte
	sent   bool
	stop   chan struct{}
}

func (b *blockingReader) Read(dst []byte) (int, error) {
	if !b.sent && len(b.prefix) > 0 {
		b.sent = true
		n := copy(dst, b.prefix)
		return n, nil
	}
	<-b.stop
	return 0, io.EOF
}

func TestReadPayloadAndReadBounded(t *testing.T) {
	t.Run("held_pipe_returns_early_on_valid_json_and_strips_bom", func(t *testing.T) {
		stop := make(chan struct{})
		defer close(stop)
		raw := []byte(`{"prompt":"hello world"}`)
		in := append([]byte{0xEF, 0xBB, 0xBF}, raw...)
		start := time.Now()
		got := ReadPayload(&heldPipeReader{head: in, stop: stop}, 2*time.Second)
		elapsed := time.Since(start)
		if elapsed >= time.Second {
			t.Fatalf("ReadPayload waited for deadline (%v) despite valid JSON", elapsed)
		}
		if string(got) != string(raw) {
			t.Fatalf("got %q, want %q", got, raw)
		}
	})

	t.Run("split_chunks_accumulate_until_valid_json", func(t *testing.T) {
		stop := make(chan struct{})
		defer close(stop)
		r := &splitChunkReader{
			parts: [][]byte{[]byte(`{"session_id":`), []byte(`"s1","ok":true}`)},
			stop:  stop,
		}
		got := ReadPayload(r, time.Second)
		if string(got) != `{"session_id":"s1","ok":true}` {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("caps_at_one_mib", func(t *testing.T) {
		big := bytes.Repeat([]byte("a"), (1<<20)+4096)
		got := ReadBounded(bytes.NewReader(big), time.Second, false)
		if len(got) != 1<<20 {
			t.Fatalf("len = %d, want %d", len(got), 1<<20)
		}
	})

	t.Run("timeout_keeps_partial_bytes_and_stop_at_value_false_drains_all", func(t *testing.T) {
		stop := make(chan struct{})
		defer close(stop)
		got := ReadBounded(&blockingReader{prefix: []byte(`{"partial":`), stop: stop}, 25*time.Millisecond, true)
		if string(got) != `{"partial":` {
			t.Fatalf("timeout partial = %q, want %q", got, `{"partial":`)
		}

		two := `{"a":1}` + "\n" + `{"b":2}`
		drained := ReadBounded(strings.NewReader(two), time.Second, false)
		if string(drained) != two {
			t.Fatalf("drained = %q, want %q", drained, two)
		}
	})
}

func TestDecodePromptTextAndNormalizePromptEvent(t *testing.T) {
	if got := DecodePromptText(json.RawMessage(`"hello <hook_context>secret</hook_context> world"`)); got != "hello  world" {
		t.Fatalf("string prompt = %q, want %q", got, "hello  world")
	}
	kimi := json.RawMessage(`[{"type":"text","text":"first <deja-recall>old</deja-recall>"},{"type":"image","text":""},{"type":"text","text":"second &lt;hook_context&gt;noise&lt;/hook_context&gt;"}]`)
	if got := DecodePromptText(kimi); got != "first \nsecond" {
		t.Fatalf("kimi prompt = %q, want %q", got, "first \nsecond")
	}
	if got := DecodePromptText(json.RawMessage(`12345`)); got != "" {
		t.Fatalf("unknown shape = %q, want empty", got)
	}

	if _, ok := NormalizePromptEvent([]byte(`{bad`)); ok {
		t.Fatal("expected ok=false for malformed prompt JSON")
	}

	grokPayload := []byte(`{
		"sessionId":"grok-sess",
		"conversationId":"conv-ignored",
		"transcriptPath":"/tmp/grok/transcript.jsonl",
		"workspaceRoot":"/tmp/grok-repo",
		"prompt":[{"type":"text","text":"<hook_context>prior</hook_context>\nfix flaky parser"}]
	}`)
	ev, ok := NormalizePromptEvent(grokPayload)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if ev.SessionID != "grok-sess" || ev.TranscriptPath != "/tmp/grok/transcript.jsonl" {
		t.Fatalf("grok adoption failed: %+v", ev)
	}
	if len(ev.WorkspaceRoots) != 1 || ev.WorkspaceRoots[0] != "/tmp/grok-repo" {
		t.Fatalf("grok workspace roots = %v, want [/tmp/grok-repo]", ev.WorkspaceRoots)
	}
	if ev.Prompt != "fix flaky parser" {
		t.Fatalf("prompt = %q, want %q", ev.Prompt, "fix flaky parser")
	}

	cursorPayload := []byte(`{
		"conversation_id":"cursor-conv",
		"workspace_roots":["/tmp/cursor-repo"],
		"prompt":"check build &lt;deja-recall&gt;stale&lt;/deja-recall&gt;"
	}`)
	cev, ok := NormalizePromptEvent(cursorPayload)
	if !ok || cev.SessionID != "cursor-conv" || len(cev.WorkspaceRoots) != 1 || cev.WorkspaceRoots[0] != "/tmp/cursor-repo" || cev.Prompt != "check build" {
		t.Fatalf("cursor normalization failed: ok=%v ev=%+v", ok, cev)
	}
}

func TestNormalizeToolEvent(t *testing.T) {
	if _, ok := NormalizeToolEvent([]byte(`not json`)); ok {
		t.Fatal("expected ok=false on invalid JSON")
	}

	grokTool := []byte(`{
		"sessionId":"s-grok",
		"transcriptPath":"/tmp/t.jsonl",
		"workspaceRoot":"/tmp/ws",
		"toolName":"shell_command",
		"toolInput":{"command":"go test","args":["./internal/hookengine","-count=1"]}
	}`)
	ev, ok := NormalizeToolEvent(grokTool)
	if !ok {
		t.Fatal("expected ok=true for grok tool")
	}
	if ev.ToolName != "shell_command" || ev.SessionID != "s-grok" || !strings.Contains(ev.ToolInput.Command, "go test ./internal/hookengine -count=1") {
		t.Fatalf("grok command normalization = %+v", ev)
	}

	traeTool := []byte(`{
		"llm_tool_name":"edit_file",
		"tool_input":{"path":"\"file:///tmp/my%20repo/app.go\"","Target":"oldA","Replacement":"newB"}
	}`)
	tev, ok := NormalizeToolEvent(traeTool)
	if !ok || tev.ToolName != "edit_file" || tev.ToolInput.FilePath != "/tmp/my repo/app.go" || tev.ToolInput.OldString != "oldA" || tev.ToolInput.NewString != "newB" {
		t.Fatalf("trae normalization failed: ok=%v ev=%+v", ok, tev)
	}

	copilotStringArgs := []byte(`{
		"tool_name":"apply_patch",
		"toolArgs":"{\"paths\":[\"/Users/alex/.claude/plans/x.md\",\"/tmp/real.go\"],\"edits\":[{\"oldText\":\"foo\",\"newText\":\"bar\"}]}"
	}`)
	cev, ok := NormalizeToolEvent(copilotStringArgs)
	if !ok || cev.ToolInput.FilePath != "/tmp/real.go" || cev.ToolInput.OldString != "foo" || cev.ToolInput.NewString != "bar" {
		t.Fatalf("copilot stringified toolArgs failed: ok=%v ev=%+v", ok, cev)
	}

	copilotObjectArgs := []byte(`{
		"tool_name":"edit",
		"toolArgs":{"Paths":["/tmp/scratchpad/ignored.md","/tmp/second.go"],"oldStr":"o1","newStr":"n1"}
	}`)
	oev, ok := NormalizeToolEvent(copilotObjectArgs)
	if !ok || oev.ToolInput.FilePath != "/tmp/second.go" || oev.ToolInput.OldString != "o1" || oev.ToolInput.NewString != "n1" {
		t.Fatalf("copilot object toolArgs failed: ok=%v ev=%+v", ok, oev)
	}

	dir := t.TempDir()
	literalColon := filepath.Join(dir, "keep:1")
	if err := os.WriteFile(literalColon, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	readStrip := []byte(`{"tool_name":"Read","cwd":"` + filepath.ToSlash(dir) + `","tool_input":{"file_path":"missing.go:10-20"}}`)
	rev, ok := NormalizeToolEvent(readStrip)
	if !ok || rev.ToolInput.FilePath != "missing.go" {
		t.Fatalf("omp selector strip failed: ok=%v path=%q", ok, rev.ToolInput.FilePath)
	}
	readRaw := []byte(`{"tool_name":"read_file","cwd":"` + filepath.ToSlash(dir) + `","tool_input":{"file_path":"raw.go:raw"}}`)
	rawEv, ok := NormalizeToolEvent(readRaw)
	if !ok || rawEv.ToolInput.FilePath != "raw.go" {
		t.Fatalf("omp :raw strip failed: ok=%v path=%q", ok, rawEv.ToolInput.FilePath)
	}
	readKeep := []byte(`{"tool_name":"Read","cwd":"` + filepath.ToSlash(dir) + `","tool_input":{"file_path":"keep:1"}}`)
	kev, ok := NormalizeToolEvent(readKeep)
	if !ok || kev.ToolInput.FilePath != "keep:1" {
		t.Fatalf("literal colon file on disk was stripped: ok=%v path=%q", ok, kev.ToolInput.FilePath)
	}

	editDialects := []byte(`{
		"tool_name":"replace_file_content",
		"tool_input":{
			"filePath":"/tmp/dialect.go",
			"TargetContent":"oldTC",
			"ReplacementContent":"newRC"
		}
	}`)
	dev, ok := NormalizeToolEvent(editDialects)
	if !ok || dev.ToolInput.OldString != "oldTC" || dev.ToolInput.NewString != "newRC" || dev.ToolInput.FilePath != "/tmp/dialect.go" {
		t.Fatalf("TargetContent dialect failed: ok=%v ev=%+v", ok, dev)
	}
}

func TestNormalizeAntigravityEvent(t *testing.T) {
	if _, ok := NormalizeAntigravityEvent(nil); ok {
		t.Fatal("empty payload should fail")
	}
	if _, ok := NormalizeAntigravityEvent([]byte(`{"invocationNum":`)); ok {
		t.Fatal("malformed payload should fail")
	}

	payload1510 := []byte(`{
		"invocationNum": 1,
		"conversationId": "conv-1510",
		"workspacePaths": [],
		"transcriptPath": "",
		"cwd": "/tmp/agy-repo",
		"lastUserInput": "<hook_context>prior</hook_context>\n<USER_REQUEST>\nwhy did tests hang\n</USER_REQUEST>"
	}`)
	ev, ok := NormalizeAntigravityEvent(payload1510)
	if !ok {
		t.Fatal("expected ok=true for 1.5.10 payload")
	}
	if got := ev.EffectiveWorkspace(""); got != "/tmp/agy-repo" {
		t.Fatalf("EffectiveWorkspace = %q, want /tmp/agy-repo", got)
	}
	if got := ev.PreferredPrompt("older transcript question"); got != "why did tests hang" {
		t.Fatalf("PreferredPrompt = %q, want %q", got, "why did tests hang")
	}

	ev.WorkspacePaths = []string{"/tmp/explicit-ws"}
	if got := ev.EffectiveWorkspace("/tmp/conv-fb"); got != "/tmp/explicit-ws" {
		t.Fatalf("workspacePaths must beat cwd and convFallback: got %q", got)
	}

	ev.LastUserInput = ""
	if got := ev.PreferredPrompt("fallback question"); got != "fallback question" {
		t.Fatalf("empty LastUserInput fallback = %q", got)
	}
}
