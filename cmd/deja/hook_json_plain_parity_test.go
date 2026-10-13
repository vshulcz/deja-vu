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
)

// dsh-deja and openclaw-deja ask for the JSON reply and give the model additionalContext,
// where it used to give it what --plain printed; the systemMessage beside it
// goes to the person. That swap is only safe while the two forms carry the
// same text for the model, so each form runs on a fresh copy of the same
// store — the hooks keep once-only state, and a second call on one store
// answers differently by design.
func TestJSONReplyCarriesWhatPlainPrints(t *testing.T) {
	seed := func(t *testing.T) string {
		t.Helper()
		withStatsStores(t)
		root := os.Getenv("DEJA_CLAUDE_ROOT")
		when := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
		writeClaudeFixture(t, filepath.Join(root, "beta", "drain.jsonl"), "drainpass", []string{
			`{"type":"user","sessionId":"drainpass","timestamp":"` + when + `","message":{"role":"user","content":"how should the zorbit queue drain on deploy"}}`,
			`{"type":"assistant","sessionId":"drainpass","timestamp":"` + when + `","message":{"role":"assistant","content":[{"type":"text","text":"we settled on draining the zorbit queue with --grace 40 before each deploy, because 10 dropped jobs"}]}}`,
		})
		for i, topic := range []string{"css grid layout for the settings page", "upgrade the postgres driver", "flaky login test on ci", "rename the billing module"} {
			id := "bg" + string(rune('a'+i))
			writeClaudeFixture(t, filepath.Join(root, "beta", id+".jsonl"), id, []string{
				`{"type":"user","sessionId":"` + id + `","timestamp":"` + when + `","message":{"role":"user","content":"` + topic + `"}}`,
				`{"type":"assistant","sessionId":"` + id + `","timestamp":"` + when + `","message":{"role":"assistant","content":[{"type":"text","text":"done: ` + topic + `"}]}}`,
			})
		}
		if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
			t.Fatal(err)
		}
		cwd := filepath.Join(t.TempDir(), "tmp", "beta")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(cwd)
		return cwd
	}
	decode := func(t *testing.T, out string) (context, notes string) {
		t.Helper()
		var resp sessionStartHookResponse
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil {
			t.Fatalf("not the hook envelope: %v\n%s", err, out)
		}
		return resp.HookSpecificOutput.AdditionalContext, resp.SystemMessage
	}

	prompt := func(t *testing.T, plain bool) string {
		cwd := seed(t)
		payload, _ := json.Marshal(map[string]any{"prompt": "how do we drain the zorbit queue", "cwd": cwd, "session_id": "parity-1"})
		var out bytes.Buffer
		if err := runHookPromptMode(index.DefaultDir(), bytes.NewReader(payload), &out, plain); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	var plainPrompt, jsonPrompt string
	t.Run("prompt-plain", func(t *testing.T) { plainPrompt = prompt(t, true) })
	t.Run("prompt-json", func(t *testing.T) { jsonPrompt = prompt(t, false) })
	if !strings.Contains(plainPrompt, "zorbit") {
		t.Fatalf("the stand never spoke, so there is nothing to compare:\n%s", plainPrompt)
	}
	ctx, notes := decode(t, jsonPrompt)
	if strings.TrimSpace(plainPrompt) != strings.TrimSpace(ctx) {
		t.Errorf("the model would read something else:\n--plain:\n%s\n--json:\n%s", plainPrompt, ctx)
	}
	if !strings.Contains(notes, "you have been here") {
		t.Errorf("the JSON reply carries no line for the person: %q", notes)
	}

	start := func(t *testing.T, plain bool) string {
		cwd := seed(t)
		payload, _ := json.Marshal(map[string]any{"session_id": "parity-2", "cwd": cwd, "source": "startup", "deja_once": true})
		withHookStdin(t, string(payload))
		return captureStdout(t, func() { _ = runHookContext(index.DefaultDir(), plain) })
	}
	var plainStart, jsonStart string
	t.Run("start-plain", func(t *testing.T) { plainStart = start(t, true) })
	t.Run("start-json", func(t *testing.T) { jsonStart = start(t, false) })
	if !strings.Contains(plainStart, "zorbit") {
		t.Fatalf("the digest never spoke, so there is nothing to compare:\n%s", plainStart)
	}
	ctx, notes = decode(t, jsonStart)
	if strings.TrimSpace(plainStart) != strings.TrimSpace(ctx) {
		t.Errorf("the model would read another digest:\n--plain:\n%s\n--json:\n%s", plainStart, ctx)
	}
	if notes == "" {
		t.Error("the session start carries no line for the person")
	}
}

// The other ways the two hooks answer: a compaction's recovery packet, and the
// nudge alone (which the asked-for handoff also goes through). Each has its
// own plain and JSON branch.
func TestJSONReplyCarriesWhatPlainPrintsForThePacketAndTheNudge(t *testing.T) {
	packet := func(t *testing.T, shape hookToolShape) string {
		dir, workspace, _ := prepareCompaction(t)
		var out bytes.Buffer
		if delivered, err := emitCompactionRecovery(dir, "compaction-fixture", workspace, "UserPromptSubmit", shape, &out); err != nil || !delivered {
			t.Fatalf("no packet: delivered=%v err=%v", delivered, err)
		}
		return out.String()
	}
	var plain, js string
	t.Run("packet-plain", func(t *testing.T) { plain = packet(t, hookToolPlain) })
	t.Run("packet-json", func(t *testing.T) { js = packet(t, hookToolClaude) })
	var resp sessionStartHookResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(js)), &resp); err != nil {
		t.Fatalf("not the hook envelope: %v\n%s", err, js)
	}
	if strings.TrimSpace(plain) == "" || strings.TrimSpace(plain) != strings.TrimSpace(resp.HookSpecificOutput.AdditionalContext) {
		t.Errorf("the packet differs between forms:\n--plain:\n%s\n--json:\n%s", plain, resp.HookSpecificOutput.AdditionalContext)
	}

	var a, b bytes.Buffer
	_ = emitNudgeOnly(&a, true, "last time this failed, it was the lockfile")
	_ = emitNudgeOnly(&b, false, "last time this failed, it was the lockfile")
	resp = sessionStartHookResponse{}
	if err := json.Unmarshal(b.Bytes(), &resp); err != nil {
		t.Fatalf("not the hook envelope: %v\n%s", err, b.String())
	}
	if strings.TrimSpace(a.String()) != strings.TrimSpace(resp.HookSpecificOutput.AdditionalContext) {
		t.Errorf("the nudge differs between forms:\n%s\n%s", a.String(), resp.HookSpecificOutput.AdditionalContext)
	}
}
