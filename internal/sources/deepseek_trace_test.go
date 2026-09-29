package sources

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestReadDeepSeekTrace_RootClean(t *testing.T) {
	log := `{"type":"session","version":3,"id":"session-root-001","createdAt":1700000000000,"cwd":"/workspace/app","isSeeded":false,"delegationDepth":0,"agentPreset":"standard"}
{"type":"turn/start","seq":0,"time":1700000001000,"data":{"turn":1}}
{"type":"step/start","seq":1,"time":1700000001100,"data":{"turn":1,"step":1}}
{"type":"user/message","seq":2,"time":1700000001200,"surfaceOp":"append","data":{"role":"user","id":"msg-u1","source":{"kind":"user"},"content":"hello world"}}
{"type":"assistant/message","seq":3,"time":1700000002000,"surfaceOp":"append","data":{"turn":1,"step":1,"message":{"role":"assistant","content":[{"type":"text","text":"hello human"}]},"stream":[]}}
{"type":"step/end","seq":4,"time":1700000002100,"data":{"turn":1,"step":1}}
{"type":"turn/end","seq":5,"time":1700000002200,"data":{"turn":1,"reason":{"kind":"completed"}}}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "session.v3.jsonl")
	if err := os.WriteFile(path, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}

	trace, err := ReadDeepSeekTrace(path)
	if err != nil {
		t.Fatalf("ReadDeepSeekTrace: %v", err)
	}
	if trace.Truncated {
		t.Errorf("trace marked truncated unexpectedly")
	}
	if trace.Unsupported {
		t.Errorf("trace marked unsupported unexpectedly")
	}
	if trace.Log != "session.v3.jsonl" {
		t.Errorf("log = %q, want session.v3.jsonl", trace.Log)
	}

	h := trace.Header
	if h.Version != 3 || h.ID != "session-root-001" || h.CWD != "/workspace/app" || h.IsSeeded || h.DelegationDepth != 0 || h.AgentPreset != "standard" {
		t.Errorf("header = %+v", h)
	}
	if h.CreatedAt.UnixMilli() != 1700000000000 {
		t.Errorf("createdAt = %v", h.CreatedAt)
	}

	if len(trace.Events) != 6 {
		t.Fatalf("events count = %d, want 6", len(trace.Events))
	}

	// Verify sequence, event types, and SHA-256 RawHash
	lines := strings.Split(strings.TrimSpace(log), "\n")[1:]
	for i, ev := range trace.Events {
		if !ev.HasSeq || ev.Seq != i {
			t.Errorf("event %d: seq=%d hasSeq=%v", i, ev.Seq, ev.HasSeq)
		}
		expectedSum := sha256.Sum256([]byte(lines[i]))
		expectedHash := hex.EncodeToString(expectedSum[:])
		if ev.RawHash != expectedHash {
			t.Errorf("event %d: RawHash = %q, want %q", i, ev.RawHash, expectedHash)
		}
	}

	if trace.Events[2].Type != "user/message" || string(trace.Events[2].SurfaceOp) != `"append"` {
		t.Errorf("event 2 surfaceOp = %s", trace.Events[2].SurfaceOp)
	}
}

func TestReadDeepSeekTrace_ForkedSeedAndTools(t *testing.T) {
	log := `{"type":"session","version":3,"id":"session-sub-999","createdAt":1700000010000,"cwd":"/workspace/app","parentSession":"session-root-001","isSeeded":true,"origin":"subagent","delegationDepth":1,"agentPreset":"cordis"}
{"type":"session/end-seed","seq":0,"time":1700000010100,"data":{"inherited":true}}
{"type":"turn/start","seq":1,"time":1700000010200,"data":{"turn":1}}
{"type":"step/start","seq":2,"time":1700000010300,"data":{"turn":1,"step":1}}
{"type":"user/message","seq":3,"time":1700000010400,"surfaceOp":"append","data":{"role":"user","id":"msg-u2","source":{"kind":"user"},"content":"run build and edit"}}
{"type":"tool/call","seq":4,"time":1700000011000,"data":{"turn":1,"step":1,"callId":"call_bash_1","name":"bash","arguments":"{\"command\":\"go test ./...\"}"}}
{"type":"tool/result","seq":5,"time":1700000011500,"surfaceOp":"append","sourceEventSeqs":[4],"data":{"turn":1,"step":1,"message":{"role":"tool","id":"res-1","source":{"kind":"tool","callId":"call_bash_1"},"content":[{"type":"tool-result","callId":"call_bash_1","isError":false,"text":"PASS\nok\tpkg\t0.02s"}]}}}
{"type":"tool/call","seq":6,"time":1700000012000,"data":{"turn":1,"step":1,"callId":"call_edit_1","name":"edit","arguments":"{\"file_path\":\"main.go\",\"old_string\":\"func old() {}\",\"new_string\":\"func new() { return true }\"}"}}
{"type":"tool/result","seq":7,"time":1700000012500,"surfaceOp":"append","sourceEventSeqs":[6],"data":{"turn":1,"step":1,"message":{"role":"tool","id":"res-2","source":{"kind":"tool","callId":"call_edit_1"},"content":[{"type":"tool-result","callId":"call_edit_1","isError":false,"text":"success"}]}}}
{"type":"assistant/message","seq":8,"time":1700000013000,"surfaceOp":"append","data":{"turn":1,"step":1,"message":{"role":"assistant","content":[{"type":"text","text":"all tests passed"}]},"stream":[]}}
{"type":"step/end","seq":9,"time":1700000013100,"data":{"turn":1,"step":1}}
{"type":"turn/end","seq":10,"time":1700000013200,"data":{"turn":1,"reason":{"kind":"completed"}}}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "session.v3.jsonl")
	if err := os.WriteFile(path, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}

	trace, err := ReadDeepSeekTrace(path)
	if err != nil {
		t.Fatalf("ReadDeepSeekTrace: %v", err)
	}
	if trace.Header.ParentSession != "session-root-001" || trace.Header.Origin != "subagent" || trace.Header.DelegationDepth != 1 || trace.Header.AgentPreset != "cordis" {
		t.Errorf("header lineage = %+v", trace.Header)
	}

	// Verify correlation via ParseDeepSeekFile
	sessions, err := ParseDeepSeekFile(path)
	if err != nil {
		t.Fatalf("ParseDeepSeekFile: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions count = %d, want 1", len(sessions))
	}
	s := sessions[0]

	// Verify enriched lineage metadata on model.Session
	if s.ID != "sub-999" {
		t.Errorf("s.ID = %q, want sub-999", s.ID)
	}
	if s.Parent != "root-001" {
		t.Errorf("s.Parent = %q, want root-001", s.Parent)
	}
	if s.Kind != "subagent" {
		t.Errorf("s.Kind = %q, want subagent", s.Kind)
	}
	if s.Agent != "cordis" {
		t.Errorf("s.Agent = %q, want cordis", s.Agent)
	}

	byRole := make(map[string][]model.Message)
	for _, m := range s.Messages {
		byRole[m.Role] = append(byRole[m.Role], m)
	}

	// Verify work roles: command, files, edit, wrote, tool-output
	if len(byRole[RoleCommand]) != 1 || byRole[RoleCommand][0].Text != "$ go test ./..." {
		t.Errorf("command = %+v", byRole[RoleCommand])
	}
	if len(byRole[RoleFiles]) != 1 || byRole[RoleFiles][0].Text != "main.go" {
		t.Errorf("files = %+v", byRole[RoleFiles])
	}
	if len(byRole[RoleEdit]) != 1 || byRole[RoleEdit][0].Text != "main.go\nfunc old() {}" {
		t.Errorf("edit = %+v", byRole[RoleEdit])
	}
	if len(byRole[RoleWrote]) != 1 || !strings.HasPrefix(byRole[RoleWrote][0].Text, "main.go\n") {
		t.Errorf("wrote = %+v", byRole[RoleWrote])
	}
	if len(byRole[RoleToolOutput]) != 2 {
		t.Errorf("tool-output count = %d, want 2", len(byRole[RoleToolOutput]))
	}
}

