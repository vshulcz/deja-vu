package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// seedDejaVuStore lays out a session the déjà vu hook will recall, and returns
// the index directory.
func seedDejaVuStore(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "vuterm", []string{
		`{"type":"user","sessionId":"vuterm","timestamp":"` + old +
			`","message":{"role":"user","content":"pgbouncer runs in transaction mode and prepared statements are off"}}`,
	})
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	t.Setenv("CLAUDE_PROJECT_DIR", cwd)
	return dir
}

// dejaVuRows reads back what the hook recorded.
func dejaVuRows(t *testing.T, dir string) []usage.Snapshot {
	t.Helper()
	b, err := os.ReadFile(usage.SnapshotPath(dir))
	if err != nil {
		return nil
	}
	var out []usage.Snapshot
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var snap usage.Snapshot
		if json.Unmarshal([]byte(line), &snap) != nil {
			continue
		}
		if snap.Kind == usage.KindDejaVu {
			out = append(out, snap)
		}
	}
	return out
}

// A payload this hook cannot read at all injects nothing: the prompt is what
// it recalls from, and there is none. That covers the whole-payload failures —
// what it does not cover is a decode that fails on a later field, which is
// where the real hole was (see the test below).
func TestAPayloadTheDejaVuHookCannotReadInjectsNothingAtAll(t *testing.T) {
	for _, c := range []struct{ name, payload string }{
		{"prose", "not a payload at all"},
		{"truncated", `{"prompt":"pgbouncer transaction mode prepared statements"`},
		{"binary", "\x00\x01\x02"},
		{"a list", "[1, 2]"},
		{"empty", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := seedDejaVuStore(t)
			var out bytes.Buffer
			if err := runHookPromptMode(dir, strings.NewReader(c.payload), &out, true); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out.String(), "transaction mode") {
				t.Errorf("a payload deja could not read still injected:\n%s", out.String())
			}
			if rows := dejaVuRows(t, dir); len(rows) != 0 {
				t.Errorf("a payload deja could not read left %d row(s): %+v", len(rows), rows)
			}
		})
	}
}

// And a payload it read is recorded with whatever receiver it named.
func TestTheDejaVuHookRecordsTheReceiverThePayloadNamed(t *testing.T) {
	for _, c := range []struct{ name, payload, into string }{
		{"named", `{"prompt":"pgbouncer transaction mode prepared statements","session_id":"ses_vu"}`, "ses_vu"},
		{"no session named", `{"prompt":"pgbouncer transaction mode prepared statements"}`, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := seedDejaVuStore(t)
			var out bytes.Buffer
			if err := runHookPromptMode(dir, strings.NewReader(c.payload), &out, true); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "transaction mode") {
				t.Fatalf("the hook injected nothing:\n%s", out.String())
			}
			rows := dejaVuRows(t, dir)
			if len(rows) != 1 {
				t.Fatalf("want one déjà vu row, got %d", len(rows))
			}
			if rows[0].Unreadable {
				t.Errorf("a payload deja read was reported as broken: %+v", rows[0])
			}
			if rows[0].Into != c.into {
				t.Errorf("into = %q, want %q", rows[0].Into, c.into)
			}
		})
	}
}

// The hole #2773 named, found by measurement rather than by reading: a decode
// that fails on a *later* field reads the prompt, injects on it, and leaves a
// row saying the host named no session — for a payload that named one deja
// could not read. Identical, in the log, to a host that named none.
func TestADecodeThatFailsAfterThePromptIsRecordedAsUnreadable(t *testing.T) {
	for _, payload := range []string{
		`{"prompt":"pgbouncer transaction mode prepared statements","session_id":123}`,
		`{"prompt":"pgbouncer transaction mode prepared statements","session_id":{"a":1}}`,
		`{"prompt":"pgbouncer transaction mode prepared statements","cwd":7}`,
	} {
		t.Run(payload, func(t *testing.T) {
			dir := seedDejaVuStore(t)
			var out bytes.Buffer
			if err := runHookPromptMode(dir, strings.NewReader(payload), &out, true); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "transaction mode") {
				t.Fatalf("the premise moved — this payload is meant to inject:\n%s", out.String())
			}
			rows := dejaVuRows(t, dir)
			if len(rows) != 1 {
				t.Fatalf("want one déjà vu row, got %d", len(rows))
			}
			if !rows[0].Unreadable {
				t.Errorf("the row reads as a host that named no session: %+v", rows[0])
			}
		})
	}
}
