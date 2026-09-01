package sources

import (
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
	return parsePiShaped(path, offset, "pi", piProjectName(path), false)
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
	// The first line is the session header — id, timestamp, cwd — and an
	// offset parse starts past it, so a resumed read fell back to the filename
	// for the id and, where the header carries the cwd, to the directory name
	// for the project. The filename is `<ISO>_<uuid>`, so appending one turn
	// filed the tail under an id the whole read never produces (#2870).
	if offset > 0 {
		if head, herr := firstJSONLObject(path); herr == nil {
			applyPiHeader(&s, head, useHeaderCwd)
		}
	}
	err := scanJSONLFromOffset(path, offset, func(m map[string]any) {
		typ, _ := m["type"].(string)
		switch typ {
		case "session":
			applyPiHeader(&s, m, useHeaderCwd)
		case "message":
			msg, ok := m["message"].(map[string]any)
			if !ok {
				return
			}
			role, _ := msg["role"].(string)
			outRole := role
			switch role {
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
			if txt != "" {
				s.Messages = append(s.Messages, model.Message{Role: outRole, Text: txt, Time: t})
			}
		}
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}

// applyPiHeader reads identity out of the `session` header line, whether it
// arrived in the scan or was fetched separately because the scan began past it.
func applyPiHeader(s *model.Session, m map[string]any, useHeaderCwd bool) {
	if typ, _ := m["type"].(string); typ != "session" {
		return
	}
	if id, _ := m["id"].(string); id != "" {
		s.ID = id
	}
	if useHeaderCwd {
		if cwd, _ := m["cwd"].(string); cwd != "" {
			s.Project = claudeProjectName(pathToProjectKey(cwd))
		}
	}
	s.Touch(parseTimeAny(m["timestamp"]))
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
