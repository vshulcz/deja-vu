package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// A session that ended a minute ago is history to the next one. Its stamp sat
// in the live file for the rest of the window, so the next session's MCP recall
// answered "No prior deja sessions matched" for the session just finished
// (#4210). The SessionEnd hook is where the harness says it is over.
func TestAnEndedSessionIsBackInMCPRecall(t *testing.T) {
	dir, liveID := liveAndPriorStore(t)
	q := json.RawMessage(`{"query":"golden test internal/store never runs"}`)
	markSessionLive(dir, liveID)
	markSessionLive(dir, "still-open-2")

	// Control: while stamped, the session is off the page.
	before, err := callMCPTool(dir, "recall", q)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if strings.Contains(before, liveID) {
		t.Fatalf("the stamped session was served anyway, so this proves nothing:\n%s", before)
	}

	payload := `{"session_id":"` + liveID + `","transcript_path":"","cwd":"/w/p","hook_event_name":"SessionEnd","reason":"exit"}`
	runHookSessionEnd(dir, strings.NewReader(payload))

	after, err := callMCPTool(dir, "recall", q)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if !strings.Contains(after, liveID) {
		t.Errorf("the session that ended is still hidden from the next one's recall:\n%s", after)
	}
	// Only the session that ended: another agent still inside its own keeps
	// its stamp.
	if !liveSessionIDs(dir)["still-open-2"] {
		t.Errorf("ending one session cleared another's stamp: %v", readLiveSessions(dir))
	}
}

// A payload with no session id, or garbage, ends nothing.
func TestSessionEndWithoutAnIDLeavesTheStamps(t *testing.T) {
	dir := t.TempDir()
	markSessionLive(dir, "live-9")
	for _, p := range []string{``, `{}`, `{"session_id":""}`, `not json`} {
		runHookSessionEnd(dir, strings.NewReader(p))
	}
	if !liveSessionIDs(dir)["live-9"] {
		t.Errorf("a payload naming no session cleared a stamp: %v", readLiveSessions(dir))
	}
}

// Claude Code: install writes SessionEnd beside the other hooks, doctor reads
// the row as wired, and uninstall gives the reader's file back as it was.
func TestClaudeAutoWiresSessionEndAndUninstallRoundTrips(t *testing.T) {
	hermeticEnv(t)
	// The reader's own SessionEnd hook sits under the event deja adds to.
	// Nested keys are in the order the writer emits: it restores the top-level
	// order only (json_shape.go), the same for every event it touches.
	path := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := "{\n  \"model\": \"opus\",\n  \"hooks\": {\n    \"SessionEnd\": [\n      {\n        \"hooks\": [\n          {\n            \"command\": \"/usr/local/bin/mine --flush\",\n            \"type\": \"command\"\n          }\n        ]\n      }\n    ]\n  }\n}\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installClaudeHook("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !hookEventWired(settingsHooks(t, b), "SessionEnd", "hook-session-end") {
		t.Fatalf("install wrote no SessionEnd hook:\n%s", b)
	}
	if !strings.Contains(string(b), "/usr/local/bin/mine --flush") {
		t.Errorf("the reader's own SessionEnd hook went:\n%s", b)
	}
	if st := claudeHookWiringState(); st.state != "wired" {
		t.Errorf("doctor reads %q (missing %v) after a fresh install", st.state, st.missing)
	}

	if _, err := installClaudeHook("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != seed {
		t.Errorf("uninstall did not give the file back as it was:\n--- got\n%s\n--- want\n%s", after, seed)
	}
}

// Gemini CLI: the extension deja writes carries SessionEnd, doctor reads it as
// wired, and uninstall takes the extension out with the rest.
func TestGeminiAutoWiresSessionEndAndUninstallRemovesIt(t *testing.T) {
	hermeticEnv(t)
	settings := filepath.Join(sources.GeminiHome(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"theme":"GitHub"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("gemini-auto", "/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	hooksPath := filepath.Join(sources.GeminiHome(), "extensions", geminiExtensionName, "hooks", "hooks.json")
	b, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	if !hookEventWired(settingsHooks(t, b), "SessionEnd", "hook-session-end") {
		t.Fatalf("the extension wires no SessionEnd hook:\n%s", b)
	}
	for _, a := range autoWirings() {
		if a.name == "gemini" {
			if st, _ := autoWiringState(a); st != "wired" {
				t.Errorf("doctor reads gemini as %q after a fresh install", st)
			}
		}
	}

	if _, err := installTarget("gemini-auto", "/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(hooksPath); !os.IsNotExist(err) {
		t.Errorf("uninstall left the extension's hooks behind: %v", err)
	}
}

func settingsHooks(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, b)
	}
	hooks, _ := root["hooks"].(map[string]any)
	return hooks
}
