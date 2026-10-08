package sources

import (
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// OpenClaw keeps an agent's sessions as agents/<agent>/sessions/<id>.jsonl in
// pi's session format, and sessions.json beside them maps each session key to
// the id it currently holds. `openclaw chat --session <key>` and `openclaw
// agent --session-key <key>` open the file that id names, so a transcript
// deleted while its key still points at it goes back as it was; sessions.json
// is OpenClaw's and is not written. The header's cwd is the agent workspace,
// which the index does not keep, and OpenClaw 2026.7 opens a header without
// one. Sessions kept in openclaw-agent.sqlite are rows of OpenClaw's own
// database and are not written back.

func init() {
	registerWriteBack("openclaw", func() []string { return []string{OpenClawRoot()} }, renderOpenClaw)
}

func renderOpenClaw(s model.Session, turns []model.Message) (string, []byte, error) {
	if filepath.Base(s.Path) == "openclaw-agent.sqlite" {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "the session is a row of OpenClaw's agent database (openclaw-agent.sqlite), and deja writes only transcript files"}
	}
	if openclawArchiveRE.MatchString(s.Path) {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "the session was archived by an OpenClaw reset or delete, so no session key opens it any more"}
	}
	if filepath.Base(filepath.Dir(s.Path)) != "sessions" || filepath.Base(s.Path) != s.ID+".jsonl" {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "the index has no agents/<agent>/sessions/<id>.jsonl path for this session, which is where OpenClaw keeps one"}
	}
	return renderPiShapedNoCWD(s, turns)
}

// renderPiShapedNoCWD is renderPiShaped with the header's cwd left out:
// renderPiShaped decodes one from the folder name, and OpenClaw's folder is
// "sessions", not an encoded directory.
func renderPiShapedNoCWD(s model.Session, turns []model.Message) (string, []byte, error) {
	path, data, err := renderPiShaped(s, turns)
	if err != nil {
		return "", nil, err
	}
	nl := strings.IndexByte(string(data), '\n')
	start := turns[0].Time
	if !s.Started.IsZero() && s.Started.Before(start) {
		start = s.Started
	}
	head, err := jsonLines([]any{map[string]any{"type": "session", "version": 3, "id": s.ID, "timestamp": writeBackStamp(start)}})
	if err != nil {
		return "", nil, err
	}
	return path, append(head, data[nl+1:]...), nil
}
