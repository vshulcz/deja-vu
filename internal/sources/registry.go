package sources

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Harness is one AI tool deja ingests. Everything the index needs to discover,
// load, match and parse a tool's sessions lives in a single registry entry, so
// adding a harness is one entry here instead of edits scattered across the index
// dispatch (load, path-match, full-parse, incremental-parse). Signatures use
// primitives only (no index types) to keep sources a dependency-free leaf.
type Harness struct {
	Name  string                 // coarse name: claude, codex, cursor, ...
	Load  func() []model.Session // full cold load of every session
	Files func() []string        // current on-disk files to consider for indexing
	Kinds []FileKind             // one or more on-disk file shapes to match+parse
}

// FileKind is one on-disk file shape belonging to a harness. A harness can have
// several (codex has rollout logs and a history file; cursor has an IDE sqlite
// db and CLI transcripts), each matched and parsed differently.
type FileKind struct {
	// Name is the fine-grained kind reported for a path (e.g. "codex-history").
	Name  string
	Match func(path string) bool
	// Parse does a full parse. sinceNano>0 asks db-backed kinds to return only
	// sessions newer than that instant; file kinds ignore it.
	Parse func(path string, sinceNano int64) ([]model.Session, error)
	// ParseFrom resumes an incremental parse: offset for append-only text logs,
	// sinceNano for db-backed kinds. nil means the kind is not incremental.
	ParseFrom func(path string, offset, sinceNano int64) ([]model.Session, error)
}

func sinceTime(nano int64) time.Time { return time.Unix(0, nano) }

// fullParse/offsetParse adapt parsers that take no time cursor to the FileKind
// signatures without a wrapper at every call site.
func fullParse(f func(string) ([]model.Session, error)) func(string, int64) ([]model.Session, error) {
	return func(p string, _ int64) ([]model.Session, error) { return f(p) }
}

func offsetParse(f func(string, int64) ([]model.Session, error)) func(string, int64, int64) ([]model.Session, error) {
	return func(p string, off, _ int64) ([]model.Session, error) { return f(p, off) }
}

// dbParse/dbParseFrom handle the two db-backed kinds (opencode, cursor) that
// filter by time rather than byte offset.
func dbParse(full func(string) ([]model.Session, error), since func(string, time.Time) ([]model.Session, error)) func(string, int64) ([]model.Session, error) {
	return func(p string, nano int64) ([]model.Session, error) {
		if nano > 0 {
			return since(p, sinceTime(nano))
		}
		return full(p)
	}
}

func dbParseFrom(full func(string) ([]model.Session, error), since func(string, time.Time) ([]model.Session, error)) func(string, int64, int64) ([]model.Session, error) {
	return func(p string, _ int64, nano int64) ([]model.Session, error) {
		if nano > 0 {
			return since(p, sinceTime(nano))
		}
		return full(p)
	}
}

func hasBase(p, base string) bool { return filepath.Base(p) == base }

