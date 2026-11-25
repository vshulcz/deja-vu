package sources

import (
	"path/filepath"

	"github.com/vshulcz/deja-vu/internal/model"
)

// GigaCodeConfigDir is the directory GigaCode CLI keeps its configuration,
// rules, and store in: $GIGACODE_HOME, or ~/.gigacode. It intentionally ignores
// QWEN_HOME so Qwen overrides do not steal GigaCode's store.
func GigaCodeConfigDir() string {
	return EnvPath("GIGACODE_HOME", filepath.Join(Home(), ".gigacode"))
}

// GigaCodeRoot is where GigaCode transcripts live (GIGACODE_HOME or ~/.gigacode).
// DEJA_GIGACODE_ROOT moves reads without changing where install writes config.
func GigaCodeRoot() string {
	return EnvPath("DEJA_GIGACODE_ROOT", GigaCodeConfigDir())
}

// GigaCodeSessionFiles returns main chat logs under
// <GigaCodeRoot>/projects/<project>/chats/*.jsonl plus sub-agent logs unless
// sub-agents are excluded.
func GigaCodeSessionFiles() []string {
	return qwenFamilySessionFiles(GigaCodeRoot())
}

// GigaCodeSidecarFiles lists JSON metadata files beside GigaCode transcripts so
// doctor classifies them as sidecars rather than unread transcripts.
func GigaCodeSidecarFiles() []string {
	return qwenFamilySidecarFiles(GigaCodeRoot())
}

// GigaCodeSubagentFile reports whether p is a GigaCode sub-agent transcript.
func GigaCodeSubagentFile(p string) bool {
	return qwenFamilySubagentFile(GigaCodeRoot(), p)
}

func gigaCodeUnderProjects(p string) bool {
	return qwenFamilyProjectsUnder(GigaCodeRoot(), p)
}

// LoadGigaCode reads every GigaCode session on this machine.
func LoadGigaCode() []model.Session {
	return parseFiles(GigaCodeSessionFiles(), ParseGigaCodeFile)
}

// GigaCodeProjectDirBase returns the encoded project dir name for path.
func GigaCodeProjectDirBase(path string) string {
	return qwenFamilyProjectDirBase(GigaCodeRoot(), path)
}

// GigaCodeSessionDir returns the working directory for a GigaCode session and
// whether the transcript recorded it.
func GigaCodeSessionDir(path string) (dir string, recorded bool) {
	return qwenFamilySessionDir(GigaCodeRoot(), path)
}

// ParseGigaCodeFile reads one GigaCode transcript.
func ParseGigaCodeFile(path string) ([]model.Session, error) {
	return ParseGigaCodeFileFromOffset(path, 0)
}

// ParseGigaCodeFileFromOffset reads a GigaCode transcript from offset on.
func ParseGigaCodeFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parseQwenFamilyFileFromOffset("gigacode", GigaCodeRoot(), path, offset)
}

func parseGigaCodeScan(path string, scan func(func(map[string]any)) error) ([]model.Session, error) {
	return parseQwenFamilyScan("gigacode", GigaCodeRoot(), path, scan)
}
