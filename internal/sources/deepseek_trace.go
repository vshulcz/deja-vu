package sources

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DeepSeekHeader captures logical session metadata from the first JSONL record.
type DeepSeekHeader struct {
	Version         int       `json:"version"`
	ID              string    `json:"id"`
	CreatedAt       time.Time `json:"created_at"`
	CWD             string    `json:"cwd,omitempty"`
	ParentSession   string    `json:"parent_session,omitempty"`
	IsSeeded        bool      `json:"is_seeded"`
	Origin          string    `json:"origin,omitempty"`
	DelegationDepth int       `json:"delegation_depth"`
	AgentPreset     string    `json:"agent_preset,omitempty"`
}

// DeepSeekEvent captures one logical event envelope and its exact raw JSON SHA-256 hash.
type DeepSeekEvent struct {
	Type            string          `json:"type"`
	Seq             int             `json:"seq,omitempty"`
	HasSeq          bool            `json:"has_seq"`
	Time            time.Time       `json:"time"`
	Data            json.RawMessage `json:"data,omitempty"`
	SurfaceOp       json.RawMessage `json:"surface_op,omitempty"`
	SourceEventSeqs []int           `json:"source_event_seqs,omitempty"`
	Ignorable       bool            `json:"ignorable,omitempty"`
	RawHash         string          `json:"raw_hash"`
}

// DeepSeekTrace is the decoded trace representation of a native DSH session log.
type DeepSeekTrace struct {
	Header      DeepSeekHeader  `json:"header"`
	Events      []DeepSeekEvent `json:"events"`
	Path        string          `json:"path"`
	Log         string          `json:"log"`
	Truncated   bool            `json:"truncated"`
	Unsupported bool            `json:"unsupported"`
	Notice      string          `json:"notice,omitempty"`
}

var canonicalDeepSeekLogPattern = regexp.MustCompile(`^session(?:\.v([1-9][0-9]*))?\.jsonl(\.zstd)?$`)

// parseCanonicalDeepSeekLogFilename parses a log filename into its format version and extension.
// It accepts session.jsonl(.zstd) (version 0) and session.v<N>.jsonl(.zstd) (version N >= 1).
func parseCanonicalDeepSeekLogFilename(filename string) (version int, ext string, ok bool) {
	m := canonicalDeepSeekLogPattern.FindStringSubmatch(filename)
	if m == nil {
		return 0, "", false
	}
	ver := 0
	if m[1] != "" {
		n, err := strconv.Atoi(m[1])
		if err != nil || n < 1 {
			return 0, "", false
		}
		ver = n
	}
	compressionExt := ".jsonl"
	if m[2] != "" {
		compressionExt += m[2]
	}
	return ver, compressionExt, true
}

// CanonicalDeepSeekLog inspects a session directory or file path and returns the path to
// the numerically highest canonical generation log. It detects same-generation encoding
// mismatches (e.g. both .jsonl and .jsonl.zstd for the same generation) and ignores
// non-canonical artifacts.
func CanonicalDeepSeekLog(path string) (string, error) {
	dir := path
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		dir = filepath.Dir(path)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}

	type cand struct {
		name    string
		version int
		ext     string
	}
	byVersion := make(map[int][]cand)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ver, ext, ok := parseCanonicalDeepSeekLogFilename(entry.Name())
		if !ok {
			continue
		}
		byVersion[ver] = append(byVersion[ver], cand{
			name:    entry.Name(),
			version: ver,
			ext:     ext,
		})
	}

	// Detect same-generation encoding mismatches
	for ver, cands := range byVersion {
		if len(cands) > 1 {
			var names []string
			for _, c := range cands {
				names = append(names, c.name)
			}
			return "", fmt.Errorf("deepseek: same-generation encoding mismatch for version %d in %s: %s", ver, dir, strings.Join(names, ", "))
		}
	}

	// Select numerically highest canonical generation
	maxVer := -1
	var best cand
	for ver, cands := range byVersion {
		if ver > maxVer {
			maxVer = ver
			best = cands[0]
		}
	}
	if maxVer == -1 {
		return "", fmt.Errorf("deepseek: no canonical session log found in %s", dir)
	}

	return filepath.Join(dir, best.name), nil
}

