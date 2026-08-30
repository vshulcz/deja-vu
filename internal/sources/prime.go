package sources

import (
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// prime-agent (github.com/PrimeIntellect-ai/prime-agent) is a pi-lineage coding
// agent: it descends from the same codebase pi does and kept the JSONL
// envelope, so the shared parsePiShaped reader does the work.
//
//	~/.prime/agent/sessions/<uuid7>.jsonl
//
// Flat, one file per session — there is no encoded project directory to read a
// name out of. The header line carries the real cwd, so that is what names the
// project (useHeaderCwd=true), the same choice omp makes for the same reason.
//
// Older layouts (`~/.pi/agent/*.jsonl` and a `--cwd--` directory under this
// root) are migrated into the flat root when prime-agent starts, so this is the
// shape a live install has. Reported with the paths and the version they were
// read from (#2529).

// PrimeConfigDir is the prime-agent configuration directory.
func PrimeConfigDir() string { return filepath.Join(Home(), ".prime", "agent") }

// PrimeRoot returns the session store root.
//
// prime-agent relocates it with two variables of its own, and deja reads them:
// a machine that moved its sessions has moved them for deja too, and asking the
// user to set a third variable to say what they already said is the kind of
// silence doctor cannot explain. DEJA_PRIME_ROOT still wins, for tests and for
// a store that is neither.
func PrimeRoot() string {
	if p := EnvPath("DEJA_PRIME_ROOT", ""); p != "" {
		return p
	}
	for _, name := range []string{"PRIME_AGENT_CODING_AGENT_SESSION_DIR", "PRIME_AGENT_SESSION_DIR"} {
		if p := EnvPath(name, ""); p != "" {
			return p
		}
	}
	return filepath.Join(PrimeConfigDir(), "sessions")
}

// PrimeSessionFiles lists transcript files under the prime-agent session root.
func PrimeSessionFiles() []string {
	return walkFiles(PrimeRoot(), func(p string) bool {
		return strings.HasSuffix(p, ".jsonl")
	})
}

// LoadPrime loads all prime-agent sessions.
func LoadPrime() []model.Session { return parseFiles(PrimeSessionFiles(), ParsePrimeFile) }

// ParsePrimeFile parses a single prime-agent session transcript.
func ParsePrimeFile(path string) ([]model.Session, error) {
	return parsePrimeFileFromOffset(path, 0)
}

// ParsePrimeFileFromOffset parses a transcript starting at a byte offset.
func ParsePrimeFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parsePrimeFileFromOffset(path, offset)
}

func parsePrimeFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parsePiShaped(path, offset, "prime", primeProject(path), true)
}

// primeProject is the fallback name when the header carries no cwd: the
// directory the file sits in, which under the flat root is the root itself and
// says nothing. Empty is the honest answer there, and the header's cwd is what
// normally names the project.
func primeProject(path string) string {
	dir := projectDir(PrimeRoot(), path)
	if dir == "" || dir == PrimeRoot() {
		return ""
	}
	return claudeProjectName(dir)
}
