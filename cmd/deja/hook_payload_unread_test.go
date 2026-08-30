package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// A hook fed something that is not a payload injects anyway — an agent that
// asked for context gets it — but the receiver goes missing from the log that
// exists to answer "to whom", and nothing told the two cases apart: a host that
// sent nothing and a host whose payload deja could not read left the same row
// (#2161).
func TestAnUnreadablePayloadIsRecordedAsOne(t *testing.T) {
	for _, c := range []struct{ name, payload, want string }{
		{"prose", "not a payload at all", "unreadable"},
		{"truncated", `{"session_id":"ses1"`, "unreadable"},
		{"binary", "\x00\x01\x02", "unreadable"},
		{"empty", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			skipWindowsEmptySessionID(t)
			withStatsStores(t)
			claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
			old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
			writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "unreadterm", []string{
				`{"type":"user","sessionId":"unreadterm","timestamp":"` + old +
					`","message":{"role":"user","content":"pgbouncer runs in transaction mode and prepared statements are off"}}`,
			})
			if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
				t.Fatal(err)
			}
			cwd := filepath.Join(t.TempDir(), "tmp", "beta")
			if err := os.MkdirAll(cwd, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Chdir(cwd)
			t.Setenv("CLAUDE_PROJECT_DIR", cwd)

			withHookStdin(t, c.payload)
			if out := captureStdout(t, func() { runHookContextPlain(t) }); !strings.Contains(out, "transaction mode") {
				t.Fatalf("the hook injected nothing, so there is no record to check:\n%q", out)
			}

			b, err := os.ReadFile(usage.SnapshotPath(index.DefaultDir()))
			if err != nil {
				t.Fatalf("nothing was written to the injection log: %v", err)
			}
			if c.want == "" {
				if strings.Contains(string(b), "unreadable") {
					t.Errorf("a hook that was sent nothing reported a broken payload:\n%s", b)
				}
				return
			}
			if !strings.Contains(string(b), `"unreadable":true`) {
				t.Errorf("the row does not say the payload could not be read:\n%s", b)
			}
			out, err := captureRun(t, "log", "5")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "into: unknown") {
				t.Errorf("the log does not say the receiver is unknown:\n%s", out)
			}
		})
	}
}
