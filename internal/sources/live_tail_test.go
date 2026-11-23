package sources

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func isolateLiveTailEnv(t *testing.T) string {
	t.Helper()
	tmp := hermeticSourcesEnv(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg-config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "xdg-data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(tmp, "xdg-state"))
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))
	t.Setenv("DEJA_QODER_ROOT", filepath.Join(tmp, "qoder"))
	t.Setenv("DEJA_CHERRYSTUDIO_ROOTS", filepath.Join(tmp, "cherrystudio"))
	t.Setenv("DEJA_EXCLUDED_HARNESSES", "")
	return tmp
}

func TestReadLiveTranscriptTailMessagesClaudeAndVariants(t *testing.T) {
	tmp := isolateLiveTailEnv(t)
	claudeSID := "11111111-2222-4333-8444-555555555555"
	claudeDir := filepath.Join(tmp, "claude", claudeProjectsDirName(), "-Users-alex-proj")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	claudePath := filepath.Join(claudeDir, claudeSID+".jsonl")
	claudeBody := strings.Join([]string{
		`{"type":"user","uuid":"u1","sessionId":"` + claudeSID + `","cwd":"/Users/alex/proj","timestamp":"2026-11-23T12:00:00Z","message":{"role":"user","content":"first question"}}`,
		`{"type":"assistant","uuid":"a1","sessionId":"` + claudeSID + `","cwd":"/Users/alex/proj","timestamp":"2026-11-23T12:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"   "}]}}`,
		`{"type":"assistant","uuid":"a2","sessionId":"` + claudeSID + `","cwd":"/Users/alex/proj","timestamp":"2026-11-23T12:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"first answer"}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(claudePath, []byte(claudeBody), 0o600); err != nil {
		t.Fatal(err)
	}

	msgs, ok := ReadLiveTranscriptTailMessages("claude", claudePath, claudeSID)
	if !ok || len(msgs) != 2 {
		t.Fatalf("claude direct read = (%d msgs, %v), want (2, true)", len(msgs), ok)
	}
	if msgs[0].Role != "user" || msgs[0].Text != "first question" || msgs[1].Role != "assistant" || msgs[1].Text != "first answer" {
		t.Fatalf("unexpected claude messages: %+v", msgs)
	}

	byIDMsgs, ok := ReadLiveTranscriptTailMessages("claude", "", claudeSID)
	if !ok || len(byIDMsgs) != 2 {
		t.Fatalf("claude id lookup = (%d msgs, %v), want (2, true)", len(byIDMsgs), ok)
	}

	// Qoder and CherryStudio keep Claude-shaped lines under their own roots;
	// live tail requests tagged "claude" accept both refined harnesses.
	qoderPath := filepath.Join(tmp, "qoder", "projects", "-Users-alex-proj", claudeSID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(qoderPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(qoderPath, []byte(claudeBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, qok := ReadLiveTranscriptTailMessages("claude", qoderPath, claudeSID); !qok || len(got) != 2 {
		t.Fatalf("qoder under claude expectation = (%d, %v), want (2, true)", len(got), qok)
	}

	cherryPath := filepath.Join(tmp, "cherrystudio", "-Users-alex-proj", claudeSID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(cherryPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cherryPath, []byte(claudeBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, cok := ReadLiveTranscriptTailMessages("claude", cherryPath, claudeSID); !cok || len(got) != 2 {
		t.Fatalf("cherrystudio direct under claude expectation = (%d, %v), want (2, true)", len(got), cok)
	}
	if !liveTailHarnessMatches("claude", "cherrystudio") || liveTailHarnessMatches("codex", "claude") {
		t.Fatal("liveTailHarnessMatches mismatch")
	}

	// Qwen uses sessionId + message.parts, handled via qwenShapedHead fallback.
	qwenSID := "22222222-3333-4444-8555-666666666666"
	qwenPath := filepath.Join(tmp, "qwen", "projects", "-Users-alex-proj", "chats", qwenSID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(qwenPath), 0o755); err != nil {
		t.Fatal(err)
	}
	qwenBody := strings.Join([]string{
		`{"uuid":"qu1","sessionId":"` + qwenSID + `","timestamp":"2026-11-23T12:00:00Z","cwd":"/Users/alex/proj","type":"user","message":{"role":"user","parts":[{"text":"hello qwen"}]}}`,
		`{"uuid":"qa1","sessionId":"` + qwenSID + `","timestamp":"2026-11-23T12:00:01Z","cwd":"/Users/alex/proj","type":"assistant","message":{"role":"assistant","parts":[{"text":"qwen reply"}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(qwenPath, []byte(qwenBody), 0o600); err != nil {
		t.Fatal(err)
	}
	qwenMsgs, ok := ReadLiveTranscriptTailMessages("qwen", qwenPath, qwenSID)
	if !ok || len(qwenMsgs) != 2 || qwenMsgs[0].Text != "hello qwen" {
		t.Fatalf("qwen live tail = (%+v, %v)", qwenMsgs, ok)
	}
}

func TestReadLiveTranscriptTailMessagesLargeCodexAndHead(t *testing.T) {
	tmp := isolateLiveTailEnv(t)
	codexRoot := filepath.Join(tmp, "codex")
	t.Setenv("CODEX_HOME", codexRoot)

	sid := "019d7777-aaaa-4000-8000-000000000099"
	dayDir := filepath.Join(codexRoot, "sessions", "2026", "11", "23")
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rolloutPath := filepath.Join(dayDir, "rollout-2026-11-23T12-00-00-"+sid+".jsonl")
	f, err := os.Create(rolloutPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	// Large (> LiveTranscriptTailBytes) rollout with oversized header (> 64 KiB)
	// that overlaps tailOffset (size ~280 KiB so header ~90 KiB > tailOffset ~24 KiB).
	metaLine := fmt.Sprintf(
		`{"timestamp":"2026-11-23T12:00:00Z","type":"session_meta","payload":{"id":%q,"cwd":"/Users/alex/proj","timestamp":"2026-11-23T12:00:00Z","base_instructions":{"text":%q}}}`+"\n",
		sid, strings.Repeat("m", 90<<10),
	)
	if _, werr := f.WriteString(metaLine); werr != nil {
		t.Fatal(werr)
	}
	padLine := fmt.Sprintf(
		`{"timestamp":"2026-11-23T12:01:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"pad %s"}]}}`+"\n",
		strings.Repeat("p", 4096),
	)
	for written := int64(len(metaLine)); written < LiveTranscriptTailBytes+(20<<10); written += int64(len(padLine)) {
		if _, werr := f.WriteString(padLine); werr != nil {
			t.Fatal(werr)
		}
	}
	tailTurn := `{"timestamp":"2026-11-23T12:59:58Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"LIVE_TAIL_RECENT_USER"}]}}` + "\n" +
		`{"timestamp":"2026-11-23T12:59:59Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"LIVE_TAIL_RECENT_ASST"}]}}` + "\n" +
		`{"timestamp":"2026-11-23T13:00:00`
	if _, werr := f.WriteString(tailTurn); werr != nil {
		t.Fatal(werr)
	}
	if cerr := f.Close(); cerr != nil {
		t.Fatal(cerr)
	}

	msgs, ok := ReadLiveTranscriptTailMessages("codex", rolloutPath, sid)
	if !ok {
		t.Fatal("ReadLiveTranscriptTailMessages(codex, path) returned false")
	}
	var joined strings.Builder
	for _, m := range msgs {
		joined.WriteString(m.Text)
		joined.WriteByte('\n')
	}
	if !strings.Contains(joined.String(), "LIVE_TAIL_RECENT_USER") || !strings.Contains(joined.String(), "LIVE_TAIL_RECENT_ASST") {
		t.Fatalf("missing recent turns in codex live tail:\n%s", joined.String())
	}

	// Second large file exercising aligned non-overlapping tail (> 400 KiB with small header).
	sidAligned := "019d7777-bbbb-4000-8000-000000000100"
	alignedPath := filepath.Join(dayDir, "rollout-2026-11-23T13-00-00-"+sidAligned+".jsonl")
	af, err := os.Create(alignedPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = af.Close() }()
	smallMeta := fmt.Sprintf(`{"timestamp":"2026-11-23T13:00:00Z","type":"session_meta","payload":{"id":%q,"cwd":"/Users/alex/proj","timestamp":"2026-11-23T13:00:00Z"}}`+"\n", sidAligned)
	if _, werr := af.WriteString(smallMeta); werr != nil {
		t.Fatal(werr)
	}
	for written := int64(len(smallMeta)); written < LiveTranscriptTailBytes+(150<<10); written += int64(len(padLine)) {
		if _, werr := af.WriteString(padLine); werr != nil {
			t.Fatal(werr)
		}
	}
	if _, werr := af.WriteString(`{"timestamp":"2026-11-23T13:59:58Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"ALIGNED_TAIL_MARKER"}]}}` + "\n"); werr != nil {
		t.Fatal(werr)
	}
	if cerr := af.Close(); cerr != nil {
		t.Fatal(cerr)
	}

	alignedMsgs, ok := ReadLiveTranscriptTailMessages("codex", "", sidAligned)
	if !ok {
		t.Fatal("ReadLiveTranscriptTailMessages(codex, id) returned false")
	}
	if len(alignedMsgs) == 0 || alignedMsgs[len(alignedMsgs)-1].Text != "ALIGNED_TAIL_MARKER" {
		t.Fatalf("aligned tail did not end with marker: %+v", alignedMsgs[len(alignedMsgs)-1])
	}

	// CodexSessionHead covers empty, missing, and valid session id.
	if _, emptyOK := CodexSessionHead(""); emptyOK {
		t.Fatal("CodexSessionHead(\"\") returned true")
	}
	if _, missOK := CodexSessionHead("019d7777-0000-4000-8000-000000000000"); missOK {
		t.Fatal("CodexSessionHead(missing) returned true")
	}
	headSess, ok := CodexSessionHead(sidAligned)
	if !ok || headSess.ID != sidAligned {
		t.Fatalf("CodexSessionHead(valid) = (%q, %v), want (%q, true)", headSess.ID, ok, sidAligned)
	}
}

func TestReadLiveTranscriptTailMessagesRejectsInvalidInputs(t *testing.T) {
	tmp := isolateLiveTailEnv(t)
	sid := "33333333-4444-4555-8666-777777777777"
	validPath := filepath.Join(tmp, "claude", claudeProjectsDirName(), "-Users-alex-proj", sid+".jsonl")
	if err := os.MkdirAll(filepath.Dir(validPath), 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","uuid":"u1","sessionId":"` + sid + `","timestamp":"2026-11-23T12:00:00Z","message":{"role":"user","content":"hi"}}` + "\n"
	if err := os.WriteFile(validPath, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}

	emptyFile := filepath.Join(tmp, "empty.jsonl")
	if err := os.WriteFile(emptyFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	noMsgsFile := filepath.Join(tmp, "nomsgs.jsonl")
	if err := os.WriteFile(noMsgsFile, []byte(`{"type":"user","uuid":"u1","sessionId":"`+sid+`","timestamp":"2026-11-23T12:00:00Z","message":{"role":"user","content":"   "}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	unrecognizedFile := filepath.Join(tmp, "unknown.jsonl")
	if err := os.WriteFile(unrecognizedFile, []byte(`{"foo":"bar"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		harness string
		path    string
		sid     string
	}{
		{"trae blocked", "trae", validPath, sid},
		{"unsupported harness", "aider", validPath, sid},
		{"no path or sid", "claude", "", ""},
		{"missing path", "claude", filepath.Join(tmp, "missing.jsonl"), sid},
		{"directory path", "claude", tmp, sid},
		{"empty file", "claude", emptyFile, sid},
		{"unrecognized format", "claude", unrecognizedFile, sid},
		{"harness mismatch", "codex", validPath, sid},
		{"sid mismatch", "claude", validPath, "other-sid"},
		{"whitespace-only messages", "claude", noMsgsFile, sid},
	}
	for _, tc := range cases {
		if msgs, ok := ReadLiveTranscriptTailMessages(tc.harness, tc.path, tc.sid); ok || len(msgs) != 0 {
			t.Errorf("%s: got (%d msgs, %v), want (nil, false)", tc.name, len(msgs), ok)
		}
	}

	if runtime.GOOS != "windows" {
		linkPath := filepath.Join(tmp, "link.jsonl")
		if err := os.Symlink(validPath, linkPath); err != nil {
			t.Fatal(err)
		}
		if msgs, ok := ReadLiveTranscriptTailMessages("claude", linkPath, sid); ok || len(msgs) != 0 {
			t.Fatalf("symlink path: got (%d msgs, %v), want (nil, false)", len(msgs), ok)
		}
	}
}