// FindDeepSeekSessionLog locates the authoritative canonical session log for the given
// session ID across all project directories under DeepSeekRoot(). It normalizes the ID,
// checks for duplicate session IDs across projects, and resolves the canonical generation.
func FindDeepSeekSessionLog(id string) (string, error) {
	cleanID := strings.TrimPrefix(strings.TrimSpace(id), "session-")
	if cleanID == "" {
		return "", fmt.Errorf("deepseek: empty session id")
	}

	root := DeepSeekRoot()
	projEntries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("deepseek: session %q: %w", id, os.ErrNotExist)
		}
		return "", err
	}

	type match struct {
		project string
		logPath string
	}
	var matches []match

	for _, pe := range projEntries {
		if !pe.IsDir() {
			continue
		}
		projDir := filepath.Join(root, pe.Name())
		sessEntries, err := os.ReadDir(projDir)
		if err != nil {
			continue
		}
		for _, se := range sessEntries {
			if !se.IsDir() {
				continue
			}
			sessCleanID := strings.TrimPrefix(se.Name(), "session-")
			if sessCleanID == cleanID || se.Name() == id {
				sessDir := filepath.Join(projDir, se.Name())
				canon, err := CanonicalDeepSeekLog(sessDir)
				if err != nil {
					return "", err
				}
				matches = append(matches, match{
					project: pe.Name(),
					logPath: canon,
				})
			}
		}
	}

	if len(matches) == 0 {
		return "", fmt.Errorf("deepseek: session %q: %w", id, os.ErrNotExist)
	}

	if len(matches) > 1 {
		firstProj := matches[0].project
		for _, m := range matches[1:] {
			if m.project != firstProj {
				return "", fmt.Errorf("deepseek: duplicate session ID %q appears in multiple project directories", id)
			}
		}
		return "", fmt.Errorf("deepseek: duplicate session directory for ID %q in project %s", id, firstProj)
	}

	return matches[0].logPath, nil
}

// readDeepSeekBytes reads raw bytes from a plaintext or Zstandard-compressed log file.
// If decompression produces data but errors on a torn/truncated final frame, it returns
// the available bytes with truncated=true and the error notice.
func readDeepSeekBytes(path string) ([]byte, bool, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false, "", err
	}
	if !strings.HasSuffix(path, ".zstd") {
		truncated := false
		var notice string
		if len(raw) > 0 && raw[len(raw)-1] != '\n' {
			truncated = true
			notice = "file does not end with newline"
		}
		return raw, truncated, notice, nil
	}

	if len(raw) == 0 {
		return nil, false, "", nil
	}

	cmd := exec.Command("zstd", "-d", "-c", "-q")
	cmd.Stdin = bytes.NewReader(raw)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	zerr := cmd.Run()
	if zerr != nil {
		if out.Len() > 0 {
			notice := strings.TrimSpace(errBuf.String())
			if notice == "" {
				notice = zerr.Error()
			}
			return out.Bytes(), true, notice, nil
		}
		return nil, false, "", fmt.Errorf("deepseek: zstd -d %s: %w: %s", filepath.Base(path), zerr, strings.TrimSpace(errBuf.String()))
	}
	return out.Bytes(), false, "", nil
}

