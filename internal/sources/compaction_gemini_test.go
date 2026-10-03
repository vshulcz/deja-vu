package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Records as Gemini CLI 0.60 writes them, taken from a real run against a
// stub API: a failing go test, a resume, go vet, then a compression.
func gTurn(id, typ string, content any) map[string]any {
	return map[string]any{"id": id, "timestamp": "2026-10-03T11:49:22.587Z", "type": typ, "content": content}
}

func gText(s string) []any { return []any{map[string]any{"text": s}} }

func gShell(id, command, output string) map[string]any {
	m := gTurn(id, "gemini", "")
	m["toolCalls"] = []any{map[string]any{
		"id": "call-" + id, "name": "run_shell_command", "args": map[string]any{"command": command},
		"result": []any{map[string]any{"functionResponse": map[string]any{
			"id": "call-" + id, "name": "run_shell_command",
			"response": map[string]any{"output": "<untrusted_context>\nOutput: " + output + "\n</untrusted_context>"},
		}}},
		"status": "success",
	}}
	return m
}

// geminiCall is the same turn the way a snapshot lists it: history-shaped, the
// call in the content and no toolCalls.
func gCall(id, command string) map[string]any {
	return gTurn(id, "gemini", []any{map[string]any{"functionCall": map[string]any{"name": "run_shell_command", "args": map[string]any{"command": command}}}})
}

func gSet(turns ...map[string]any) map[string]any {
	list := make([]any, len(turns))
	for i, t := range turns {
		list[i] = t
	}
	return map[string]any{"$set": map[string]any{"messages": list}}
}

var (
	geminiHeader = map[string]any{"sessionId": "g-1", "projectHash": "h", "startTime": "2026-10-03T11:49:20Z", "lastUpdated": "2026-10-03T11:49:20Z", "kind": "main"}
	geminiCtx    = gTurn("ctx", "user", gText("<session_context>\nThis is the Gemini CLI."))
	geminiAsk    = gTurn("u1", "user", gText("fix the parser test"))
	geminiTest   = gShell("g1", "go test ./parser/...", "--- FAIL: TestParseSeed (0.00s)\n    parse_test.go:7: want 3, got 4\nFAIL\nExit Code: 1")
	geminiSaid   = gTurn("g2", "gemini", "The parser test fails: want 3, got 4. I decided to fix parse.go next.")
	geminiVetAsk = gTurn("u2", "user", gText("run vet"))
	geminiVet    = gShell("g3", "go vet ./...", "(empty)")
	geminiResume = []map[string]any{
		geminiHeader, gSet(geminiCtx), {"$set": map[string]any{"sessionId": "g-1"}},
		gSet(geminiCtx, geminiAsk, gCall("g1", "go test ./parser/..."), geminiSaid),
	}
	geminiSummary = gTurn("s1", "user", gText("<state_snapshot><overall_goal>fix parser</overall_goal></state_snapshot>"))
	geminiAck     = gTurn("s2", "gemini", "Got it. Thanks for the additional context!")
)

