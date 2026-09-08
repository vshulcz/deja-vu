package sources

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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
// sessions.json in the same directory is store metadata and compaction
// checkpoints (<id>.checkpoint.<uuid>.jsonl) are context snapshots; both are
// skipped. What a reset or a delete leaves behind is not: OpenClaw renames the
// transcript to <id>.jsonl.reset.<ts> or <id>.jsonl.deleted.<ts>, and since the
// SQLite flip an explicit delete writes <id>.jsonl.deleted.<ts>.zst — which is
// exactly the history someone asks deja for after losing it (#2997).
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

// openclawArchiveRE matches what a reset or a delete renames a transcript to.
// The timestamp is whatever OpenClaw stamped it with, and the .zst is the
// compressed form an explicit delete writes since the SQLite flip.
var openclawArchiveRE = regexp.MustCompile(`\.jsonl\.(reset|deleted)\.[0-9]+(\.zst)?$`)

// openclawTranscript reports whether p is a live transcript directly inside
// an agent's sessions dir (agents/<id>/sessions/<file>.jsonl).
func openclawTranscript(root, p string) bool {
	archived := openclawArchiveRE.MatchString(p)
	if !strings.HasSuffix(p, ".jsonl") && !archived {
		return false
	}
	if openclawCheckpointRE.MatchString(p) {
		return false
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 3 || parts[1] != "sessions" {
		return false
	}
	// A reset writes the archive and starts a new transcript under the old
	// name; taking both would index the same conversation twice. The live file
	// is the one the agent is still writing to, so the archive stands down.
	if archived {
		if _, err := os.Stat(openclawArchiveLive(p)); err == nil {
			return false
		}
	}
	return true
}

// openclawArchiveLive is the transcript an archive was renamed from.
func openclawArchiveLive(p string) string {
	at := strings.Index(p, ".jsonl.")
	if at < 0 {
		return p
	}
	return p[:at] + ".jsonl"
}

// OpenClawSessionFiles lists live transcript files for all agents.
func OpenClawSessionFiles() []string {
	root := OpenClawRoot()
	return walkFiles(root, func(p string) bool { return openclawTranscript(root, p) })
}

// OpenClawSidecarFiles lists the json and jsonl files an OpenClaw store keeps
// beside its transcripts that the reader declines on purpose: the per-session
// trajectory-path.json, the sessions.json list and the checkpoint snapshots.
// doctor counted them as transcripts it could not read, once per session
// (#3317). An archive of a reset needs no entry: its name ends in the
// timestamp or .zst, so the walk never reaches it.
func OpenClawSidecarFiles() []string {
	root := OpenClawRoot()
	return walkFiles(root, func(p string) bool {
		if openclawTranscript(root, p) {
			return false
		}
		switch {
		case strings.HasSuffix(p, ".trajectory-path.json"):
			return true
		case filepath.Base(p) == "sessions.json":
			return true
		case openclawCheckpointRE.MatchString(p):
			return true
		}
		return false
	})
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
	if openclawArchiveRE.MatchString(path) {
		return parseOpenClawArchive(path)
	}
	return parsePiShaped(path, offset, "openclaw", openclawProject(path), true)
}

// parseOpenClawArchive reads what a reset or a delete left behind. The file
// never grows, so it is read whole rather than from an offset, and the session
// keeps the id it had before the rename — that is the id the store, the key
// mapping and anyone looking for it still use.
func parseOpenClawArchive(path string) ([]model.Session, error) {
	read := path
	if strings.HasSuffix(path, ".zst") {
		plain, err := zstdToTemp(path)
		if err != nil {
			return nil, err
		}
		defer func() { _ = os.Remove(plain) }()
		read = plain
	}
	ss, err := parsePiShaped(read, 0, "openclaw", openclawProject(path), true)
	for i := range ss {
		ss[i].Path = path
		ss[i].ID = strings.TrimSuffix(filepath.Base(openclawArchiveLive(path)), ".jsonl")
	}
	return ss, err
}

// zstdToTemp decompresses a .zst archive into a temporary file and returns its
// path. deja carries no Go dependencies, so the frames go through the same
// `zstd` CLI the Zed and DeepSeek stores already need; the caller removes the
// file. A scanner that reads from a path is what every transcript parser here
// takes, and an archive is small enough that a temporary copy is cheaper than
// teaching all of them to read a stream.
func zstdToTemp(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	cmd := exec.Command("zstd", "-d", "-c", "-q")
	cmd.Stdin = bytes.NewReader(raw)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("openclaw: zstd -d %s: %w: %s", filepath.Base(path), err,
			strings.TrimSpace(errBuf.String()))
	}
	f, err := os.CreateTemp("", "deja-openclaw-*.jsonl")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(out.Bytes()); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
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
