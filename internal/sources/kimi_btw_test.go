package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Kimi Code runs a /btw side question in a fork of the main agent: state.json
// registers agent-N with forkedFrom "main", and its wire.jsonl opens with a
// copy of main's context, then the btw reminder, the question and the answer.
// The question is the person's own words, and deja left the whole file out as
// a sub-agent (#4484). Only what follows the reminder is read, as a fork of
// the session it was asked in.
//
// 0.28 marks the reminder {kind: system_trigger, name: btw}; 0.43 and 2.x
// write it through the reminder service as {kind: injection, variant: btw},
// and from 2.x an Agent call with fork: true is a forkedFrom agent as well.
func TestKimiBtwForkKeepsTheQuestionNotTheCopiedContext(t *testing.T) {
	for name, origin := range map[string]string{
		"0.28": `{"kind":"system_trigger","name":"btw"}`,
		"2.x":  `{"kind":"injection","variant":"btw","ownerPromptId":"p7"}`,
	} {
		t.Run(name, func(t *testing.T) { testKimiBtwFork(t, origin) })
	}
}

func testKimiBtwFork(t *testing.T, btwOrigin string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", filepath.Join(root, "home"))
	t.Setenv("KIMI_CODE_HOME", "")
	t.Setenv("DEJA_KIMI_ROOT", filepath.Join(root, "kimi"))
	put := func(p, body string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	session := filepath.Join(root, "kimi", "sessions", "wd_proj_ab", "session_t01")
	put(filepath.Join(session, "state.json"), `{"createdAt":"2026-07-01T10:00:00.000Z","title":"parent task","workDir":"/tmp/proj",
"agents":{"main":{"homedir":"agents/main","type":"main"},
"agent-1":{"homedir":"agents/agent-1","type":"sub","parentAgentId":"main","forkedFrom":"main"},
"agent-2":{"homedir":"agents/agent-2","type":"sub","parentAgentId":"main"},
"agent-3":{"homedir":"agents/agent-3","type":"sub","parentAgentId":"main","forkedFrom":"main"}}}`)
	put(filepath.Join(session, "agents", "main", "wire.jsonl"), kimiWireHead)
	fork := put(filepath.Join(session, "agents", "agent-1", "wire.jsonl"),
		`{"type":"metadata","protocol_version":"1.4","created_at":1782295300000}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"first question"}],"toolCalls":[],"origin":{"kind":"user"}},"time":1782295300001}
{"type":"context.append_message","message":{"role":"assistant","content":[{"type":"text","text":"answer one, joined"}],"toolCalls":[]},"time":1782295300002}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"<system-reminder>\nThis is a side-channel conversation with the user.\n</system-reminder>"}],"toolCalls":[],"origin":`+btwOrigin+`},"time":1782295300003}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"what does the btwjitter knob do"}],"toolCalls":[],"origin":{"kind":"user"}},"time":1782295305000}
{"type":"context.append_loop_event","event":{"type":"step.begin","uuid":"b1"},"time":1782295306000}
{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"text","text":"it spreads the retries"}},"time":1782295306100}
{"type":"context.append_loop_event","event":{"type":"step.end","uuid":"b1"},"time":1782295306200}
`)
	// A sub-agent the main agent spawned is not a fork: read by default as its
	// task, answer and changes (#5229), left out under =0.
	sub := put(filepath.Join(session, "agents", "agent-2", "wire.jsonl"),
		`{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"search the repo"}]},"time":1782295400000}
`)

	// An Agent call with fork: true copies main's context too, and is a
	// sub-agent run, not the person's question.
	agentFork := put(filepath.Join(session, "agents", "agent-3", "wire.jsonl"),
		`{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"first question"}],"toolCalls":[],"origin":{"kind":"user"}},"time":1782295300001}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"audit the retry path"}],"toolCalls":[],"origin":{"kind":"system_trigger","name":"subagent"}},"time":1782295500000}
`)

	for _, sw := range []string{"", "1", "0"} {
		t.Setenv("DEJA_INCLUDE_SUBAGENTS", sw)
		files := strings.Join(KimiSessionFiles(), "\n")
		if !strings.Contains(files, fork) {
			t.Errorf("DEJA_INCLUDE_SUBAGENTS=%q: the /btw fork is not read: %s", sw, files)
		}
		if strings.Contains(files, sub) != (sw != "0") {
			t.Errorf("DEJA_INCLUDE_SUBAGENTS=%q: spawned sub-agent read=%v", sw, strings.Contains(files, sub))
		}
		if strings.Contains(files, agentFork) != (sw == "1") {
			t.Errorf("DEJA_INCLUDE_SUBAGENTS=%q: Agent fork: true sub-agent read=%v", sw, strings.Contains(files, agentFork))
		}
		ss, err := ParseKimiFile(fork)
		if err != nil || len(ss) != 1 {
			t.Fatalf("DEJA_INCLUDE_SUBAGENTS=%q: parsed %d sessions: %v", sw, len(ss), err)
		}
		s := ss[0]
		if s.Kind != "fork" || s.Parent != "session_t01" || s.ID == "session_t01" {
			t.Errorf("fork read as id=%q kind=%q parent=%q", s.ID, s.Kind, s.Parent)
		}
		got := make([]string, 0, len(s.Messages))
		for _, m := range s.Messages {
			got = append(got, m.Role+": "+m.Text)
		}
		want := []string{"user: what does the btwjitter knob do", "assistant: it spreads the retries"}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("fork messages = %q, want %q", got, want)
		}
		if start := time.UnixMilli(1782295305000); !s.Started.Equal(start) {
			t.Errorf("fork starts %v, want the question's time %v", s.Started, start)
		}
	}
	// The reminder sits before any later offset, so a tail read would lose
	// where the fork's own turns start: the file is read whole.
	if fi, err := os.Stat(fork); err != nil || kimiTailResumes(fork, fi.Size()) {
		t.Errorf("a /btw fork resumes from an offset: %v", err)
	}
	// The Agent fork is no /btw fork, and resumes from an offset as before.
	if fi, err := os.Stat(agentFork); err != nil || !kimiTailResumes(agentFork, fi.Size()) {
		t.Errorf("an Agent fork no longer resumes from an offset: %v", err)
	}
	// Control: main resumes as before.
	main := filepath.Join(session, "agents", "main", "wire.jsonl")
	if fi, err := os.Stat(main); err != nil || !kimiTailResumes(main, fi.Size()) {
		t.Errorf("main no longer resumes from an offset: %v", err)
	}
}
