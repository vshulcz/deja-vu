package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The hosts that reach deja only through MCP — Hermes, Kilo, OpenClaw and the
// rest of the paste-only list — had no way to the package `deja handoff`
// prints. The mode hands it over by id, by harness, or as the newest session
// here that nobody is writing right now.
func TestTheHandoffModeHandsOverAnotherSession(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	write := func(id, text, reply string, hoursAgo int) {
		dir := filepath.Join(root, "-work-api")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-time.Duration(hoursAgo) * time.Hour).UTC()
		lines := fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/api","message":{"role":"user","content":%q}}`+"\n"+
			`{"type":"assistant","sessionId":%q,"timestamp":%q,"cwd":"/work/api","message":{"role":"assistant","content":%q}}`+"\n",
			id, at.Format(time.RFC3339), text, id, at.Add(time.Minute).Format(time.RFC3339), reply)
		if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(lines), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("aaaa1111-older", "the ledger cutover stalls at the reconcile step", "Root cause: the reconcile job holds a lock the cutover waits on.", 5)
	write("bbbb2222-newer", "what is left of the retry budget work", "Decided to cap retries at three.", 1)
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(tmp, "work", "api")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(work)

	call := func(args string) string {
		t.Helper()
		text, err := callMCPTool(dir, "deja", json.RawMessage(args))
		if err != nil {
			t.Fatalf("%s: %v", args, err)
		}
		return text
	}

	byID := call(`{"mode":"handoff","q":"aaaa1111"}`)
	if !strings.Contains(byID, "reconcile job holds a lock") {
		t.Errorf("handoff by id did not carry that session's conclusion:\n%s", byID)
	}
	if !strings.Contains(byID, "untrusted reference data") {
		t.Errorf("transcript text went out without the frame:\n%s", byID)
	}

	if got := call(`{"mode":"handoff"}`); !strings.Contains(got, "cap retries at three") {
		t.Errorf("with nothing named, the newest session here was not the one handed over:\n%s", got)
	}

	// The newest session is the caller's own while it is being written, and
	// handing an agent its own session back is not a handoff.
	markSessionLive(dir, "bbbb2222-newer")
	if got := call(`{"mode":"handoff"}`); !strings.Contains(got, "reconcile job holds a lock") {
		t.Errorf("the session being written was handed back to itself:\n%s", got)
	}
	// A named harness is a different request: the session it names may have
	// stopped a minute ago on a usage limit, inside the live window.
	if got := call(`{"mode":"handoff","q":"claude"}`); !strings.Contains(got, "cap retries at three") {
		t.Errorf("a harness name did not hand over that harness's newest session:\n%s", got)
	}

	if got := call(`{"mode":"handoff","q":"zzzz9999"}`); !strings.Contains(got, "no session matches") {
		t.Errorf("an id that names nothing was not said so:\n%s", got)
	}
}
