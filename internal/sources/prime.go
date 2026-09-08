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

// PrimeConfigDir is the prime-agent user directory — settings, extensions,
// skills and the default session root. PRIME_AGENT_CODING_AGENT_DIR moves it
// (config.ts getAgentDir), and moves it for deja too: read only the session
// variables, deja wrote settings.json where prime never looked and called it
// wired (#3276).
func PrimeConfigDir() string {
	// prime expands a leading ~ itself (config.ts expandTildePath); read the
	// same value the same way.
	if p := EnvPath("PRIME_AGENT_CODING_AGENT_DIR", ""); p != "" {
		return expandTilde(p)
	}
	return filepath.Join(Home(), ".prime", "agent")
}

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
	// The current name first, the legacy one second — prime's own order
	// (config.ts: ENV_SESSION_DIR ?? ENV_LEGACY_SESSION_DIR), so the two
	// agree on the root when both are set (#3277).
	for _, name := range []string{"PRIME_AGENT_SESSION_DIR", "PRIME_AGENT_CODING_AGENT_SESSION_DIR"} {
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
