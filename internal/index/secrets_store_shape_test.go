package index

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/redact"
)

// The file is the actionable half of this report, and the rule that decides
// whether to print one used to be "the extension is .jsonl". Three harnesses
// write one file per session under another extension — Cline and half of VS
// Code Copilot Chat write .json, the DeepSeek Harness writes .zstd — and their
// rows arrived with no file at all. Counted on this machine's store: 69
// sessions sent to look in a store deja would not name.
func TestAPerSessionFileIsNamedWhateverItsExtension(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	when := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	msg := model.Message{Role: "user", Text: "export GITHUB_TOKEN=" + redact.Marker + "github-token]", Time: when}
	writeStore(t, dir, []model.Session{
		{ID: "a", Harness: "cline", Path: "/tmp/tasks/a/ui_messages.json", Updated: when, Messages: []model.Message{msg}},
		{ID: "b", Harness: "deepseek", Path: "/tmp/.dsh/b.jsonl.zstd", Updated: when, Messages: []model.Message{msg}},
		{ID: "c", Harness: "zed", Path: "/tmp/zed/threads.db", Updated: when, Messages: []model.Message{msg}},
		{ID: "d", Harness: "goose", Path: "/tmp/goose/sessions.sqlite", Updated: when, Messages: []model.Message{msg}},
	})

	scan, err := ScanSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range scan.Findings {
		got[f.ID] = f.Path
	}
	if len(got) != 4 {
		t.Fatalf("want a finding per session, got %d: %+v", len(got), scan.Findings)
	}
	for _, id := range []string{"a", "b"} {
		if got[id] == "" {
			t.Errorf("session %s keeps its own file and the report named none", id)
		}
	}
	// A database holds every other session too, so naming it sends someone to
	// a file they must not edit.
	for _, id := range []string{"c", "d"} {
		if got[id] != "" {
			t.Errorf("session %s is a database store and the report named %q", id, got[id])
		}
	}
}
