package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The clause that names what a build could not read (#1993) lives in
// harnessNarration, which only a full pass calls. After the first build every
// run a person sees is the append path, and that one counted the loss into the
// manifest and said nothing (#2007).
func TestAnIncrementalRunSaysWhatItCouldNotRead(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	proj := filepath.Join(tmp, "claude", "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(proj, "s1.jsonl")
	if err := os.WriteFile(path, []byte(
		`{"type":"user","sessionId":"s1","cwd":"/tmp/app","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"why does pgbouncer time out"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// Two more turns, one of them carrying a raw escape inside the JSON string,
	// which makes the line invalid JSON.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(
		`{"type":"user","sessionId":"s1","cwd":"/tmp/app","timestamp":"2026-01-02T03:04:06Z","message":{"role":"user","content":"the pool ` + "\x1b" + `[31mtimed out"}}` + "\n" +
			`{"type":"assistant","sessionId":"s1","cwd":"/tmp/app","timestamp":"2026-01-02T03:04:07Z","message":{"role":"assistant","content":"raised it to 40"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := Ensure(dir, "", false, &out); err != nil {
		t.Fatal(err)
	}
	said := out.String()

	// The premise: this has to be the append path, not a rebuild that happens
	// to narrate. Without it the case would pass on the line #1993 already
	// covers.
	if !strings.Contains(said, "updated 1 file") {
		t.Fatalf("this did not take the append path, so it does not measure what it is about:\n%s", said)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.IngestHealth["claude"].MalformedLines; got != 1 {
		t.Fatalf("the pass counted %d unreadable lines, so there is nothing for the run to report", got)
	}

	if !strings.Contains(said, "— 1 line skipped") {
		t.Errorf("the run that read the loss never mentioned it:\n%s", said)
	}
}
