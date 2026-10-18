package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// Copilot CLI 1.0.98: a parent whose `task` sub-agent writes notes.txt and
// which writes parent.txt itself, then a /fork of it that only says hi. The
// fork holds copies of every record at the source's times. Blame gave both
// lines to the fork, and the sub-agent's line to the parent before that
// (#5216, #5217).
func TestBlameCopilotCreditsTheSubagentAndTheSourceNotTheFork(t *testing.T) {
	const (
		parentID = "f67048ad-042c-42f4-ac33-5db51c4d48b0"
		childID  = "717532a2-bf69-4164-be09-03daacd1762f"
		forkID   = "2f07ff7f-aa0e-4b85-93fb-2096b6e9833f"
		notes    = "release notes drafted by the helper sub-agent"
		mine     = "the parent session wrote this whole line itself"
	)
	work := t.TempDir()
	root := t.TempDir()
	t.Setenv("DEJA_COPILOT_ROOT", root)
	q := func(s string) string { return strings.ReplaceAll(s, `\`, `\\`) }
	notesPath, parentPath := filepath.Join(work, "notes.txt"), filepath.Join(work, "parent.txt")
	copied := []string{
		`{"type":"user.message","data":{"content":"write the release notes with a helper agent"},"timestamp":"2026-10-18T05:31:47.305Z"}`,
		`{"type":"tool.execution_start","data":{"toolCallId":"call_task","toolName":"task","arguments":{"prompt":"draft the notes","agent_type":"general-purpose"}},"timestamp":"2026-10-18T05:31:47.355Z"}`,
		`{"type":"subagent.started","agentId":"` + childID + `","data":{"toolCallId":"call_task","agentName":"general-purpose"},"timestamp":"2026-10-18T05:31:47.356Z"}`,
		`{"type":"user.message","agentId":"` + childID + `","data":{"content":"draft the notes"},"timestamp":"2026-10-18T05:31:47.399Z"}`,
		`{"type":"tool.execution_start","agentId":"` + childID + `","data":{"toolCallId":"call_create","toolName":"create","arguments":{"path":"` + q(notesPath) + `","file_text":"` + notes + `\n"}},"timestamp":"2026-10-18T05:31:47.407Z"}`,
		`{"type":"assistant.message","agentId":"` + childID + `","data":{"content":"Created notes.txt."},"timestamp":"2026-10-18T05:31:49.063Z"}`,
		`{"type":"assistant.message","data":{"content":"The helper wrote notes.txt."},"timestamp":"2026-10-18T05:31:51.660Z"}`,
		`{"type":"user.message","data":{"content":"the parent writes its own file"},"timestamp":"2026-10-18T05:33:10.000Z"}`,
		`{"type":"tool.execution_start","data":{"toolCallId":"call_parent","toolName":"create","arguments":{"path":"` + q(parentPath) + `","file_text":"` + mine + `\n"}},"timestamp":"2026-10-18T05:33:10.100Z"}`,
	}
	start := func(id, at string) string {
		return `{"type":"session.start","data":{"sessionId":"` + id + `","startTime":"` + at + `","context":{"cwd":"` + q(work) + `"}},"timestamp":"` + at + `"}`
	}
	write := func(id string, lines []string) string {
		dir := filepath.Join(root, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "events.jsonl")
		if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	src := append([]string{start(parentID, "2026-10-18T05:31:46.707Z")}, copied...)
	fork := append([]string{start(forkID, "2026-10-18T05:34:00.000Z")}, copied...)
	fork = append(fork,
		`{"type":"session.info","data":{"infoType":"fork","message":"Forked from \"write the release notes with a helper agent\" (`+parentID+`) as \"write the release notes with a helper agent (fork)\" (`+forkID+`)."},"timestamp":"2026-10-18T05:34:00.002Z"}`,
		`{"type":"user.message","data":{"content":"hi"},"timestamp":"2026-10-18T05:34:10.000Z"}`,
	)
	var all []model.Session
	for _, p := range []string{write(forkID, fork), write(parentID, src)} {
		ss, err := sources.ParseCopilotFile(p)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, ss...)
	}
	commit := lineCommit{SHA: "7b969b0e", When: time.Date(2026, 10, 18, 6, 0, 0, 0, time.UTC)}
	for _, tc := range []struct{ path, text, want string }{
		{notesPath, notes, childID},
		{parentPath, mine, parentID},
	} {
		if err := os.WriteFile(tc.path, []byte(tc.text+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		target := search.BlameTarget{FullPath: tc.path, Base: filepath.Base(tc.path), Line: 1}
		// Either order the candidates arrive in.
		for _, ss := range [][]model.Session{all, reversed(all)} {
			got, ok := attributeLine(ss, target, commit, nil)
			if !ok || got.Session.ID != tc.want {
				t.Errorf("%s:1 credited %q, want %s", filepath.Base(tc.path), got.Session.ID, tc.want)
			}
		}
	}

	// The sub-agent hangs under its parent; resume sends the reader there.
	for _, s := range all {
		if s.ID != childID {
			continue
		}
		if _, _, err := resumeCommand(s); err == nil || !strings.Contains(err.Error(), "deja resume "+parentID) {
			t.Errorf("resume of the sub-agent = %v, want a pointer at its parent", err)
		}
	}
}

func reversed(ss []model.Session) []model.Session {
	out := make([]model.Session, len(ss))
	for i, s := range ss {
		out[len(ss)-1-i] = s
	}
	return out
}
