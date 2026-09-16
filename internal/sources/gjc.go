package sources

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// gajae-code (`gjc`) is another pi descendant: a `session` header carrying the
// id and the cwd, then one `message` line per turn, so parsePiShaped reads it.
//
//	~/.gjc/agent/sessions/<project-slug>/<session>.jsonl          the session
//	~/.gjc/agent/sessions/<project-slug>/<session>/N-*.jsonl      its sub-agents
//
// The second layout is a pass run by a sub-agent, one file per pass. Those are
// skipped the way Claude Code's and Cursor's are: a sub-agent's transcript
// repeats the parent's work in its own words and, indexed as a session of its
// own, competes with the parent for the same recall slot.
// DEJA_INCLUDE_SUBAGENTS=1 takes them, the same switch as everywhere else
// (#3647).

// GjcConfigDir is the agent directory; GJC_CODING_AGENT_DIR moves it.
func GjcConfigDir() string {
	if p := EnvPath("GJC_CODING_AGENT_DIR", ""); p != "" {
		return expandTilde(p)
	}
	return filepath.Join(Home(), ".gjc", "agent")
}

// GjcRoot is the session store root. DEJA_GJC_ROOT wins.
func GjcRoot() string {
	return EnvPath("DEJA_GJC_ROOT", filepath.Join(GjcConfigDir(), "sessions"))
}

// GjcSessionFiles lists the transcripts, sub-agent passes excluded unless they
// are asked for.
func GjcSessionFiles() []string {
	return walkFiles(GjcRoot(), gjcWanted)
}

func gjcWanted(p string) bool {
	if !strings.HasSuffix(p, ".jsonl") {
		return false
	}
	if gjcSubagentPath(p) && os.Getenv("DEJA_INCLUDE_SUBAGENTS") != "1" {
		return false
	}
	return true
}

// gjcSubagentPath reports whether a transcript is a sub-agent pass: those sit
// one directory deeper than a session, under a directory named for the session
// they belong to.
func gjcSubagentPath(p string) bool {
	rel, err := filepath.Rel(GjcRoot(), p)
	if err != nil {
		return false
	}
	return len(strings.Split(filepath.ToSlash(rel), "/")) > 2
}

// GjcUnderRoot lets the registry claim a path for incremental ingest.
func GjcUnderRoot(p string) bool {
	return strings.HasPrefix(p, GjcRoot()) && gjcWanted(p)
}

func LoadGjc() []model.Session { return parseFiles(GjcSessionFiles(), ParseGjcFile) }

// ParseGjcFile reads one transcript.
func ParseGjcFile(path string) ([]model.Session, error) {
	return ParseGjcFileFromOffset(path, 0)
}

// ParseGjcFileFromOffset is the incremental read.
func ParseGjcFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parsePiShaped(path, offset, "gjc", gjcProject(path), true)
}

func gjcProject(path string) string {
	dir := projectDir(GjcRoot(), path)
	if dir == "" || dir == GjcRoot() {
		return ""
	}
	return claudeProjectName(dir)
}
