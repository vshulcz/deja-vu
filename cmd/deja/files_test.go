package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/jsonout"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/termwidth"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"
)

// Every count `files` prints is taken over the sessions it read, and it reads
// at most filesMaxSessions of them. On a store where 290 sessions said the
// word the answer was "250 sessions" — the read budget presented as the size
// of the match, with nothing in the line to tell the two apart.
func TestFilesSaysWhenTheReadBudgetBit(t *testing.T) {
	tmp := hermeticEnv(t)
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(repo, "shared.go")
	if err := os.WriteFile(shared, []byte("package repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	const over = 40
	total := filesMaxSessions + over
	for i := range total {
		id := fmt.Sprintf("s%04d", i)
		day := 1 + i/24
		hour := i % 24
		lines := []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":"2026-07-%02dT%02d:00:00Z","message":{"role":"user","content":"the migration rollback again"}}`, id, repo, day, hour),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":"2026-07-%02dT%02d:05:00Z","message":{"role":"assistant","content":[{"type":"text","text":"on it"},{"type":"tool_use","name":"Edit","input":{"file_path":%q}}]}}`, id, repo, day, hour, shared),
		}
		writeClaudeFixture(t, filepath.Join(root, "projects", "-repo", id+".jsonl"), id, lines)
	}
	if err := index.Ensure(os.Getenv("DEJA_INDEX_DIR"), "", false, nil); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "files", "migration")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "files touched while working on") {
		t.Fatalf("the probe measured a command that answered nothing:\n%s", out)
	}
	if !strings.Contains(out, fmt.Sprintf("of %d matching sessions", total)) {
		t.Errorf("%d sessions matched, the line reports only the %d that were read:\n%s", total, filesMaxSessions, out)
	}

	// The other side: under the budget the note must not appear, or every
	// answer carries a caveat about a cap that never bit.
	small := hermeticEnv(t)
	smallRepo := filepath.Join(small, "repo")
	if err = os.MkdirAll(filepath.Join(smallRepo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	smallFile := filepath.Join(smallRepo, "one.go")
	if err = os.WriteFile(smallFile, []byte("package repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	smallRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	lines := []string{
		fmt.Sprintf(`{"type":"user","sessionId":"only","cwd":%q,"timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"the migration rollback again"}}`, smallRepo),
		fmt.Sprintf(`{"type":"assistant","sessionId":"only","cwd":%q,"timestamp":"2026-07-01T10:05:00Z","message":{"role":"assistant","content":[{"type":"text","text":"on it"},{"type":"tool_use","name":"Edit","input":{"file_path":%q}}]}}`, smallRepo, smallFile),
	}
	writeClaudeFixture(t, filepath.Join(smallRoot, "projects", "-repo", "only.jsonl"), "only", lines)
	if err = index.Ensure(os.Getenv("DEJA_INDEX_DIR"), "", false, nil); err != nil {
		t.Fatal(err)
	}
	quiet, err := captureRun(t, "files", "migration")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(quiet, "most recent were read") {
		t.Errorf("one matching session, and the answer still names a read budget:\n%s", quiet)
	}
}

// Retrieval hands candidates back in map order. `files` took the first
// filesMaxSessions of them, so on a store with more matches than the cap a
// different quarter of the sessions was counted on every run and the top file
// changed between two runs of the same binary over an unchanged index (#1072).
//
// The store here has 40 sessions past the cap. Each of the oldest 40 touches a
// file nothing else touches, so a cap that keeps the newest never reports one
// of them; a cap that keeps whatever the map yielded reports many.
func TestFilesCapKeepsTheNewestSessions(t *testing.T) {
	tmp := hermeticEnv(t)
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(repo, "shared.go")
	if err := os.WriteFile(shared, []byte("package repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	const old = 40
	total := filesMaxSessions + old
	for i := range total {
		id := fmt.Sprintf("s%04d", i)
		// i < old are the oldest; each touches a file of its own.
		touched := shared
		if i < old {
			touched = filepath.Join(repo, fmt.Sprintf("stale%02d.go", i))
			if err := os.WriteFile(touched, []byte("package repo\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		day := 1 + i/24
		hour := i % 24
		lines := []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":"2026-07-%02dT%02d:00:00Z","message":{"role":"user","content":"the migration rollback again"}}`, id, repo, day, hour),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":"2026-07-%02dT%02d:05:00Z","message":{"role":"assistant","content":[{"type":"text","text":"on it"},{"type":"tool_use","name":"Edit","input":{"file_path":%q}}]}}`, id, repo, day, hour, touched),
		}
		writeClaudeFixture(t, filepath.Join(root, "projects", "-repo", id+".jsonl"), id, lines)
	}
	if err := index.Ensure(os.Getenv("DEJA_INDEX_DIR"), "", false, nil); err != nil {
		t.Fatal(err)
	}

	first, err := captureRun(t, "files", "migration", "--limit", "60")
	if err != nil {
		t.Fatal(err)
	}
	if stale := strings.Count(first, "stale"); stale != 0 {
		t.Errorf("the cap kept %d of the %d oldest sessions — it took whatever order retrieval yielded:\n%s", stale, old, first)
	}
	// And the same answer twice: nondeterminism is what the reader actually
	// hit, and one run cannot show it.
	second, err := captureRun(t, "files", "migration", "--limit", "60")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("two runs over an unchanged index disagreed:\n--- first\n%s\n--- second\n%s", first, second)
	}
}

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
	filesAskFromNowhere(t, tmp)
	return repo
}

// filesAskFromNowhere runs `files` from a directory that is no project, so the
// answer does not depend on the checkout the tests run in. The package
// directory is a repository, and its worktrees' names — whatever the
// developer called them — become the scope `files` answers in (#4383).
func filesAskFromNowhere(t *testing.T, tmp string) {
	t.Helper()
	nowhere := filepath.Join(tmp, "nowhere")
	if err := os.MkdirAll(nowhere, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nowhere)
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
	filesTrustPolicyCase(t)
}

// `files` scopes its answer to the project of the working directory, and every
// worktree of that repository adds its names. A test running in this checkout
// read the developer's worktrees: one called `t` put "imported:svc" in scope
// and left the local session out, so the case above failed only while that
// worktree existed (#4383).
func TestFilesTrustPolicyIgnoresTheRunnersWorktrees(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	base := t.TempDir()
	main := filepath.Join(base, "checkout")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", main}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "seed")
	git("worktree", "add", "-q", "--detach", filepath.Join(base, "t"))
	t.Chdir(main)
	filesTrustPolicyCase(t)
}

func filesTrustPolicyCase(t *testing.T) {
	t.Helper()
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
	filesAskFromNowhere(t, tmp)
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

// files has its own flag loop, so the refusals every sibling command makes were
// missing here: a flag with no value, an unknown flag, an empty --project and an
// empty topic all passed through and the answer looked like an answer (#1628).
func TestFilesRefusesWhatDoesNothing(t *testing.T) {
	hermeticEnv(t)
	dir := t.TempDir()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"retry", "--limit"}, "--limit needs value"},
		{[]string{"retry", "--project"}, "--project needs value"},
		{[]string{"retry", "--project", ""}, "--project needs value"},
		{[]string{"retry", "--unknown"}, "unknown flag"},
		{[]string{""}, "usage"},
	} {
		err := runFiles(dir, tc.args, io.Discard)
		if err == nil {
			t.Errorf("files %v was accepted", tc.args)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("files %v: %v, want it to mention %q", tc.args, err, tc.want)
		}
	}
	// The controls: the shapes that work must keep working. An empty store
	// answers rather than failing, so these are about the parser, not the hits.
	for _, args := range [][]string{
		{"retry"},
		{"retry", "--limit", "3"},
		{"retry", "--project", "api"},
		{"retry", "backoff"},
	} {
		if err := runFiles(dir, args, io.Discard); err != nil {
			t.Errorf("files %v: %v", args, err)
		}
	}
}

// A topic can legitimately start with a dash — `--feature-flag`, `-x`. Refusing
// unknown flags would have left those unaskable, so files takes the same `--`
// escape parseSearch does (#1628).
func TestFilesTakesADashedTopicAfterDoubleDash(t *testing.T) {
	hermeticEnv(t)
	dir := t.TempDir()
	for _, args := range [][]string{
		{"--", "--feature-flag"},
		{"--", "-x"},
		{"--", "--limit", "3"},
	} {
		if err := runFiles(dir, args, io.Discard); err != nil {
			t.Errorf("files %v: %v", args, err)
		}
	}
	// The control: without the escape, a dashed token is still a mistake.
	if err := runFiles(dir, []string{"--feature-flag"}, io.Discard); err == nil {
		t.Error("a dashed topic was taken as a topic without the -- escape")
	}
	// And `--` with nothing after it names no topic.
	if err := runFiles(dir, []string{"--"}, io.Discard); err == nil {
		t.Error("a bare -- was accepted as a topic")
	}
}

// `files` matches by proximity to where a topic was discussed, which is
// deliberate and measured — so a topic nobody said out loud correctly matches
// nothing. What it did was stop there, on the surface most likely to be handed
// a filename, while blame and how both answer it (#2646).
func TestFilesPointsAtTheRecordsServedByRole(t *testing.T) {
	roleServedStore(t)
	out, err := captureRun(t, "files", "widgetpipeline")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no sessions mention") {
		t.Fatalf("the fixture no longer misses, so this pins nothing:\n%s", out)
	}
	if !strings.Contains(out, "deja blame") {
		t.Fatalf("nothing says a file on this machine matches it:\n%s", out)
	}
	if !strings.Contains(out, "deja how") {
		t.Fatalf("nothing says a command on this machine matches it:\n%s", out)
	}
}

// A topic this machine never saw is answered as before.
func TestFilesSaysNothingExtraForAnUnknownTopic(t *testing.T) {
	roleServedStore(t)
	out, err := captureRun(t, "files", "zonkomatic")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "deja blame") || strings.Contains(out, "deja how") {
		t.Fatalf("a word this machine never saw was pointed somewhere:\n%s", out)
	}
}

// The envelope test that came with `files --json` runs against the synthetic
// fixture, where the query returns no rows on any machine I have checked — so
// its per-row assertions never execute and the ranked branch is unpinned. This
// seeds a store whose recorded paths sit under a directory with a .git in it,
// which is what `inRepository` looks for, and checks the three counts a
// consumer re-ranks on: near touches, the sessions they came from, and the
// total that makes a file specific to the topic rather than merely busy.
func TestFilesJSONCarriesTheRankedRows(t *testing.T) {
	tmp := hermeticEnv(t)
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	render := filepath.Join(repo, "render.go")
	routes := filepath.Join(repo, "routes.go")
	for _, p := range []string{render, routes} {
		if err := os.WriteFile(p, []byte("package app\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	claude := filepath.Join(tmp, "claude")
	proj := filepath.Join(claude, "-repo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// Marshalled rather than pasted together: a Windows path inside a JSON
	// string is full of backslash escapes — `repo\render.go` carries a \r — so
	// a hand-built line parses as nothing there and the store comes up empty.
	rec := func(sid, role string, content any, at string) []byte {
		b, err := json.Marshal(map[string]any{
			"type": role, "sessionId": sid, "cwd": repo, "timestamp": at,
			"message": map[string]any{"role": role, "content": content},
		})
		if err != nil {
			t.Fatal(err)
		}
		return append(b, '\n')
	}
	edit := func(path string) []any {
		return []any{map[string]any{"type": "tool_use", "name": "Edit",
			"input": map[string]any{"file_path": path, "old_string": "a", "new_string": "b"}}}
	}
	// Three sessions on the topic. render.go is touched in all three, routes.go
	// in one, so the ranking has something to order and `sessions` differs
	// between the two rows.
	for i, sid := range []string{"s1", "s2", "s3"} {
		day := string(rune('1' + i))
		body := rec(sid, "user", "the envoy renderer keeps dropping routes", "2026-10-0"+day+"T09:00:00Z")
		body = append(body, rec(sid, "assistant", edit(render), "2026-10-0"+day+"T09:01:00Z")...)
		if sid == "s1" {
			body = append(body, rec(sid, "assistant", edit(routes), "2026-10-01T09:02:00Z")...)
		}
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), body, 0o600); err != nil {
			t.Fatal(err)
		}
		// A seed the parser cannot read indexes as nothing, and the assertions
		// below then fail for a reason that has nothing to do with ranking —
		// which is how this test went red on Windows and nowhere else.
		for line := range strings.SplitSeq(strings.TrimSpace(string(body)), "\n") {
			if !json.Valid([]byte(line)) {
				t.Fatalf("the seeded transcript is not JSON: %s", line)
			}
		}
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "files", "--json", "envoy")
	if err != nil {
		t.Fatalf("files --json: %v\n%s", err, out)
	}
	var env struct {
		SessionsScanned int `json:"sessions_scanned"`
		Matched         int `json:"matched"`
		Files           []struct {
			Path     string `json:"path"`
			Near     int    `json:"near"`
			Sessions int    `json:"sessions"`
			Total    int    `json:"total"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(env.Files) == 0 {
		t.Fatalf("the ranked branch returned no rows, so nothing here is pinned:\n%s", out)
	}
	if env.SessionsScanned == 0 || env.Matched == 0 {
		t.Fatalf("rows without the counts behind them: %+v\n%s", env, out)
	}
	byPath := map[string]int{}
	for _, f := range env.Files {
		byPath[f.Path] = f.Sessions
		if f.Near <= 0 {
			t.Fatalf("%s is in the list with no near touch: %+v", f.Path, f)
		}
		if f.Total < f.Near {
			t.Fatalf("%s has total %d below near %d", f.Path, f.Total, f.Near)
		}
	}
	// render.go was touched in three sessions and routes.go in one: the counts
	// a consumer re-ranks on have to carry that difference, not just be
	// present.
	if byPath[render] != 3 {
		t.Fatalf("render.go reports %d sessions, want 3:\n%s", byPath[render], out)
	}
	if got, ok := byPath[routes]; ok && got != 1 {
		t.Fatalf("routes.go reports %d sessions, want 1:\n%s", got, out)
	}
}

// `deja files` read its matching sessions one identity at a time, and a
// per-identity read walks the whole record log because records carry no
// per-session offset. On a 5000-session store that was 250 walks of a 59 MB
// log — 1.6 s of a 1.9 s command (#1069).
func TestFilesReadsTheRecordLogOnce(t *testing.T) {
	tmp := hermeticEnv(t)
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	const sessions = 12
	for i := range sessions {
		id := fmt.Sprintf("s%02d", i)
		path := filepath.Join(repo, fmt.Sprintf("mod%02d.go", i))
		if err := os.WriteFile(path, []byte("package repo\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		lines := []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":"2026-07-%02dT10:00:00Z","message":{"role":"user","content":"the migration rollback again"}}`, id, repo, i+1),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":"2026-07-%02dT10:05:00Z","message":{"role":"assistant","content":[{"type":"text","text":"on it"},{"type":"tool_use","name":"Edit","input":{"file_path":%q}}]}}`, id, repo, i+1, path),
		}
		writeClaudeFixture(t, filepath.Join(root, "projects", "-repo", id+".jsonl"), id, lines)
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	before := index.RecordLogScans()
	out, err := captureRun(t, "files", "migration")
	if err != nil {
		t.Fatal(err)
	}
	scans := index.RecordLogScans() - before
	if !strings.Contains(out, "files touched while working on") {
		t.Fatalf("the probe measured a command that answered nothing:\n%s", out)
	}
	if scans > 1 {
		t.Errorf("files walked the record log %d times for %d sessions, want 1", scans, sessions)
	}
}

// blame had the same loop, bounded at 50 sessions — a bound on how many walks
// of the whole log, not on the work (#1069).
func TestBlameReadsTheRecordLogOnce(t *testing.T) {
	tmp := hermeticEnv(t)
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(repo, "shared.go")
	if err := os.WriteFile(target, []byte("package repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	const sessions = 12
	for i := range sessions {
		id := fmt.Sprintf("b%02d", i)
		lines := []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":"2026-07-%02dT10:00:00Z","message":{"role":"user","content":"touch the shared file"}}`, id, repo, i+1),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":"2026-07-%02dT10:05:00Z","message":{"role":"assistant","content":[{"type":"text","text":"done"},{"type":"tool_use","name":"Edit","input":{"file_path":%q}}]}}`, id, repo, i+1, target),
		}
		writeClaudeFixture(t, filepath.Join(root, "projects", "-repo", id+".jsonl"), id, lines)
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	before := index.RecordLogScans()
	out, err := captureRun(t, "blame", target)
	if err != nil {
		t.Fatal(err)
	}
	scans := index.RecordLogScans() - before
	if strings.TrimSpace(out) == "" {
		t.Fatalf("the probe measured a command that answered nothing")
	}
	if scans > 1 {
		t.Errorf("blame walked the record log %d times for %d sessions, want 1", scans, sessions)
	}
}

// Asked in one repository, `deja files` used to answer with the busiest
// project's files: on a real store a generic topic named eight paths and not
// one of them was in the repository the question came from (#3713).
func TestFilesAnswersAboutTheProjectItWasAskedIn(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	here := filepath.Join(tmp, "work", "hereproj")
	if err := os.MkdirAll(filepath.Join(here, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	write := func(id, cwd, file string, touches int) {
		if err := os.MkdirAll(filepath.Join(cwd, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		root := filepath.Join(tmp, "claude", "proj-"+id)
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		fmt.Fprintf(&b, `{"type":"user","sessionId":%q,"cwd":%q,"timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"the timeout on the worker"}}`+"\n", id, cwd)
		for i := range touches {
			fmt.Fprintf(&b, `{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":"2026-07-20T10:0%d:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Read","input":{"file_path":%q}}]}}`+"\n",
				id, cwd, i+1, file)
		}
		if err := os.WriteFile(filepath.Join(root, id+".jsonl"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The other project is busier, which is what put it in front of the answer.
	write("hereproj", here, filepath.Join(here, "internal", "worker.go"), 2)
	write("otherproj", filepath.Join(tmp, "work", "otherproj"), filepath.Join(tmp, "work", "otherproj", "queue", "handler.go"), 9)

	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	t.Chdir(here)

	var buf strings.Builder
	if err := runFiles(index.DefaultDir(), []string{"timeout"}, &buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if strings.Contains(got, "handler.go") {
		t.Errorf("another project's file answered a question asked here:\n%s", got)
	}
	if !strings.Contains(got, "worker.go") {
		t.Errorf("this project's own file is missing:\n%s", got)
	}
	// And the answer says which project it is about, so an agent reading the
	// paths knows whether they are in the tree it is standing in.
	if !strings.Contains(got, "hereproj") {
		t.Errorf("the scope is not named:\n%s", got)
	}

	// The machine is still one flag away, and it has the busier project in it.
	buf.Reset()
	if err := runFiles(index.DefaultDir(), []string{"timeout", "--all-projects"}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "handler.go") {
		t.Errorf("--all-projects should reach the whole machine:\n%s", buf.String())
	}

	// A project with nothing to say gets the machine's answer rather than
	// "nobody mentioned it": an agent told nothing exists invents something.
	empty := filepath.Join(tmp, "work", "emptyproj")
	if err := os.MkdirAll(filepath.Join(empty, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(empty)
	buf.Reset()
	if err := runFiles(index.DefaultDir(), []string{"timeout"}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "handler.go") || !strings.Contains(buf.String(), "nothing in this project") {
		t.Errorf("a project with nothing to say should widen and say so:\n%s", buf.String())
	}
}

// A file name can hold an escape or a carriage return on any Unix host, and
// the path is recorded from the tool call verbatim. #1090 stripped those from
// the reading surfaces; this row was missed, so a name with a colour escape
// recoloured the rest of the screen and one with a return printed its tail
// over its head — the row then named a file the reader does not have.
func TestFilesRowStripsWhatTheTerminalActsOn(t *testing.T) {
	tmp := hermeticEnv(t)
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, 4)
	for _, name := range []string{
		"retry_\x1b[31mred\x1b[0m_handler.go",
		"retry_\rrewound_handler.go",
		"retry_\x07bell_handler.go",
		"plain_retry_handler.go",
	} {
		full := filepath.Join(repo, "src", name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("package x\n"), 0o644); err != nil {
			// Windows refuses these names; there the defect cannot be reached.
			t.Skipf("cannot create %q here: %v", name, err)
		}
		paths = append(paths, full)
	}

	root := filepath.Join(tmp, "claude", "proj-h")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	var body strings.Builder
	body.WriteString(`{"type":"user","sessionId":"h1","cwd":` + jsonString(repo) + `,"timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"the retry storm on checkout"}}` + "\n")
	for _, p := range paths {
		q, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		body.WriteString(`{"type":"assistant","sessionId":"h1","cwd":` + jsonString(repo) +
			`,"timestamp":"2026-07-20T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":` +
			string(q) + `}}]}}` + "\n")
	}
	if err := os.WriteFile(filepath.Join(root, "h1.jsonl"), []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	if err := runFiles(index.DefaultDir(), []string{"retry"}, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	// The fixture has to have reached the table, or this passes on an empty
	// answer.
	if !strings.Contains(out, "handler.go") {
		t.Fatalf("no rows in the table: %q", out)
	}
	for _, r := range out {
		if r == '\n' {
			continue
		}
		if unicode.IsControl(r) {
			t.Errorf("a control character reached the terminal: %q in %q", r, out)
			break
		}
	}

	// And the rows still line up: the escape bytes were counted as width
	// before, so the row carrying them ended in a different column.
	var widths []int
	var rows []string
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(line, "  ") {
			rows = append(rows, line)
			widths = append(widths, termwidth.Columns(line))
		}
	}
	if len(rows) < 2 {
		t.Fatalf("wrong fixture: %d rows", len(rows))
	}
	for i, w := range widths {
		if w != widths[0] {
			t.Errorf("row %d is %d columns, row 0 is %d:\n%s", i, w, widths[0], strings.Join(rows, "\n"))
		}
	}
}

// The escapes are stripped before the path is measured, not after. A run of
// them collapses to one space, so measuring the path with them still in it
// buys fewer characters of the name than the column has room for.
func TestFilesRowPathTrimsAfterStripping(t *testing.T) {
	const dir = "/w/repo/a_long_enough_directory_to_force_a_trim/nested/"
	const name = "_retry_backoff_and_jitter_handler.go"
	plain := filesRowPath(dir+"plain"+name, 40)
	escaped := filesRowPath(dir+"plain\x1b\x1b\x1b\x1b\x1b\x1b"+name, 40)
	if termwidth.Columns(escaped) != termwidth.Columns(plain) {
		t.Errorf("the escapes spent %d columns of the budget:\n  plain   %q\n  escaped %q",
			termwidth.Columns(plain)-termwidth.Columns(escaped), plain, escaped)
	}
	if termwidth.Columns(plain) > 40 {
		t.Errorf("row is %d columns: %q", termwidth.Columns(plain), plain)
	}
	// The trim has to have bitten, or both sides fit whole and the comparison
	// above is about nothing.
	if !strings.HasPrefix(plain, "…") || !strings.HasPrefix(escaped, "…") {
		t.Fatalf("wrong fixture, nothing was trimmed:\n  %q\n  %q", plain, escaped)
	}
	if !strings.HasSuffix(plain, "handler.go") {
		t.Fatalf("wrong fixture, the name did not survive the trim: %q", plain)
	}
}

// A newline in a file name is not a second row of deja's output.
func TestFilesRowPathStaysOnOneLine(t *testing.T) {
	got := filesRowPath("/w/repo/src/retry_\nsecond_line_handler.go", 56)
	if strings.Contains(got, "\n") {
		t.Errorf("the row carries a newline: %q", got)
	}
	if !strings.Contains(got, "second_line_handler.go") {
		t.Errorf("the tail after the newline was dropped rather than joined: %q", got)
	}
}

func TestTopicTimesNeedsHalfTheWords(t *testing.T) {
	at := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	ms := []model.Message{
		{Role: "user", Text: "the block compression floor", Time: at},
		{Role: "user", Text: "unrelated talk", Time: at.Add(time.Minute)},
		{Role: "files", Text: "/repo/block.go", Time: at},
		{Role: "assistant", Text: "no timestamp here"},
	}
	got := topicTimes(ms, []string{"block", "compression"})
	if len(got) != 1 || !got[0].Equal(at) {
		t.Fatalf("want the one message that carries the words, got %v", got)
	}
	// Half the words is enough: a turn saying "block layout" is still that work.
	if len(topicTimes(ms, []string{"block", "layout"})) != 1 {
		t.Fatal("half the words should match")
	}
	if len(topicTimes(ms, nil)) != 0 {
		t.Fatal("no needles, no anchors")
	}
}

func TestTopicTimesSkipsFileAndCommandRecords(t *testing.T) {
	at := time.Now()
	ms := []model.Message{
		{Role: "files", Text: "/repo/envoy/service.go", Time: at},
		{Role: "command", Text: "grep envoy service", Time: at},
	}
	if got := topicTimes(ms, []string{"envoy", "service"}); len(got) != 0 {
		t.Fatalf("a path is not someone discussing the subject, got %v", got)
	}
}

func TestWithinTime(t *testing.T) {
	at := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	if !withinTime(at.Add(5*time.Minute), []time.Time{at}) {
		t.Fatal("five minutes is inside a twenty minute window")
	}
	if withinTime(at.Add(time.Hour), []time.Time{at}) {
		t.Fatal("an hour later is different work")
	}
	if withinTime(time.Time{}, []time.Time{at}) {
		t.Fatal("a record with no time cannot be placed")
	}
}

func TestFilesWindowIsATimeSpan(t *testing.T) {
	// This is the whole feature: an untyped 20 here is twenty nanoseconds and
	// nothing is ever near anything.
	if filesWindow < time.Minute {
		t.Fatalf("filesWindow = %v, which is not a usable span", filesWindow)
	}
}

func TestIsScratch(t *testing.T) {
	for _, p := range []string{
		"/Users/x/.claude/projects/a.jsonl",
		"/Users/x/work/scratchpad/probe.py",
		"/Users/x/repo/node_modules/lib/index.js",
		"/Users/x/repo/build.log",
	} {
		if !isScratch(p) {
			t.Errorf("%s should be scratch", p)
		}
	}
	if isScratch("/Users/x/repo/internal/index/store.go") {
		t.Error("source in a repo is not scratch")
	}
}

func TestInRepositoryWalksUpForGit(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "internal", "index")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if inRepositoryUncached(filepath.Join(deep, "store.go")) {
		t.Fatal("no .git anywhere above it yet")
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !inRepositoryUncached(filepath.Join(deep, "store.go")) {
		t.Fatal("a file under a .git root belongs to a repository")
	}
	other := t.TempDir()
	if inRepositoryUncached(filepath.Join(other, "note.md")) {
		t.Fatal("a loose file beside no repository should be dropped")
	}
}

func TestTrimPath(t *testing.T) {
	// The head that was dropped is marked, or two files under one directory
	// read as relative paths starting in different places (#727).
	got := trimPath("/Users/x/coding/goprojects/deja-vu/internal/index/store.go")
	if got != "…/deja-vu/internal/index/store.go" {
		t.Fatalf("got %q", got)
	}
	if got := trimPath("/a/b.go"); got != "a/b.go" {
		t.Fatalf("short paths keep their shape, got %q", got)
	}
}

func TestLowerAllDropsShortWords(t *testing.T) {
	got := lowerAll([]string{"Sing", "Box", "in", "GO"})
	if len(got) != 2 || got[0] != "sing" || got[1] != "box" {
		t.Fatalf("got %v", got)
	}
}

// `deja files --json` (#1930). Every sibling command has one; this one printed
// prose only, so a caller scripting against it had to parse a padded column.
func TestFilesJSONEnvelope(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "synthetic", "claude"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(t.TempDir(), "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(t.TempDir(), "opencode.db"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index.db"))

	out, err := captureRun(t, "files", "--json", "parser")
	if err != nil {
		t.Fatalf("files --json err=%v out=%q", err, out)
	}
	var env struct {
		SchemaVersion   int    `json:"schema_version"`
		Query           string `json:"query"`
		SessionsScanned int    `json:"sessions_scanned"`
		Matched         int    `json:"matched"`
		ReadCapped      bool   `json:"read_capped"`
		Truncated       bool   `json:"truncated"`
		Filtered        int    `json:"filtered"`
		Withheld        int    `json:"withheld"`
		Ignored         int    `json:"ignored"`
		Files           []struct {
			Path     string `json:"path"`
			Near     int    `json:"near"`
			Sessions int    `json:"sessions"`
			Total    int    `json:"total"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("files --json is not valid JSON: %v\n%s", err, out)
	}
	if env.SchemaVersion != jsonout.Version {
		t.Fatalf("schema_version=%d want %d", env.SchemaVersion, jsonout.Version)
	}
	if env.Query != "parser" {
		t.Fatalf("query=%q want %q", env.Query, "parser")
	}
	// The list may legitimately be empty on this fixture; what must hold is
	// that every row is well formed and that `total` is never smaller than
	// `near`, since near-touches are a subset of the touches counted.
	for _, f := range env.Files {
		if f.Path == "" {
			t.Fatalf("row with no path: %+v", f)
		}
		if f.Total < f.Near {
			t.Fatalf("total %d < near %d for %s", f.Total, f.Near, f.Path)
		}
		if f.Sessions <= 0 {
			t.Fatalf("row claims no sessions: %+v", f)
		}
	}
}

// A miss must still be an envelope, not prose on stdout: a consumer that got
// one shape on a hit and a sentence on a miss would have to sniff the output
// before parsing it.
func TestFilesJSONOnAMissIsStillAnEnvelope(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(t.TempDir(), "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(t.TempDir(), "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(t.TempDir(), "opencode.db"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index.db"))

	out, err := captureRun(t, "files", "--json", "nothing-mentions-this-topic")
	if err != nil {
		t.Fatalf("files --json on a miss err=%v out=%q", err, out)
	}
	var env struct {
		SchemaVersion int   `json:"schema_version"`
		Files         []any `json:"files"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("miss is not valid JSON: %v\n%s", err, out)
	}
	if env.SchemaVersion != jsonout.Version {
		t.Fatalf("schema_version=%d want %d", env.SchemaVersion, jsonout.Version)
	}
	if len(env.Files) != 0 {
		t.Fatalf("miss returned %d files", len(env.Files))
	}
}

// `files` refuses unknown flags by design (#1628); --json must not have opened
// a hole in that.
func TestFilesStillRefusesUnknownFlags(t *testing.T) {
	if err := runFiles(t.TempDir(), []string{"--jsonn", "topic"}, io.Discard); err == nil {
		t.Fatal("unknown flag accepted")
	}
	if err := runFiles(t.TempDir(), []string{"--json"}, io.Discard); err == nil {
		t.Fatal("--json with no topic accepted")
	}
}

// The words a topic is asked in are the needles the file list anchors on, and
// they were taken from argv rather than from the query: `deja files "a b c"`
// looked for the whole sentence inside one message and found nothing, while
// `deja files a b c` — the same words, different shell quoting — answered
// (#3409).
func TestFilesAnswersTheSameQuotedOrNot(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "retry.go"), []byte("package api\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-w-api", "p1.jsonl"), "p1", []string{
		`{"type":"user","sessionId":"p1","cwd":"/w/api","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"the zonkobuffer drops the last attempt of the retry loop"}}`,
		`{"type":"assistant","sessionId":"p1","cwd":"/w/api","timestamp":"2026-07-01T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"` + jsonEscaped(t, filepath.Join(repo, "retry.go")) + `","old_string":"a","new_string":"b"}}]}}`,
	})
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	split, err := captureRun(t, "files", "zonkobuffer", "retry", "loop")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(split, "retry.go") {
		t.Fatalf("the fixture does not answer even word by word, so this pins nothing:\n%s", split)
	}
	quoted, err := captureRun(t, "files", "zonkobuffer retry loop")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(quoted, "retry.go") {
		t.Errorf("quoting the same words empties the answer:\n%s", quoted)
	}
}

// The path column is a column, and a path under a Chinese directory is one
// rune and two columns per character. Counting runes let a 27-rune path fill
// 46 of a 30-column budget, so the counts beside it landed wherever the text
// ended (#604 budgeted the column; this is the same column measured right).
func TestTrimPathToBoundsColumns(t *testing.T) {
	for _, tc := range []struct {
		name  string
		in    string
		width int
	}{
		{"latin", "…/very/long/directory/name/retry_backoff_handler.go", 30},
		{"chinese", "…/项目/内部服务/重试队列/退避处理器实现文件.go", 30},
		{"mixed", "…/项目/src/内部服务/queue/退避处理器.go", 24},
		{"narrow", "…/项目/内部服务/重试队列/退避处理器实现文件.go", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := trimPathTo(tc.in, tc.width)
			if n := termwidth.Columns(got); n > tc.width {
				t.Errorf("trimPathTo(%q, %d) prints %d columns: %q", tc.in, tc.width, n, got)
			}
			// What comes back has to be a literal tail of the input, mark
			// aside: a cut through a wide character would leave text that is
			// the right width and not the same path.
			if tail := strings.TrimPrefix(got, "…"); !strings.HasSuffix(tc.in, tail) {
				t.Errorf("trimPathTo(%q, %d) = %q, which is not a tail of it", tc.in, tc.width, got)
			}
		})
	}
}

// A path that already fits is not touched, and a wide character is never cut
// in half to reach the budget exactly.
func TestTrimPathToLeavesWhatFits(t *testing.T) {
	short := "…/项目/退避.go"
	if got := trimPathTo(short, 30); got != short {
		t.Errorf("a path inside the budget was trimmed: %q", got)
	}
	// 12 columns of a path whose characters are two columns wide: the cut has
	// to stop at 12, not at 13 with half a character.
	got := trimPathTo("…/项目/内部服务/重试队列/退避处理器.go", 12)
	if n := termwidth.Columns(got); n > 12 {
		t.Errorf("cut to %d columns: %q", n, got)
	}
	if !strings.HasPrefix(got, "…") {
		t.Errorf("the cut is not marked: %q", got)
	}
}

// End to end: every row of the table has to end its path in the same column,
// or the counts do not read as a column at all.
func TestFilesTableAlignsCJKPaths(t *testing.T) {
	tmp := hermeticEnv(t)
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, 3)
	for _, rel := range []string{
		"项目/内部服务/重试队列/退避处理器实现文件.go",
		"项目/内部服务/重试队列/工作进程唤醒抖动逻辑.go",
		"src/app/internal/queue/retry_backoff_handler.go",
	} {
		full := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, full)
	}

	root := filepath.Join(tmp, "claude", "proj-cj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	var body strings.Builder
	body.WriteString(`{"type":"user","sessionId":"c1","cwd":` + jsonString(repo) + `,"timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"the retry storm on checkout"}}` + "\n")
	for _, p := range paths {
		body.WriteString(`{"type":"assistant","sessionId":"c1","cwd":` + jsonString(repo) + `,"timestamp":"2026-07-20T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":` + jsonString(p) + `}}]}}` + "\n")
	}
	if err := os.WriteFile(filepath.Join(root, "c1.jsonl"), []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	if err := runFiles(index.DefaultDir(), []string{"retry"}, &buf); err != nil {
		t.Fatal(err)
	}

	var widths []int
	var rows []string
	for line := range strings.SplitSeq(buf.String(), "\n") {
		if !strings.HasPrefix(line, "  ") {
			continue
		}
		rows = append(rows, line)
		widths = append(widths, termwidth.Columns(line))
	}
	if len(rows) != 3 {
		t.Fatalf("wrong fixture: %d rows in %q", len(rows), buf.String())
	}
	// One of them has to be a CJK path, or the test is about Latin alignment.
	if !strings.Contains(strings.Join(rows, ""), "项目") {
		t.Fatalf("no CJK row in the table: %q", rows)
	}
	// And each row still names a real file: padding to a column budget must
	// not be reached by cutting through a character.
	for _, row := range rows {
		shown := strings.TrimPrefix(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(row), "1 session")), "…")
		var named bool
		for _, p := range paths {
			if strings.HasSuffix(filepath.ToSlash(p), shown) {
				named = true
				break
			}
		}
		if !named {
			t.Errorf("row names no seeded file: %q", row)
		}
	}
	for i, w := range widths {
		if w != widths[0] {
			t.Errorf("row %d is %d columns, row 0 is %d:\n%s", i, w, widths[0], strings.Join(rows, "\n"))
		}
	}
}

// A synced index holds paths as the machine that wrote them spelled them, so a
// Windows transcript puts `C:\src\app\x.go` in front of a Unix reader. Splitting
// on filepath.Separator alone made such a path a single segment, so its head was
// never removed: the Unix twin of the same file trimmed to four segments and the
// Windows one printed whole, past the 56-wide column it shares. CrossBase
// (commands.go) was written for the same reason on the same kind of path.
func TestTrimPathTrimsAWindowsOriginPathToo(t *testing.T) {
	cases := []struct{ in, want string }{
		{`C:\src\app\internal\deep\handler.go`, "…/app/internal/deep/handler.go"},
		{`C:\a\b\c.go`, "C:/a/b/c.go"},
		// Mixed, which a path that crossed a sync can be.
		{`C:\src/app\internal/deep\x.go`, "…/app/internal/deep/x.go"},
		// And the Unix behaviour is untouched.
		{"/a/b/c/d.go", "a/b/c/d.go"},
		{"/a/b/c/d/e.go", "…/b/c/d/e.go"},
		{"retry.go", "retry.go"},
	}
	for _, c := range cases {
		if got := trimPath(c.in); got != c.want {
			t.Errorf("trimPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// The column is 56 wide; a real Windows checkout path is longer than that
	// untrimmed, which is the whole point.
	long := `C:\Users\someone\source\repos\Company\Project\src\Services\PaymentHandler.cs`
	if got := trimPath(long); len(got) > 56 || !strings.HasPrefix(got, "…/") {
		t.Errorf("a long windows path came back as %q (%d chars)", got, len(got))
	}
}

// Two files under one directory printed as "tmp/b7/repo/x.go" and
// "b7/repo/internal/y.go", which read as relative paths starting in different
// places rather than as one tree with its head removed (#727).
func TestTrimPathMarksTheCut(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/tmp/b7/repo/retry.go", "tmp/b7/repo/retry.go"},
		{"/tmp/b7/repo/internal/backoff.go", "…/b7/repo/internal/backoff.go"},
		{"/a/b/c/d/e/f/g.go", "…/d/e/f/g.go"},
		{"retry.go", "retry.go"},
		{"repo/retry.go", "repo/retry.go"},
		// Four segments is the limit, and dropping the empty root of an
		// absolute path is not a truncation.
		{"/a/b/c/d.go", "a/b/c/d.go"},
		{"/a/b/c/d/e.go", "…/b/c/d/e.go"},
	}
	for _, c := range cases {
		if got := trimPath(c.in); got != c.want {
			t.Errorf("trimPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A filter the caller set is not the topic's fault: sessions can mention it and
// still be absent because they are in another project (#727).
func TestFilesEmptyAnswerNamesTheProjectFilter(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	body := `{"type":"user","sessionId":"s1","cwd":"/w/p","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"the retry storm on checkout"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	if err := runFiles(index.DefaultDir(), []string{"retry", "--project", "nosuch"}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `in project "nosuch"`) {
		t.Errorf("filtered answer: %q", buf.String())
	}
	buf.Reset()
	if err := runFiles(index.DefaultDir(), []string{"nothing-like-this"}, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "in project") {
		t.Errorf("unfiltered answer mentions a project: %q", buf.String())
	}
}
