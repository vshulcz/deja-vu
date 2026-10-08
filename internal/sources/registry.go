package sources

import (
	"bytes"
	"encoding/json"
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
	// Resumes reports whether the bytes from offset can be read on their own
	// and added to what is stored. nil means always. A kind whose new lines can
	// rewrite a record already stored says no, and the file is read whole.
	Resumes func(path string, offset int64) bool
	// Sidecar fingerprints the files beside a transcript that the reader takes
	// the session's title, workspace or clock from. The agent writes them
	// without touching the transcript, late or on a rename, so the fingerprint
	// is part of the file state and a change re-reads the session (#4319,
	// #4446). nil when the transcript holds it all.
	Sidecar func(path string) (size, stamp int64)
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

// dbParse/dbParseFrom handle the db-backed kinds — opencode, cursor, goose,
// grok, hermes and zed — which filter by time rather than byte offset.
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

// resumesUnlessAnswering is the Resumes of a format that files a tool call and
// its result as two lines joined by an id. A tail that answers a call made
// before it cannot be read on its own: the call is stored already, without the
// exit status or the refusal its result carries, and only a read that holds
// both marks it (#4443). hint is a substring every call and result line holds,
// so the rest of the tail is not decoded; answers gives the ids of the calls a
// line makes and the id of the call it answers, when the answer changes what
// the call recorded.
func resumesUnlessAnswering(hint string, answers func(m map[string]any) (calls []string, answered string)) func(string, int64) bool {
	return func(path string, offset int64) bool {
		if offset <= 0 {
			return true
		}
		made := map[string]bool{}
		ok := true
		_ = scanJSONLBytes(path, offset, func(line []byte) {
			if !ok || !bytes.Contains(line, []byte(hint)) {
				return
			}
			var m map[string]any
			d := json.NewDecoder(bytes.NewReader(line))
			d.UseNumber()
			if d.Decode(&m) != nil {
				return
			}
			calls, answered := answers(m)
			for _, id := range calls {
				made[id] = true
			}
			if answered != "" && !made[answered] {
				ok = false
			}
		})
		return ok
	}
}

func hasBase(p, base string) bool { return filepath.Base(p) == base }

// underRoot is the plain claim a single-root harness makes: this path is inside
// my store and has my extension.
func underRoot(p, root, ext string) bool {
	return root != "" && strings.HasPrefix(p, root) && strings.HasSuffix(p, ext)
}

// Registry returns the harnesses deja reads, in load order. Flattening the
// kinds preserves the original path-match precedence (matches are on disjoint
// roots/basenames, so order only needs to stay deterministic).
//
// DEJA_STORES narrows it; without that variable this is every harness deja
// knows, which is what it has always been.
func Registry() []Harness {
	all := allHarnesses()
	want, ok := storesSelection()
	if !ok {
		return all
	}
	out := make([]Harness, 0, len(want))
	for _, h := range all {
		if want[h.Name] {
			out = append(out, h)
		}
	}
	return out
}

// AllHarnesses is every harness deja knows how to read, whatever DEJA_STORES
// says. The set `--harness` accepts, and the number the documentation counts:
// silencing a store for one run does not make deja a tool that reads fewer
// harnesses.
func AllHarnesses() []Harness { return allHarnesses() }

func allHarnesses() []Harness {
	return []Harness{
		{
			Name: "claude", Load: LoadClaude, Files: ClaudeFiles,
			Kinds: []FileKind{{
				Name:      "claude",
				Match:     func(p string) bool { return strings.HasSuffix(p, ".jsonl") && UnderClaudeRoot(p) },
				Parse:     fullParse(ParseClaudeFile),
				ParseFrom: offsetParse(ParseClaudeFileFromOffset),
				Resumes:   claudeExitResumes,
			}},
		},
		{
			Name: "codex", Load: LoadCodex, Files: CodexFiles,
			Kinds: []FileKind{
				{
					Name:      "codex-history",
					Match:     func(p string) bool { return hasBase(p, "history.jsonl") && underCodexRoot(p, CodexRoot()) },
					Parse:     fullParse(ParseCodexHistory),
					ParseFrom: offsetParse(ParseCodexHistoryFromOffset),
				},
				{
					Name: "codex",
					Match: func(p string) bool {
						return codexRolloutWanted(p) && underAnyCodexRoot(p) && underAnyCodexSessionsRoot(p)
					},
					Parse:     fullParse(ParseCodexRollout),
					ParseFrom: offsetParse(ParseCodexRolloutFromOffset),
					Resumes:   codexResumes,
				},
			},
		},
		{
			// TRAE CLI 2.0, a codex-rs fork writing Codex rollouts under
			// ~/.trae/cli. Its own entry rather than another Codex root: the
			// sessions resume with traex, and its user-turn rule differs.
			Name: "trae", Load: LoadTrae, Files: TraeFiles,
			Kinds: []FileKind{
				{
					Name:      "trae-history",
					Match:     func(p string) bool { return hasBase(p, "history.jsonl") && underCodexRoot(p, TraeRoot()) },
					Parse:     fullParse(ParseTraeHistory),
					ParseFrom: offsetParse(ParseTraeHistoryFromOffset),
				},
				{
					Name:      "trae",
					Match:     func(p string) bool { return codexRolloutWanted(p) && underTraeSessions(p) },
					Parse:     fullParse(ParseTraeRollout),
					ParseFrom: offsetParse(ParseTraeRolloutFromOffset),
					Resumes:   traeResumes,
				},
			},
		},
		{
			Name: "opencode", Load: LoadOpencode,
			Files: func() []string {
				return append([]string{OpencodeDB()}, OpencodeDiffFiles()...)
			},
			Kinds: []FileKind{
				{
					Name:      "opencode",
					Match:     func(p string) bool { return p == OpencodeDB() },
					Parse:     dbParse(parseOpencodeStore, parseOpencodeStoreSince),
					ParseFrom: dbParseFrom(parseOpencodeStore, parseOpencodeStoreSince),
				},
				{
					// The per-session diff store beside the database: for most
					// sessions it is the only record of what they changed
					// (#3791). Keyed on the same session ids: a changed diff is
					// read as its session, whole, the way a full build holds it.
					Name: "opencode-diff",
					Match: func(p string) bool {
						return strings.HasPrefix(p, OpencodeDiffDir()+string(filepath.Separator)) &&
							strings.HasSuffix(p, ".json")
					},
					Parse: fullParse(ParseOpencodeDiffSession),
				},
			},
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
			Name: "amp", Load: LoadAmp, Files: AmpThreadFiles,
			Kinds: []FileKind{{
				Name: "amp",
				Match: func(p string) bool {
					return strings.HasSuffix(p, ".json") && strings.HasPrefix(p, AmpRoot()+string(filepath.Separator))
				},
				Parse: fullParse(ParseAmpFile),
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
			Name: "grok",
			Load: func() []model.Session { return append(LoadGrok(), LoadGrokDB()...) },
			Files: func() []string {
				files := GrokSessionFiles()
				if db := GrokDB(); fileExists(db) {
					files = append(files, db)
				}
				return files
			},
			Kinds: []FileKind{{
				Name: "grok",
				Match: func(p string) bool {
					return hasBase(p, "updates.jsonl") && strings.HasPrefix(p, filepath.Join(GrokRoot(), "sessions"))
				},
				Parse:     fullParse(ParseGrokFile),
				ParseFrom: offsetParse(ParseGrokFileFromOffset),
				Resumes:   GrokResumes,
				Sidecar:   besideSidecar("summary.json"),
			}, {
				// The maintained CLI writes no session files at all: one
				// SQLite store beside the config, like opencode's.
				Name:      "grok",
				Match:     func(p string) bool { return p == GrokDB() },
				Parse:     dbParse(func(p string) ([]model.Session, error) { return ParseGrokDBSince(p, time.Time{}) }, ParseGrokDBSince),
				ParseFrom: dbParseFrom(func(p string) ([]model.Session, error) { return ParseGrokDBSince(p, time.Time{}) }, ParseGrokDBSince),
			}},
		},
		{
			Name: "hermes", Load: LoadHermes, Files: HermesSessionFiles,
			Kinds: []FileKind{{
				Name: "hermes",
				// Current Hermes uses ~/.hermes/state.db; older builds shard
				// stores under ~/.hermes/profiles/<name>/state.db.
				Match: func(p string) bool {
					return p == filepath.Join(HermesHome(), "state.db") ||
						(filepath.Base(p) == "state.db" && strings.HasPrefix(p, HermesProfilesRoot()+string(filepath.Separator)))
				},
				Parse:     dbParse(ParseHermesDB, ParseHermesDBSince),
				ParseFrom: dbParseFrom(ParseHermesDB, ParseHermesDBSince),
			}, {
				// The Postgres store is a DSN, not a file; the index matches its
				// token and re-reads by timestamp watermark like the other
				// db-backed kinds.
				Name:      "hermes-pg",
				Match:     func(p string) bool { return IsHermesPGStore(p) },
				Parse:     func(_ string, nano int64) ([]model.Session, error) { return ParseHermesPG(HermesPGDSN(), nano) },
				ParseFrom: func(_ string, _, nano int64) ([]model.Session, error) { return ParseHermesPG(HermesPGDSN(), nano) },
			}},
		},
		{
			Name: "goose", Load: LoadGoose, Files: GooseSessionFiles,
			Kinds: []FileKind{{
				Name: "goose-jsonl",
				Match: func(p string) bool {
					if !strings.HasSuffix(p, ".jsonl") {
						return false
					}
					for _, dir := range GooseSessionsDirs() {
						if strings.HasPrefix(p, dir) {
							return true
						}
					}
					return false
				},
				Parse:     fullParse(ParseGooseFile),
				ParseFrom: offsetParse(ParseGooseFileFromOffset),
				Resumes:   gooseExitResumes,
			}, {
				Name: "goose-db",
				Match: func(p string) bool {
					for _, db := range GooseDBs() {
						if p == db {
							return true
						}
					}
					return false
				},
				Parse:     dbParse(ParseGooseDB, ParseGooseDBSince),
				ParseFrom: dbParseFrom(ParseGooseDB, ParseGooseDBSince),
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
				Resumes:   kimiTailResumes,
				Sidecar:   kimiSidecar,
			}},
		},
		{
			Name: "cline", Load: LoadCline, Files: ClineSessionFiles,
			Kinds: []FileKind{{
				Name: "cline-sdk",
				Match: func(p string) bool {
					return strings.HasSuffix(p, ".messages.json") && strings.HasPrefix(p, ClineSessionsDir())
				},
				Parse:   fullParse(ParseClineFile),
				Sidecar: clineSDKSidecar,
			}, {
				Name: "cline-vscode",
				Match: func(p string) bool {
					if !hasBase(p, "api_conversation_history.json") {
						return false
					}
					// Roo writes the same filename in the same layout. Without
					// the root check this kind claims every Roo task, and Roo
					// history is filed under cline.
					for _, root := range ClineLegacyRoots() {
						if strings.HasPrefix(p, root) {
							return true
						}
					}
					return false
				},
				Parse:   fullParse(ParseClineFile),
				Sidecar: clineVSCodeSidecar,
			}},
		},
		{
			// Cherry Studio runs Claude Code sessions from a desktop app and
			// writes them in Claude's own format, with a snapshot per stream
			// chunk that the reader collapses (#3644). Its pi and dsh agents
			// keep stock pi and dsh logs beside them (#4342); this entry sits
			// before deepseek's, whose kind matches a log by name alone.
			Name: "cherrystudio", Load: LoadCherryStudio, Files: CherryStudioSessionFiles,
			Kinds: []FileKind{
				{
					Name: "cherrystudio",
					Match: func(p string) bool {
						return strings.HasSuffix(p, ".jsonl") && CherryStudioUnderRoot(p)
					},
					Parse:     fullParse(ParseCherryStudioFile),
					ParseFrom: offsetParse(ParseCherryStudioFileFromOffset),
					Resumes:   CherryStudioResumes,
				},
				{
					Name:      "cherrystudio-pi",
					Match:     cherryStudioPiFile,
					Parse:     fullParse(ParseCherryStudioFile),
					ParseFrom: offsetParse(ParseCherryStudioPiFileFromOffset),
					Resumes:   piResumes,
				},
				{
					Name:  "cherrystudio-dsh",
					Match: cherryStudioDshFile,
					Parse: fullParse(ParseCherryStudioDshFile),
				},
			},
		},
		{
			// Senpi and Kimchi are pi descendants and kept its envelope, so
			// both entries are a root and a name (#3647).
			Name: "senpi", Load: LoadSenpi, Files: SenpiSessionFiles,
			Kinds: []FileKind{{
				Name:      "senpi",
				Match:     func(p string) bool { return underRoot(p, SenpiRoot(), ".jsonl") },
				Parse:     fullParse(ParseSenpiFile),
				ParseFrom: offsetParse(ParseSenpiFileFromOffset),
				Resumes:   piResumes,
			}},
		},
		{
			Name: "gjc", Load: LoadGjc, Files: GjcSessionFiles,
			Kinds: []FileKind{{
				Name:      "gjc",
				Match:     GjcUnderRoot,
				Parse:     fullParse(ParseGjcFile),
				ParseFrom: offsetParse(ParseGjcFileFromOffset),
				Resumes:   piResumes,
			}},
		},
		{
			Name: "kimchi", Load: LoadKimchi, Files: KimchiSessionFiles,
			Kinds: []FileKind{{
				Name:      "kimchi",
				Match:     KimchiUnderRoot,
				Parse:     fullParse(ParseKimchiFile),
				ParseFrom: offsetParse(ParseKimchiFileFromOffset),
				Resumes:   piResumes,
			}},
		},
		{
			// Command Code and ZCode both write a flat role/content transcript
			// under a Claude-shaped project directory (#3647).
			Name: "commandcode", Load: LoadCommandCode, Files: CommandCodeSessionFiles,
			Kinds: []FileKind{{
				Name:      "commandcode",
				Match:     CommandCodeUnderRoot,
				Parse:     fullParse(ParseCommandCodeFile),
				ParseFrom: offsetParse(ParseCommandCodeFileFromOffset),
				Resumes:   commandCodeExitResumes,
			}},
		},
		{
			Name: "zcode", Load: LoadZCode, Files: ZCodeSessionFiles,
			Kinds: []FileKind{{
				Name:      "zcode",
				Match:     ZCodeUnderRoot,
				Parse:     fullParse(ParseZCodeFile),
				ParseFrom: offsetParse(ParseZCodeFileFromOffset),
			}, {
				// The CLI's database, in OpenCode's schema — the same pair Kilo
				// has (#3675).
				Name:      "zcode-db",
				Match:     func(p string) bool { return p == ZCodeDB() },
				Parse:     dbParse(ParseZCodeDB, ParseZCodeDBSince),
				ParseFrom: dbParseFrom(ParseZCodeDB, ParseZCodeDBSince),
			}, {
				// The snapshots an older ZCode kept, one JSON file a
				// conversation, read whole (#4432).
				Name:    "zcode-legacy",
				Match:   ZCodeLegacyUnderRoot,
				Parse:   fullParse(ParseZCodeLegacyFile),
				Sidecar: zcodeLegacySidecar,
			}},
		},
		{
			// Kiro writes one format from its CLI and another from the IDE,
			// both under ~/.kiro/sessions (#3103), and a headless kiro-cli run
			// goes into its database instead (#4300).
			Name: "kiro", Load: LoadKiro, Files: KiroSessionFiles,
			Kinds: []FileKind{{
				Name:      "kiro-cli",
				Match:     KiroUnderCLI,
				Parse:     fullParse(ParseKiroCLIFile),
				ParseFrom: offsetParse(ParseKiroCLIFileFromOffset),
				Resumes:   KiroCLIResumes,
			}, {
				Name:      "kiro-ide",
				Match:     KiroUnderIDE,
				Parse:     fullParse(ParseKiroIDEFile),
				ParseFrom: offsetParse(ParseKiroIDEFileFromOffset),
			}, {
				// `kiro-cli chat --no-interactive` writes only here (#4300).
				Name:      "kiro-db",
				Match:     func(p string) bool { return p == KiroDB() },
				Parse:     dbParse(ParseKiroDB, ParseKiroDBSince),
				ParseFrom: dbParseFrom(ParseKiroDB, ParseKiroDBSince),
			}},
		},
		{
			// Kilo Code keeps the extension's task files and the CLI's
			// OpenCode-schema database; both parsers are already here, so this
			// entry is paths and a name (#3643).
			Name: "kilocode", Load: LoadKilo, Files: KiloSessionFiles,
			Kinds: []FileKind{{
				Name: "kilocode-task",
				Match: func(p string) bool {
					return hasBase(p, "api_conversation_history.json") && kiloUnderTasks(p)
				},
				Parse:   fullParse(ParseKiloTask),
				Sidecar: besideSidecar("history_item.json"),
			}, {
				Name:      "kilocode-db",
				Match:     func(p string) bool { return p == KiloDB() },
				Parse:     dbParse(ParseKiloDB, ParseKiloDBSince),
				ParseFrom: dbParseFrom(ParseKiloDB, ParseKiloDBSince),
			}},
		},
		{
			Name: "roo", Load: LoadRoo, Files: RooTaskFiles,
			Kinds: []FileKind{{
				Name: "roo",
				Match: func(p string) bool {
					if !hasBase(p, "api_conversation_history.json") {
						return false
					}
					for _, root := range RooRoots() {
						if strings.HasPrefix(p, root) {
							return true
						}
					}
					return false
				},
				Parse:   fullParse(ParseRooTask),
				Sidecar: besideSidecar("history_item.json"),
			}},
		},
		{
			Name: "crush", Load: LoadCrush, Files: CrushDBs,
			Kinds: []FileKind{{
				Name: "crush",
				Match: func(p string) bool {
					if !hasBase(p, "crush.db") {
						return false
					}
					for _, db := range CrushDBs() {
						if p == db {
							return true
						}
					}
					return false
				},
				Parse:     dbParse(ParseCrushDB, ParseCrushDBSince),
				ParseFrom: dbParseFrom(ParseCrushDB, ParseCrushDBSince),
			}},
		},
		{
			Name: "continue", Load: LoadContinue, Files: ContinueSessionFiles,
			Kinds: []FileKind{{
				Name: "continue",
				Match: func(p string) bool {
					return continueSessionFile(p) && strings.HasPrefix(p, filepath.Join(ContinueRoot(), "sessions"))
				},
				Parse: fullParse(ParseContinueFile),
			}},
		},
		{
			Name: "pi", Load: LoadPi, Files: PiSessionFiles,
			Kinds: []FileKind{{
				Name:      "pi",
				Match:     func(p string) bool { return strings.HasSuffix(p, ".jsonl") && strings.HasPrefix(p, PiRoot()) },
				Parse:     fullParse(ParsePiFile),
				ParseFrom: offsetParse(ParsePiFileFromOffset),
				Resumes:   piResumes,
			}},
		},
		{
			Name: "prime", Load: LoadPrime, Files: PrimeSessionFiles,
			Kinds: []FileKind{{
				Name:      "prime",
				Match:     isPrimeFile,
				Parse:     fullParse(ParsePrimeFile),
				ParseFrom: offsetParse(ParsePrimeFileFromOffset),
				Resumes:   piResumes,
			}},
		},
		{
			Name: "omp", Load: LoadOmp, Files: OmpSessionFiles,
			Kinds: []FileKind{{
				Name:      "omp",
				Match:     func(p string) bool { return strings.HasSuffix(p, ".jsonl") && underOmpRoot(p) },
				Parse:     fullParse(ParseOmpFile),
				ParseFrom: offsetParse(ParseOmpFileFromOffset),
				Resumes:   piResumes,
			}},
		},
		{
			Name: "openclaw", Load: LoadOpenClaw, Files: OpenClawStoreFiles,
			Kinds: []FileKind{{
				Name:      "openclaw",
				Match:     func(p string) bool { return openclawTranscript(OpenClawRoot(), p) },
				Parse:     fullParse(ParseOpenClawFile),
				ParseFrom: offsetParse(ParseOpenClawFileFromOffset),
				Resumes:   piResumes,
			}, {
				// The per-agent SQLite store the 2026.8 flip made canonical;
				// the JSONL kind above is what older installs and archives hold.
				Name: "openclaw-db",
				Match: func(p string) bool {
					return hasBase(p, "openclaw-agent.sqlite") && strings.HasPrefix(p, OpenClawRoot()+string(filepath.Separator))
				},
				Parse:     dbParse(ParseOpenClawDB, ParseOpenClawDBSince),
				ParseFrom: dbParseFrom(ParseOpenClawDB, ParseOpenClawDBSince),
			}},
		},
		{
			Name: "copilot", Load: LoadCopilot, Files: CopilotSessionFiles,
			Kinds: []FileKind{{
				Name:      "copilot",
				Match:     func(p string) bool { return hasBase(p, "events.jsonl") && strings.HasPrefix(p, CopilotRoot()) },
				Parse:     fullParse(ParseCopilotFile),
				ParseFrom: offsetParse(ParseCopilotFileFromOffset),
				Resumes:   copilotResumes,
			}},
		},
		{
			Name: "copilot-chat", Load: LoadCopilotChat, Files: CopilotChatSessionFiles,
			Kinds: []FileKind{{
				Name:  "copilot-chat",
				Match: copilotChatMatch,
				Parse: fullParse(ParseCopilotChatFile),
			}},
		},
		{
			// CodeWhale, the Rust TUI that shipped as deepseek-tui until
			// v0.8.41. A different store from the DeepSeek Harness below: one
			// pretty-printed JSON file per session, no zstd.
			Name: "codewhale", Load: LoadCodeWhale, Files: CodeWhaleSessionFiles,
			Kinds: []FileKind{{
				Name:  "codewhale",
				Match: isCodeWhaleSession,
				Parse: fullParse(ParseCodeWhaleFile),
			}},
		},
		{
			// CodeBuddy Code and WorkBuddy: Claude Code's project tree, OpenAI
			// Responses-style items inside. Read whole: a title record can
			// arrive after the turns it names.
			Name: "codebuddy", Load: LoadCodeBuddy, Files: CodeBuddySessionFiles,
			Kinds: []FileKind{{
				Name: "codebuddy",
				Match: func(p string) bool {
					return isCodeBuddySession(p) || CodeBuddySubagentFile(p)
				},
				Parse: fullParse(ParseCodeBuddyFile),
			}},
		},
		{
			// Reasonix writes flat role/content lines, but its clock and
			// workspace sit in sidecars beside the transcript, and a compaction
			// rewrites the file, so it is read whole rather than from an offset.
			// 1.x's events.frames is zstd-framed and replayed whole too: an
			// upsert or a history replace rewrites what came before.
			Name: "reasonix", Load: LoadReasonix, Files: ReasonixSessionFiles,
			Kinds: []FileKind{{
				Name:    "reasonix",
				Match:   IsReasonixSession,
				Parse:   fullParse(ParseReasonixFile),
				Sidecar: reasonixSidecar,
			}},
		},
		{
			// Muse Code: one event-sourced log per session directory. The title
			// can arrive late (session.name.changed), so the log is read whole.
			Name: "muse", Load: LoadMuse, Files: MuseSessionFiles,
			Kinds: []FileKind{{
				Name:  "muse",
				Match: isMuseSession,
				Parse: fullParse(ParseMuseFile),
			}},
		},
		{
			// DeepSeek Harness writes one log per session, zstd-framed by
			// default, so a machine without the zstd CLI sees the files and
			// reads nothing out of them (SkipReason says so).
			Name: "deepseek", Load: LoadDeepSeek, Files: DeepSeekSessionFiles,
			Kinds: []FileKind{{
				Name:  "deepseek",
				Match: isDeepSeekLog,
				Parse: fullParse(ParseDeepSeekFile),
			}},
		},
		{
			// Zed's agent writes no session files: one SQLite store under its
			// data dir, whose thread bodies are zstd frames rather than JSON.
			Name: "zed", Load: LoadZed, Files: func() []string { return []string{ZedDB()} },
			Kinds: []FileKind{{
				Name:      "zed",
				Match:     func(p string) bool { return p == ZedDB() },
				Parse:     dbParse(ParseZedDB, ParseZedDBSince),
				ParseFrom: dbParseFrom(ParseZedDB, ParseZedDBSince),
			}},
		},
		{
			// Junie, the CLI and the agent AI Assistant runs: a directory per
			// session with an events.jsonl the CLI appends to. A block is
			// updated in place by later lines, so it is read whole.
			Name: "junie", Load: LoadJunie, Files: JunieSessionFiles,
			Kinds: []FileKind{{
				Name:    "junie",
				Match:   isJunieSession,
				Parse:   fullParse(ParseJunieFile),
				Sidecar: besideSidecar("state.json"),
			}},
		},
		{
			// JetBrains AI Assistant: chats in each project's workspace file
			// under the IDE's config directory, an agent chat's work in
			// aia-task-history beside it.
			Name: "jetbrains", Load: LoadJetBrains, Files: JetBrainsSessionFiles,
			Kinds: []FileKind{{
				Name:    "jetbrains",
				Match:   isJetBrainsWorkspace,
				Parse:   fullParse(ParseJetBrainsFile),
				Sidecar: jetBrainsSidecar,
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

// HarnessNames lists every coarse harness name deja knows, in registry order.
// It is the set `--harness` accepts — independent of what is installed, so a
// known-but-empty harness stays valid and only a typo is rejected.
func HarnessNames() []string {
	reg := allHarnesses()
	out := make([]string, 0, len(reg))
	for _, h := range reg {
		out = append(out, h.Name)
	}
	return out
}

// IsKnownHarness reports whether name is a harness deja can read.
func IsKnownHarness(name string) bool {
	for _, h := range allHarnesses() {
		if h.Name == name {
			return true
		}
	}
	return false
}

// HarnessForKind names the store a fine-grained kind belongs to: "cline-sdk"
// and "cline-vscode" are both cline. A caller holding a kind and speaking to a
// person wants this one — the index run narrates per store, and looking a kind
// up under the harness's own name found nothing (#2229).
func HarnessForKind(kind string) string {
	for _, h := range allHarnesses() {
		if h.Name == kind {
			return h.Name
		}
		for _, k := range h.Kinds {
			if k.Name == kind {
				return h.Name
			}
		}
	}
	return ""
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

// KindsWithOffsetParsers names every kind that can resume a parse where the
// last pass stopped. The index gates its append path on this rather than on a
// list of harness names it has to remember to grow (#2870).
func KindsWithOffsetParsers() []string {
	var out []string
	for _, h := range Registry() {
		for _, k := range h.Kinds {
			if k.ParseFrom != nil {
				out = append(out, k.Name)
			}
		}
	}
	return out
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
