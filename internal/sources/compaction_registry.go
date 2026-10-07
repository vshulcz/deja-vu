package sources

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// compactionScanFiles bounds the fallback that looks for a session by its id in
// files whose path does not name it: the hook runs on the host's critical path.
const compactionScanFiles = 64

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
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return out
	}
	if real != path {
		out = append(out, real)
	}
	home := Home()
	if realHome, err := filepath.EvalSymlinks(home); err == nil && realHome != home {
		if rel, err := filepath.Rel(realHome, real); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
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
	for _, h := range Registry() {
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
		for i, f := range recent {
			if i >= compactionScanFiles {
				break
			}
			k, ok := kindOf(f.path)
			if !ok {
				continue
			}
			// A database holds its ids where a byte search may not find them
			// as written; it is read and asked instead.
			if strings.HasSuffix(f.path, ".jsonl") || strings.HasSuffix(f.path, ".json") {
				if b, err := os.ReadFile(f.path); err != nil || !bytes.Contains(b, []byte(id)) {
					continue
				}
			}
			out = append(out, compactionCandidate{h.Name, f.path, k})
		}
		return out
	}
	return nil
}

// compactionSessionWorkspace is the directory a session recorded running in,
// read where each store keeps it — the same places resume reads it from.
func compactionSessionWorkspace(harness, path string, s model.Session) string {
	switch harness {
	case "pi", "gjc", "kimchi", "senpi", "omp", "openclaw", "cherrystudio":
		return PiHeaderCwd(path)
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
		_, cwd, _ := codexRolloutHead(path)
		return cwd
	case "claude":
		return ClaudeSessionDir(path)
	}
	// opencode-shaped stores keep the directory as the session's path.
	if s.Path != "" && s.Path != path {
		if info, err := os.Stat(s.Path); err == nil && info.IsDir() {
			return s.Path
		}
	}
	return ""
}
