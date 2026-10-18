package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A fork whose source went on gets the source back for what the source did
// after the fork, and not for the turns the fork holds as its own copy: the
// whole source came back, copied turns included (#5335).
func TestMCPGivesAForkOnlyWhatItsSourceDidAfterTheFork(t *testing.T) {
	const (
		src  = "aaaaaaaa-0000-4000-8000-00000000000a"
		fork = "ffffffff-0000-4000-8000-00000000000f"
	)
	for _, marked := range []bool{true, false} {
		dir := claudeForkStoreWent(t, marked, true)
		if err := index.Ensure(dir, "", true, io.Discard); err != nil {
			t.Fatal(err)
		}
		markSessionLive(dir, fork)
		for _, tool := range []string{"recall", "recall_context"} {
			out, err := callMCPTool(dir, tool, json.RawMessage(`{"query":"zebracount"}`))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out, "marmotquill exporter drops rows") {
				t.Errorf("marked=%v %s: the fork got its own copied turns back from the source:\n%s", marked, tool, out)
			}
			out, err = callMCPTool(dir, tool, json.RawMessage(`{"query":"ospreyquill"}`))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, src[:8]) {
				t.Errorf("marked=%v %s: what the source did after the fork is missing:\n%s", marked, tool, out)
			}
		}
		endSessionLive(dir, fork)
		// Control: asked from elsewhere, the source answers with its copied turns.
		out, err := callMCPTool(dir, "recall", json.RawMessage(`{"query":"zebracount"}`))
		if err != nil || !strings.Contains(out, "marmotquill exporter drops rows") {
			t.Fatalf("marked=%v: control not answered with the opening, so this proves nothing (%v):\n%s", marked, err, out)
		}
	}
}

// The per-prompt recall the same way: a fork asking about what it copied is
// not handed its source, which went on after the fork and kept every copied
// turn (#5335).
func TestPromptRecallGivesAForkOnlyWhatItsSourceDidAfterTheFork(t *testing.T) {
	const (
		src  = "aaaaaaaa-0000-4000-8000-00000000000a"
		fork = "ffffffff-0000-4000-8000-00000000000f"
	)
	for _, marked := range []bool{true, false} {
		dir := claudeForkStoreWent(t, marked, true)
		if err := index.Ensure(dir, "", true, io.Discard); err != nil {
			t.Fatal(err)
		}
		cwd := filepath.Join(os.Getenv("HOME"), "work", "app")
		ask := func(sid, prompt string) string {
			payload, _ := json.Marshal(map[string]string{"session_id": sid, "cwd": cwd, "prompt": prompt})
			var out strings.Builder
			if err := runHookPrompt(dir, strings.NewReader(string(payload)), &out); err != nil {
				t.Fatal(err)
			}
			return out.String()
		}
		const q = "the marmotquill exporter drops rows past 4096 again, what fixed the marmotquill exporter?"
		if out := ask(fork, q); strings.Contains(out, src[:8]) {
			t.Errorf("marked=%v: the fork was handed its source for turns it copied:\n%s", marked, out)
		}
		if out := ask("someone-else", q); !strings.Contains(out, src[:8]) {
			t.Errorf("marked=%v: the control was not told about the source, so this proves nothing:\n%s", marked, out)
		}
	}
}

// And the start digest of a fork resumed: the source is there for what it did
// after the fork, not for the turns the fork holds already (#5335).
func TestStartDigestGivesAForkOnlyWhatItsSourceDidAfterTheFork(t *testing.T) {
	const fork = "ffffffff-0000-4000-8000-00000000000f"
	for _, marked := range []bool{true, false} {
		dir := claudeForkStoreWent(t, marked, true)
		if err := index.Ensure(dir, "", true, io.Discard); err != nil {
			t.Fatal(err)
		}
		cwd := filepath.Join(os.Getenv("HOME"), "work", "app")
		run := func(sid string) string {
			payload, _ := json.Marshal(map[string]string{"session_id": sid, "source": "resume", "cwd": cwd})
			withHookStdin(t, string(payload))
			return captureStdout(t, func() {
				if err := runHookContext(dir, true); err != nil {
					t.Error(err)
				}
			})
		}
		out := run(fork)
		if !strings.Contains(out, "ospreyquill flushes now") || strings.Contains(out, "paged, all rows come out now") {
			t.Errorf("marked=%v: the fork's digest is not cut at the fork point:\n%s", marked, out)
		}
		if out := run("someone-else"); !strings.Contains(out, "paged, all rows come out now") {
			t.Errorf("marked=%v: the control's digest lacks the opening, so this proves nothing:\n%s", marked, out)
		}
	}
}
