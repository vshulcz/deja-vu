package sources

import (
	"os"
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
//
// That root is not the only one. A named profile (`omp --profile <name>`,
// `OMP_PROFILE`) relocates the whole user scope, sessions included, and an
// existing `omp` directory under XDG_DATA_HOME relocates it again — verified by
// running omp 17.4.1 both ways and following where the transcript landed. Under
// XDG the `agent` segment is gone: `$XDG_DATA_HOME/omp/sessions/...`.
//
// Reading only the default profile is the failure deja can least afford: the
// store is there, the sessions are there, and the answer is an empty history
// with nothing said about why.

// OmpConfigDir is the omp configuration directory.
func OmpConfigDir() string { return filepath.Join(Home(), ".omp", "agent") }

// OmpRoot returns the default profile's session root, overridable via
// DEJA_OMP_ROOT. When the override is set it is the only root read: a test or a
// relocated install means that directory and not this machine's profiles.
func OmpRoot() string { return EnvPath("DEJA_OMP_ROOT", filepath.Join(OmpConfigDir(), "sessions")) }

// OmpSessionRoots is every session root omp may write to on this machine: the
// default profile, each named profile, and their XDG counterparts when the user
// has moved omp's data there.
func OmpSessionRoots() []string {
	if p := os.Getenv("DEJA_OMP_ROOT"); p != "" {
		return []string{p}
	}
	home := filepath.Join(Home(), ".omp")
	roots := []string{filepath.Join(home, "agent", "sessions")}
	roots = append(roots, ompProfileRoots(filepath.Join(home, "profiles"), "agent")...)
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		base := filepath.Join(xdg, "omp")
		if fi, err := os.Stat(base); err == nil && fi.IsDir() {
			roots = append(roots, filepath.Join(base, "sessions"))
			roots = append(roots, ompProfileRoots(filepath.Join(base, "profiles"), "")...)
		}
	}
	return roots
}

// ompProfileRoots lists the session directory of every named profile under
// dir. The middle segment is "agent" under ~/.omp and empty under XDG, which is
// how omp itself lays the two out.
func ompProfileRoots(dir, middle string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		parts := []string{dir, e.Name()}
		if middle != "" {
			parts = append(parts, middle)
		}
		out = append(out, filepath.Join(append(parts, "sessions")...))
	}
	return out
}

// OmpSessionFiles lists transcript files under every omp session root.
func OmpSessionFiles() []string {
	var files []string
	for _, root := range OmpSessionRoots() {
		files = append(files, walkFiles(root, func(p string) bool {
			return strings.HasSuffix(p, ".jsonl")
		})...)
	}
	return files
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
//
// The root passed in does not matter here and is left as the default one: a
// transcript sits directly under its project directory, so projectDir returns
// that directory whether or not the file is under the root it was given. A
// per-root lookup was written first and removed after a mutation showed it
// changed nothing — the same project comes out for a profile session either
// way.
func ompProject(path string) string {
	return claudeProjectName(projectDir(OmpRoot(), path))
}
