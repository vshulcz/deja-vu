package sources

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// ZCode (Z.ai's agentic development environment) writes the same flat
// transcript Command Code does, under the same project layout:
//
//	~/.zcode/projects/<encoded-cwd>/<session>.jsonl
//
// It also keeps a SQLite store, which is read through OpenCode's schema. That
// was left unread while no sample of it was in hand (#3647); the schema is now
// attested by a third-party tool that reads the live database — zcode-stats
// 0.8.0 names `~/.zcode/cli/db/db.sqlite` and queries `session(directory,
// task_type, version, time_created, time_updated)`, `message(id, session_id,
// data)` with `json_extract(data,'$.role')`, and `part(data)` with
// `json_extract(data,'$.type')`. That is OpenCode's schema, which deja already
// parses for OpenCode itself and for Kilo's CLI, so this is one more root
// rather than a new reader. Not yet checked against a running ZCode, which the
// registry entry says (#3675).

// ZCodeConfigDir is ZCode's user directory: the project store, and under
// `cli/config.json` everything it is configured with — the server map and the
// hooks both live in that one file.
func ZCodeConfigDir() string { return filepath.Join(Home(), ".zcode") }

// ZCodeRoot is the project store root. DEJA_ZCODE_ROOT replaces it.
func ZCodeRoot() string {
	return EnvPath("DEJA_ZCODE_ROOT", filepath.Join(ZCodeConfigDir(), "projects"))
}

// ZCodeDB is the CLI's SQLite store. DEJA_ZCODE_DB overrides the path outright,
// the way DEJA_KILO_DB does for Kilo's.
func ZCodeDB() string {
	if p := os.Getenv("DEJA_ZCODE_DB"); p != "" {
		return p
	}
	return filepath.Join(ZCodeConfigDir(), "cli", "db", "db.sqlite")
}

// ParseZCodeDB reads that store through OpenCode's schema.
func ParseZCodeDB(db string) ([]model.Session, error) {
	return parseOpencodeSchemaDB("zcode", db, "", 0)
}

// ParseZCodeDBSince is ParseZCodeDB bounded by the incremental watermark.
func ParseZCodeDBSince(db string, t time.Time) ([]model.Session, error) {
	if t.IsZero() {
		return ParseZCodeDB(db)
	}
	return parseOpencodeSchemaDB("zcode", db, opencodeSinceWhere(t), 0)
}

// ZCodeSessionFiles lists the transcripts, and the database when it holds
// anything — the same pair Kilo has.
func ZCodeSessionFiles() []string {
	out := ZCodeTranscriptFiles()
	if fi, err := os.Stat(ZCodeDB()); err == nil && fi.Size() > 0 {
		out = append(out, ZCodeDB())
	}
	return out
}

// ZCodeUnderRoot lets the registry claim a path for incremental ingest.
func ZCodeUnderRoot(p string) bool {
	return strings.HasPrefix(p, ZCodeRoot()) && strings.HasSuffix(p, ".jsonl")
}

// LoadZCode reads both stores: the transcripts with the flat-role reader, the
// CLI database with OpenCode's, the way LoadKilo does for Kilo's two.
func LoadZCode() []model.Session {
	ss := parseFiles(ZCodeTranscriptFiles(), ParseZCodeFile)
	dbSS, _ := ParseZCodeDB(ZCodeDB())
	return append(ss, dbSS...)
}

// ZCodeTranscriptFiles is the JSONL half on its own. Callers that parse with
// the transcript reader need it: handed the database, that reader answers zero
// and a store deja reads correctly reports itself broken.
func ZCodeTranscriptFiles() []string {
	return walkFiles(ZCodeRoot(), func(p string) bool {
		return strings.HasSuffix(p, ".jsonl")
	})
}

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
