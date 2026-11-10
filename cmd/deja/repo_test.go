package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// repoPerson points every store deja reads and writes at one person's temp
// home. Two of them on one test stand are two machines.
func repoPerson(t *testing.T, base, who string) string {
	t.Helper()
	home := filepath.Join(base, who)
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(home, "claude"))
	t.Setenv("HERMES_HOME", filepath.Join(home, "hermes"))
	t.Setenv("DEJA_NOTES_FILE", "")
	return home
}

func repoGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "init.defaultBranch=main", "-c", "pull.rebase=false"}, args...)
	cmd := exec.CommandContext(t.Context(), "git", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func repoStandBase(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return base
}

// One person writes an entry into a repository, a second clones it and gets
// the entry in recall and at session start; parallel additions merge.
func TestRepoMemoryTravelsWithAClone(t *testing.T) {
	base := repoStandBase(t)
	origin := filepath.Join(base, "origin.git")
	repoGit(t, base, "init", "-q", "--bare", origin)

	repoPerson(t, base, "alice")
	alice := filepath.Join(base, "alice", "src", "webhooks")
	repoGit(t, base, "clone", "-q", origin, alice)
	t.Chdir(alice)
	if _, err := captureRun(t, "remember", "the outbox table owns webhook retries"); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "repo", "candidates", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var cands []repoCandidate
	if err = json.Unmarshal([]byte(out), &cands); err != nil || len(cands) == 0 {
		t.Fatalf("candidates %q: %v", out, err)
	}
	if cands[0].Kind != "decision" || !strings.Contains(cands[0].Text, "outbox table") {
		t.Fatalf("candidate = %+v", cands[0])
	}
	if out, err = captureRun(t, "repo", "add", cands[0].ID); err != nil || !strings.Contains(out, "added 1 entry") {
		t.Fatalf("add: %q %v", out, err)
	}
	if out, err = captureRun(t, "repo", "add", "--kind", "rejected", "retry with jitter in the webhook sender broke idempotency"); err != nil {
		t.Fatalf("add --kind: %q %v", out, err)
	}
	if !strings.Contains(out, "nothing is committed") {
		t.Errorf("add did not say it commits nothing: %q", out)
	}
	if st := repoGit(t, alice, "status", "--porcelain"); !strings.Contains(st, ".deja/") {
		t.Fatalf("nothing to review: %q", st)
	}
	b, _ := os.ReadFile(sources.RepoMemoryPath(alice))
	for _, leak := range []string{base, "alice/src", "claude:"} {
		if strings.Contains(string(b), leak) {
			t.Errorf("file holds %q:\n%s", leak, b)
		}
	}
	repoGit(t, alice, "add", ".deja")
	repoGit(t, alice, "commit", "-qm", "repo memory")
	repoGit(t, alice, "push", "-q", "origin", "main")

	repoPerson(t, base, "bob")
	bob := filepath.Join(base, "bob", "code", "webhooks")
	repoGit(t, base, "clone", "-q", origin, bob)
	if _, err = os.Stat(filepath.Join(bob, ".deja", "README.md")); err != nil {
		t.Errorf("the clone has no .deja/README.md: %v", err)
	}
	t.Chdir(bob)
	out, err = captureRun(t, "recall", "retry jitter webhook")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[rejected] retry with jitter in the webhook sender broke idempotency") || !strings.Contains(out, "repo:") {
		t.Fatalf("bob's recall misses the entry:\n%s", out)
	}
	if !strings.HasPrefix(out, recallFrameHeader) {
		t.Errorf("recall is not framed:\n%s", out)
	}
	if strings.Contains(out, "abandoned one approach") {
		t.Errorf("a rejected entry read as an abandoned session:\n%s", out)
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	digest := hookDigest(dir)
	if !strings.Contains(digest, "from this repository's .deja/memory.jsonl") || !strings.Contains(digest, "[rejected] retry with jitter") {
		t.Fatalf("session start misses the entry:\n%s", digest)
	}
	if i, j := strings.Index(digest, "[rejected]"), strings.Index(digest, "[decision]"); i < 0 || j < 0 || i > j {
		t.Errorf("rejected should lead the block:\n%s", digest)
	}

	// The trust policy keeps it out per path.
	pol := filepath.Join(base, "bob", "config", "deja", "policy.json")
	if err = os.MkdirAll(filepath.Dir(pol), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(pol, []byte(`{"activations":{"auto":{"repo":false},"mcp":{"repo":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if d := hookDigest(dir); strings.Contains(d, "retry with jitter") {
		t.Errorf("denied repo memory reached session start:\n%s", d)
	}
	if out, _ := captureRun(t, "recall", "retry jitter webhook"); strings.Contains(out, "[rejected] retry with jitter") {
		t.Errorf("denied repo memory reached recall:\n%s", out)
	}
	if out, _ := captureRun(t, "repo"); !strings.Contains(out, "keeps repo memory out of mcp, auto") {
		t.Errorf("repo list does not name the policy:\n%s", out)
	}
	_ = os.Remove(pol)

	// Both add an entry at once; the merge takes both.
	if _, err = captureRun(t, "repo", "add", "--kind", "gotcha", "the staging receiver drops payloads over 1 MB"); err != nil {
		t.Fatal(err)
	}
	repoGit(t, bob, "commit", "-qam", "bob entry")
	repoPerson(t, base, "alice")
	t.Chdir(alice)
	if _, err = captureRun(t, "repo", "add", "--kind", "decision", "signatures use the delivery id, not the attempt"); err != nil {
		t.Fatal(err)
	}
	repoGit(t, alice, "commit", "-qam", "alice entry")
	repoGit(t, alice, "push", "-q", "origin", "main")
	t.Chdir(bob)
	repoGit(t, bob, "pull", "-q", "--no-edit", "origin", "main")
	entries, bad, err := sources.ReadRepoMemory(sources.RepoMemoryPath(bob))
	if err != nil || bad != 0 || len(entries) != 4 {
		b, _ := os.ReadFile(sources.RepoMemoryPath(bob))
		t.Fatalf("after merge: %d entries, %d bad, %v\n%s", len(entries), bad, err, b)
	}
}

// A reversal an agent reported in a session is proposed as a rejected entry,
// and its provenance names the agent and the day only.
func TestRepoCandidatesFromASession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture names a Claude project folder the unix way")
	}
	base := repoStandBase(t)
	home := repoPerson(t, base, "erin")
	root := filepath.Join(base, "erin", "webhooks")
	repoGit(t, base, "init", "-q", root)
	folder := filepath.Join(home, "claude", strings.ReplaceAll(root, "/", "-"))
	lines := []string{
		`{"type":"user","sessionId":"s1","cwd":"` + root + `","timestamp":"2026-08-03T10:00:00Z","message":{"role":"user","content":"deliveries get duplicated, add retry with jitter"}}`,
		`{"type":"assistant","sessionId":"s1","cwd":"` + root + `","timestamp":"2026-08-03T10:35:00Z","message":{"role":"assistant","content":[{"type":"text","text":"Reverted the retry with jitter: it broke idempotency because each attempt got a new delivery id.\nWe decided to keep one delivery id per event and let the outbox own retries."}]}}`,
	}
	writeClaudeFixture(t, filepath.Join(folder, "s1.jsonl"), "s1", lines)
	t.Chdir(root)
	out, err := captureRun(t, "repo", "candidates")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "rejected Reverted the retry with jitter") || !strings.Contains(out, "claude 2026-08-03") {
		t.Fatalf("candidates:\n%s", out)
	}
	if !strings.Contains(out, "decision") {
		t.Errorf("the session's decision is not proposed:\n%s", out)
	}
	if _, err := os.Stat(sources.RepoMemoryPath(root)); err == nil {
		t.Error("candidates wrote the file")
	}
	if out, err := captureRun(t, "repo", "candidates", "--since", "1d"); err != nil || !strings.Contains(out, "no candidates") {
		t.Errorf("--since kept old candidates: %q %v", out, err)
	}
}

func TestRepoAddRefusesWithoutRedaction(t *testing.T) {
	t.Setenv("DEJA_NO_REDACT", "1")
	if _, _, err := repoSafeText("anything"); err == nil || !strings.Contains(err.Error(), "DEJA_NO_REDACT") {
		t.Fatalf("err = %v", err)
	}
}

func TestRepoSafeTextMasksSecretsAndHomes(t *testing.T) {
	out, masked, err := repoSafeText("use token ghp_abcdefghijklmnopqrstuvwxyz0123456789 from /Users/alice/src/app/.env and mail ops@corp.example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"ghp_", "/Users/alice", "ops@"} {
		if strings.Contains(out, leak) {
			t.Errorf("%q survived: %s", leak, out)
		}
	}
	if masked < 3 {
		t.Errorf("masked = %d in %q", masked, out)
	}
	if _, _, err := repoSafeText(strings.Repeat("word ", sources.RepoTextMax)); err == nil {
		t.Error("an entry over the cap was accepted")
	}
	if _, _, err := repoSafeText(" \n "); err == nil {
		t.Error("an empty entry was accepted")
	}
}

func TestRepoRememberThroughMCP(t *testing.T) {
	base := repoStandBase(t)
	repoPerson(t, base, "carol")
	root := filepath.Join(base, "carol", "api")
	repoGit(t, base, "init", "-q", root)
	t.Chdir(root)
	dir := os.Getenv("DEJA_INDEX_DIR")
	out, err := callMCPTool(dir, "remember", []byte(`{"text":"the vendored client is patched, do not bump it","project":"repo:gotcha"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "not committed") {
		t.Errorf("reply = %q", out)
	}
	entries, _, err := sources.ReadRepoMemory(sources.RepoMemoryPath(root))
	if err != nil || len(entries) != 1 || entries[0].Kind != "gotcha" {
		t.Fatalf("entries %v err %v", entries, err)
	}
	if _, err := callMCPTool(dir, "remember", []byte(`{"text":"x","project":"repo:opinion"}`)); err == nil {
		t.Error("an unknown kind was accepted")
	}
	// A plain project still files a note on this machine.
	if _, err := callMCPTool(dir, "remember", []byte(`{"text":"local only","project":"api"}`)); err != nil {
		t.Fatal(err)
	}
	if entries, _, _ := sources.ReadRepoMemory(sources.RepoMemoryPath(root)); len(entries) != 1 {
		t.Errorf("a note went into the repository: %v", entries)
	}
}

func TestRepoCommandErrors(t *testing.T) {
	base := repoStandBase(t)
	repoPerson(t, base, "dan")
	t.Chdir(base)
	if _, err := captureRun(t, "repo"); err == nil || !strings.Contains(err.Error(), "not inside a git repository") {
		t.Errorf("outside a repo: %v", err)
	}
	root := filepath.Join(base, "r")
	repoGit(t, base, "init", "-q", root)
	t.Chdir(root)
	if out, err := captureRun(t, "repo"); err != nil || !strings.Contains(out, "no .deja/memory.jsonl") {
		t.Errorf("empty list: %q %v", out, err)
	}
	if _, err := captureRun(t, "repo", "add", "ffffffffff"); err == nil || !strings.Contains(err.Error(), "not a candidate id") {
		t.Errorf("unknown id: %v", err)
	}
	if _, err := captureRun(t, "repo", "add", "--kind", "opinion", "x"); err == nil {
		t.Error("unknown kind accepted")
	}
	if _, err := captureRun(t, "repo", "add"); err == nil {
		t.Error("add with nothing accepted")
	}
	if _, err := captureRun(t, "repo", "--bogus"); err == nil {
		t.Error("unknown flag accepted")
	}
	if _, err := captureRun(t, "repo", "candidates", "--limit", "0"); err == nil {
		t.Error("--limit 0 accepted")
	}
	if out, err := captureRun(t, "repo", "candidates"); err != nil || !strings.Contains(out, "no candidates") {
		t.Errorf("no candidates: %q %v", out, err)
	}
	if _, err := captureRun(t, "repo", "add", "--kind=decision", "--", "-v is the verbose flag"); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "repo", "add", "--kind", "decision", "-v is the verbose flag"); err == nil {
		t.Errorf("a dash word without -- was taken: %q", out)
	}
	out, err := captureRun(t, "repo", "--json")
	if err != nil || !strings.Contains(out, `"kind":"decision"`) {
		t.Errorf("--json: %q %v", out, err)
	}
	if err := os.WriteFile(sources.RepoMemoryPath(root), []byte("<<<<<<< HEAD\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, _ := captureRun(t, "repo"); !strings.Contains(out, "cannot read") {
		t.Errorf("bad line not counted: %q", out)
	}
	if _, err := captureRun(t, "repo", "add", "--kind", "decision", "another"); err == nil || !strings.Contains(err.Error(), "fix or delete") {
		t.Errorf("write over a conflict: %v", err)
	}
}

func TestRepoRemoveAndFreshCloneSurfaces(t *testing.T) {
	base := repoStandBase(t)
	repoPerson(t, base, "erin")
	root := filepath.Join(base, "erin", "billing")
	repoGit(t, base, "init", "-q", root)
	t.Chdir(root)
	dir := os.Getenv("DEJA_INDEX_DIR")

	if _, err := captureRun(t, "repo", "add", "--kind", "gotcha", "webhook_handler.go must verify X-Signature before json.Unmarshal"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "repo", "add", "--kind", "rejected", "never run goreleaser without --clean in release scripts"); err != nil {
		t.Fatal(err)
	}

	// 1. Cold SessionStart BEFORE any index exists on this machine still serves .deja/memory.jsonl.
	oldSpawn := spawnWarmup
	spawnWarmup = func(string, string) error { return nil }
	t.Cleanup(func() { spawnWarmup = oldSpawn })
	entry := hookDigestBuild(dir, root, nil, false, nil)
	if !strings.Contains(entry.Digest, "webhook_handler.go must verify X-Signature") {
		t.Fatalf("cold hookDigestBuild without manifest missed repo memory: %q", entry.Digest)
	}
	if got := repoMemoryCountInDigest(entry.Digest); got != 2 {
		t.Errorf("repoMemoryCountInDigest = %d, want 2", got)
	}

	// 2. Point-of-action PreToolUse hook on file edit and command with 0 local sessions.
	fileIn := toolHookInput{ToolName: "Edit", CWD: root}
	fileIn.ToolInput.FilePath = filepath.Join(root, "internal", "webhook_handler.go")
	if line := toolHookLine(dir, root, fileIn); !strings.Contains(line, "X-Signature") {
		t.Errorf("toolHookLine for file on fresh clone = %q, want X-Signature gotcha", line)
	}
	cmdIn := toolHookInput{ToolName: "Bash", CWD: root}
	cmdIn.ToolInput.Command = "goreleaser release"
	if line := toolHookLine(dir, root, cmdIn); !strings.Contains(line, "goreleaser") {
		t.Errorf("toolHookLine for command on fresh clone = %q, want goreleaser rejected note", line)
	}

	// 3. Orient mode on a fresh clone with 0 local sessions surfaces .deja/memory.jsonl.
	orientOut, err := callMCPTool(dir, "deja", []byte(`{"mode":"orient"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(orientOut, ".deja/memory.jsonl") || !strings.Contains(orientOut, "X-Signature") {
		t.Errorf("orient on fresh clone = %q", orientOut)
	}

	// 4. Doctor noteBucketsRegrouped does not mistake indexed .deja/memory.jsonl entries for regrouped day-notes.
	if moved := noteBucketsRegrouped(dir); moved != 0 {
		t.Errorf("noteBucketsRegrouped = %d, want 0", moved)
	}

	// 5. deja repo rm removes an entry by id or 1-based number.
	entries, _, err := sources.ReadRepoMemory(sources.RepoMemoryPath(root))
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries = %v, %v", entries, err)
	}
	rmOut, err := captureRun(t, "repo", "rm", entries[0].ID)
	if err != nil || !strings.Contains(rmOut, "removed 1 entry") {
		t.Fatalf("repo rm: %q, %v", rmOut, err)
	}
	after, _, err := sources.ReadRepoMemory(sources.RepoMemoryPath(root))
	if err != nil || len(after) != 1 {
		t.Fatalf("after rm: %v, %v", after, err)
	}
}

func TestRepoZeroFrictionCLIAndTaskRanking(t *testing.T) {
	base := repoStandBase(t)
	repoPerson(t, base, "frank")
	root := filepath.Join(base, "frank", "payments")
	repoGit(t, base, "init", "-q", root)
	t.Chdir(root)

	// 1. Subcommand shorthands (`deja repo gotcha`, `deja repo rejected`, `deja repo decision`) and auto-kind `deja repo add`.
	if _, err := captureRun(t, "repo", "gotcha", "stripe_webhook.go must verify Stripe-Signature before reading body"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "repo", "add", "never use floating-point math in invoice_calc.go; use integer cents"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "repo", "decision", "all ledger rows are append-only in ledger_store.go"); err != nil {
		t.Fatal(err)
	}
	entries, _, err := sources.ReadRepoMemory(sources.RepoMemoryPath(root))
	if err != nil || len(entries) != 3 {
		t.Fatalf("entries = %v, %v", entries, err)
	}

	// 2. Task-aware ranking: when ledger_store.go is in taskFiles, its decision entry ranks ahead of unrelated gotchas.
	block := repoMemoryBlock(root, policy.Load(), "internal/ledger_store.go")
	iLedger := strings.Index(block, "ledger_store.go")
	iStripe := strings.Index(block, "stripe_webhook.go")
	if iLedger < 0 || iStripe < 0 || iLedger > iStripe {
		t.Errorf("expected task-matched ledger_store.go entry to lead repoMemoryBlock:\n%s", block)
	}

	// 3. Remove by 1-based number (`deja repo rm 1`) and `remember --repo` unification.
	if _, err = captureRun(t, "repo", "rm", "1"); err != nil {
		t.Fatalf("repo rm 1: %v", err)
	}
	if _, err = captureRun(t, "remember", "--repo", "never retry 402 card_declined responses in charge_worker.go"); err != nil {
		t.Fatalf("remember --repo: %v", err)
	}
	entries, _, err = readRepoMemoryAndNotes(root)
	if err != nil || len(entries) != 3 {
		t.Fatalf("after remember --repo: %v, %v", entries, err)
	}

	// 4. `deja repo init` populates .deja/memory.jsonl from history candidates in one command.
	_ = os.Remove(sources.RepoMemoryPath(root))
	if _, err = captureRun(t, "remember", "idempotency keys expire after 24 hours in redis_store.go"); err != nil {
		t.Fatal(err)
	}
	initOut, err := captureRun(t, "repo", "init", "--limit", "2")
	if err != nil || !strings.Contains(initOut, "added") {
		t.Fatalf("repo init: %q, %v", initOut, err)
	}
	if usableRepoCandidateLine("Починил (998fce953), локальный бинарник пересобран") {
		t.Error("ephemeral status report was accepted as a repo candidate")
	}
}

func TestRepoAutoSyncAndToggle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture names a Claude project folder the unix way")
	}
	base := repoStandBase(t)
	home := repoPerson(t, base, "grace")
	root := filepath.Join(base, "grace", "orders")
	repoGit(t, base, "init", "-q", root)
	workerFile := filepath.Join(root, "internal", "worker.go")
	if err := os.MkdirAll(filepath.Dir(workerFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workerFile, []byte("package internal\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repoGit(t, root, "add", "internal/worker.go")
	repoGit(t, root, "commit", "-qm", "add worker")

	folder := filepath.Join(home, "claude", strings.ReplaceAll(root, "/", "-"))
	lines := []string{
		`{"type":"user","sessionId":"s1","cwd":"` + root + `","timestamp":"2026-08-03T10:00:00Z","message":{"role":"user","content":"parallelize order dispatch"}}`,
		`{"type":"assistant","sessionId":"s1","cwd":"` + root + `","timestamp":"2026-08-03T10:05:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"` + workerFile + `","old_string":"package internal","new_string":"package internal // v2"}}]}}`,
		`{"type":"user","sessionId":"s1","cwd":"` + root + `","timestamp":"2026-08-03T10:06:00Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`,
		`{"type":"assistant","sessionId":"s1","cwd":"` + root + `","timestamp":"2026-08-03T10:10:00Z","message":{"role":"assistant","content":[{"type":"text","text":"Reverted the goroutine fan-out because concurrent dispatch broke FIFO per-account ordering."}]}}`,
	}
	writeClaudeFixture(t, filepath.Join(folder, "s1.jsonl"), "s1", lines)
	t.Chdir(root)
	dir := os.Getenv("DEJA_INDEX_DIR")

	syncOut, err := captureRun(t, "repo", "sync")
	if err != nil || !strings.Contains(syncOut, "synced .deja/memory.jsonl") {
		t.Fatalf("repo sync: %q, %v", syncOut, err)
	}
	entries, _, err := sources.ReadRepoMemory(sources.RepoMemoryPath(root))
	if err != nil || len(entries) != 1 || len(entries[0].Files) != 1 || entries[0].Files[0] != "internal/worker.go" {
		t.Fatalf("synced entries = %+v, err=%v", entries, err)
	}

	// PreToolUse matches internal/worker.go via e.Files even though the text never names worker.go.
	fileIn := toolHookInput{ToolName: "Edit", CWD: root}
	fileIn.ToolInput.FilePath = workerFile
	if line := toolHookLine(dir, root, fileIn); !strings.Contains(line, "FIFO per-account ordering") {
		t.Errorf("PreToolUse on bound file = %q, want FIFO per-account ordering", line)
	}

	// Toggle off and back on.
	if out, err := captureRun(t, "repo", "off"); err != nil || !strings.Contains(out, "are off") || !sources.RepoAutoDisabled(root) {
		t.Fatalf("repo off: %q, %v", out, err)
	}
	if out, err := captureRun(t, "repo", "on"); err != nil || !strings.Contains(out, "are on") || sources.RepoAutoDisabled(root) {
		t.Fatalf("repo on: %q, %v", out, err)
	}
}

func TestRepoCandidateTrimAndMentionedFileAttribution(t *testing.T) {
	// Sentence-boundary trim avoids cutting mid-word when a sentence ends in the second half.
	long := strings.Repeat("a", 180) + ". " + strings.Repeat("b", 140) + " opencode.db.bak"
	got := trimRepoCandidateText(long)
	if got != strings.Repeat("a", 180)+"." {
		t.Fatalf("expected sentence-boundary cut, got %q", got)
	}
	// When no sentence boundary exists, word-boundary trim avoids cutting mid-token.
	noPeriod := strings.Repeat("word ", 58) + "opencode.db.bak_very_long_filename"
	gotWord := trimRepoCandidateText(noPeriod)
	if strings.Contains(gotWord, "opencode.db.") {
		t.Fatalf("expected word-boundary cut before mid-token, got %q", gotWord)
	}

	// Mentioned files take priority over unrelated recent edits from another episode.
	edits := []repoEditEvent{
		{file: "cmd/deja/hook_antigravity.go"},
		{file: "internal/redact/redact.go"},
	}
	files := episodeFilesForText(
		"В internal/redact/redact.go исправлена ошибка маскирования прованс-тегов.",
		edits, 0, len(edits), time.Time{}, []string{"cmd/deja/hook_antigravity.go", "internal/redact/redact.go"},
	)
	if len(files) != 1 || files[0] != "internal/redact/redact.go" {
		t.Fatalf("mentioned file attribution = %v, want [internal/redact/redact.go]", files)
	}
	// Unique basename mention resolves to its repo path.
	baseFiles := episodeFilesForText(
		"В redact.go исправлена ошибка маскирования.",
		edits, 0, len(edits), time.Time{}, nil,
	)
	if len(baseFiles) != 1 || baseFiles[0] != "internal/redact/redact.go" {
		t.Fatalf("basename file attribution = %v, want [internal/redact/redact.go]", baseFiles)
	}
	// Current-turn edit slice [start:end] scopes attribution when no file is named in text.
	turnFiles := episodeFilesForText(
		"исправлена ошибка маскирования",
		edits, 1, 2, time.Time{}, []string{"cmd/deja/hook_antigravity.go"},
	)
	if len(turnFiles) != 1 || turnFiles[0] != "internal/redact/redact.go" {
		t.Fatalf("turn-scoped file attribution = %v, want [internal/redact/redact.go]", turnFiles)
	}

	for _, noisy := range []string{
		"PR #5960 (`@KiMelody`) — описана причина падения всех 12 тестовых шардов и способ поддержки config.json.",
		"Карта проекта и онбординг — было C, стало A В mcp_orient.go: - Команды ранжируются выше.",
		"You asked whether the Zed context-server duplicate wiring was already fixed; it was, in #1571.",
	} {
		if usableRepoCandidateLine(noisy) {
			t.Errorf("usableRepoCandidateLine(%q) = true, want false", noisy)
		}
	}

	root := t.TempDir()
	_, err := sources.AddRepoEntries(root, []sources.RepoEntry{
		{ID: "1111111111", Kind: "decision", Text: "PR #5960 (`@KiMelody`) — описана причина падения всех 12 тестовых шардов и способ поддержки config.json.", From: "antigravity 2026-11-07"},
		{ID: "2222222222", Kind: "gotcha", Text: "Never include the count of supported coding agents inside docs/assets/og.png so social preview images do not become stale.", From: "deja 2026-11-01"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := pruneFilteredRepoEntries(root); n != 1 {
		t.Fatalf("pruneFilteredRepoEntries = %d, want 1", n)
	}
	left, _, err := sources.ReadRepoMemory(sources.RepoMemoryPath(root))
	if err != nil || len(left) != 1 || left[0].ID != "2222222222" {
		t.Fatalf("remaining entries = %+v, err = %v", left, err)
	}
}

func TestHookPrecisionAndGuardrails(t *testing.T) {
	base := repoStandBase(t)
	_ = repoPerson(t, base, "grace")
	root := filepath.Join(base, "grace", "svc")
	repoGit(t, base, "init", "-q", root)
	guardedFile := filepath.Join(root, "internal", "auth", "token.go")
	plainFile := filepath.Join(root, "internal", "auth", "plain.go")
	if err := os.MkdirAll(filepath.Dir(guardedFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(guardedFile, []byte("package auth\nfunc VerifyToken() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plainFile, []byte("package auth\nfunc PlainHelper() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repoGit(t, root, "add", "internal/auth/token.go", "internal/auth/plain.go")
	repoGit(t, root, "commit", "-qm", "add auth files")

	_, err := sources.AddRepoEntries(root, []sources.RepoEntry{
		{
			ID:    "aaaa111122",
			Kind:  "rejected",
			Text:  "Never cache raw JWT strings in memory inside internal/auth/token.go; hash with SHA-256 first.",
			Files: []string{"internal/auth/token.go"},
			From:  "deja 2026-11-08",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(base, "index.db")
	// 1. PreToolUse Read on guarded file emits the .deja/memory.jsonl guardrail;
	// PreToolUse Read on plain file stays silent.
	inGuarded := toolHookInput{HookEventName: "PreToolUse", ToolName: "Read", CWD: root}
	inGuarded.ToolInput.FilePath = guardedFile
	readGuarded := fileReadGuardLine(dir, root, inGuarded)
	if !strings.Contains(readGuarded, "in .deja/memory.jsonl:") || !strings.Contains(readGuarded, "Never cache raw JWT strings") {
		t.Fatalf("PreToolUse Read on guarded file = %q, want .deja/memory.jsonl guardrail", readGuarded)
	}
	inPlain := toolHookInput{HookEventName: "PreToolUse", ToolName: "Read", CWD: root}
	inPlain.ToolInput.FilePath = plainFile
	readPlain := fileReadGuardLine(dir, root, inPlain)
	if readPlain != "" {
		t.Fatalf("PreToolUse Read on unguarded file = %q, want empty", readPlain)
	}

	// 2. PostToolUse failure citing a guarded file in compiler/test output surfaces its .deja/memory.jsonl rule.
	failDiag := fixPairLineFor(dir, root, "go test ./internal/auth/...", "--- FAIL: TestVerify (0.01s)\n    token.go:42: raw token cached")
	if !strings.Contains(failDiag, "in .deja/memory.jsonl (internal/auth/token.go): [rejected]") {
		t.Fatalf("fixPairLineFor on guarded file failure = %q, want .deja/memory.jsonl diagnostic", failDiag)
	}

	// 3. sharesEnvironmentTerm filters unrelated error/remedy pairs and keeps matching ones.
	if sharesEnvironmentTerm("IndexError: list index out of range", "gh api repos/acme/widget/commits") {
		t.Error("sharesEnvironmentTerm accepted unrelated IndexError -> gh api pair")
	}
	if !sharesEnvironmentTerm("zsh:1: command not found: golangci-lint", "/Users/alex/go/bin/golangci-lint run") {
		t.Error("sharesEnvironmentTerm rejected matching missing-program remedy")
	}

	// 4. orientSkipFixError and orientDestructiveRemedy filter branch-protection push errors and git push remedies.
	if !orientSkipFixError("error: failed to push some refs to 'https://github.com/org/repo.git'") {
		t.Error("orientSkipFixError did not skip git push ref rejection")
	}
	if !orientDestructiveRemedy("git push -u origin feat/test") {
		t.Error("orientDestructiveRemedy did not filter git push remedy")
	}

	// 5. search.SafeLine strips Braille spinner characters without touching normal text.
	if got := search.SafeLine("Output: ⣾ ⣽ ⣻ ⢿ ⡿ ⣟ ⣯ ⣷ build passed"); got != "Output: build passed" {
		t.Fatalf("SafeLine spinner strip = %q, want %q", got, "Output: build passed")
	}

	// 6. Live cross-file caller guard & zero-history sibling test target resolution.
	callerFile := filepath.Join(root, "cmd", "server", "handler.go")
	err = os.MkdirAll(filepath.Dir(callerFile), 0o755)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(callerFile, []byte("package server\nimport \"svc/internal/auth\"\nfunc Handle() { auth.PlainHelper() }\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	testFile := filepath.Join(root, "internal", "auth", "plain_test.go")
	err = os.WriteFile(testFile, []byte("package auth\nimport \"testing\"\nfunc TestPlainHelperFlow(t *testing.T) {\n\tPlainHelper()\n}\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	editLine := fileHookLineOutsideWithEdit(dir, root, plainFile, "func PlainHelper(strict bool) error {\n\treturn nil\n}", nil)
	if !strings.Contains(editLine, "callers of PlainHelper: cmd/server/handler.go:3") {
		t.Fatalf("zero-history signature edit line = %q, want caller cmd/server/handler.go:3", editLine)
	}
	if !strings.Contains(editLine, "test target: `go test ./internal/auth -run '^TestPlainHelperFlow$'`") {
		t.Fatalf("zero-history signature edit line = %q, want sibling test target", editLine)
	}

	// 7. repoMemoryBlock counts runes rather than UTF-8 bytes so non-ASCII entries do not starve the block.
	_, err = sources.AddRepoEntries(root, []sources.RepoEntry{
		{
			ID:   "bbbb222233",
			Kind: "decision",
			Text: "Первое архитектурное правило проекта на русском языке, которое раньше занимало слишком много байт в UTF-8 и вытесняло остальные записи.",
			From: "deja 2026-11-08",
		},
		{
			ID:   "cccc333344",
			Kind: "gotcha",
			Text: "Второе важное правило проекта на русском языке, которое обязательно должно помещаться в стартовый блок памяти репозитория.",
			From: "deja 2026-11-08",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	block := repoMemoryBlock(root, policy.Load())
	if strings.Count(block, "  · [") != 3 || !strings.Contains(block, "Первое архитектурное правило") || !strings.Contains(block, "Второе важное правило") {
		t.Fatalf("repoMemoryBlock with Cyrillic entries = %q, want all 3 rows shown", block)
	}

	// 8. Turn-0 Symbol Brief on UserPromptSubmit when prompt names a repo symbol without file path.
	symBrief := promptDirectContext(dir, root, "sess-sym-1", "почему падает функция PlainHelper при вызове?")
	if !strings.Contains(symBrief, "defined in internal/auth/plain.go:2: func PlainHelper()") || !strings.Contains(symBrief, "test target: `go test ./internal/auth -run '^TestPlainHelperFlow$'`") {
		t.Fatalf("promptDirectContext symbol brief = %q, want file:line + signature + test target", symBrief)
	}

	// 9. PostToolUse compiler error resolves live symbol signature from repo.
	compDiag := fixPairLineFor(dir, root, "go build ./...", "cmd/server/handler.go:3:18: too many arguments in call to auth.PlainHelper\n\thave (bool)\n\twant ()")
	if !strings.Contains(compDiag, "symbol signature in repo — internal/auth/plain.go:2: func PlainHelper()") {
		t.Fatalf("fixPairLineFor compiler signature diagnostic = %q, want live repo signature", compDiag)
	}

	// 10. Broken Test Assertion Predictor on PreToolUse Edit & Instant Failing Test Source Line on PostToolUse.
	err = os.WriteFile(testFile, []byte("package auth\nimport \"testing\"\nfunc TestPlainHelperFlow(t *testing.T) {\n\tPlainHelper()\n\tif got := \"auth-v1-header\"; got != \"auth-v1-header\" {\n\t\tt.Fatal(got)\n\t}\n}\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	assertWarn := testAssertionLiteralLine(root, plainFile, "const header = \"auth-v1-header\"", "const header = \"auth-v2-header\"")
	if !strings.Contains(assertWarn, "test assertion in plain_test.go:5 (TestPlainHelperFlow) checks \"auth-v1-header\"") {
		t.Fatalf("testAssertionLiteralLine = %q, want plain_test.go:5 assertion warning", assertWarn)
	}
	failSnippet := fixPairLineFor(dir, root, "go test ./internal/auth/...", "--- FAIL: TestPlainHelperFlow (0.01s)\n    plain_test.go:4: unexpected result\nFAIL")
	if !strings.Contains(failSnippet, "failing test line — internal/auth/plain_test.go:4 (TestPlainHelperFlow): PlainHelper()") {
		t.Fatalf("failingTestSourceLine = %q, want plain_test.go:4 source line", failSnippet)
	}

	// 11. Polyglot Zero-History Test Target Filter on Python and TS/JS.
	pyFile := filepath.Join(root, "internal", "auth", "login.py")
	pyTestFile := filepath.Join(root, "internal", "auth", "test_login.py")
	if werr := os.WriteFile(pyFile, []byte("def login_user():\n    pass\n"), 0o644); werr != nil {
		t.Fatal(werr)
	}
	if werr := os.WriteFile(pyTestFile, []byte("def test_login_flow():\n    login_user()\n"), 0o644); werr != nil {
		t.Fatal(werr)
	}
	pyLine := siblingTestLineFor(root, pyFile, []string{"login_user"})
	if !strings.Contains(pyLine, "pytest internal/auth/test_login.py -k 'test_login_flow'") {
		t.Fatalf("siblingTestLineFor python = %q, want pytest -k filter", pyLine)
	}

	tsFile := filepath.Join(root, "internal", "auth", "client.ts")
	tsTestFile := filepath.Join(root, "internal", "auth", "client.test.ts")
	if werr := os.WriteFile(tsFile, []byte("export function callClient() {}\n"), 0o644); werr != nil {
		t.Fatal(werr)
	}
	if werr := os.WriteFile(tsTestFile, []byte("it('handles client flow', () => {\n  callClient()\n})\n"), 0o644); werr != nil {
		t.Fatal(werr)
	}
	tsLine := siblingTestLineFor(root, tsFile, []string{"callClient"})
	if !strings.Contains(tsLine, "npx vitest run internal/auth/client.test.ts -t 'handles client flow'") {
		t.Fatalf("siblingTestLineFor ts = %q, want npx vitest run -t filter", tsLine)
	}

	// 12. Polyglot Broken Test Assertion Predictor on Python.
	pyFoo := filepath.Join(root, "internal", "auth", "foo.py")
	pyFooTest := filepath.Join(root, "internal", "auth", "test_foo.py")
	if werr := os.WriteFile(pyFooTest, []byte("def test_foo():\n    assert token == \"secret-token-v1\"\n"), 0o644); werr != nil {
		t.Fatal(werr)
	}
	assertPy := testAssertionLiteralLine(root, pyFoo, "token = \"secret-token-v1\"", "token = \"secret-token-v2\"")
	if !strings.Contains(assertPy, "test assertion in test_foo.py:2 (test_foo) checks \"secret-token-v1\"") {
		t.Fatalf("testAssertionLiteralLine python = %q, want test_foo.py:2 assertion warning", assertPy)
	}

	// 13. Polyglot Instant Failing Test Source Line on pytest and vitest.
	pyFailSnippet := failingTestSourceLine(root, "FAILED internal/auth/test_login.py:2: in test_login_flow\nAssertionError\nFAIL", policy.Load())
	if !strings.Contains(pyFailSnippet, "failing test line — internal/auth/test_login.py:2 (test_login_flow): login_user()") {
		t.Fatalf("failingTestSourceLine pytest = %q, want test_login.py:2 source line", pyFailSnippet)
	}
	tsFailSnippet := failingTestSourceLine(root, "FAIL internal/auth/client.test.ts > handles client flow\n ❯ internal/auth/client.test.ts:2:3\n AssertionError", policy.Load())
	if !strings.Contains(tsFailSnippet, "failing test line — internal/auth/client.test.ts:2 (handles client flow): callClient()") {
		t.Fatalf("failingTestSourceLine vitest = %q, want client.test.ts:2 source line", tsFailSnippet)
	}

	// 14. Coupled-File git commit Guardrail.
	_, err = sources.AddRepoEntries(root, []sources.RepoEntry{
		{
			ID:    "coupled-install-auto",
			Kind:  "gotcha",
			Text:  "Editing install_auto.go requires updating extensions/opencode/index.js alongside it",
			Files: []string{"cmd/deja/install_auto.go", "extensions/opencode/index.js"},
			From:  "deja 2026-11-08",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	guardWarn := coupledCommitGuardForFiles(root, []string{"cmd/deja/install_auto.go"})
	if !strings.Contains(guardWarn, "commit touches cmd/deja/install_auto.go without coupled extensions/opencode/index.js") ||
		!strings.Contains(guardWarn, "in .deja/memory.jsonl: [gotcha]") {
		t.Fatalf("coupledCommitGuardForFiles = %q, want commit touches guard warning", guardWarn)
	}
	if okWarn := coupledCommitGuardForFiles(root, []string{"cmd/deja/install_auto.go", "extensions/opencode/index.js"}); okWarn != "" {
		t.Fatalf("coupledCommitGuardForFiles with both coupled files = %q, want empty", okWarn)
	}
	if !orientSkipFixError("grep -n \"func orient\" cmd/deja/*.go") {
		t.Error("orientSkipFixError did not skip grep exit-1 search command")
	}
	if isOrientActionableCorrection("The following is a <SYSTEM_MESSAGE> not actually sent by the user.") {
		t.Error("isOrientActionableCorrection accepted <SYSTEM_MESSAGE> wrapper")
	}

	// 15. Multi-harness edit dialects (MultiEdit, Target/Replacement, oldText/newText, full-file Write).
	var inpMulti toolHookInput
	multiJSON := `{"tool_name":"MultiEdit","tool_input":{"file_path":"cmd/server/handler.go","edits":[{"old_string":"foo","new_string":"bar"},{"target":"alpha","replacement":"beta"}]}}`
	err = json.Unmarshal([]byte(multiJSON), &inpMulti)
	if err != nil {
		t.Fatal(err)
	}
	inpMulti.adopt()
	if inpMulti.ToolInput.OldString != "foo\nalpha" || inpMulti.ToolInput.NewString != "bar\nbeta" {
		t.Fatalf("MultiEdit adopt: got old=%q, new=%q", inpMulti.ToolInput.OldString, inpMulti.ToolInput.NewString)
	}

	var inpTarget toolHookInput
	targetJSON := `{"tool_name":"edit_file","tool_input":{"Target":"auth-v1-header","Replacement":"auth-v2-header","path":"internal/auth/plain.go"}}`
	err = json.Unmarshal([]byte(targetJSON), &inpTarget)
	if err != nil {
		t.Fatal(err)
	}
	inpTarget.adopt()
	if inpTarget.ToolInput.FilePath != "internal/auth/plain.go" || inpTarget.ToolInput.OldString != "auth-v1-header" || inpTarget.ToolInput.NewString != "auth-v2-header" {
		t.Fatalf("Target/Replacement adopt: got path=%q old=%q new=%q", inpTarget.ToolInput.FilePath, inpTarget.ToolInput.OldString, inpTarget.ToolInput.NewString)
	}

	var inpSnake toolHookInput
	snakeJSON := `{"tool_name":"str_replace_editor","tool_input":{"oldText":"hello","newText":"world","path":"foo.go"}}`
	err = json.Unmarshal([]byte(snakeJSON), &inpSnake)
	if err != nil {
		t.Fatal(err)
	}
	inpSnake.adopt()
	if inpSnake.ToolInput.OldString != "hello" || inpSnake.ToolInput.NewString != "world" {
		t.Fatalf("oldText/newText adopt: got old=%q new=%q", inpSnake.ToolInput.OldString, inpSnake.ToolInput.NewString)
	}

	// Full-file Write on existing file on disk: triggers testAssertionLiteralLine and crossFileCallersLine.
	err = os.WriteFile(plainFile, []byte("package auth\nconst header = \"auth-v1-header\"\nfunc PlainHelper() {}\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	var inpWrite toolHookInput
	writeJSON := fmt.Sprintf(`{"tool_name":"write_file","tool_input":{"path":%q,"content":"package auth\nfunc PlainHelper(flag bool) error {\n\treturn nil\n}\n"}}`, plainFile)
	err = json.Unmarshal([]byte(writeJSON), &inpWrite)
	if err != nil {
		t.Fatal(err)
	}
	inpWrite.adopt()
	writeHookLine := fileLineFor(dir, root, inpWrite)
	if !strings.Contains(writeHookLine, "callers of PlainHelper: cmd/server/handler.go:3") {
		t.Fatalf("full-file write line = %q, want callers of PlainHelper", writeHookLine)
	}
	if !strings.Contains(writeHookLine, "test assertion in plain_test.go:5") {
		t.Fatalf("full-file write line = %q, want broken test assertion warning", writeHookLine)
	}

	// 16. Universal Live Repo Intelligence Recall Brief for symbol query.
	realCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	realRepoRoot := filepath.Dir(filepath.Dir(realCWD))
	liveBrief := liveRecallBrief(dir, realRepoRoot, "coupledCommitGuardLine")
	if !strings.Contains(liveBrief, "live repo brief:") ||
		!strings.Contains(liveBrief, "defined in cmd/deja/hook_tool.go:") ||
		!strings.Contains(liveBrief, "func coupledCommitGuardLine") ||
		!strings.Contains(liveBrief, "callers of coupledCommitGuardLine: cmd/deja/hook_tool.go:") ||
		!strings.Contains(liveBrief, "test target: `go test ./cmd/deja -run '^TestHookPrecisionAndGuardrails$'`") {
		t.Fatalf("liveRecallBrief = %q, want definition + caller + test target", liveBrief)
	}

	// 17. Anti-Hallucination Package Symbol Predictor.
	searchDir := filepath.Join(root, "internal", "search")
	err = os.MkdirAll(searchDir, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(filepath.Join(searchDir, "snippet_format.go"), []byte("package search\n\nfunc SafeLine(s string) string {\n\treturn s\n}\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	repoGit(t, root, "add", "internal/search/snippet_format.go")
	repoGit(t, root, "commit", "-qm", "add search")

	hallucinatedEdit := fmt.Sprintf(`{"tool_name":"Edit","tool_input":{"file_path":%q,"old_string":"func VerifyToken() {}","new_string":"func VerifyToken() {\n\t_ = search.CleanLine(\"jwt\")\n}"}}`, guardedFile)
	var inpHal toolHookInput
	err = json.Unmarshal([]byte(hallucinatedEdit), &inpHal)
	if err != nil {
		t.Fatal(err)
	}
	inpHal.adopt()
	halLine := fileLineFor(dir, root, inpHal)
	if !strings.Contains(halLine, "search has no exported CleanLine") ||
		!strings.Contains(halLine, "closest match in internal/search/snippet_format.go:3: func SafeLine(s string) string") {
		t.Fatalf("hallucinated symbol line = %q, want CleanLine -> SafeLine closest match warning", halLine)
	}

	// 18. Live Same-Session User Rule Pinning.
	installAutoDir := filepath.Join(root, "cmd", "deja")
	err = os.MkdirAll(installAutoDir, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(filepath.Join(installAutoDir, "install_auto.go"), []byte("package main\n\nfunc InstallAuto() {}\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	repoGit(t, root, "add", "cmd/deja/install_auto.go")
	repoGit(t, root, "commit", "-qm", "add install_auto")

	userPromptJSON := fmt.Sprintf(`{"hook_event_name":"UserPromptSubmit","prompt":"never use cp in install_auto.go, always use install -m 755","cwd":%q,"session_id":"live-pin-session"}`, root)
	var promptOut bytes.Buffer
	err = runHookPrompt(dir, strings.NewReader(userPromptJSON), &promptOut)
	if err != nil {
		t.Fatal(err)
	}
	var inpLive toolHookInput
	liveEditJSON := fmt.Sprintf(`{"tool_name":"Edit","tool_input":{"file_path":"cmd/deja/install_auto.go","old_string":"func InstallAuto() {}","new_string":"func InstallAuto() { /* edit */ }"},"cwd":%q,"session_id":"live-pin-session"}`, root)
	err = json.Unmarshal([]byte(liveEditJSON), &inpLive)
	if err != nil {
		t.Fatal(err)
	}
	inpLive.adopt()
	liveRuleLine := fileLineFor(dir, root, inpLive)
	if !strings.Contains(liveRuleLine, "in .deja/memory.jsonl: [rule] never use cp in install_auto.go, always use install -m 755") {
		t.Fatalf("live rule line = %q, want pinned rule surfaced in fileLineFor", liveRuleLine)
	}

	// 19. Relational Turn-Pair Proposal -> Refusal & Anti-Nagging Session Rule (/compact scenario).
	stepsCompact := []transcriptStep{
		// Newest first, as transcriptTailSteps returns:
		{
			Type:    "USER_INPUT",
			Content: "<USER_REQUEST>\nдавай доработаем все дыры, а компакт я делать не буду, он итак автоматический на 262к\n</USER_REQUEST>",
		},
		{
			Type:    "PLANNER_RESPONSE",
			Content: "Прожми здесь **`/compact`** — это сразу сбросит стоимость шага с `235k` до `~20k`.",
		},
	}
	syncAntigravitySessionRules(dir, "", "compact-rule-test", stepsCompact)
	for _, tc := range []struct {
		lang   string
		prompt string
	}{
		{"RU", "ну что? как идет экономия и эффективная трата лимитов?"},
		{"EN", "how is token quota saving and context budget going?"},
		{"ZH", "现在的额度节省和上下文优化效果怎么样？"},
		{"JA", "トークン節約とコンテキスト圧縮の効果はどうですか？"},
	} {
		antiNagOut := promptDirectContext(dir, "", "compact-rule-test", tc.prompt)
		if !strings.Contains(antiNagOut, "User standing rule (do not violate or re-suggest): Do not suggest /compact: компакт я делать не буду, он итак автоматический на 262к") {
			t.Fatalf("[%s] antiNagOut = %q, want standing /compact refusal surfaced", tc.lang, antiNagOut)
		}
	}
	if unrelated := promptDirectContext(dir, "", "compact-rule-test", "fix typo in README.md"); strings.Contains(unrelated, "Do not suggest /compact") {
		t.Fatalf("unrelated prompt triggered /compact rule: %q", unrelated)
	}

	// 20. Co-Occurrence Context Vector across sessions: assistant proposes `--fast-flush`
	// for `rocksdb` / `badger`, user refuses without repeating `rocksdb` or `badger`, and a
	// later session in the same project asks about `rocksdb` durability.
	stepsRocks := []transcriptStep{
		{
			Type:    "USER_INPUT",
			Content: "<USER_REQUEST>\nНет, падает надежность при сбое питания\n</USER_REQUEST>",
		},
		{
			Type:    "PLANNER_RESPONSE",
			Content: "Включить `--fast-flush` для `rocksdb` в `badger` ради ускорения записи?",
		},
	}
	syncAntigravitySessionRules(dir, root, "rocksdb-session-1", stepsRocks)
	crossSessionOut := promptDirectContext(dir, root, "rocksdb-session-2", "давай ускорим запись через rocksdb в этой ветке")
	if !strings.Contains(crossSessionOut, "Do not suggest --fast-flush: Нет, падает надежность при сбое питания") {
		t.Fatalf("crossSessionOut = %q, want --fast-flush refusal recalled via rocksdb co-occurrence context", crossSessionOut)
	}

	// 21. Polyglot sibling test discovery (Python, TypeScript, Rust), cross-file callers,
	// Rust panic target parsing, and compiler symbol signature lookup.
	pyDir := filepath.Join(root, "pypkg")
	if err = os.MkdirAll(pyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(pyDir, "worker.py"), []byte("def process_batch(items):\n    return len(items)\n"), 0o644)
	_ = os.WriteFile(filepath.Join(pyDir, "test_worker.py"), []byte("def test_process_batch():\n    assert process_batch([1, 2]) == 2\n"), 0o644)
	if got := siblingTestLineFor(root, filepath.Join(pyDir, "worker.py"), []string{"process_batch"}); !strings.Contains(got, "pytest pypkg/test_worker.py -k 'test_process_batch'") {
		t.Fatalf("Python siblingTestLineFor = %q", got)
	}

	tsDir := filepath.Join(root, "tspkg")
	if err = os.MkdirAll(tsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(tsDir, "router.ts"), []byte("export function resolveRoute(p: string) { return '/api/v1/orders'; }\n"), 0o644)
	_ = os.WriteFile(filepath.Join(tsDir, "router.test.ts"), []byte("it('resolves route path', () => {\n  expect(resolveRoute('/api/v1/orders')).toBe('/api/v1/orders');\n});\n"), 0o644)
	if got := siblingTestLineFor(root, filepath.Join(tsDir, "router.ts"), []string{"resolveRoute"}); !strings.Contains(got, "npx vitest run tspkg/router.test.ts -t 'resolves route path'") {
		t.Fatalf("TS siblingTestLineFor = %q", got)
	}

	rsDir := filepath.Join(root, "rspkg")
	if err = os.MkdirAll(rsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(rsDir, "engine.rs"), []byte("pub fn compute_hash(x: u32) -> u32 { x + 1 }\n#[test]\nfn test_compute_hash() {\n    assert_eq!(compute_hash(1), 2);\n}\n"), 0o644)
	if got := siblingTestLineFor(root, filepath.Join(rsDir, "engine.rs"), []string{"compute_hash"}); !strings.Contains(got, "cargo test test_compute_hash") {
		t.Fatalf("Rust siblingTestLineFor = %q", got)
	}

	_ = os.WriteFile(filepath.Join(installAutoDir, "caller.go"), []byte("package main\n\nfunc RunCaller() {\n\tInstallAuto()\n}\n"), 0o644)
	if got := crossFileCallersLine(root, filepath.Join(installAutoDir, "install_auto.go"), []string{"InstallAuto"}); !strings.Contains(got, "callers of InstallAuto: cmd/deja/caller.go:4") {
		t.Fatalf("crossFileCallersLine = %q", got)
	}

	rustPanicOut := "thread 'engine::tests::test_compute_hash' panicked at rspkg/engine.rs:4:5:\nassertion `left == right` failed\n--- FAIL: TestSomething (0.01s)\nerror: build failed"
	if fp, ln, tn := parseRustPanicTarget(rustPanicOut); fp != "rspkg/engine.rs" || ln != 4 || tn != "test_compute_hash" {
		t.Fatalf("parseRustPanicTarget = (%q, %d, %q)", fp, ln, tn)
	}
	if probes := nearErrorProbesFromOutput(rustPanicOut); len(probes) < 2 {
		t.Fatalf("nearErrorProbesFromOutput = %v", probes)
	}
	if line := failingTestSourceLine(root, rustPanicOut, policy.Load()); !strings.Contains(line, "rspkg/engine.rs:4") {
		t.Fatalf("failingTestSourceLine(rustPanic) = %q", line)
	}
	if line := compilerSymbolSignatureLine(root, "cmd/deja/caller.go:4:2: too many arguments in call to InstallAuto\n", policy.Load()); !strings.Contains(line, "func InstallAuto()") {
		t.Fatalf("compilerSymbolSignatureLine = %q", line)
	}
	if line := repoMemoryLineForFailedOutput(root, "go test ./...", "--- FAIL: TestToken (0.01s)\n    internal/auth/token.go:3: unexpected token", policy.Load()); !strings.Contains(line, "Never cache raw JWT strings") {
		t.Fatalf("repoMemoryLineForFailedOutput = %q", line)
	}
	if disk, rel := locateFailingFileInScanRoot(root, "engine.rs", "", ""); !strings.HasSuffix(rel, "engine.rs") || disk == "" {
		t.Fatalf("locateFailingFileInScanRoot = (%q, %q)", disk, rel)
	}
	if sbLine, _ := promptSymbolBriefLine(root, "check InstallAuto callers", nil); !strings.Contains(sbLine, "defined in cmd/deja/install_auto.go:3") {
		t.Fatalf("promptSymbolBriefLine = %q", sbLine)
	}
	if litLine := testAssertionLiteralLine(root, filepath.Join(tsDir, "router.ts"), "export function resolveRoute() { return '/api/v1/orders'; }", "export function resolveRoute() { return '/api/v2/orders'; }"); !strings.Contains(litLine, "router.test.ts") {
		t.Fatalf("testAssertionLiteralLine = %q", litLine)
	}
	if enc := enclosingSymbolForSnippet("func ProcessOrder() {\n\tvalidateOrderPayload := 12345\n}\n", "+ validateOrderPayload := 12345"); enc != "ProcessOrder" {
		t.Fatalf("enclosingSymbolForSnippet = %q", enc)
	}
	if !coChangeCompanionBetter("foo_test.go", "foo.go") || !isAmbiguousCoChangeBase("main.go") {
		t.Fatal("expected coChangeCompanionBetter and isAmbiguousCoChangeBase to return true")
	}
	authDir := filepath.Join(root, "internal", "auth")
	if err = os.MkdirAll(authDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(authDir, "token.go"), []byte("package auth\n\nfunc VerifyBearerToken(raw string) bool { return raw != \"\" }\n"), 0o644)
	if halLine := hallucinatedPkgSymbolLine(root, filepath.Join(installAutoDir, "caller.go"), "", "auth.VerifyBearerTokens(\"x\")"); !strings.Contains(halLine, "VerifyBearerToken") {
		t.Fatalf("hallucinatedPkgSymbolLine = %q", halLine)
	}
	if chg := changedDeclarationSymbols(root, filepath.Join(authDir, "token.go"), "func VerifyBearerToken(raw string, strict bool) bool {\n"); len(chg) != 1 || chg[0] != "VerifyBearerToken" {
		t.Fatalf("changedDeclarationSymbols = %v", chg)
	}
	_ = os.WriteFile(filepath.Join(pyDir, "handler.py"), []byte("def validate_webhook_sig(x):\n    return x\n"), 0o644)
	if got := siblingTestLineFor(root, filepath.Join(pyDir, "handler.py"), []string{"process_batch"}); !strings.Contains(got, "pytest pypkg/test_worker.py -k 'test_process_batch'") {
		t.Fatalf("Python dir-scan siblingTestLineFor = %q", got)
	}
	_ = os.WriteFile(filepath.Join(tsDir, "service.ts"), []byte("export function dispatchService(p: string) { return p; }\n"), 0o644)
	if got := siblingTestLineFor(root, filepath.Join(tsDir, "service.ts"), []string{"resolveRoute"}); !strings.Contains(got, "npx vitest run tspkg/router.test.ts -t 'resolves route path'") {
		t.Fatalf("TS dir-scan siblingTestLineFor = %q", got)
	}
	syncAntigravitySessionRules("", root, "conv-sync-test", []transcriptStep{
		{Type: "PLANNER_RESPONSE", Content: "Should I enable --unsafe-cache on token validation?"},
		{Type: "USER_INPUT", Content: "<USER_REQUEST>\nНикогда не используй --unsafe-cache в internal/auth/token.go\n</USER_REQUEST>"},
	})
	maybePinLiveUserRule("", root, "conv-sync-test", "Никогда не используй --unsafe-cache в internal/auth/token.go")
	_ = salvageFromPayload(`{"tool_response":{"stderr":"line1\n\t\"quoted\"\\path"}}`)
	_ = salvageFromPayload(`{"error":"direct error\r\nline2"}`)
	for _, tc := range []struct{ in, want string }{
		{"func TestAlpha(t *testing.T) {", "TestAlpha"},
		{"def test_beta():", "test_beta"},
		{"fn test_gamma() {", "test_gamma"},
		{"it('runs delta', () => {", "runs delta"},
	} {
		if got := testHeaderName(tc.in); got != tc.want {
			t.Fatalf("testHeaderName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	// 22. Multi-harness live turn-pair refusal & prevAssistant continuation parity:
	// Claude/Codex/Cursor transcript_path and Cline in-memory messages in runHookPromptMode.
	claudeTranscript := filepath.Join(t.TempDir(), "claude-session.jsonl")
	claudeRows := strings.Join([]string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Try running **` + "`--skip-fsync`" + `** on ` + "`wal_writer`" + ` to speed up commits."}]}}`,
		`{"type":"user","message":{"role":"user","content":"No, we never disable fsync on production storage"}}`,
	}, "\n") + "\n"
	err = os.WriteFile(claudeTranscript, []byte(claudeRows), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	var claudeHookOut bytes.Buffer
	var claudePayload []byte
	claudePayload, err = json.Marshal(map[string]string{
		"prompt":          "No, we never disable fsync on production storage",
		"session_id":      "claude-live-1",
		"transcript_path": claudeTranscript,
		"cwd":             root,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = runHookPromptMode(dir, bytes.NewReader(claudePayload), &claudeHookOut, true)
	if err != nil {
		t.Fatal(err)
	}
	// Another session in the same repo mentions `wal_writer` (co-occurring term from assistant turn)
	// or a short continuation after assistant re-proposes `--skip-fsync`:
	var clineMsgs []byte
	clineMsgs, err = json.Marshal([]map[string]string{
		{"role": "assistant", "content": "Should we enable `--skip-fsync` on `wal_writer` now?"},
		{"role": "user", "content": "давай"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var clineHookOut bytes.Buffer
	var clinePayload []byte
	clinePayload, err = json.Marshal(map[string]any{
		"prompt":     "давай",
		"session_id": "cline-live-2",
		"cwd":        root,
		"harness":    "cline",
		"messages":   json.RawMessage(clineMsgs),
	})
	if err != nil {
		t.Fatal(err)
	}
	err = runHookPromptMode(dir, bytes.NewReader(clinePayload), &clineHookOut, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(clineHookOut.String(), "Do not suggest --skip-fsync: No, we never disable fsync on production storage") {
		t.Fatalf("cline short continuation hook output = %q, want --skip-fsync standing rule triggered by prevAssistant", clineHookOut.String())
	}

	// 23. Deferred/sidecar edit argument preservation (Antigravity, Goose, Reasonix):
	// Verify that AntigravityEditCalls, editFileCallLines, and rxHookToolInput preserve
	// old_string/new_string so caller and symbol-level guards fire on deferred harnesses too.
	agySteps := []transcriptStep{
		{
			Type: "PLANNER_RESPONSE",
			ToolCalls: []any{
				map[string]any{
					"name": "edit_file",
					"args": map[string]any{
						"Path":        plainFile,
						"Target":      "func PlainHelper() error {",
						"Replacement": "func PlainHelper(strict bool) error {",
					},
				},
			},
		},
	}
	agyCalls := latestEditCallsFromSteps(agySteps)
	if len(agyCalls) != 1 || !strings.Contains(agyCalls[0].OldString, "func PlainHelper()") || !strings.Contains(agyCalls[0].NewString, "strict bool") {
		t.Fatalf("latestEditCallsFromSteps = %+v, want OldString and NewString preserved", agyCalls)
	}
	agyEditLine := editFileCallLines(dir, agyCalls, "agy-caller-session", root)
	if !strings.Contains(agyEditLine, "PlainHelper") || !strings.Contains(agyEditLine, "cmd/server/handler.go") {
		t.Fatalf("editFileCallLines (Antigravity/Goose) = %q, want zero-history caller guard for PlainHelper", agyEditLine)
	}
	_, rxInput := rxHookToolInput("edit_file", `{"path":"internal/auth/plain.go","old_string":"func PlainHelper() error {","new_string":"func PlainHelper(mode int) error {"}`, root)
	if rxInput["old_string"] == "" || rxInput["new_string"] == "" {
		t.Fatalf("rxHookToolInput = %+v, want old_string and new_string preserved", rxInput)
	}

	// 24. Structural memory discipline:
	// - Conversational replies without paths/symbols are not pinned to live_rules.jsonl.
	// - Single-session auto-mined decisions (Sessions < 2) are not auto-synced and are pruned.
	// - orientTestRemedyMatchesFail rejects compound verify commands ("gofmt -w ... && go test ...").
	pinExtractedLiveRule(dir, root, "chat-only-sess", "не закрывай, там попросили изменения")
	entries24, _, _ := readRepoMemoryAndNotes(root)
	for _, e := range entries24 {
		if strings.Contains(e.Text, "не закрывай") {
			t.Fatalf("conversational reply without path/symbol was pinned to live_rules.jsonl: %+v", e)
		}
	}
	if autoSyncEligible(repoCandidate{Kind: "decision", tier: 1, Sessions: 1, Files: []string{"internal/auth/plain.go"}}) {
		t.Fatal("single-session decision candidate should not be autoSyncEligible")
	}
	if !autoSyncEligible(repoCandidate{Kind: "decision", tier: 1, Sessions: 2, Files: []string{"internal/auth/plain.go"}}) {
		t.Fatal("multi-session decision candidate (Sessions >= 2) should be autoSyncEligible")
	}
	if orientTestRemedyMatchesFail("--- FAIL: TestPlainHelper (0.01s)", "gofmt -w internal/auth/plain.go && go test ./internal/auth/...") {
		t.Fatal("orientTestRemedyMatchesFail should reject compound gofmt && go test command on --- FAIL:")
	}
	if orientCommand("$ go test ./... 2>&1 | tail -50") != "go test ./..." {
		t.Fatalf("orientCommand pipe stripping = %q, want %q", orientCommand("$ go test ./... 2>&1 | tail -50"), "go test ./...")
	}
	if !orientDestructiveRemedy("git rebase --abort") {
		t.Fatal("orientDestructiveRemedy should reject git rebase --abort as a trap remedy")
	}
	if stripViewerPipe("$ golangci-lint run | head -40") != "$ golangci-lint run" {
		t.Fatalf("stripViewerPipe = %q, want %q", stripViewerPipe("$ golangci-lint run | head -40"), "$ golangci-lint run")
	}
	pinExtractedLiveRule(dir, root, "chat-refusal-sess", "Do not suggest restore: не закрывай, там попросили изменения")
	entries24b, _, _ := readRepoMemoryAndNotes(root)
	for _, e := range entries24b {
		if strings.Contains(e.Text, "Do not suggest restore") {
			t.Fatalf("conversational 'Do not suggest restore' without code symbol/path was pinned: %+v", e)
		}
	}
	pkMulti := promptPicker{
		leadTerms: []string{"ranking", "blame", "json", "parse"},
		idfOf:     map[string]float64{"ranking": 4.5, "blame": 5.2, "json": 3.1, "parse": 2.8},
	}
	sessTwoNamed := model.Session{
		ID:      "two-named",
		Project: "deja-vu",
		Messages: []model.Message{
			{Role: "user", Text: "how does blame ranking order sessions?"},
		},
	}
	if !pkMulti.leadTermsMeet(sessTwoNamed) {
		t.Fatal("leadTermsMeet should accept session carrying top-2 highest-IDF named lead terms (blame + ranking)")
	}
}
