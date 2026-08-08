package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

func writeEditFixture(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	proj := filepath.Join(tmp, "claude", "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	line := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	line(`{"type":"user","sessionId":"claude-edit","cwd":"/w","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"clean up the retry helper"}}`)
	line(`{"type":"assistant","sessionId":"claude-edit","cwd":"/w","timestamp":"2026-01-02T03:05:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/w/pkg/retry.go","old_string":"func retry() error {\n\treturn nil\n}"}}]}}`)
	line(`{"type":"assistant","sessionId":"claude-edit","cwd":"/w","timestamp":"2026-01-02T03:06:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"MultiEdit","input":{"file_path":"/w/pkg/retry.go","edits":[{"old_string":"const backoff = 1"},{"old_string":"const ceiling = 9"}]}}]}}`)
	if err := os.WriteFile(filepath.Join(proj, "s.jsonl"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return tmp
}

func TestRestoreListsAndWritesSpans(t *testing.T) {
	writeEditFixture(t)
	out, err := captureRun(t, "restore", "retry.go")
	if err != nil {
		t.Fatalf("restore: %v (out=%q)", err, out)
	}
	if !strings.Contains(out, "3 replaced spans") {
		t.Fatalf("want three spans including the MultiEdit sub-edits, got:\n%s", out)
	}

	// Newest first, so the MultiEdit spans lead and the function body is last.
	out, err = captureRun(t, "restore", "retry.go", "--span", "3")
	if err != nil {
		t.Fatalf("restore --span: %v", err)
	}
	if !strings.Contains(out, "func retry() error {") || !strings.Contains(out, "return nil") {
		t.Fatalf("span should come back byte for byte, got:\n%s", out)
	}

	dst := filepath.Join(t.TempDir(), "recovered.txt")
	out, err = captureRun(t, "restore", "retry.go", "--span", "3", "-o", dst)
	if err != nil {
		t.Fatalf("restore -o: %v", err)
	}
	body, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "func retry() error {\n\treturn nil\n}" {
		t.Fatalf("recovered file = %q", body)
	}
	if !strings.Contains(out, "not the file") {
		t.Fatalf("output must say what it is handing back, got %q", out)
	}
}

// restore hands back the exact bytes a session recorded, so a trust rule that
// withholds a peer's content must hold here too — it was the one direct-access
// command that read an imported edit span aloud while show, share, handoff and
// ctx refused (#1026).
func TestRestoreHonoursTrustPolicy(t *testing.T) {
	tmp := writeEditFixture(t) // a local session that edited /w/pkg/retry.go
	cfg := filepath.Join(tmp, "config")
	t.Setenv("XDG_CONFIG_HOME", cfg)
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	// A peer's edit to the same file, arriving by sync.
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	peer := `{"harness":"claude","session_id":"peer","project":"svc","role":"edit","text":"pkg/retry.go\nIMPORTEDSPAN a peer's replaced code"}` + "\n"
	if err := os.WriteFile(filepath.Join(exp, "deja-sync.jsonl"), []byte(peer), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}

	spanOutputs := func() string {
		var b strings.Builder
		for _, n := range []string{"1", "2", "3", "4"} {
			out, _ := captureRun(t, "restore", "retry.go", "--span", n)
			b.WriteString(out)
		}
		return b.String()
	}

	// Allowed: the imported span comes back like any other.
	writePolicy(t, `{"activations":{"search":{"imported":true}}}`)
	if !strings.Contains(spanOutputs(), "IMPORTEDSPAN") {
		t.Fatalf("imported span did not restore under an allowing rule")
	}
	// Denied: the imported span is withheld, the local ones stay.
	writePolicy(t, `{"activations":{"search":{"imported":false}}}`)
	got := spanOutputs()
	if strings.Contains(got, "IMPORTEDSPAN") {
		t.Errorf("restore leaked an imported span under deny-imported:\n%s", got)
	}
	if !strings.Contains(got, "func retry() error {") {
		t.Errorf("restore over-blocked a local span under deny-imported:\n%s", got)
	}
}

func TestRestoreRefusesToWriteOverTheOriginal(t *testing.T) {
	writeEditFixture(t)
	// Same path in and out: this command exists because something overwrote
	// work, so it must not do the same.
	err := runRestore(os.Getenv("DEJA_INDEX_DIR"), []string{"retry.go", "--span", "1", "-o", "retry.go"}, os.Stdout)
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("want a refusal, got %v", err)
	}
}

func TestRestoreUnknownPathAndBadArgs(t *testing.T) {
	writeEditFixture(t)
	out, err := captureRun(t, "restore", "nothing-here.go")
	if err != nil || !strings.Contains(out, "no replaced spans") {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if err := runRestore(t.TempDir(), nil, os.Stdout); err == nil {
		t.Fatal("no path should be a usage error")
	}
	if err := runRestore(t.TempDir(), []string{"a.go", "--span", "x"}, os.Stdout); err == nil {
		t.Fatal("--span wants a number")
	}
	if err := runRestore(os.Getenv("DEJA_INDEX_DIR"), []string{"retry.go", "--span", "99"}, os.Stdout); err == nil {
		t.Fatal("out of range span should fail")
	}
}

func TestPathMatches(t *testing.T) {
	if !pathMatches("/w/pkg/retry.go", "retry.go") {
		t.Error("a bare file name should match")
	}
	if !pathMatches("/w/pkg/retry.go", "pkg/retry.go") {
		t.Error("a path suffix should match")
	}
	if !pathMatches("/w/pkg/retry.go", "/w/pkg/retry.go") {
		t.Error("the whole path should match")
	}
	if pathMatches("/w/pkg/retry.go", "other.go") {
		t.Error("a different file should not match")
	}
	if pathMatches("/w/pkgretry.go", "retry.go") {
		t.Error("a suffix that is not a path segment should not match")
	}
}

func TestRedactionNote(t *testing.T) {
	if redactionNote("token = [redacted:credential]") == "" {
		t.Error("a redacted span must be flagged, restoring it is not byte-exact")
	}
	if redactionNote("func x() {}") != "" {
		t.Error("clean spans carry no note")
	}
}

func TestShortID(t *testing.T) {
	if got := shortID("abcdefghijklmnop"); got != "abcdefghijkl" {
		t.Fatalf("got %q", got)
	}
	if got := shortID("short"); got != "short" {
		t.Fatalf("got %q", got)
	}
}

// Ranked retrieval samples postings per session, so in a session with hundreds
// of edit records the one being looked for never reached the candidate set and
// the whole session stayed invisible. Measured on a real store: 41 of 1,621
// recorded paths were unreachable, and both traced cases sat in the two
// largest sessions — the worst place to lose them (#647).
func TestRestoreFindsSpansInACrowdedSession(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-big")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	edit := func(path, old string) string {
		return `{"type":"assistant","sessionId":"big","cwd":"/w","timestamp":"2026-07-20T10:00:00Z",` +
			`"message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"` + path +
			`","old_string":"` + old + `","new_string":"new"}}]}}` + "\n"
	}
	var b strings.Builder
	// A session with hundreds of edit records, which is the shape restore has
	// to handle.
	//
	// Worth stating plainly: this test does not fail against the ranked
	// implementation. I tried to reproduce the miss synthetically — 400 edits,
	// then 2,000 and 9,000 speech messages carrying the needle word — and the
	// ranked path found the span every time. The failure is real but its
	// trigger is something the fixtures do not capture: on the live store
	// `index.Search("telebot.go", Role:"edit")` returns 0 sessions while the
	// log holds 3 matching spans, and `deja restore telebot.go` went from "no
	// replaced spans recorded" to listing all three. So this pins the scan's
	// correctness; the regression it guards against is documented in #647
	// with the numbers, not reproduced here.
	b.WriteString(`{"type":"user","sessionId":"big","cwd":"/w","timestamp":"2026-07-20T09:00:00Z","message":{"role":"user","content":"a long refactor"}}` + "\n")
	b.WriteString(edit("/w/needle.go", "the bytes that stopped existing"))
	for i := 0; i < 200; i++ {
		b.WriteString(fmt.Sprintf(`{"type":"assistant","sessionId":"big","cwd":"/w","timestamp":"2026-07-20T10:00:00Z","message":{"role":"assistant","content":"step %d of the refactor, needle and other words"}}`, i) + "\n")
	}
	for i := 0; i < 400; i++ {
		b.WriteString(edit(fmt.Sprintf("/w/filler%03d.go", i), fmt.Sprintf("old filler %d", i)))
	}
	if err := os.WriteFile(filepath.Join(root, "big.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	spans, err := findRestoreSpans(dir, "needle.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 1 {
		t.Fatalf("found %d spans for a file buried in a crowded session, want 1", len(spans))
	}
	if !strings.Contains(spans[0].body, "stopped existing") {
		t.Fatalf("wrong span: %q", spans[0].body)
	}
	// What stats counts and what restore hands back must be the same number.
	total, files, err := index.SpanInventory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if total != 401 || files != 401 {
		t.Fatalf("inventory = %d spans / %d files, want 401 each", total, files)
	}
}
