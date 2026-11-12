package sources

import (
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// QoderConfigDir is the native Qoder CLI configuration directory. Qoder —
// Alibaba's AI IDE and CLI — writes Claude Code-shaped transcripts: the same
// <root>/projects/<flattened cwd>/<session uuid>.jsonl layout and the same
// per-line format (type, sessionId, parentUuid, uuid, isSidechain,
// message{role,content}, permissionMode, ...). DEJA_QODER_ROOT intentionally
// does not affect this function because that variable only relocates reads,
// the way DEJA_QWEN_ROOT does not affect QwenConfigDir.
func QoderConfigDir() string { return filepath.Join(Home(), ".qoder") }

// QoderRoot is the config dir deja reads Qoder sessions from.
func QoderRoot() string { return EnvPath("DEJA_QODER_ROOT", QoderConfigDir()) }

// QoderSessionFiles lists every transcript under <root>/projects. Qoder marks
// sub-agent turns with isSidechain inside the session file itself, so there
// is no separate subagent tree and no sidecar shape to exclude today.
func QoderSessionFiles() []string {
	return walkFiles(filepath.Join(QoderRoot(), "projects"), func(p string) bool {
		return strings.HasSuffix(p, ".jsonl")
	})
}

// QoderUnderRoot reports whether a path belongs to this store, so the registry
// can claim it without stealing a stock Claude transcript.
func QoderUnderRoot(p string) bool {
	return strings.HasPrefix(p, filepath.Join(QoderRoot(), "projects"))
}

// LoadQoder reads every Qoder session on this machine.
func LoadQoder() []model.Session { return parseFiles(QoderSessionFiles(), ParseQoderFile) }

// ParseQoderFile reads one Qoder transcript. The lines are ordinary Claude
// Code JSONL, so the Claude decoder does the work; only the harness
// attribution differs, which keeps Qoder sessions out of Claude's filters,
// stats and writebacks. Cherry Studio's ParseCherryStudioFileFromOffset is
// the precedent for reading Claude-shaped lines under their own name.
func ParseQoderFile(path string) ([]model.Session, error) {
	return parseQoderFromOffset(path, 0)
}

// ParseQoderFileFromOffset reads a Qoder transcript from offset on.
func ParseQoderFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parseQoderFromOffset(path, offset)
}

func parseQoderFromOffset(path string, offset int64) ([]model.Session, error) {
	return parseClaudeTypedWithOptions(path, func(fn func([]byte)) error {
		return scanJSONLBytes(path, offset, fn)
	}, claudeParseOptions{Harness: "qoder"})
}
