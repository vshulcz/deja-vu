package sources

import (
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// ZCode (Z.ai's agentic development environment) writes the same flat
// transcript Command Code does, under the same project layout:
//
//	~/.zcode/projects/<encoded-cwd>/<session>.jsonl
//
// It also keeps a SQLite store beside it, which deja does not read yet: no
// sample of that schema is in hand, and guessing at one is how a reader ends up
// silently skipping the half of a store it does not understand. The transcripts
// are the conversation either way (#3647).

// ZCodeRoot is the project store root. DEJA_ZCODE_ROOT replaces it.
func ZCodeRoot() string {
	return EnvPath("DEJA_ZCODE_ROOT", filepath.Join(Home(), ".zcode", "projects"))
}

// ZCodeSessionFiles lists the transcripts.
func ZCodeSessionFiles() []string {
	return walkFiles(ZCodeRoot(), func(p string) bool {
		return strings.HasSuffix(p, ".jsonl")
	})
}

// ZCodeUnderRoot lets the registry claim a path for incremental ingest.
func ZCodeUnderRoot(p string) bool {
	return strings.HasPrefix(p, ZCodeRoot()) && strings.HasSuffix(p, ".jsonl")
}

func LoadZCode() []model.Session { return parseFiles(ZCodeSessionFiles(), ParseZCodeFile) }

// ParseZCodeFile reads one transcript.
func ParseZCodeFile(path string) ([]model.Session, error) {
	return ParseZCodeFileFromOffset(path, 0)
}

// ParseZCodeFileFromOffset is the incremental read.
func ParseZCodeFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parseFlatRoleJSONL(path, offset, "zcode", zcodeProject(path))
}

func zcodeProject(path string) string {
	dir := projectDir(ZCodeRoot(), path)
	if dir == "" || dir == ZCodeRoot() {
		return ""
	}
	return claudeProjectName(dir)
}
