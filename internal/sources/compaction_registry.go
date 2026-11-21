package sources

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// compactionScanFiles bounds the fallback that looks for a session by its id in
// files whose path does not name it: the hook runs on the host's critical path.
// compactionProbeWindow and compactionProbeTotalBytes bound how much of each
// unnamed transcript (head+tail) and total fallback scan we read so an unknown
// session_id cannot stall a prompt hook reading hundreds of megabytes (#6133).
const (
	compactionScanFiles       = 64
	compactionProbeWindow     = 64 << 10
	compactionProbeTotalBytes = 8 << 20
)

// ReadCompactionSession captures a session through the reader that indexes it,
// for a host whose transcript has no bounded tail reader of its own. The host
// names the session by its id, and usually its harness or transcript file; the
// file comes from the registry — the one the host named when it is a file of
// that store, else the store's file that holds the id — and the session is the
// one the harness's own parser reads out of it under that id. Nothing is
// guessed: no session with that id, no capture.
//
// hint is the workspace the hook runs in. It stands in for the session's own
// only when the session records none and its project is the hint's.
func ReadCompactionSession(harness, nativeSessionID, transcriptPath, hint string) (CompactionTranscript, error) {
	if strings.TrimSpace(nativeSessionID) == "" {
		return CompactionTranscript{}, fmt.Errorf("%w: missing session id", ErrTranscriptIdentity)
	}
	var candidates []compactionCandidate
	if transcriptPath != "" {
		candidates = compactionCandidatesForPath(harness, transcriptPath)
	}
	if len(candidates) == 0 && harness != "" {
		candidates = compactionCandidatesByID(harness, nativeSessionID)
	}
	if len(candidates) == 0 {
		return CompactionTranscript{}, ErrUnsupportedCompactionTranscript
	}
	for _, c := range candidates {
		info, err := os.Stat(c.path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		sessions, _ := c.kind.Parse(c.path, 0)
		for _, s := range sessions {
			if s.ID != nativeSessionID || len(s.Messages) == 0 {
				continue
			}
			if s.Path == "" {
				s.Path = c.path
			}
			workspace := compactionSessionWorkspace(c.harness, c.path, s)
			if workspace == "" && hint != "" && s.Project != "" && s.Project == projectName(hint) {
				workspace = hint
			}
			return CompactionTranscript{
				Session: s, Harness: c.harness, NativeSessionID: nativeSessionID, Workspace: workspace,
				Path: c.path, Fingerprint: sessionFingerprint(s), SourceSize: info.Size(), SourceMTime: info.ModTime(),
				// The parser gives no byte offsets, so the compaction-to-edit
				// metric has nothing to count against.
				MetricComplete: false,
			}, nil
		}
	}
	return CompactionTranscript{}, fmt.Errorf("%w: no session %q in %s", ErrTranscriptIdentity, nativeSessionID, compactionCandidateNames(candidates))
}

type compactionCandidate struct {
	harness string
	path    string
	kind    FileKind
}

func compactionCandidateNames(cs []compactionCandidate) string {
	seen := map[string]bool{}
	var names []string
	for _, c := range cs {
		if !seen[c.harness] {
			seen[c.harness] = true
			names = append(names, c.harness)
		}
	}
	return strings.Join(names, ",")
}

// CompactionTranscriptHarness is the harness whose store holds path, or "" when
// no store deja reads claims it.
func CompactionTranscriptHarness(path string) string {
	for _, h := range Registry() {
		for _, k := range h.Kinds {
			if compactionKindMatches(k, path) != "" {
				return h.Name
			}
		}
	}
	return ""
}

// compactionKindMatches is the spelling of path the kind claims: the host may
// hand over a path through a symlink (/tmp on macOS) that the store root does
// not use, or the other way round.
func compactionKindMatches(k FileKind, path string) string {
	if k.Match == nil {
		return ""
	}
	for _, p := range compactionPathSpellings(path) {
		if k.Match(p) {
			return p
		}
	}
	return ""
}

// compactionPathSpellings is path as the host gave it, resolved, and resolved
// back under the home as deja spells it. A host hands over the path it
// resolved — /private/tmp/... on macOS for a home under /tmp — while the
// store roots keep the configured spelling, so neither of the first two
// matched a root and Grok's capture failed with transcript_unavailable.
func compactionPathSpellings(path string) []string {
	out := []string{path}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return out
	}
	if resolved != path {
		out = append(out, resolved)
	}
	home := Home()
	if realHome, err := filepath.EvalSymlinks(home); err == nil && realHome != home {
		if rel, err := filepath.Rel(realHome, resolved); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			out = append(out, filepath.Join(home, rel))
		}
	}
	return out
}