// Registry returns every harness in load order. Flattening the kinds preserves
// the original path-match precedence (matches are on disjoint roots/basenames,
// so order only needs to stay deterministic).
func Registry() []Harness {
	return []Harness{
		{
			Name: "claude", Load: LoadClaude, Files: ClaudeFiles,
			Kinds: []FileKind{{
				Name:      "claude",
				Match:     func(p string) bool { return strings.HasSuffix(p, ".jsonl") && strings.HasPrefix(p, ClaudeRoot()) },
				Parse:     fullParse(ParseClaudeFile),
				ParseFrom: offsetParse(ParseClaudeFileFromOffset),
			}},
		},
		{
			Name: "codex", Load: LoadCodex, Files: CodexFiles,
			Kinds: []FileKind{
				{
					Name:      "codex-history",
					Match:     func(p string) bool { return hasBase(p, "history.jsonl") && strings.HasPrefix(p, CodexRoot()) },
					Parse:     fullParse(ParseCodexHistory),
					ParseFrom: offsetParse(ParseCodexHistoryFromOffset),
				},
				{
					Name: "codex",
					Match: func(p string) bool {
						return strings.HasSuffix(p, ".jsonl") && strings.Contains(filepath.Base(p), "rollout-") && strings.HasPrefix(p, filepath.Join(CodexRoot(), "sessions"))
					},
					Parse:     fullParse(ParseCodexRollout),
					ParseFrom: offsetParse(ParseCodexRolloutFromOffset),
				},
			},
		},
		{
			Name: "opencode", Load: LoadOpencode, Files: func() []string { return []string{OpencodeDB()} },
			Kinds: []FileKind{{
				Name:      "opencode",
				Match:     func(p string) bool { return p == OpencodeDB() },
				Parse:     dbParse(ParseOpencodeDB, ParseOpencodeDBSince),
				ParseFrom: dbParseFrom(ParseOpencodeDB, ParseOpencodeDBSince),
			}},
		},
		{
			Name: "aider", Load: LoadAider, Files: AiderFiles,
			Kinds: []FileKind{{
				Name:  "aider",
				Match: func(p string) bool { return hasBase(p, ".aider.chat.history.md") },
				Parse: fullParse(ParseAiderFile),
			}},
		},
		{
			Name: "gemini", Load: LoadGemini, Files: GeminiChatFiles,
			Kinds: []FileKind{{
				Name: "gemini",
				Match: func(p string) bool {
					return strings.HasPrefix(p, filepath.Join(GeminiRoot(), "tmp")) && (strings.HasSuffix(p, ".json") || strings.HasSuffix(p, ".jsonl"))
				},
				Parse: fullParse(ParseGeminiFile),
			}},
		},
		{
			Name: "cursor", Load: LoadCursor,
			Files: func() []string { return append(append([]string{}, CursorDBs()...), CursorTranscripts()...) },
			Kinds: []FileKind{
				{
					Name:      "cursor-db",
					Match:     func(p string) bool { return hasBase(p, "state.vscdb") && strings.HasPrefix(p, CursorUserRoot()) },
					Parse:     dbParse(ParseCursorDB, ParseCursorDBSince),
					ParseFrom: dbParseFrom(ParseCursorDB, ParseCursorDBSince),
				},
				{
					Name: "cursor",
					Match: func(p string) bool {
						return strings.HasSuffix(p, ".jsonl") && strings.HasPrefix(p, filepath.Join(CursorCLIRoot(), "projects"))
					},
					Parse: fullParse(ParseCursorTranscript),
				},
			},
		},
		{
			Name: "antigravity", Load: LoadAntigravity, Files: AntigravityTranscripts,
			Kinds: []FileKind{{
				Name: "antigravity",
				Match: func(p string) bool {
					if !hasBase(p, "transcript.jsonl") {
						return false
					}
					for _, root := range AntigravityRoots() {
						if strings.HasPrefix(p, root+string(filepath.Separator)) {
							return true
						}
					}
					return false
				},
				Parse: fullParse(ParseAntigravityFile),
			}},
		},
		{
			Name: "grok", Load: LoadGrok, Files: GrokSessionFiles,
			Kinds: []FileKind{{
				Name: "grok",
				Match: func(p string) bool {
					return hasBase(p, "updates.jsonl") && strings.HasPrefix(p, filepath.Join(GrokRoot(), "sessions"))
				},
				Parse: fullParse(ParseGrokFile),
			}},
		},
		{
			Name: "qwen", Load: LoadQwen, Files: QwenSessionFiles,
			Kinds: []FileKind{{
				Name: "qwen",
				Match: func(p string) bool {
					return strings.HasSuffix(p, ".jsonl") && strings.HasPrefix(p, filepath.Join(QwenRoot(), "projects"))
				},
				Parse:     fullParse(ParseQwenFile),
				ParseFrom: offsetParse(ParseQwenFileFromOffset),
			}},
		},
		{
			Name: "kimi", Load: LoadKimi, Files: KimiSessionFiles,
			Kinds: []FileKind{{
				Name: "kimi",
				Match: func(p string) bool {
					return hasBase(p, "wire.jsonl") && strings.HasPrefix(p, filepath.Join(KimiRoot(), "sessions"))
				},
				Parse:     fullParse(ParseKimiFile),
				ParseFrom: offsetParse(ParseKimiFileFromOffset),
			}},
		},
		{
			Name: "cline", Load: LoadCline, Files: ClineSessionFiles,
			Kinds: []FileKind{{
				Name: "cline-sdk",
				Match: func(p string) bool {
					return strings.HasSuffix(p, ".messages.json") && strings.HasPrefix(p, ClineSessionsDir())
				},
				Parse: fullParse(ParseClineFile),
			}, {
				Name: "cline-vscode",
				Match: func(p string) bool {
					return hasBase(p, "api_conversation_history.json")
				},
				Parse: fullParse(ParseClineFile),
			}},
		},
		{
			Name: "pi", Load: LoadPi, Files: PiSessionFiles,
			Kinds: []FileKind{{
				Name:      "pi",
				Match:     func(p string) bool { return strings.HasSuffix(p, ".jsonl") && strings.HasPrefix(p, PiRoot()) },
				Parse:     fullParse(ParsePiFile),
				ParseFrom: offsetParse(ParsePiFileFromOffset),
			}},
		},
		{
			Name: "copilot", Load: LoadCopilot, Files: CopilotSessionFiles,
			Kinds: []FileKind{{
				Name:      "copilot",
				Match:     func(p string) bool { return hasBase(p, "events.jsonl") && strings.HasPrefix(p, CopilotRoot()) },
				Parse:     fullParse(ParseCopilotFile),
				ParseFrom: offsetParse(ParseCopilotFileFromOffset),
			}},
		},
		{
			Name: "deja", Load: LoadNotes, Files: func() []string { return []string{NotesFile()} },
			Kinds: []FileKind{{
				Name:      "deja",
				Match:     func(p string) bool { return p == NotesFile() },
				Parse:     fullParse(ParseNotesFile),
				ParseFrom: offsetParse(ParseNotesFileFromOffset),
			}},
		},
	}
}

// KindForPath returns the fine-grained kind whose Match accepts p, or "".
func KindForPath(p string) string {
	for _, h := range Registry() {
		for _, k := range h.Kinds {
			if k.Match(p) {
				return k.Name
			}
		}
	}
	return ""
}

// KindForPathKind returns the full FileKind whose Match accepts p, for
// callers that need to parse, not just classify.
func KindForPathKind(p string) (FileKind, bool) {
	for _, h := range Registry() {
		for _, k := range h.Kinds {
			if k.Match(p) {
				return k, true
			}
		}
	}
	return FileKind{}, false
}
