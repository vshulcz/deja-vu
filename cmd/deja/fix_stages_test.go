package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The two doors onto the fix store answer at different stages on purpose: a
// person asking gets told what deja holds and how sure it is, while the hook
// that interrupts unasked stays quiet until a remedy is confirmed. Nothing
// pinned the ladder, and the asymmetry is easy to lose in either direction.
func TestTheFixLadderSpeaksAtTheRightStage(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	const wall = "psql: FATAL: sorry, too many clients already"
	addSession := func(id string, hoursAgo int) {
		t.Helper()
		base := time.Now().Add(-time.Duration(hoursAgo) * time.Hour).UTC()
		rows := []string{
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"make serve"}}]}}`,
				id, base.Format(time.RFC3339)),
			fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/api","message":{"role":"user","content":[{"type":"tool_result","content":%q}]}}`,
				id, base.Add(2*time.Minute).Format(time.RFC3339), wall),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"brew services restart postgresql"}}]}}`,
				id, base.Add(5*time.Minute).Format(time.RFC3339)),
		}
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	afterHook := func(session string) string {
		t.Helper()
		payload := fmt.Sprintf(`{"hook_event_name":"PostToolUse","tool_name":"Bash",`+
			`"tool_input":{"command":"make serve"},"tool_response":{"stderr":%q},`+
			`"cwd":"/work/api","session_id":%q}`, wall, session)
		var out bytes.Buffer
		if err := runHookToolAfter(dir, strings.NewReader(payload), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	// Stage one: nobody has hit it.
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	asked, err := captureRun(t, "fix", wall)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(asked, "no session on this machine ran a command after that error") {
		t.Errorf("stage one, asked: %q", asked)
	}
	if got := afterHook("a1"); strings.TrimSpace(got) != "" {
		t.Errorf("stage one, the hook spoke about an error nobody has hit:\n%s", got)
	}

	// Stage two: one session ran something after it. Held, not offered.
	addSession("one", 6)
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	asked, err = captureRun(t, "fix", wall)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(asked, "nothing has confirmed it worked") {
		t.Errorf("stage two, asked: %q", asked)
	}
	if got := afterHook("a2"); strings.TrimSpace(got) != "" {
		t.Errorf("stage two, the hook offered a remedy nothing has confirmed:\n%s", got)
	}

	// Stage three: a second session did the same, so both doors answer.
	addSession("two", 3)
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	asked, err = captureRun(t, "fix", wall)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(asked, "brew services restart postgresql") {
		t.Errorf("stage three, asked: %q", asked)
	}
	if got := afterHook("a3"); !strings.Contains(got, "brew services restart postgresql") {
		t.Errorf("stage three, the hook stayed quiet about a confirmed remedy:\n%s", got)
	}
}