// compactionHarnessByLayout names the harness of a file its PreCompact names
// from the file's own layout, for the hosts whose payload says nothing else and
// whose store can sit under a root deja does not scan: VS Code Copilot Chat's
// GitHub.copilot-chat/transcripts/<id>.jsonl in any profile, and Copilot CLI's
// session-state/<id>/events.jsonl (Grok and Reasonix write an events.jsonl too,
// so the directory has to say Copilot).
func compactionHarnessByLayout(path string) string {
	switch {
	case copilotAgentTranscript(path):
		return "copilot-chat"
	case filepath.Base(path) == "events.jsonl" && filepath.Base(filepath.Dir(filepath.Dir(path))) == "session-state":
		return "copilot"
	}
	return ""
}

func compactionCandidatesForPath(harness, path string) []compactionCandidate {
	var out []compactionCandidate
	for _, h := range Registry() {
		if harness != "" && h.Name != harness {
			continue
		}
		for _, k := range h.Kinds {
			if p := compactionKindMatches(k, path); p != "" {
				out = append(out, compactionCandidate{h.Name, p, k})
			}
		}
	}
	if len(out) > 0 {
		return out
	}
	if harness == "" {
		harness = compactionHarnessByLayout(path)
	}
	if harness == "" {
		return nil
	}
	// A file outside the root the store is read from — a host told to keep its
	// sessions elsewhere — is still that harness's format.
	for _, h := range Registry() {
		if h.Name != harness {
			continue
		}
		for _, k := range h.Kinds {
			if k.Parse != nil {
				out = append(out, compactionCandidate{h.Name, path, k})
			}
		}
	}
	return out
}

// compactionCandidatesByID finds the files of harness that can hold the
// session: first those whose path names the id, which is how most stores file
// a session, then the most recently written few that hold the id at all.
func compactionCandidatesByID(harness, id string) []compactionCandidate {
	for _, h := range StoresToRead() {
		if h.Name != harness || h.Files == nil {
			continue
		}
		files := h.Files()
		kindOf := func(p string) (FileKind, bool) {
			for _, k := range h.Kinds {
				if k.Match != nil && k.Match(p) {
					return k, true
				}
			}
			return FileKind{}, false
		}
		var named []compactionCandidate
		for _, p := range files {
			if !strings.Contains(p, id) {
				continue
			}
			if k, ok := kindOf(p); ok {
				named = append(named, compactionCandidate{h.Name, p, k})
			}
		}
		if len(named) > 0 {
			return named
		}
		type dated struct {
			path string
			mod  int64
		}
		var recent []dated
		for _, p := range files {
			if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
				recent = append(recent, dated{p, info.ModTime().UnixNano()})
			}
		}
		sort.Slice(recent, func(i, j int) bool { return recent[i].mod > recent[j].mod })
		var out []compactionCandidate
		needle := []byte(id)
		var totalRead int64
		for i, f := range recent {
			if i >= compactionScanFiles {
				break
			}
			k, ok := kindOf(f.path)
			if !ok {
				continue
			}
			keep, stop := compactionCandidateProbeMatch(h.Name, f.path, id, needle, &totalRead)
			if stop {
				break
			}
			if !keep {
				continue
			}
			out = append(out, compactionCandidate{h.Name, f.path, k})
		}
		return out
	}
	return nil
}

func compactionCandidateProbeMatch(harness, path, id string, needle []byte, totalRead *int64) (keep, stop bool) {
	switch {
	case isCompactionCompressedTranscript(path):
		// Compressed rollouts name the session id in their filename; skip full
		// archive decompression when probing for an unnamed id.
		return false, false
	case isCompactionTextTranscript(path):
		if *totalRead >= compactionProbeTotalBytes {
			return false, true
		}
		found, n := fileContainsTokenBounded(path, needle)
		*totalRead += n
		return found, false
	case isCompactionDBPath(path):
		if checked, found := compactionDBContainsID(harness, path, id); checked {
			return found, false
		}
		return true, false
	default:
		return true, false
	}
}

func isCompactionTextTranscript(path string) bool {
	return strings.HasSuffix(path, ".jsonl") ||
		strings.HasSuffix(path, ".json") ||
		strings.HasSuffix(path, ".txt") ||
		strings.HasSuffix(path, ".md")
}

