package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
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
	// The reader's own SessionEnd hook sits under the event deja adds to, in
	// the order Claude Code's /hooks screen writes it: matcher before hooks,
	// type before command — not the order marshalling would sort them into.
	path := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{
  "model": "opus",
  "hooks": {
    "SessionEnd": [
      {
        "matcher": "clear",
        "hooks": [
          {
            "type": "command",
            "command": "/usr/local/bin/mine --flush",
            "timeout": 5
          }
        ]
      }
    ],
    "PreToolUse": [
      {
        "matcher": "Read",
        "hooks": [
          {
            "type": "command",
            "command": "/usr/local/bin/mine --audit"
          }
        ]
      }
    ]
  }
}
`
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
	// Their entries come through the install as they wrote them, not only
	// after the uninstall.
	for _, block := range []string{
		"      {\n        \"matcher\": \"clear\",\n        \"hooks\": [\n          {\n            \"type\": \"command\",\n            \"command\": \"/usr/local/bin/mine --flush\",\n            \"timeout\": 5\n          }\n        ]\n      }",
		"      {\n        \"matcher\": \"Read\",\n        \"hooks\": [\n          {\n            \"type\": \"command\",\n            \"command\": \"/usr/local/bin/mine --audit\"\n          }\n        ]\n      }",
	} {
		if !strings.Contains(string(b), block) {
			t.Errorf("install rewrote the reader's own entry; want it as written:\n%s\n--- in\n%s", block, b)
		}
	}
	// No timeout: Claude Code then gives the hook its SessionEnd default of
	// 1.5 s, and a timeout of ours would only cut that budget or, larger,
	// hold every /exit and /clear the reader makes.
	for _, e := range settingsHooks(t, b)["SessionEnd"].([]any) {
		for _, h := range e.(map[string]any)["hooks"].([]any) {
			h := h.(map[string]any)
			if _, has := h["timeout"]; has && strings.HasSuffix(h["command"].(string), " hook-session-end") {
				t.Errorf("deja's SessionEnd entry carries a timeout: %v", h["timeout"])
			}
		}
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

// Gemini CLI: the extension deja writes carries SessionEnd, and uninstall takes
// the extension out with the rest.
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

// Claude Code moves a session to the background by forking it: a new id whose
// transcript is a copy of the old one, then SessionEnd for the old id. With
// the old stamp cleared, the fork's recall answered with the copy — its own
// opening question under the old id, which the window used to shield (#3945).
// A session that asked the same thing at another time is not a copy and stays.
func TestAForkDoesNotGetItsSourceBackAsRecall(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-w-p")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(id, role, ts, text string) string {
		b, _ := json.Marshal(map[string]any{"type": role, "sessionId": id, "cwd": "/w/p", "timestamp": ts,
			"message": map[string]any{"role": role, "content": text}})
		return string(b) + "\n"
	}
	ask := "The golden test in internal/store never runs here. Find the exact command that runs it and proof that it passes."
	write := func(id, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	opened := "2026-09-30T10:00:00.123Z"
	source := line("src-1", "user", opened, ask) + line("src-1", "assistant", "2026-09-30T10:00:05.000Z", "Looking at the Makefile now.")
	write("src-1", source)
	write("fork-2", line("fork-2", "user", opened, ask)+line("fork-2", "assistant", "2026-09-30T10:00:05.000Z", "Looking at the Makefile now.")+
		line("fork-2", "user", "2026-09-30T10:03:00.000Z", "keep going in the background"))
	write("earlier-3", line("earlier-3", "user", "2026-08-04T09:00:00Z", ask)+
		line("earlier-3", "assistant", "2026-08-04T09:02:00Z", "Run SVC_FIXTURES=$PWD/fixtures make test — that runs the golden test in internal/store and it passes."))
	// Started in the same whole second as earlier-3, on something else: a
	// harness that stamps seconds gives two sessions run together one start
	// time, and that alone does not make either a copy of the other.
	write("parallel-4", line("parallel-4", "user", "2026-08-04T09:00:00Z", "rename the config loader and update every caller in cmd/"))
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	markSessionLive(dir, "parallel-4")
	markSessionLive(dir, "src-1")
	markSessionLive(dir, "fork-2")
	runHookSessionEnd(dir, strings.NewReader(`{"session_id":"src-1","hook_event_name":"SessionEnd","reason":"prompt_input_exit"}`))
	if liveSessionIDs(dir)["src-1"] {
		t.Fatal("SessionEnd left the source's stamp, so this proves nothing")
	}

	got, err := callMCPTool(dir, "recall", json.RawMessage(`{"query":"golden test internal/store never runs"}`))
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if strings.Contains(got, "src-1") {
		t.Errorf("the fork got its own transcript back under the source's id:\n%s", got)
	}
	if !strings.Contains(got, "earlier-3") {
		t.Errorf("a session that asked the same thing at another time was hidden as a copy:\n%s", got)
	}
}

// forkStore writes Claude transcripts under one project and builds the index.
func forkStore(t *testing.T, sessions map[string][][3]string) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-w-p")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for id, turns := range sessions {
		var body strings.Builder
		for _, turn := range turns {
			b, _ := json.Marshal(map[string]any{"type": turn[0], "sessionId": id, "cwd": "/w/p", "timestamp": turn[1],
				"message": map[string]any{"role": turn[0], "content": turn[2]}})
			body.Write(b)
			body.WriteByte('\n')
		}
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(body.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

const forkOpening = "The golden test in internal/store never runs here. Find the exact command that runs it and proof that it passes."

// A fork that went on to its own work and ended is history, even while the
// session it was forked from is open again: it shares that session's opening,
// not its transcript.
func TestAForkThatWentOnIsNotHiddenWhenItsSourceResumes(t *testing.T) {
	opened := "2026-09-30T10:00:00.123Z"
	dir := forkStore(t, map[string][][3]string{
		"src-1": {{"user", opened, forkOpening}, {"assistant", "2026-09-30T10:00:05.000Z", "Looking at the Makefile now."},
			{"user", "2026-10-01T09:00:00.000Z", "back to this, what did the Makefile say"}},
		"fork-2": {{"user", opened, forkOpening}, {"assistant", "2026-09-30T10:00:05.000Z", "Looking at the Makefile now."},
			{"user", "2026-09-30T10:05:00.000Z", "the websocket reconnect test is flaky, fix the backoff jitter"},
			{"assistant", "2026-09-30T10:09:00.000Z", "Raised the reconnect backoff jitter to 250ms; the websocket reconnect test passes 50 of 50."}},
	})
	q := json.RawMessage(`{"query":"websocket reconnect backoff jitter"}`)
	before, err := callMCPTool(dir, "recall", q)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if !strings.Contains(before, "fork-2") {
		t.Fatalf("with nothing live the fork is not found, so this proves nothing:\n%s", before)
	}
	markSessionLive(dir, "src-1")
	got, err := callMCPTool(dir, "recall", q)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if !strings.Contains(got, "fork-2") {
		t.Errorf("a fork that did its own work was hidden as a copy of the live session:\n%s", got)
	}
}

// Two sessions that open on the same millisecond with different openings —
// a subagent batch, siblings started together — are not copies of each other.
func TestASessionOpenedTheSameInstantOnSomethingElseIsNotHidden(t *testing.T) {
	opened := "2026-09-30T10:00:00.123Z"
	dir := forkStore(t, map[string][][3]string{
		"src-1": {{"user", opened, forkOpening},
			{"assistant", "2026-09-30T10:02:00.000Z", "Run SVC_FIXTURES=$PWD/fixtures make test — that runs the golden test in internal/store and it passes."}},
		"par-3": {{"user", opened, "rename the config loader and update every caller in cmd/"}},
	})
	markSessionLive(dir, "par-3")
	got, err := callMCPTool(dir, "recall", json.RawMessage(`{"query":"golden test internal/store never runs"}`))
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if !strings.Contains(got, "src-1") {
		t.Errorf("a session that only shares the live one's start time was hidden:\n%s", got)
	}
}
