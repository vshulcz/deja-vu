package sources

import (
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// omp (Oh My Pi, github.com/can1357/oh-my-pi) is a pi-lineage coding agent.
// It keeps pi-format JSONL transcripts under its session root:
//
//	~/.omp/agent/sessions/<encoded-project>/<ISO-timestamp>_<uuid>.jsonl
//
// The transcript shape is the same one pi and OpenClaw write — a `session`
// header line carrying id/timestamp/cwd, then typed `message` events — so the
// shared parsePiShaped reader does the work. omp's project directories use
// Claude Code's single-dash encoding (e.g. `-Code-pleasure-course`), and the
// session header carries a real `cwd`, so the header cwd is promoted to the
// project key (useHeaderCwd=true) rather than decoding the lossy directory
// name.

// OmpConfigDir is the omp configuration directory.
func OmpConfigDir() string { return filepath.Join(Home(), ".omp", "agent") }

// OmpRoot returns the session store root, overridable via DEJA_OMP_ROOT.
func OmpRoot() string { return EnvPath("DEJA_OMP_ROOT", filepath.Join(OmpConfigDir(), "sessions")) }

// OmpSessionFiles lists transcript files under the omp session root.
func OmpSessionFiles() []string {
	return walkFiles(OmpRoot(), func(p string) bool {
		return strings.HasSuffix(p, ".jsonl")
	})
}

// LoadOmp loads all omp sessions.
func LoadOmp() []model.Session { return parseFiles(OmpSessionFiles(), ParseOmpFile) }

// ParseOmpFile parses a single omp session transcript.
func ParseOmpFile(path string) ([]model.Session, error) {
	return ParseOmpFileFromOffset(path, 0)
}

// ParseOmpFileFromOffset parses an omp transcript starting at a byte offset.
func ParseOmpFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parsePiShaped(path, offset, "omp", ompProject(path), true)
}

// ompProject attributes a session to its project directory; the header cwd,
// when the session ran with one, overrides inside parsePiShaped. omp encodes
// the project directory with Claude Code's single-dash scheme, so the same
// decoder pi uses resolves it.
func ompProject(path string) string {
	dir := projectDir(OmpRoot(), path)
	return claudeProjectName(dir)
}
