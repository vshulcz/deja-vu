package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"testing"
)

// The index keeps a transcript as it found it, control bytes included, and the
// stripping happens where the text is served. That is not a preference — it is
// what `TestNothingServedCarriesAControlByte` in cmd/deja rests on: those cases
// index a dirty transcript and then drive each writer, so an indexer that
// sanitised on the way in would leave every one of them passing while proving
// nothing.
//
// Which is the failure this file exists to catch, and the same shape as a blame
// case that answered `[]` and passed for a season (#1985).
func TestTheIndexKeepsWhatTheTranscriptBrought(t *testing.T) {
	tmp := t.TempDir()
	claude := filepath.Join(tmp, "claude", "-tmp-app")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	dir := filepath.Join(tmp, "index.db")

	// The escape is written the way a harness writes it — as a \u001b escape in
	// the JSON, which decodes to the byte. A raw control byte inside a JSON
	// string is not valid JSON at all, and the line would be dropped before any
	// of this: that is what made the first version of this fixture index
	// nothing.
	dirty := "the build failed: \x1b[31mERROR\x1b[0m\x07 pgbouncer timed out"
	line := func(role string) string {
		b, err := json.Marshal(map[string]any{
			"type": role, "sessionId": "s1", "cwd": "/tmp/app",
			"timestamp": "2026-08-20T10:00:00Z",
			"message":   map[string]any{"role": role, "content": dirty},
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	body := line("user") + "\n" + line("assistant") + "\n"
	if err := os.WriteFile(filepath.Join(claude, "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// Through the accessor a reader uses, not the bytes of a file: a gob holds
	// fields this is not about, and finding an escape somewhere in it says
	// nothing about the message text the serving path will strip.
	got, ok, err := FindByIdentity(dir, "claude", "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("the session did not come back from the index")
	}
	var text string
	for _, m := range got.Messages {
		text += m.Text
	}
	if text == "" {
		t.Fatal("the session came back with no messages, so this measures nothing")
	}
	if !strings.ContainsAny(text, "\x1b\x07") {
		t.Errorf("the index stripped the transcript's control bytes on the way in, so the "+
			"serving-path cases in cmd/deja now prove nothing and need a dirty record of their own: %q", text)
	}
}
