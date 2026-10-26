package index

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/evidence"
)

// receiptTurns writes, from minute at on, a person asking, a test run that
// fails, and the model saying what failed: the line a receipt backs.
func receiptTurns(t *testing.T, path, id string, at int, test string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	ts := func(i int) string {
		return time.Date(2026, 3, 4, 10, at+i, 0, 0, time.UTC).Format(time.RFC3339)
	}
	tid := fmt.Sprintf("toolu_%s_%d", id, at)
	for i, rec := range []map[string]any{
		{"type": "user", "message": map[string]any{"role": "user", "content": "why does " + test + " fail in slug_test.go"}},
		{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{
			"type": "tool_use", "id": tid, "name": "Bash", "input": map[string]any{"command": "go test -run " + test + " ./slug"}}}}},
		{"type": "user", "message": map[string]any{"role": "user", "content": []any{map[string]any{
			"type": "tool_result", "tool_use_id": tid, "content": "--- FAIL: " + test + " (0.00s)\n    slug_test.go:41: got \"a-b\" want \"a_b\"\nFAIL", "is_error": true}}}},
		{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{
			"type": "text", "text": test + " failed at slug_test.go:41 because the separator is a hyphen, want a_b"}}}},
	} {
		rec["sessionId"] = id
		rec["timestamp"] = ts(i)
		rec["cwd"] = "/work/app"
		rec["entrypoint"] = "cli"
		rec["uuid"] = fmt.Sprintf("%s-%d-%d", id, at, i)
		b, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			t.Fatal(err)
		}
	}
}

func receiptOf(t *testing.T, dir, id, test string) []string {
	t.Helper()
	r := evidence.Open(dir)
	if r == nil {
		t.Fatalf("no receipts in %s", dir)
	}
	return r.Lookup("claude", id, test+" failed at slug_test.go:41 because the separator is a hyphen, want a_b")
}

// A session that arrives after the last full build gets its receipts in the
// next ordinary pass, both as a new transcript and as turns appended to one
// the index already holds, and the file is the one a rebuild writes.
func TestReceiptsFollowIncrementalPasses(t *testing.T) {
	dir, proj := pairStore(t)
	receiptTurns(t, filepath.Join(proj, "one.jsonl"), "one", 0, "TestSlugEmpty")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := receiptOf(t, dir, "one", "TestSlugEmpty"); len(got) == 0 {
		t.Fatal("the first build wrote no receipt for its session")
	}

	// A new transcript, then more turns in the first one.
	receiptTurns(t, filepath.Join(proj, "two.jsonl"), "two", 0, "TestSlugUnicode")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	receiptTurns(t, filepath.Join(proj, "one.jsonl"), "one", 10, "TestSlugTrim")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ id, test string }{{"one", "TestSlugEmpty"}, {"two", "TestSlugUnicode"}, {"one", "TestSlugTrim"}} {
		got := receiptOf(t, dir, c.id, c.test)
		if len(got) == 0 || !strings.Contains(got[0], c.test) {
			t.Errorf("%s/%s: receipt after the ordinary pass = %q, want the failing run", c.id, c.test, got)
		}
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"claude:one", "claude:two"} {
		if !evidence.Open(dir).Covers(m.Sessions[key].Updated) {
			t.Errorf("%s: receipts do not cover the session after the pass that changed it", key)
		}
	}

	inc, err := os.ReadFile(filepath.Join(dir, "receipts.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err = Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	full, err := os.ReadFile(filepath.Join(dir, "receipts.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(inc, full) {
		t.Errorf("receipts after the ordinary passes (%d bytes) differ from a rebuild's (%d bytes)", len(inc), len(full))
	}
}

// Without receipts from a full build, an ordinary pass writes none: a file
// with only the new session's would cover every other one.
func TestIncrementalPassWritesNoPartialReceipts(t *testing.T) {
	dir, proj := pairStore(t)
	receiptTurns(t, filepath.Join(proj, "one.jsonl"), "one", 0, "TestSlugEmpty")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "receipts.bin")); err != nil {
		t.Fatal(err)
	}
	receiptTurns(t, filepath.Join(proj, "two.jsonl"), "two", 0, "TestSlugUnicode")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "receipts.bin")); err == nil {
		t.Error("an ordinary pass wrote receipts for its own sessions alone")
	}
}
