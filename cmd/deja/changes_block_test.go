package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// recall_context is the "read this session" tool, and for a coding session
// the part worth reading is the code it left. The digest reads from the top,
// where the task and the code reading are, so the fix of a session whose
// conversation outgrows the budget never reached the agent: on 48
// SWE-ContextBench tasks the digest of the past session that fixed the same
// code carried none of it. The code now comes ahead of the conversation, as
// the session left it, without the agent's scratch script or a draft it
// rewrote.
func TestRecallContextShowsTheCodeTheSessionLeft(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "cccc0001-1111-4000-8000-d6e7f8a9b0c1"
	var b strings.Builder
	n := 0
	line := func(typ string, content any) {
		n++
		raw := mustMarshal(t, map[string]any{
			"type": typ, "sessionId": id, "cwd": "/proj",
			"timestamp": fmt.Sprintf("2026-07-01T10:%02d:%02dZ", n/60, n%60),
			"message":   map[string]any{"role": typ, "content": content},
		})
		b.Write(raw)
		b.WriteByte('\n')
	}
	tool := func(name string, input map[string]string) []any {
		return []any{map[string]any{"type": "tool_use", "id": fmt.Sprintf("t%d", n), "name": name, "input": input}}
	}
	line("user", "squashed migrations still carry index_together")
	for i := range 30 {
		line("assistant", []any{map[string]any{"type": "text", "text": fmt.Sprintf("reading the optimizer, step %d: %s", i, strings.Repeat("the operations reduce in pairs ", 12))}})
	}
	line("assistant", tool("Write", map[string]string{"file_path": "/proj/reproduce_bug.py", "content": "print('SCRATCHPAD')"}))
	line("assistant", tool("Edit", map[string]string{"file_path": "/proj/django/ops.py", "old_string": "def reduce(self):\n    pass", "new_string": "def reduce(self):\n    return DRAFT"}))
	line("assistant", tool("Edit", map[string]string{"file_path": "/proj/django/ops.py", "old_string": "return DRAFT", "new_string": "return [AddIndex(self.name)]"}))
	line("assistant", tool("Edit", map[string]string{"file_path": "/proj/tests/test_ops.py", "old_string": "def test_a(): pass", "new_string": "def test_a(): assert reduce()"}))
	line("assistant", []any{map[string]any{"type": "text", "text": "Fixed: reduce turns the rename into an AddIndex."}})
	if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	raw, n1, _, _, err := recallContextResult(dir, id, "")
	if err != nil || n1 != 1 {
		t.Fatalf("n=%d err=%v", n1, err)
	}
	// What the MCP tool sends: cut to the reply's budget.
	text := fitContextDigest(raw, id, id, contextMCPBudget-recallFrameOverhead)
	at := strings.Index(text, "## what it changed")
	if at < 0 {
		t.Fatalf("no changes block:\n%s", text)
	}
	if conv := strings.Index(text, "reading the optimizer"); conv >= 0 && conv < at {
		t.Fatalf("the code comes after the conversation, where the cut takes it:\n%s", text)
	}
	lib, test := strings.Index(text, "+     return [AddIndex(self.name)]"), strings.Index(text, "assert reduce()")
	if lib < 0 || test < 0 || lib > test {
		t.Fatalf("the library change and then its test must show:\n%s", text)
	}
	for _, bad := range []string{"SCRATCHPAD", "DRAFT"} {
		if strings.Contains(text, bad) {
			t.Fatalf("shows %q:\n%s", bad, text)
		}
	}
	if !strings.Contains(text, "django/ops.py:") || strings.Contains(text, "/proj/django") {
		t.Fatalf("paths should read relative to the project:\n%s", text)
	}
}

