package sources

import (
	"encoding/json"
	"math"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// PiConfigDir is the native pi coding agent configuration directory.
func PiConfigDir() string { return filepath.Join(Home(), ".pi", "agent") }

// PiRoot returns the session store root, overridable via DEJA_PI_ROOT.
func PiRoot() string { return EnvPath("DEJA_PI_ROOT", filepath.Join(PiConfigDir(), "sessions")) }

// PiSessionFiles lists transcript files under the pi session root.
func PiSessionFiles() []string {
	return walkFiles(PiRoot(), func(p string) bool {
		return strings.HasSuffix(p, ".jsonl")
	})
}

// LoadPi loads all pi sessions.
func LoadPi() []model.Session { return parseFiles(PiSessionFiles(), ParsePiFile) }

// ParsePiFile parses a single pi session transcript.
func ParsePiFile(path string) ([]model.Session, error) {
	return parsePiFileFromOffset(path, 0)
}

// ParsePiFileFromOffset parses a pi session transcript starting at a byte offset.
func ParsePiFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parsePiFileFromOffset(path, offset)
}

func parsePiFileFromOffset(path string, offset int64) ([]model.Session, error) {
	// The header's cwd names the project, the folder only when it has none:
	// pi folds every / into a -, so the folder cannot tell my-app from my/app
	// (#4427).
	return parsePiShaped(path, offset, "pi", piProjectName(path), true)
}

// parsePiShaped parses a pi-format transcript (shared by pi and OpenClaw,
// whose agent runtime is the same lineage). useHeaderCwd promotes the session
// header's cwd to the project key when present.
func parsePiShaped(path string, offset int64, harness, project string, useHeaderCwd bool) ([]model.Session, error) {
	s := model.Session{
		Harness: harness,
		ID:      strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		Project: project,
		Path:    path,
	}
	r := newPiReader(&s, useHeaderCwd)
	err := scanJSONLWithHeaderFromOffsetFunc(path, offset, headerLookahead, isPiHeader, r.line)
	r.finish()
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}

// line folds one transcript line into the session: the header, the
// user/assistant/toolResult messages, and the tool calls the assistant made.
// Shared by the JSONL transcripts and OpenClaw's SQLite store, whose
// event_json rows are the same lines.
func (r *piReader) line(m map[string]any) {
	s := r.s
	typ, _ := m["type"].(string)
	switch typ {
	case "session":
		applyPiHeader(s, m, r.useHeaderCwd)
		if cwd, _ := m["cwd"].(string); cwd != "" {
			r.cwd = cwd
		}
	case "compaction", "branch_summary":
		// pi writes a compaction, and the summary of a branch it left, as
		// top-level entries with the text under summary (session-manager
		// appendCompaction, branchWithSummary; pi 0.73-1.1, omp 17.4, gjc
		// 0.18, senpi, kimchi, prime 0.9, OpenClaw 2026.9).
		if txt, _ := m["summary"].(string); strings.TrimSpace(txt) != "" {
			t := parseTimeAny(m["timestamp"])
			s.Touch(t)
			s.Messages = append(s.Messages, model.Message{Role: RoleSummary, Text: strings.TrimSpace(txt), Time: t})
		}
	case "message":
		msg, ok := m["message"].(map[string]any)
		if !ok {
			return
		}
		role, _ := msg["role"].(string)
		outRole := role
		switch role {
		case "compactionSummary", "branchSummary":
			// The same summaries as messages, which prime's schema allows.
			if txt, _ := msg["summary"].(string); strings.TrimSpace(txt) != "" {
				t := parseTimeAny(m["timestamp"])
				s.Touch(t)
				s.Messages = append(s.Messages, model.Message{Role: RoleSummary, Text: strings.TrimSpace(txt), Time: t})
			}
			return
		case "user", "assistant":
			// speech, kept under its own role
		case "toolResult":
			// Command output and errors carry the same recall value every
			// other harness with structured tool output indexes: roleToolOutput
			// powers friction and `--role tool`. pi kept it in the transcript
			// but the parser dropped everything but speech, so pi users alone
			// had no friction and could not search a command's output.
			outRole = RoleToolOutput
		default:
			return
		}
		t := parseTimeAny(m["timestamp"])
		s.Touch(t)
		txt := textFromContent(msg["content"])
		if name, _ := msg["toolName"].(string); role == "toolResult" && name == "eval" {
			txt = evalText(txt)
		}
		if txt != "" {
			s.Messages = append(s.Messages, model.Message{Role: outRole, Text: txt, Time: t})
		}
		switch role {
		case "assistant":
			r.toolCalls(msg["content"], t)
		case "toolResult":
			r.toolResult(msg, t)
		}
	}
}

// applyPiHeader reads identity out of the `session` header line, whether it
// arrived in the scan or was fetched separately because the scan began past it.
func applyPiHeader(s *model.Session, m map[string]any, useHeaderCwd bool) {
	if !isPiHeader(m) {
		return
	}
	if id, _ := m["id"].(string); id != "" {
		s.ID = id
	}
	// The cwd as it is, not encoded into a folder name and decoded back: that
	// round trip is the guess between my-app and my/app the header settles
	// (#4427).
	if useHeaderCwd {
		if name := cwdProjectName(str(m["cwd"])); name != "" {
			s.Project = name
		}
	}
	// A prime-agent rlm.spawn child names its parent's transcript in the
	// header and sits one or more levels down (#4407).
	if depth, _ := m["rlmDepth"].(json.Number); depth != "" && depth != "0" {
		if parent, _ := m["parentSession"].(string); parent != "" {
			s.Kind = "subagent"
			s.Parent = strings.TrimSuffix(filepath.Base(parent), ".jsonl")
		}
	}
	s.Touch(parseTimeAny(m["timestamp"]))
}

// isPiHeader reports whether a line is the `session` header.
func isPiHeader(m map[string]any) bool {
	typ, _ := m["type"].(string)
	return typ == "session"
}

// PiHeaderCwd is the working directory a pi-shaped transcript's `session`
// header records, or "" when it records none. gjc, Kimchi and Senpi reopen a
// session only from that directory, so resume runs there (#4395, #4400,
// #4426).
func PiHeaderCwd(path string) string {
	m := leadingJSONLHeader(path, math.MaxInt64, headerLookahead, isPiHeader)
	cwd, _ := m["cwd"].(string)
	return cwd
}

// piProjectName derives the project display name from the encoded directory
// name. pi uses the same "--" encoding as Claude Code.
func piProjectName(path string) string {
	dir := projectDir(PiRoot(), path)
	return claudeProjectName(dir)
}

// PiProjectDirBase returns the encoded project dir name for a transcript
// path, e.g. "--Users-x-projects-app--" for .../sessions/--Users-x-projects-app--/s.jsonl.
func PiProjectDirBase(path string) string {
	dir := projectDir(PiRoot(), path)
	base := filepath.Base(dir)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return ""
	}
	return base
}
