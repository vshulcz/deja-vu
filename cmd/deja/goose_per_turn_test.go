package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The prompt hook returned immediately unless MOIM was set, because the file it
// wrote was read once when the session started. That is not true of where the
// recall lives now: measured against goose 1.48 with a stub endpoint, a change
// to AGENTS.md between two turns of one session arrives on the second — the
// file is re-read every turn. So the guard was the only thing keeping per-turn
// recall to the wrapper.
func TestTheGoosePromptHookRefreshesWithoutTheWrapper(t *testing.T) {
	cfg := gooseHomeForTest(t)
	path := filepath.Join(cfg, "goose", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const mine = "# my own notes\n\nalways use pgx\n"
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}

	// No MOIM in the environment: this is plain `goose`.
	if err := refreshGooseForPrompt(t.TempDir(), []byte(`{"prompt":"pgbouncer pool"}`)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Whatever the store answered — including nothing — the reader's own lines
	// have to survive. The prompt path wrote the file raw and erased them.
	if !strings.Contains(string(b), "always use pgx") {
		t.Errorf("the prompt refresh overwrote the reader's file:\n%s", b)
	}
}

// And the writer both halves share keeps the markers on the file that is the
// reader's, while the MOIM file — deja's own — is written whole.
func TestGooseRecallWritesAMarkedBlockOnlyInTheReadersFile(t *testing.T) {
	cfg := gooseHomeForTest(t)
	agents := filepath.Join(cfg, "goose", "AGENTS.md")
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

// And it has to actually write: a test that only checks the reader's lines
// survive passes just as well when the hook returns without doing anything,
// which is what it did before the guard came off.
func TestTheGoosePromptHookWritesWhatItFound(t *testing.T) {
	hermeticEnv(t)
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
		line("assistant", "The fix was raising default_pool_size to 40.") + "\n"
	if err := os.WriteFile(filepath.Join(proj, "gp1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", "")

	if err := refreshGooseForPrompt(dir, []byte(`{"prompt":"pgbouncer timing out","cwd":"/app"}`)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(gooseHintsPath())
	if err != nil {
		t.Fatalf("the prompt hook wrote nothing: %v", err)
	}
	if !strings.Contains(string(b), gooseRecallStart) {
		t.Errorf("no recall block was written:\n%s", b)
	}
	if !strings.Contains(string(b), "pgbouncer") {
		t.Errorf("the block does not carry what was asked about:\n%s", b)
	}
}
