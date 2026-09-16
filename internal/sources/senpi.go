package sources

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Senpi (OmO Native) and Kimchi Coding are both pi descendants: they kept the
// JSONL envelope pi writes, so the shared parsePiShaped reader does the work
// and this file is the two roots and their names.
//
//	Senpi:  ${SENPI_CODING_AGENT_DIR:-~/.senpi/agent}/sessions/<encoded-cwd>/*.jsonl
//	Kimchi: ${KIMCHI_CODING_AGENT_DIR:-~/.config/kimchi/harness}/sessions/*.jsonl
//
// Senpi keeps the encoded project directory pi has, so that names the project;
// Kimchi's root is flat, and the header line's cwd is what names it there — the
// choice omp and prime-agent already make for the same reason.

// SenpiConfigDir is the agent directory. SENPI_CODING_AGENT_DIR moves it, and
// moves it for deja: a machine that has said where its sessions are should not
// have to say it twice.
func SenpiConfigDir() string {
	if p := EnvPath("SENPI_CODING_AGENT_DIR", ""); p != "" {
		return expandTilde(p)
	}
	return filepath.Join(Home(), ".senpi", "agent")
}

// SenpiRoot is the session store root. DEJA_SENPI_ROOT wins, for tests and for
// a store that is neither.
func SenpiRoot() string {
	return EnvPath("DEJA_SENPI_ROOT", filepath.Join(SenpiConfigDir(), "sessions"))
}

// SenpiSessionFiles lists the transcripts.
func SenpiSessionFiles() []string {
	return walkFiles(SenpiRoot(), func(p string) bool {
		return strings.HasSuffix(p, ".jsonl")
	})
}

func LoadSenpi() []model.Session { return parseFiles(SenpiSessionFiles(), ParseSenpiFile) }

// ParseSenpiFile reads one transcript.
func ParseSenpiFile(path string) ([]model.Session, error) {
	return ParseSenpiFileFromOffset(path, 0)
}

// ParseSenpiFileFromOffset is the incremental read.
func ParseSenpiFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parsePiShaped(path, offset, "senpi", senpiProject(path), false)
}

// senpiProject reads the name out of the encoded directory, the way pi's does.
func senpiProject(path string) string {
	dir := projectDir(SenpiRoot(), path)
	if dir == "" || dir == SenpiRoot() {
		return ""
	}
	return claudeProjectName(dir)
}

// KimchiConfigDir is Kimchi's harness directory; KIMCHI_CODING_AGENT_DIR moves
// it. The default sits under the config home rather than a dot directory of its
// own, so XDG_CONFIG_HOME moves it too.
func KimchiConfigDir() string {
	if p := EnvPath("KIMCHI_CODING_AGENT_DIR", ""); p != "" {
		return expandTilde(p)
	}
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(Home(), ".config")
	}
	return filepath.Join(cfg, "kimchi", "harness")
}

// KimchiRoot is the session store root. DEJA_KIMCHI_ROOT wins.
func KimchiRoot() string {
	return EnvPath("DEJA_KIMCHI_ROOT", filepath.Join(KimchiConfigDir(), "sessions"))
}

// KimchiSessionFiles lists the transcripts.
func KimchiSessionFiles() []string {
	return walkFiles(KimchiRoot(), func(p string) bool {
		return strings.HasSuffix(p, ".jsonl")
	})
}

func LoadKimchi() []model.Session { return parseFiles(KimchiSessionFiles(), ParseKimchiFile) }

// ParseKimchiFile reads one transcript.
func ParseKimchiFile(path string) ([]model.Session, error) {
	return ParseKimchiFileFromOffset(path, 0)
}

// ParseKimchiFileFromOffset is the incremental read. The root is flat, so the
// header's cwd is what names the project.
func ParseKimchiFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parsePiShaped(path, offset, "kimchi", kimchiProject(path), true)
}

func kimchiProject(path string) string {
	dir := projectDir(KimchiRoot(), path)
	if dir == "" || dir == KimchiRoot() {
		return ""
	}
	return claudeProjectName(dir)
}
