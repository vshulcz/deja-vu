package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Plain goose has one global AGENTS.md for every session on the machine, and
// it re-reads it every turn. Per-prompt recall written there reached every
// other running session: on a stand, B's recall arrived in A's next request
// (#4795). So without the wrapper's own MOIM file the prompt hook leaves the
// file alone, the session-start digest and the reader's lines included.
func TestThePlainGoosePromptHookLeavesTheSharedFileAlone(t *testing.T) {
	hermeticEnv(t)
	dir := gooseRecallFixture(t)
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", "")
	path := gooseHintsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const mine = "# my own notes\n\nalways use pgx\n"
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := refreshGooseForPrompt(dir, []byte(`{"prompt":"pgbouncer timing out","cwd":"/app"}`)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != mine {
		t.Errorf("a prompt's recall went into the file every goose session reads:\n%s", b)
	}
}

// And the writer both halves share keeps the markers on the file that is the
// reader's, while the MOIM file — deja's own — is written whole.
func TestGooseRecallWritesAMarkedBlockOnlyInTheReadersFile(t *testing.T) {
	goose := gooseHomeForTest(t)
	agents := filepath.Join(goose, "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(agents), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agents, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeGooseRecall("recalled text"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(agents)
	if !strings.Contains(string(b), gooseRecallStart) || !strings.Contains(string(b), "mine") {
		t.Errorf("AGENTS.md was not edited in place:\n%s", b)
	}

	moim := filepath.Join(t.TempDir(), "recall.md")
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", moim)
	if err := writeGooseRecall("recalled text"); err != nil {
		t.Fatal(err)
	}
	m, err := os.ReadFile(moim)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(m), gooseRecallStart) {
		t.Errorf("the MOIM file is deja's own and needs no markers:\n%s", m)
	}
	if strings.TrimSpace(string(m)) != "recalled text" {
		t.Errorf("MOIM = %q", m)
	}
}

// And under the wrapper it has to actually write: a test that only checks the
// shared file is untouched passes just as well when the hook does nothing.
func TestTheGoosePromptHookWritesWhatItFound(t *testing.T) {
	hermeticEnv(t)
	dir := gooseRecallFixture(t)
	moim := filepath.Join(t.TempDir(), "deja-recall-1.md")
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", moim)

	if err := refreshGooseForPrompt(dir, []byte(`{"prompt":"pgbouncer timing out","cwd":"/app"}`)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(moim)
	if err != nil {
		t.Fatalf("the prompt hook wrote nothing: %v", err)
	}
	if !strings.Contains(string(b), "pgbouncer") {
		t.Errorf("the block does not carry what was asked about:\n%s", b)
	}
	// A second write replaces the first without a backup: the wrapper removes
	// its file on exit, and a .bak beside it would stay behind every run.
	if err := refreshGooseForPrompt(dir, []byte(`{"prompt":"default_pool_size raised","cwd":"/app"}`)); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(moim)); len(entries) != 1 {
		t.Errorf("the recall file left company behind: %v", entries)
	}
}

// gooseRecallFixture indexes one session the prompt "pgbouncer timing out"
// recalls, and returns the index.
func gooseRecallFixture(t *testing.T) string {
	t.Helper()
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	proj := filepath.Join(claude, "-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(role, text string) string {
		return `{"type":"` + role + `","sessionId":"gp1","cwd":"/app",` +
			`"timestamp":"2026-07-30T03:04:05Z","message":{"role":"` + role + `","content":"` + text + `"}}`
	}
	body := line("user", "why does pgbouncer keep timing out") + "\n" +
		line("assistant", "pgbouncer was timing out on a full pool; the fix was raising default_pool_size to 40.") + "\n"
	if err := os.WriteFile(filepath.Join(proj, "gp1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}
