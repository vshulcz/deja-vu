package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// writeFilesFixture lays down a claude session that discusses one subject and
// opens files while doing it, inside a directory that looks like a repository.
func writeFilesFixture(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(tmp, "claude", "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	line := func(format string, a ...any) {
		fmt.Fprintf(&b, format+"\n", a...)
	}
	line(`{"type":"user","sessionId":"claude-files","cwd":%q,"timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"the frobnicator retry loop is wrong"}}`, repo)
	// Two files opened right after the subject came up, one of them twice.
	for i, p := range []string{"retry.go", "retry.go", "loop.go"} {
		line(`{"type":"assistant","sessionId":"claude-files","cwd":%q,"timestamp":"2026-01-02T03:0%d:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Read","input":{"file_path":%q}}]}}`,
			repo, 5+i, filepath.Join(repo, p))
	}
	// A file opened hours later, while something else was being discussed.
	line(`{"type":"user","sessionId":"claude-files","cwd":%q,"timestamp":"2026-01-02T09:00:00Z","message":{"role":"user","content":"unrelated packaging work"}}`, repo)
	line(`{"type":"assistant","sessionId":"claude-files","cwd":%q,"timestamp":"2026-01-02T09:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Read","input":{"file_path":%q}}]}}`,
		repo, filepath.Join(repo, "packaging.go"))
	if err := os.WriteFile(filepath.Join(proj, "s.jsonl"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestFilesCommandRanksNearbyTouches(t *testing.T) {
	writeFilesFixture(t)
	out, err := captureRun(t, "files", "frobnicator", "retry")
	if err != nil {
		t.Fatalf("files: %v (out=%q)", err, out)
	}
	if !strings.Contains(out, "retry.go") || !strings.Contains(out, "loop.go") {
		t.Fatalf("want the files opened beside the subject, got:\n%s", out)
	}
	if strings.Contains(out, "packaging.go") {
		t.Fatalf("a file touched hours later is different work, got:\n%s", out)
	}
	if strings.Index(out, "retry.go") > strings.Index(out, "loop.go") {
		t.Fatalf("the file touched twice should rank first, got:\n%s", out)
	}
}

func TestFilesCommandSaysWhenNothingIsNear(t *testing.T) {
	writeFilesFixture(t)
	out, err := captureRun(t, "files", "packaging")
	if err != nil {
		t.Fatalf("files: %v", err)
	}
	// The subject exists and a file was opened a minute later, so this is the
	// hit path; the refusal path is a subject nobody discussed.
	if out == "" {
		t.Fatal("expected some output")
	}
	out, err = captureRun(t, "files", "quantumfluxcapacitor")
	if err != nil {
		t.Fatalf("files: %v", err)
	}
	if !strings.Contains(out, "no sessions mention") && !strings.Contains(out, "none of them recorded") {
		t.Fatalf("want an honest refusal, got %q", out)
	}
}

func TestFilesCommandArgumentErrors(t *testing.T) {
	hermeticEnv(t)
	if err := runFiles(t.TempDir(), nil, os.Stdout); err == nil {
		t.Fatal("no topic should be a usage error")
	}
	if err := runFiles(t.TempDir(), []string{"topic", "--limit", "zero"}, os.Stdout); err == nil {
		t.Fatal("--limit wants a number")
	}
}

// `friction`, `restore` and `stats` all agree that a file was worked on;
// `files` said no file was ever recorded. It was — the path simply is not
// under a repository on this disk today, which is a fact about the disk rather
// than about the past (#664).
func TestFilesDistinguishesFilteredFromAbsent(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-f")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	// A path that was recorded but has no .git above it now.
	// Forward slashes: the path is embedded in JSON, and a Windows separator
	// would be read as an escape sequence, so the file record never forms and
	// the test would pass for the wrong reason.
	gone := filepath.ToSlash(filepath.Join(tmp, "vanished-repo", "pipeline.go"))
	body := `{"type":"user","sessionId":"f1","cwd":"/w/f","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"fix the widget pipeline retry"}}` + "\n" +
		`{"type":"assistant","sessionId":"f1","cwd":"/w/f","timestamp":"2026-07-21T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Read","input":{"file_path":"` + gone + `"}}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "f1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runFiles(dir, []string{"pipeline"}, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, "none of them recorded a file") {
		t.Fatalf("claimed nothing was recorded when a file was:\n%s", got)
	}
	if !strings.Contains(got, "not under a repository") {
		t.Fatalf("does not say why the file is missing from the answer:\n%s", got)
	}

	// And the genuinely-empty case keeps its own wording.
	out.Reset()
	if err := runFiles(dir, []string{"nothing-mentions-this"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "none of them recorded a file") &&
		!strings.Contains(out.String(), "no sessions mention") {
		t.Fatalf("the empty case lost its wording:\n%s", out.String())
	}
}

// `files` lists the paths a session opened, so a trust rule that withholds a
// peer's content must hold here too — it read imported file paths aloud while
// search, blame and restore all refused (#1026).
func TestFilesCommandHonoursTrustPolicy(t *testing.T) {
	repo := writeFilesFixture(t) // a local session touching retry.go under repo
	cfg := filepath.Join(filepath.Dir(filepath.Dir(repo)), "config")
	t.Setenv("XDG_CONFIG_HOME", cfg)
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	// A peer's session opened a file under the same repo, arriving by sync.
	tmp := filepath.Dir(repo)
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	// Build the records with json.Marshal so the file path — which carries
	// backslashes on Windows — is escaped, not hand-spliced into a raw string.
	at := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	var peer []byte
	for _, r := range []index.SyncRecord{
		{Harness: "claude", SessionID: "peer", Project: "svc", Role: "user", Text: "the frobnicator retry loop, peer take", Time: at},
		{Harness: "claude", SessionID: "peer", Project: "svc", Role: "files", Text: filepath.Join(repo, "peer_secret.go"), Time: at.Add(30 * time.Second)},
	} {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		peer = append(peer, append(b, '\n')...)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync.jsonl"), peer, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}

	writePolicy(t, `{"activations":{"search":{"imported":true}}}`)
	if out, _ := captureRun(t, "files", "frobnicator", "retry"); !strings.Contains(out, "peer_secret.go") {
		t.Fatalf("imported file did not surface under an allowing rule:\n%s", out)
	}
	writePolicy(t, `{"activations":{"search":{"imported":false}}}`)
	out, _ := captureRun(t, "files", "frobnicator", "retry")
	if strings.Contains(out, "peer_secret.go") {
		t.Errorf("files leaked a peer's file path under deny-imported:\n%s", out)
	}
	if !strings.Contains(out, "retry.go") {
		t.Errorf("files over-blocked the local session under deny-imported:\n%s", out)
	}
}

// When the only session that mentions the topic is one a rule withholds,
// `files` used to say "no sessions mention it" — looked-and-absent, when the
// truth is the rule hid it. search and last already name the rule (#686, #680).
func TestFilesCommandNamesTheTrustPolicyOnAnEmptyResult(t *testing.T) {
	tmp := hermeticEnv(t)
	cfg := filepath.Join(tmp, "config")
	t.Setenv("XDG_CONFIG_HOME", cfg)
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The only match is an imported session; no local session mentions it.
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	var batch []byte
	for _, r := range []index.SyncRecord{
		{Harness: "claude", SessionID: "peer", Project: "svc", Role: "user", Text: "the frobnicator retry loop", Time: at},
		{Harness: "claude", SessionID: "peer", Project: "svc", Role: "files", Text: filepath.Join(repo, "retry.go"), Time: at.Add(30 * time.Second)},
	} {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		batch = append(batch, append(b, '\n')...)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync.jsonl"), batch, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}

	writePolicy(t, `{"activations":{"search":{"imported":false}}}`)
	out, _ := captureRun(t, "files", "frobnicator", "retry")
	if !strings.Contains(out, "trust policy") {
		t.Errorf("files did not name the rule that emptied the result:\n%s", out)
	}
	if strings.Contains(out, "no sessions mention") {
		t.Errorf("files still reads as looked-and-absent when a rule hid the match:\n%s", out)
	}
}