// A session that changed nothing reads as it did.
func TestRecallContextWithoutEditsHasNoChangesBlock(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "cccc0002-1111-4000-8000-d6e7f8a9b0c1"
	line := `{"type":"user","message":{"role":"user","content":"why is the pool exhausted"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"` + id + `","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	text, _, _, _, err := recallContextResult(dir, id, "")
	if err != nil || strings.Contains(text, "what it changed") {
		t.Fatalf("err=%v\n%s", err, text)
	}
}

func TestChangeDiffKeepsOnlyWhatChanged(t *testing.T) {
	old := "a\nb\nc\nd\ne\nf"
	got := changeDiff(old, "a\nb\nc\nX\ne\nf")
	want := "  b\n  c\n- d\n+ X\n  e\n  f"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// A Codex session's edits are apply_patch calls; reading the session back
// shows them the same way (#4950).
func TestRecallContextShowsACodexSessionsPatch(t *testing.T) {
	tmp := hermeticEnv(t)
	day := filepath.Join(tmp, "codex", "sessions", "2026", "07", "01")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "019a0001-1111-7000-8000-d6e7f8a9b0c1"
	rec := func(at, typ string, payload any) string {
		b := mustMarshal(t, map[string]any{"timestamp": at, "type": typ, "payload": payload})
		return string(b) + "\n"
	}
	patch := "*** Begin Patch\n*** Update File: /proj/django/ops.py\n@@\n def reduce(self):\n-    pass\n+    return [AddIndex(self.name)]\n*** End Patch\n"
	body := rec("2026-07-01T10:00:00Z", "session_meta", map[string]any{"id": id, "cwd": "/proj"}) +
		rec("2026-07-01T10:00:01Z", "response_item", map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "squashed migrations still carry index_together"}}}) +
		rec("2026-07-01T10:00:02Z", "response_item", map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "c1", "input": patch}) +
		rec("2026-07-01T10:00:03Z", "response_item", map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Fixed: reduce turns the rename into an AddIndex."}}})
	if err := os.WriteFile(filepath.Join(day, "rollout-2026-07-01T10-00-00-"+id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	text, n, _, _, err := recallContextResult(dir, id, "")
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if !strings.Contains(text, "## what it changed") || !strings.Contains(text, "django/ops.py:") || !strings.Contains(text, "+     return [AddIndex(self.name)]") {
		t.Fatalf("the patch is not in the session read back:\n%s", text)
	}
}

func TestRecallIncludesCompactCodeDiffForMatchedSymbol(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "cccc0003-1111-4000-8000-d6e7f8a9b0c1"
	user := `{"type":"user","message":{"role":"user","content":"fix MaxIdleConns retry in pool.go"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"` + id + `","cwd":"/proj"}` + "\n"
	edit := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/proj/internal/db/pool.go","old_string":"MaxIdleConns: 2,\n","new_string":"MaxIdleConns: 16,\n"}},{"type":"text","text":"We decided to raise the idle connection floor in pool.go so bursts reuse sockets."}]},"timestamp":"2026-07-01T10:01:00Z","sessionId":"` + id + `","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(user+edit), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	text, _, _, _, err := recallTextResult(dir, "MaxIdleConns pool", "", 5, 0, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "code it changed (internal/db/pool.go):") || !strings.Contains(text, "+ MaxIdleConns: 16,") {
		t.Fatalf("expected compact code diff in recall output, got:\n%s", text)
	}
}

func TestRecallDiversifiesUncoveredQueryTerms(t *testing.T) {
	s1 := model.Session{
		ID:      "s1",
		Harness: "claude",
		Project: "proj",
		Messages: []model.Message{
			{Role: "user", Text: "check pgbouncer pooling configuration"},
		},
	}
	s2 := model.Session{
		ID:      "s2",
		Harness: "claude",
		Project: "proj",
		Messages: []model.Message{
			{Role: "user", Text: "another pgbouncer pooling tune"},
		},
	}
	s3 := model.Session{
		ID:      "s3",
		Harness: "claude",
		Project: "proj",
		Messages: []model.Message{
			{Role: "user", Text: "quasar failover regression check"},
		},
	}
	hits := []search.Hit{
		{Session: s1, Tier: search.TierRelevance},
		{Session: s2, Tier: search.TierRelevance},
		{Session: s3, Tier: search.TierRelevance},
	}
	idf := map[string]float64{"pgbouncer": 2.0, "quasar": 5.0}
	out := diversifyUncoveredTerms(hits, "pgbouncer quasar", idf, "")
	if len(out) != 3 || out[0].Session.ID != "s1" || out[1].Session.ID != "s3" {
		t.Fatalf("expected s3 covering uncovered term quasar promoted to slot 2, got [%s, %s, %s]",
			out[0].Session.ID, out[1].Session.ID, out[2].Session.ID)
	}

	// When added names slot 1, promote into slot 2 instead.
	s4 := model.Session{
		ID:      "s4",
		Harness: "claude",
		Project: "proj",
		Messages: []model.Message{
			{Role: "user", Text: "another pgbouncer note"},
		},
	}
	hitsWithAdded := []search.Hit{
		{Session: s1, Tier: search.TierRelevance},
		{Session: s2, Tier: search.TierRelevance},
		{Session: s4, Tier: search.TierRelevance},
		{Session: s3, Tier: search.TierRelevance},
	}
	outAdded := diversifyUncoveredTerms(hitsWithAdded, "pgbouncer quasar", idf, "claude:s2")
	if len(outAdded) != 4 || outAdded[1].Session.ID != "s2" || outAdded[2].Session.ID != "s3" {
		t.Fatalf("expected s3 promoted to slot 3 when slot 2 is machine-best, got [%s, %s, %s, %s]",
			outAdded[0].Session.ID, outAdded[1].Session.ID, outAdded[2].Session.ID, outAdded[3].Session.ID)
	}

	// RoleEdit/RoleWrote fallback in sessionChanges & recallCodeChangedLines.
	inMem := model.Session{
		ID:      "in-mem",
		Harness: "aider",
		Messages: []model.Message{
			{Role: "edit", Text: "internal/db/pool.go\nMaxIdleConns: 2"},
			{Role: "wrote", Text: "internal/db/pool.go\n+ MaxIdleConns: 16"},
		},
	}
	diffLines := recallCodeChangedLines(inMem, "MaxIdleConns")
	if len(diffLines) == 0 || !strings.Contains(strings.Join(diffLines, ""), "+ MaxIdleConns: 16") {
		t.Fatalf("expected RoleEdit/RoleWrote fallback diff, got %v", diffLines)
	}
}
