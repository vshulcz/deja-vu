package sources

import (
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Command Code (commandcode.ai) writes one transcript per session under a
// Claude-Code-style project directory:
//
//	~/.commandcode/projects/<encoded-cwd>/<session>.jsonl
//
// Each line is one message, flat: role, content, timestamp, sessionId. Beside
// it sits `<session>.checkpoints.jsonl`, a snapshot stream rather than a
// conversation — read as a transcript it adds a session with no words in it, so
// it is skipped by name (#3647).

// CommandCodeRoot is the project store root. DEJA_COMMANDCODE_ROOT replaces it.
func CommandCodeRoot() string {
	return EnvPath("DEJA_COMMANDCODE_ROOT", filepath.Join(Home(), ".commandcode", "projects"))
}

// CommandCodeSessionFiles lists the transcripts, checkpoints excluded.
func CommandCodeSessionFiles() []string {
	return walkFiles(CommandCodeRoot(), commandCodeIsTranscript)
}

// commandCodeIsTranscript reports whether a path is a conversation rather than
// the checkpoint stream that shares its extension.
func commandCodeIsTranscript(p string) bool {
	return strings.HasSuffix(p, ".jsonl") && !strings.HasSuffix(p, ".checkpoints.jsonl")
}

// CommandCodeUnderRoot lets the registry claim a path for incremental ingest.
func CommandCodeUnderRoot(p string) bool {
	return strings.HasPrefix(p, CommandCodeRoot()) && commandCodeIsTranscript(p)
}

func LoadCommandCode() []model.Session {
	return parseFiles(CommandCodeSessionFiles(), ParseCommandCodeFile)
}

// ParseCommandCodeFile reads one transcript.
func ParseCommandCodeFile(path string) ([]model.Session, error) {
	return ParseCommandCodeFileFromOffset(path, 0)
}

// ParseCommandCodeFileFromOffset is the incremental read.
func ParseCommandCodeFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parseFlatRoleJSONL(path, offset, "commandcode", commandCodeProject(path))
}

func commandCodeProject(path string) string {
	dir := projectDir(CommandCodeRoot(), path)
	if dir == "" || dir == CommandCodeRoot() {
		return ""
	}
	return claudeProjectName(dir)
}
