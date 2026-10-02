package sources

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// PrimeArtifactsRoot is where prime-agent keeps a session's artifacts, beside
// the session root. rlm.spawn writes each child's transcript there, as
// session-artifacts/<parent-id>/sub-<n>/<child-id>.jsonl (#4407).
func PrimeArtifactsRoot() string {
	return filepath.Join(filepath.Dir(PrimeRoot()), "session-artifacts")
}

// PrimeRoots are the directories prime-agent transcripts live under.
func PrimeRoots() []string { return []string{PrimeRoot(), PrimeArtifactsRoot()} }

// PrimeSessionFiles lists transcript files under the prime-agent session root
// and the spawned children's transcripts under the artifacts root, unless
// DEJA_INCLUDE_SUBAGENTS=0 leaves children out.
func PrimeSessionFiles() []string {
	files := walkFiles(PrimeRoot(), func(p string) bool {
		return strings.HasSuffix(p, ".jsonl")
	})
	if os.Getenv("DEJA_INCLUDE_SUBAGENTS") == "0" {
		return files
	}
	return append(files, walkFiles(PrimeArtifactsRoot(), isPrimeChildTranscript)...)
}

// isPrimeChildTranscript is a child's transcript under the artifacts root: a
// .jsonl in a sub-<n> directory. semantic-edges.jsonl sits beside it and is
// prime's own event log.
func isPrimeChildTranscript(p string) bool {
	return strings.HasSuffix(p, ".jsonl") && filepath.Base(p) != "semantic-edges.jsonl" &&
		strings.HasPrefix(filepath.Base(filepath.Dir(p)), "sub-")
}

// IsPrimeChildPath reports whether p is an rlm.spawn child's transcript.
func IsPrimeChildPath(p string) bool {
	return strings.HasPrefix(p, PrimeArtifactsRoot()+string(filepath.Separator)) && isPrimeChildTranscript(p)
}

// isPrimeFile reports whether p is a transcript PrimeSessionFiles would list.
func isPrimeFile(p string) bool {
	if strings.HasPrefix(p, PrimeArtifactsRoot()+string(filepath.Separator)) {
		return isPrimeChildTranscript(p) && os.Getenv("DEJA_INCLUDE_SUBAGENTS") != "0"
	}
	return strings.HasSuffix(p, ".jsonl") && strings.HasPrefix(p, PrimeRoot())
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
	ss, err := parsePiShaped(path, offset, "prime", primeProject(path), true)
	// A spawned child comes in the way a Claude Code subagent does: the task,
	// what it changed and how it ended, unless the reader asked for the whole
	// run. Whole, its reading competed with the parent for recall (#3009,
	// #4407).
	if IsPrimeChildPath(path) && os.Getenv("DEJA_INCLUDE_SUBAGENTS") != "1" {
		for i := range ss {
			ss[i].Messages = KeepSubagentTail(ss[i].Messages)
		}
	}
	return ss, err
}

// PrimeSessionDir is the cwd a prime-agent session's header records, for the
// cd in front of `prime-agent --resume`: prime resumes a session only from the
// project it ran in (#4408).
func PrimeSessionDir(path string) string {
	if path == "" {
		return ""
	}
	header := leadingJSONLHeader(path, math.MaxInt64, headerLookahead, isPiHeader)
	cwd, _ := header["cwd"].(string)
	return cwd
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

// cellDiffs records the files a prime ipython cell changed. prime's one tool
// runs Python, and its edit skill, `await edit(path, old_str, new_str)`, is how
// the agent changes a file; each change it made is on the cell's result as
// details.diffs[{path, oldStr, newStr}], which prime reads back itself to list
// a session's edited files (#4526). A diff is there only for a change that
// was written, so a cell that fails afterwards keeps it.
func (r *piReader) cellDiffs(v any, t time.Time) {
	diffs, _ := v.([]any)
	var calls []any
	for _, d := range diffs {
		m, _ := d.(map[string]any)
		p := r.abs(str(m["path"]))
		if p == "" {
			continue
		}
		calls = append(calls, map[string]any{"type": "tool_use", "name": "edit",
			"input": map[string]any{"path": p, "oldText": str(m["oldStr"]), "newText": str(m["newStr"])}})
	}
	if len(calls) == 0 {
		return
	}
	if IndexToolPaths() {
		if p := toolPathsIn(calls, piDialect); p != "" {
			r.add(RoleFiles, p, t)
		}
	}
	for _, c := range calls {
		one := []any{c}
		if IndexEdits() {
			for _, span := range editSpansIn(one, piDialect) {
				r.add(RoleEdit, span, t)
			}
		}
		if IndexWrites() {
			for _, w := range wroteRecordsIn(one, piDialect) {
				r.add(RoleWrote, w, t)
			}
		}
	}
}