func isCompactionCompressedTranscript(path string) bool {
	return strings.HasSuffix(path, ".zst") || strings.HasSuffix(path, ".gz")
}

func isCompactionDBPath(path string) bool {
	return strings.HasSuffix(path, ".db") ||
		strings.HasSuffix(path, ".sqlite") ||
		strings.HasSuffix(path, ".sqlite3") ||
		strings.HasSuffix(path, ".vscdb")
}

func fileContainsTokenBounded(path string, needle []byte) (bool, int64) {
	f, err := os.Open(path)
	if err != nil {
		return false, 0
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return false, 0
	}
	sz := info.Size()
	if sz <= 2*compactionProbeWindow {
		buf := make([]byte, sz)
		n, readErr := io.ReadFull(f, buf)
		if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
			return false, int64(n)
		}
		return bytes.Contains(buf[:n], needle), int64(n)
	}
	buf := make([]byte, compactionProbeWindow)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false, int64(n)
	}
	if bytes.Contains(buf[:n], needle) {
		return true, int64(n)
	}
	m, err := f.ReadAt(buf, sz-compactionProbeWindow)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, int64(n + m)
	}
	return bytes.Contains(buf[:m], needle), int64(n + m)
}

func compactionDBContainsID(harness, path, id string) (checked, found bool) {
	esc := sqlEscape(id)
	var queries []string
	switch harness {
	case "goose", "hermes", "devin", "crush", "grok":
		queries = []string{`select 1 from sessions where id='` + esc + `' limit 1`}
	case "opencode", "kilocode", "mimo", "zcode":
		queries = []string{`select 1 from session where id='` + esc + `' limit 1`}
	case "teleagent":
		queries = []string{
			`select 1 from session where id='` + esc + `' limit 1`,
			`select 1 from session_v2 where id='` + esc + `' limit 1`,
		}
	case "omnigent":
		queries = []string{`select 1 from conversations where id='` + esc + `' limit 1`}
	case "orca":
		queries = []string{`select 1 from agent_session_records where session_id='` + esc + `' limit 1`}
	case "zed":
		queries = []string{`select 1 from threads where id='` + esc + `' limit 1`}
	case "kiro":
		queries = []string{`select 1 from conversations_v2 where conversation_id='` + esc + `' limit 1`}
	case "openclaw":
		queries = []string{`select 1 from transcript_events where session_id='` + esc + `' limit 1`}
	default:
		return false, false
	}
	for _, q := range queries {
		out, err := sqliteOutput(path, q)
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(out)) != "" {
			return true, true
		}
		checked = true
	}
	return checked, false
}

// compactionSessionWorkspace is the directory a session recorded running in,
// read where each store keeps it — the same places resume reads it from.
func compactionSessionWorkspace(harness, path string, s model.Session) string {
	switch harness {
	case "pi", "gjc", "kimchi", "senpi", "omp", "openclaw":
		return PiHeaderCwd(path)
	case "cherrystudio":
		if cwd := PiHeaderCwd(path); cwd != "" {
			return cwd
		}
		return ClaudeSessionDir(path)
	case "prime":
		return PrimeSessionDir(path)
	case "kimi":
		return KimiSessionDir(path)
	case "qwen":
		dir, _ := QwenSessionDir(path)
		return dir
	case "grok":
		if strings.HasSuffix(path, "updates.jsonl") {
			return GrokCWDForSession(path)
		}
	case "cursor":
		if cwd, _ := CursorChatCWD(path); cwd != "" {
			return cwd
		}
		if base := CursorTranscriptProjectDirBase(path); base != "" {
			return ResolveEncodedPath(base)
		}
	case "copilot":
		return copilotHeadCWD(path)
	case "copilot-chat":
		return CopilotChatWorkspaceDir(path)
	case "cline":
		return ClineSessionDir(path)
	case "trae", "codex":
		_, cwd, _, _ := codexRolloutHead(path)
		return cwd
	case "claude", "qoder":
		return ClaudeSessionDir(path)
	case "mcode":
		return McodeSessionWorkspace(s.ID)
	case "devin":
		return DevinSessionDir(path, s.ID)
	case "omnigent":
		return OmnigentSessionDir(path, s.ID)
	case "orca":
		return OrcaSessionDir(path, s.ID)
	case "ante":
		return AnteSessionDir(path)
	}
	// opencode-shaped stores keep the directory as the session's path.
	if s.Path != "" && s.Path != path {
		if info, err := os.Stat(s.Path); err == nil && info.IsDir() {
			return s.Path
		}
	}
	return ""
}