func writeGeminiChat(t *testing.T, records ...map[string]any) (path, workspace string) {
	t.Helper()
	root := t.TempDir()
	workspace = filepath.Join(root, "proj")
	idDir := filepath.Join(root, "gemini", "tmp", "proj")
	if err := os.MkdirAll(filepath.Join(idDir, "chats"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idDir, ".project_root"), []byte(workspace), 0o644); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, r := range records {
		line, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	path = filepath.Join(idDir, "chats", "session-2026-10-03T11-49-g1.jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path, workspace
}

func geminiSessionText(tr CompactionTranscript) string {
	var b strings.Builder
	for _, m := range tr.Session.Messages {
		b.WriteString(m.Role + ": " + m.Text + "\n")
	}
	return b.String()
}

func TestGeminiCompactionIsFoundWithTheTurnsBeforeIt(t *testing.T) {
	records := append([]map[string]any{geminiHeader, gSet(geminiCtx), geminiAsk, geminiTest, geminiSaid}, geminiResume...)
	records = append(records, geminiVetAsk, geminiVet,
		map[string]any{"$set": map[string]any{"sessionId": "g-1"}},
		gSet(geminiCtx, geminiSummary, geminiAck, gTurn("k1", "user", gText("run vet")), gCall("k2", "go vet ./...")),
		gTurn("g4", "gemini", "go vet is clean."))
	path, workspace := writeGeminiChat(t, records...)
	tr, found, err := ReadGeminiCompaction(path, "g-1")
	if err != nil || !found {
		t.Fatalf("compaction not found: found=%v err=%v", found, err)
	}
	text := geminiSessionText(tr)
	for _, want := range []string{"fix the parser test", "$ go test ./parser/...  → exit 1", "want 3, got 4", "$ go vet ./...", "run vet"} {
		if !strings.Contains(text, want) {
			t.Errorf("captured session lacks %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"state_snapshot", "go vet is clean"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("captured session holds %q from after the compaction:\n%s", unwanted, text)
		}
	}
	if tr.Workspace != workspace || tr.Harness != "gemini" {
		t.Fatalf("identity: workspace %q harness %q", tr.Workspace, tr.Harness)
	}
	// The same compaction read again after more turns is the same capture.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString(`{"id":"g5","type":"gemini","content":"later"}` + "\n")
	_ = f.Close()
	again, found, err := ReadGeminiCompaction(path, "g-1")
	if err != nil || !found || again.Fingerprint != tr.Fingerprint {
		t.Fatalf("a later turn changed the capture: %v %v %q vs %q", found, err, again.Fingerprint, tr.Fingerprint)
	}
	if _, _, err := ReadGeminiCompaction(path, "g-2"); err == nil {
		t.Fatal("another session id read this transcript")
	}
}

func TestGeminiRewritesThatAreNotCompactions(t *testing.T) {
	base := []map[string]any{geminiHeader, gSet(geminiCtx), geminiAsk, geminiTest, geminiSaid}
	cases := map[string][]map[string]any{
		// A resume writes the opening context alone, then the history back.
		"resume": append(append([]map[string]any{}, base...), geminiResume...),
		// Masking rewrites what a turn says and keeps every turn.
		"masking": append(append([]map[string]any{}, base...), gSet(geminiCtx, geminiAsk, gCall("g1", "go test ./parser/..."), geminiSaid)),
		// A rewind cuts the end.
		"rewind": append(append([]map[string]any{}, base...), map[string]any{"$rewindTo": "g2"}),
	}
	for name, records := range cases {
		path, _ := writeGeminiChat(t, records...)
		if _, found, err := ReadGeminiCompaction(path, "g-1"); err != nil || found {
			t.Errorf("%s read as a compaction: found=%v err=%v", name, found, err)
		}
	}
}

// Later builds append the summary turns and then a $patch that takes the old
// ones out (services/chatRecordingService.ts updateMessagesFromHistory).
func TestGeminiPatchCompactionStopsBeforeTheSummaryTurns(t *testing.T) {
	path, _ := writeGeminiChat(t, geminiHeader, geminiAsk, geminiTest, geminiSaid, geminiSummary, geminiAck,
		map[string]any{"$patch": map[string]any{"removeIds": []string{"u1", "g1", "g2"}, "orderIds": []string{"s1", "s2"}}})
	tr, found, err := ReadGeminiCompaction(path, "g-1")
	if err != nil || !found {
		t.Fatalf("patch compaction not found: found=%v err=%v", found, err)
	}
	text := geminiSessionText(tr)
	if !strings.Contains(text, "go test ./parser/...") || strings.Contains(text, "state_snapshot") || strings.Contains(text, "Got it") {
		t.Fatalf("boundary misplaced:\n%s", text)
	}
}

// A resume used to cost a session every command before it: the snapshot it
// writes lists the call in the turn's content, not in toolCalls.
func TestGeminiResumeKeepsTheCommandsBeforeIt(t *testing.T) {
	records := append([]map[string]any{geminiHeader, gSet(geminiCtx), geminiAsk, geminiTest, geminiSaid}, geminiResume...)
	path, _ := writeGeminiChat(t, records...)
	ss, err := ParseGeminiFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	var text strings.Builder
	for _, m := range ss[0].Messages {
		text.WriteString(m.Role + ": " + m.Text + "\n")
	}
	if !strings.Contains(text.String(), "command: $ go test ./parser/...  → exit 1") {
		t.Fatalf("the resume dropped the command:\n%s", text.String())
	}
}
