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
	// The same string the serving-path cases use, carriage return included: a
	// fixture that drops one of the bytes leaves that byte unguarded there.
	dirty := "the build failed: \x1b[31mERROR\x1b[0m\x07 pgbouncer pool timed out\r and retried"
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
	if len(got.Messages) < 2 {
		t.Fatalf("the session came back with %d messages, so this measures nothing", len(got.Messages))
	}
	// Per message and per byte: concatenating them would let a strip that
	// touches one role through, and "any of these bytes" would let a strip that
	// touches one byte through. Both pass a laxer version of this test.
	for _, m := range got.Messages {
		for _, b := range []string{"\x1b", "\x07", "\r"} {
			if !strings.Contains(m.Text, b) {
				t.Errorf("the index stripped %q from a %s message on the way in, so the serving-path "+
					"cases in cmd/deja prove nothing about it: %q", b, m.Role, m.Text)
			}
		}
	}
}