// ReadDeepSeekTrace reads and parses a native DeepSeek Harness session log into a DeepSeekTrace.
// Clean EOF is complete even if a turn was left open; only torn tail records or partial Zstd
// frames are marked as truncated. Unsupported format generations (>3) are explicitly flagged.
// Malformed headers or middle rows return a descriptive error.
func ReadDeepSeekTrace(path string) (*DeepSeekTrace, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if fi.IsDir() {
		canon, err := CanonicalDeepSeekLog(path)
		if err != nil {
			return nil, err
		}
		path = canon
	}

	raw, truncated, notice, err := readDeepSeekBytes(path)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("deepseek: empty session log %s", path)
	}

	lines := bytes.Split(raw, []byte("\n"))
	if len(lines) == 0 || (len(lines) == 1 && len(bytes.TrimSpace(lines[0])) == 0) {
		return nil, fmt.Errorf("deepseek: empty session log %s", path)
	}

	// First line must be the session header
	headerLine := bytes.TrimSpace(lines[0])
	if len(headerLine) == 0 {
		return nil, fmt.Errorf("deepseek: malformed session header in %s: empty first line", path)
	}

	var headerMap map[string]any
	if err := json.Unmarshal(headerLine, &headerMap); err != nil {
		return nil, fmt.Errorf("deepseek: malformed session header in %s: %w", path, err)
	}
	typ, _ := headerMap["type"].(string)
	if typ != "session" {
		return nil, fmt.Errorf("deepseek: malformed session header in %s: expected type 'session', got %q", path, typ)
	}

	ver := 0
	if v, ok := headerMap["version"].(float64); ok {
		ver = int(v)
	}

	header := DeepSeekHeader{
		Version: ver,
	}
	if s, ok := headerMap["id"].(string); ok {
		header.ID = s
	}
	if created := parseTimeAny(headerMap["createdAt"]); !created.IsZero() {
		header.CreatedAt = created
	}
	if s, ok := headerMap["cwd"].(string); ok {
		header.CWD = s
	}
	if s, ok := headerMap["parentSession"].(string); ok {
		header.ParentSession = s
	}
	if b, ok := headerMap["isSeeded"].(bool); ok {
		header.IsSeeded = b
	}
	if s, ok := headerMap["origin"].(string); ok {
		header.Origin = s
	}
	if d, ok := headerMap["delegationDepth"].(float64); ok {
		header.DelegationDepth = int(d)
	}
	if s, ok := headerMap["agentPreset"].(string); ok {
		header.AgentPreset = s
	}

	logBase := filepath.Base(path)
	if header.Version > 3 {
		return &DeepSeekTrace{
			Header:      header,
			Path:        path,
			Log:         logBase,
			Unsupported: true,
			Notice:      fmt.Sprintf("unsupported session format v%d; this build understands up to v3", header.Version),
		}, nil
	}

	trace := &DeepSeekTrace{
		Header:    header,
		Path:      path,
		Log:       logBase,
		Truncated: truncated,
		Notice:    notice,
	}

	// Parse subsequent event rows
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := bytes.TrimRight(line, "\r")
		trimmed = bytes.TrimSpace(trimmed)
		if len(trimmed) == 0 {
			continue
		}

		// Compute RawHash: full lowercase SHA-256 of the exact event row excluding newline
		sum := sha256.Sum256(trimmed)
		rawHash := hex.EncodeToString(sum[:])

		var env struct {
			Type            string          `json:"type"`
			Seq             *int            `json:"seq"`
			Seq0            *int            `json:"seq0"`
			Time            any             `json:"time"`
			Time0           any             `json:"time0"`
			Data            json.RawMessage `json:"data"`
			SurfaceOp       json.RawMessage `json:"surfaceOp"`
			SourceEventSeqs []int           `json:"sourceEventSeqs"`
			Ignorable       *bool           `json:"ignorable"`
		}

		if err := json.Unmarshal(trimmed, &env); err != nil {
			// Torn tail at EOF check: if this is the last line or trailing fragment
			if i == len(lines)-1 || (i == len(lines)-2 && len(bytes.TrimSpace(lines[len(lines)-1])) == 0) {
				trace.Truncated = true
				if trace.Notice == "" {
					trace.Notice = fmt.Sprintf("torn tail at EOF: %v", err)
				}
				break
			}
			// Middle row error is non-recoverable
			return nil, fmt.Errorf("deepseek: malformed event row at line %d in %s: %w", i+1, path, err)
		}

		ev := DeepSeekEvent{
			Type:            env.Type,
			Data:            env.Data,
			SurfaceOp:       env.SurfaceOp,
			SourceEventSeqs: env.SourceEventSeqs,
			RawHash:         rawHash,
		}
		if env.Seq != nil {
			ev.Seq = *env.Seq
			ev.HasSeq = true
		} else if env.Seq0 != nil {
			ev.Seq = *env.Seq0
			ev.HasSeq = true
		}
		if env.Ignorable != nil && *env.Ignorable {
			ev.Ignorable = true
		}
		if env.Time != nil {
			ev.Time = parseTimeAny(env.Time)
		} else if env.Time0 != nil {
			ev.Time = parseTimeAny(env.Time0)
		}

		trace.Events = append(trace.Events, ev)
	}

	// If no torn tail occurred during line parsing or decompression, EOF is complete
	return trace, nil
}