func TestReadDeepSeekTrace_SurfaceReplace(t *testing.T) {
	log := `{"type":"session","version":3,"id":"session-replace","createdAt":1700000000000}
{"type":"turn/start","seq":0,"time":1700000001000,"data":{"turn":1}}
{"type":"step/start","seq":1,"time":1700000001100,"data":{"turn":1,"step":1}}
{"type":"tool/call","seq":2,"time":1700000001200,"data":{"turn":1,"step":1,"callId":"c1","name":"read","arguments":"{\"file_path\":\"big.txt\"}"}}
{"type":"tool/result","seq":3,"time":1700000001300,"surfaceOp":"append","sourceEventSeqs":[2],"data":{"turn":1,"step":1,"message":{"role":"tool","id":"r1","source":{"kind":"tool","callId":"c1"},"content":[{"type":"tool-result","callId":"c1","text":"gigantic output"}]}}}
{"type":"compaction/prune","seq":4,"time":1700000001400,"data":{"shadowedSeqs":[3],"shadowedRange":{"startSeq":3,"endSeq":3},"shadowedTokenCount":5000}}
{"type":"tool/result","seq":5,"time":1700000001500,"surfaceOp":{"op":"replace","startSeq":3,"endSeq":3},"sourceEventSeqs":[2],"data":{"turn":1,"step":1,"message":{"role":"tool","id":"r1","source":{"kind":"tool","callId":"c1"},"content":[{"type":"tool-result","callId":"c1","text":"[pruned]"}]}}}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "session.v3.jsonl")
	if err := os.WriteFile(path, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}

	trace, err := ReadDeepSeekTrace(path)
	if err != nil {
		t.Fatalf("ReadDeepSeekTrace: %v", err)
	}
	if len(trace.Events) != 6 {
		t.Fatalf("events count = %d, want 6", len(trace.Events))
	}

	prunedRes := trace.Events[5]
	if prunedRes.Type != "tool/result" {
		t.Errorf("type = %q", prunedRes.Type)
	}
	if !strings.Contains(string(prunedRes.SurfaceOp), `"replace"`) || !strings.Contains(string(prunedRes.SurfaceOp), `"startSeq":3`) {
		t.Errorf("surfaceOp = %s", prunedRes.SurfaceOp)
	}
	if len(prunedRes.SourceEventSeqs) != 1 || prunedRes.SourceEventSeqs[0] != 2 {
		t.Errorf("sourceEventSeqs = %v", prunedRes.SourceEventSeqs)
	}
}

func TestReadDeepSeekTrace_UnsupportedGeneration(t *testing.T) {
	log := `{"type":"session","version":4,"id":"session-future-001","createdAt":1700000000000}
{"type":"turn/start","seq":0,"time":1700000001000,"data":{"turn":1}}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "session.v4.jsonl")
	if err := os.WriteFile(path, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}

	trace, err := ReadDeepSeekTrace(path)
	if err != nil {
		t.Fatalf("expected nil error for unsupported generation, got: %v", err)
	}
	if !trace.Unsupported {
		t.Errorf("trace.Unsupported = false, want true")
	}
	if !strings.Contains(trace.Notice, "unsupported session format v4") {
		t.Errorf("trace.Notice = %q", trace.Notice)
	}

	// ParseDeepSeekFile must gracefully return nil, nil on unsupported generations
	ss, err := ParseDeepSeekFile(path)
	if err != nil {
		t.Errorf("ParseDeepSeekFile: %v", err)
	}
	if len(ss) != 0 {
		t.Errorf("sessions = %v, want empty", ss)
	}
}

