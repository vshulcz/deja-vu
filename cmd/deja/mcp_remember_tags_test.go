package main

import (
	"os"
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
