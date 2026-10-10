package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// The CLI `remember --tag` stores navigation tags, but the MCP remember tool
// dropped them: an agent that tagged a note over MCP got an untagged note, and
// `deja "#tag"` never found it. The tool now threads tags through the same
// AppendNoteTagged path the CLI uses.
func TestMCPRememberStoresTags(t *testing.T) {
	withStatsStores(t)
	dir := os.Getenv("DEJA_INDEX_DIR")

	_, err := callMCPTool(dir, "remember", []byte(`{"text":"tagged decision","project":"tp","tags":["urgent","perf"]}`))
	if err != nil {
		t.Fatal(err)
	}

	var body strings.Builder
	for _, s := range sources.LoadNotes() {
		for _, m := range s.Messages {
			body.WriteString(m.Text)
			body.WriteByte('\n')
		}
	}
	got := body.String()
	if !strings.Contains(got, "#urgent") || !strings.Contains(got, "#perf") {
		t.Errorf("remember dropped its tags; note body = %q", got)
	}
}

// A note stored over MCP without a project went under "notes", where no hook
// in the project it was about would serve it. It is filed the way the CLI
// files it: under the project the server runs in.
func TestMCPRememberFilesUnderTheWorkingProject(t *testing.T) {
	withStatsStores(t)
	dir := os.Getenv("DEJA_INDEX_DIR")
	work := filepath.Join(t.TempDir(), "payments")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(work)
	out, err := callMCPTool(dir, "remember", []byte(`{"text":"ledger retries go through the outbox table"}`))
	if err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	want := sources.ClaudeProjectName(cwd)
	if !strings.Contains(out, projectForEcho(want)) {
		t.Errorf("remember answered %q, want it filed under %s", out, want)
	}
	notes := sources.LoadNotes()
	if len(notes) == 0 {
		t.Fatal("no note stored")
	}
	for _, s := range notes {
		if s.Project != want {
			t.Errorf("note filed under %q, want %q", s.Project, want)
		}
	}
}