func TestReadDeepSeekTrace_MalformedHeader(t *testing.T) {
	cases := []struct {
		name, content string
	}{
		{"empty file", ""},
		{"not json", "not a json line\n"},
		{"wrong type", `{"type":"turn/start","seq":0}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "session.v3.jsonl")
			if err := os.WriteFile(path, []byte(c.content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := ReadDeepSeekTrace(path)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", c.name)
			}
		})
	}
}

func TestReadDeepSeekTrace_MalformedMiddleRow(t *testing.T) {
	log := `{"type":"session","version":3,"id":"session-bad-mid","createdAt":1700000000000}
{"type":"turn/start","seq":0,"time":1700000001000,"data":{"turn":1}}
{malformed json line in middle}
{"type":"turn/end","seq":1,"time":1700000002000,"data":{"turn":1}}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "session.v3.jsonl")
	if err := os.WriteFile(path, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := ReadDeepSeekTrace(path)
	if err == nil {
		t.Fatalf("expected error for malformed middle row, got nil")
	}
	if !strings.Contains(err.Error(), "malformed event row at line 3") {
		t.Errorf("error = %v, want malformed event row line 3", err)
	}
}

func TestReadDeepSeekTrace_TornTailAtEOF(t *testing.T) {
	// A log where the writer was terminated mid-write on the last event (incomplete JSON line, no trailing newline)
	log := `{"type":"session","version":3,"id":"session-torn","createdAt":1700000000000}
{"type":"turn/start","seq":0,"time":1700000001000,"data":{"turn":1}}
{"type":"tool/call","seq":1,"time":1700000002000,"data":{"name":"bash","arguments":"{\"command\":\"ls`

	dir := t.TempDir()
	path := filepath.Join(dir, "session.v3.jsonl")
	if err := os.WriteFile(path, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}

	trace, err := ReadDeepSeekTrace(path)
	if err != nil {
		t.Fatalf("expected recovery on torn tail at EOF, got err: %v", err)
	}
	if !trace.Truncated {
		t.Errorf("trace.Truncated = false, want true")
	}
	if len(trace.Events) != 1 {
		t.Errorf("events count = %d, want 1 valid event preceding torn tail", len(trace.Events))
	}
}

func TestCanonicalDeepSeekLog_HighestGeneration(t *testing.T) {
	dir := t.TempDir()
	sessDir := filepath.Join(dir, "--project--", "session-multi-gen")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create legacy v0, intermediate v1, and current v3
	v0 := filepath.Join(sessDir, "session.jsonl")
	v1 := filepath.Join(sessDir, "session.v1.jsonl")
	v3 := filepath.Join(sessDir, "session.v3.jsonl")
	lock := filepath.Join(sessDir, "session.lock")
	bak := filepath.Join(sessDir, "session.v3.jsonl.bak")

	for _, p := range []string{v0, v1, v3, lock, bak} {
		if err := os.WriteFile(p, []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	canon, err := CanonicalDeepSeekLog(sessDir)
	if err != nil {
		t.Fatalf("CanonicalDeepSeekLog: %v", err)
	}
	if canon != v3 {
		t.Errorf("canon = %q, want %q (highest version v3)", canon, v3)
	}

	// Also test passing the file path instead of directory
	canonFromFile, err := CanonicalDeepSeekLog(v0)
	if err != nil {
		t.Fatalf("CanonicalDeepSeekLog(v0): %v", err)
	}
	if canonFromFile != v3 {
		t.Errorf("canonFromFile = %q, want %q", canonFromFile, v3)
	}
}

func TestCanonicalDeepSeekLog_EncodingMismatch(t *testing.T) {
	dir := t.TempDir()
	sessDir := filepath.Join(dir, "session-mismatch")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Same generation v3 exists as both plaintext and zstd
	f1 := filepath.Join(sessDir, "session.v3.jsonl")
	f2 := filepath.Join(sessDir, "session.v3.jsonl.zstd")
	_ = os.WriteFile(f1, []byte("plain"), 0o600)
	_ = os.WriteFile(f2, []byte("compressed"), 0o600)

	_, err := CanonicalDeepSeekLog(sessDir)
	if err == nil {
		t.Fatalf("expected same-generation encoding mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "same-generation encoding mismatch") {
		t.Errorf("err = %v, want same-generation encoding mismatch", err)
	}
}

func TestFindDeepSeekSessionLog(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_DEEPSEEK_ROOT", root)

	proj1 := filepath.Join(root, "--project-1--")
	proj2 := filepath.Join(root, "--project-2--")

	sess1 := filepath.Join(proj1, "session-abc-123")
	sess2 := filepath.Join(proj2, "def-456") // no session- prefix on directory

	if err := os.MkdirAll(sess1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sess2, 0o755); err != nil {
		t.Fatal(err)
	}

	log1 := filepath.Join(sess1, "session.v3.jsonl")
	log2 := filepath.Join(sess2, "session.v3.jsonl")
	_ = os.WriteFile(log1, []byte("data"), 0o600)
	_ = os.WriteFile(log2, []byte("data"), 0o600)

	// Lookup with prefix
	found1, err := FindDeepSeekSessionLog("session-abc-123")
	if err != nil {
		t.Fatalf("FindDeepSeekSessionLog(session-abc-123): %v", err)
	}
	if found1 != log1 {
		t.Errorf("found1 = %q, want %q", found1, log1)
	}

	// Lookup without prefix
	found1Bare, err := FindDeepSeekSessionLog("abc-123")
	if err != nil {
		t.Fatalf("FindDeepSeekSessionLog(abc-123): %v", err)
	}
	if found1Bare != log1 {
		t.Errorf("found1Bare = %q, want %q", found1Bare, log1)
	}

	// Lookup second session
	found2, err := FindDeepSeekSessionLog("def-456")
	if err != nil {
		t.Fatalf("FindDeepSeekSessionLog(def-456): %v", err)
	}
	if found2 != log2 {
		t.Errorf("found2 = %q, want %q", found2, log2)
	}

	// Non-existent session
	_, err = FindDeepSeekSessionLog("non-existent")
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected ErrNotExist, got: %v", err)
	}

	// Duplicate session in multiple projects
	dupSess := filepath.Join(proj2, "session-abc-123")
	_ = os.MkdirAll(dupSess, 0o755)
	_ = os.WriteFile(filepath.Join(dupSess, "session.v3.jsonl"), []byte("data"), 0o600)

	_, err = FindDeepSeekSessionLog("abc-123")
	if err == nil || !strings.Contains(err.Error(), "duplicate session ID") {
		t.Errorf("expected duplicate session ID error, got: %v", err)
	}
}
