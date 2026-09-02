package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// OpenClaw (github.com/openclaw/openclaw) runs pi-lineage agents; each agent
// keeps pi-format JSONL transcripts under the state dir:
//
//	${OPENCLAW_STATE_DIR:-~/.openclaw}/agents/<agentId>/sessions/<sessionId>.jsonl
//
// sessions.json in the same directory is store metadata, compaction
// checkpoints (<id>.checkpoint.<uuid>.jsonl) are context snapshots, and
// archived transcripts carry .deleted/.reset/.bak suffixes — all skipped.
// Verified against openclaw src/config/sessions/{paths,artifacts}.ts.

// OpenClawStateDir is the OpenClaw state root.
func OpenClawStateDir() string {
	return EnvPath("OPENCLAW_STATE_DIR", filepath.Join(Home(), ".openclaw"))
}

// OpenClawRoot returns the agents root, overridable via DEJA_OPENCLAW_ROOT.
func OpenClawRoot() string {
	return EnvPath("DEJA_OPENCLAW_ROOT", filepath.Join(OpenClawStateDir(), "agents"))
}

var openclawCheckpointRE = regexp.MustCompile(`(?i)\.checkpoint\.[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\.jsonl$`)

// openclawTranscript reports whether p is a live transcript directly inside
// an agent's sessions dir (agents/<id>/sessions/<file>.jsonl).
func openclawTranscript(root, p string) bool {
	if !strings.HasSuffix(p, ".jsonl") || openclawCheckpointRE.MatchString(p) {
		return false
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	return len(parts) == 3 && parts[1] == "sessions"
}

// OpenClawSessionFiles lists live transcript files for all agents.
func OpenClawSessionFiles() []string {
	root := OpenClawRoot()
	return walkFiles(root, func(p string) bool { return openclawTranscript(root, p) })
}

// LoadOpenClaw loads all OpenClaw sessions.
func LoadOpenClaw() []model.Session {
	ss := parseFiles(OpenClawSessionFiles(), ParseOpenClawFile)
	for _, db := range OpenClawAgentDBs() {
		got, _ := ParseOpenClawDB(db)
		ss = append(ss, got...)
	}
	return ss
}

// ParseOpenClawFile parses a single OpenClaw transcript.
func ParseOpenClawFile(path string) ([]model.Session, error) {
	return ParseOpenClawFileFromOffset(path, 0)
}

// ParseOpenClawFileFromOffset parses an OpenClaw transcript from a byte offset.
func ParseOpenClawFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parsePiShaped(path, offset, "openclaw", openclawProject(path), true)
}

// openclawProject attributes a session to its agent id; the header cwd, when
// the session ran with one, overrides inside parsePiShaped.
func openclawProject(path string) string {
	if agent := filepath.Base(filepath.Dir(filepath.Dir(path))); agent != "" && agent != "." && agent != string(filepath.Separator) {
		return "openclaw-" + agent
	}
	return "openclaw"
}

// OpenClawSessionKey returns the session key that owns a transcript, or "" when
// the store does not name one. OpenClaw addresses a conversation by key
// (`agent:<id>:<name>`) rather than by the uuid its file is named after: the
// terminal UI takes `--session <key>` and nothing takes the uuid. The mapping
// lives in sessions.json beside the transcripts.
func OpenClawSessionKey(path string) string {
	b, err := os.ReadFile(filepath.Join(filepath.Dir(path), "sessions.json"))
	if err != nil {
		return ""
	}
	var store map[string]struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(b, &store); err != nil {
		return ""
	}
	id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	for key, rec := range store {
		if rec.SessionID == id {
			return key
		}
	}
	return ""
}
