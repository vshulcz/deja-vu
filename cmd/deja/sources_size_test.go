package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sourcesLine(t *testing.T, harness string) string {
	t.Helper()
	out, err := captureRun(t, "sources")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, harness+"\t") {
			return line
		}
	}
	t.Fatalf("no %s line in:\n%s", harness, out)
	return ""
}

// `deja sources` answers "where is my history and how much of it is there". It
// summed the tree it looks in rather than the files it opens, so a codex store
// reported 108 MB of plugins, caches and sqlite WALs around 1.3 MB of
// transcripts (#654).
func TestSourcesSizeCountsOnlyWhatItReads(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "codex")
	if err := os.MkdirAll(filepath.Join(root, "sessions", "2026", "08"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CODEX_ROOT", root)
	rollout := `{"type":"session_meta","payload":{"id":"sess-1","cwd":"/work/app"}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"why does the build fail"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "sessions", "2026", "08", "rollout-sess-1.jsonl"), []byte(rollout), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "plugins", "blob.bin"), make([]byte, 10<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	line := sourcesLine(t, "codex")
	if strings.Contains(line, "MB") {
		t.Errorf("the size counts files deja never opens: %s", line)
	}
	if !strings.Contains(line, "sessions=1") {
		t.Errorf("the transcript stopped being read: %s", line)
	}
	if !strings.Contains(line, filepath.Join("sessions", "2026", "08")) {
		t.Errorf("the path does not name where the transcripts are: %s", line)
	}
}

// A harness with nothing on this machine still names somewhere to look, rather
// than an empty column: the empty-machine advice sends people here.
func TestSourcesNamesARootWhenNothingIsRead(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "empty-codex")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CODEX_ROOT", root)
	line := sourcesLine(t, "codex")
	if !strings.Contains(line, root) {
		t.Errorf("an empty store names no path: %s", line)
	}
	if !strings.Contains(line, "size=0 B") {
		t.Errorf("an empty store reports a size: %s", line)
	}
}

// A discovery rule can name something that is not a file on this machine:
// hermes returns a token for a Postgres store. Reading a directory off that
// gave "." as the store's location.
func TestSourcesIgnoresAStoreThatIsNotAFile(t *testing.T) {
	tmp := hermeticEnv(t)
	profiles := filepath.Join(tmp, "hermes", "profiles")
	if err := os.MkdirAll(profiles, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_HERMES_ROOT", filepath.Join(tmp, "hermes"))
	t.Setenv("DEJA_HERMES_PG_DSN", "postgres://user:pw@localhost:5432/hermes")
	line := sourcesLine(t, "hermes")
	if strings.Contains(line, "\t.\t") || strings.Contains(line, "hermes-pg:") {
		t.Errorf("the location is not a place on disk: %s", line)
	}
	if !strings.Contains(line, "size=0 B") {
		t.Errorf("a store deja cannot weigh reports a size: %s", line)
	}
}
