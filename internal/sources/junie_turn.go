package sources

import (
	"path/filepath"
	"strings"
	"time"
)

// Junie fires no PostToolUse and no PreCompact (ExternalHookEvent in 3110.7
// has SessionStart, UserPromptSubmit, PreToolUse, PermissionRequest, Stop,
// StopFailure and SessionEnd). Both arrive in events.jsonl instead: a command
// that failed as a TerminalBlockUpdatedEvent with status FAILED, and a
// compaction as a ContextCompactionBlockUpdatedEvent, written when the next
// task starts. The hooks that do fire read them from there.

// JunieTurnState is what a session's log says at a hook: the newest command,
// when it failed, and the newest compaction.
type JunieTurnState struct {
	Failure, FailureCommand, FailureID string
	CompactionID                       string
	CompactionAt                       time.Time
}

// JunieTurn reads the session's log. A finished task replays every one of its
// blocks with the event's state COMPLETED; those are skipped, so the newest
// command is the one the agent ran last, not one replayed after it.
func JunieTurn(sessionID string) JunieTurnState {
	var st JunieTurnState
	path := JunieSessionEvents(sessionID)
	if path == "" {
		return st
	}
	_ = scanJSONL(path, func(m map[string]any) {
		if m["kind"] != "SessionA2uxEvent" {
			return
		}
		ev, _ := m["event"].(map[string]any)
		ae, _ := ev["agentEvent"].(map[string]any)
		step, _ := ae["stepId"].(string)
		switch ae["kind"] {
		case "ContextCompactionBlockUpdatedEvent":
			if step != "" {
				st.CompactionID, st.CompactionAt = step, junieTime(m["timestampMs"])
			}
		case "TerminalBlockUpdatedEvent":
			if ev["state"] == "COMPLETED" || step == "" {
				return
			}
			status, _ := ae["status"].(string)
			if status == "IN_PROGRESS" {
				return
			}
			st.Failure, st.FailureCommand, st.FailureID = "", "", ""
			if status == "FAILED" {
				out, _ := ae["output"].(string)
				if strings.TrimSpace(out) == "" {
					out, _ = ae["details"].(string)
				}
				st.Failure = strings.TrimSpace(out)
				st.FailureCommand, _ = ae["command"].(string)
				st.FailureID = step
			}
		}
	})
	return st
}

// ReadJunieCompaction captures a Junie session as it stood before its newest
// compaction. found is false when it has none.
func ReadJunieCompaction(sessionID string) (CompactionTranscript, bool, error) {
	path := JunieSessionEvents(sessionID)
	if path == "" {
		return CompactionTranscript{}, false, nil
	}
	st := JunieTurn(sessionID)
	if st.CompactionID == "" || st.CompactionAt.IsZero() {
		return CompactionTranscript{}, false, nil
	}
	info, err := regularCompactionFile(path)
	if err != nil {
		return CompactionTranscript{}, false, err
	}
	s, err := parseJunieEvents(path, st.CompactionAt)
	if err != nil || len(s.Messages) == 0 {
		return CompactionTranscript{}, false, err
	}
	return CompactionTranscript{
		Session: s, Harness: "junie", NativeSessionID: s.ID,
		Workspace: junieIndexFor(filepath.Dir(path)).ProjectDir, Path: path,
		Fingerprint: hashCompactionBytes([]byte("junie\x00" + s.ID + "\x00" + st.CompactionID)),
		SourceSize:  info.Size(), SourceMTime: info.ModTime(),
	}, true, nil
}
