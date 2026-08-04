package index

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// version 12 resharded non-ASCII tokens across buckets; 13 interned the key
// and source path of every record; 14 deflates the record payload; 15 drops
// the derivable offset from every bucket directory entry; 16 folds Traditional
// CJK to Simplified before keying, so an index written before it holds keys
// this build never queries; 17 filters out deja's own injected recall, which
// earlier builds indexed back in, so an existing index is rebuilt once to drop
// the copies it already holds.
// 18: file paths from tool calls are indexed (#558). An index built before this
// has none of them, so it is rebuilt rather than reused.
// 20: opencode tool output is parsed (#621) and each session records what it
// tripped over (#576). An index built before this has neither, and neither can
// be derived from what it does hold.
// 21: Codex sessions are keyed on the ThreadId rather than the SessionId
// (#635), so every existing Codex entry has the wrong identity, and the
// harness preamble Codex injects as a user turn is stripped (#636).
// 22: titles are derived by the rules from #671, #693 and #770. Those three
// shipped without a bump, so indexes built under the old rules kept the titles
// they derived then — incremental ingest reuses SessionMeta for a file it has
// already read, and no ordinary `deja index` re-derives it (#784).
const version = 22
const maxIndexedText = 64 * 1024

// maxRecordSize bounds a single serialized record. A record is one message
// (text capped at maxIndexedText) plus small metadata, so anything larger is
// a corrupt length prefix — reject it rather than allocate up to 4 GiB.
const maxRecordSize = 8 << 20

var bucketMagic = []byte("DJB1")

// errCorruptIndex marks unreadable index structures (e.g. a bucket file cut
// short by a crash). Callers treat it as a cache miss and rebuild.
var errCorruptIndex = errors.New("corrupt index")

// IsCorrupt reports whether err means the on-disk index is damaged and a
// rebuild will heal it.
func IsCorrupt(err error) bool { return errors.Is(err, errCorruptIndex) }

var lastIngestFiles int

// HarnessCount is one harness's share of a build.
type HarnessCount struct {
	Name     string
	Sessions int
	Messages int
}

// BuildSummary describes the most recent (re)build in this process; the CLI
// uses it to greet a first-ever index with a summary instead of silence.
type BuildSummary struct {
	Initial    bool
	Sessions   int
	Messages   int
	Harnesses  int
	PerHarness []HarnessCount
}

var LastBuild BuildSummary

// SuppressHarnessNarration silences the per-harness progress lines for one
// build; the CLI sets it when it is about to greet a first index with the
// same numbers in the summary block.
var SuppressHarnessNarration bool

type FileState struct {
	Path          string `json:"path"`
	Size          int64  `json:"size"`
	MTime         int64  `json:"mtime"`
	MetadataSize  int64  `json:"metadata_size,omitempty"`
	MetadataMTime int64  `json:"metadata_mtime,omitempty"`
	CWDSize       int64  `json:"cwd_size,omitempty"`
	CWDMTime      int64  `json:"cwd_mtime,omitempty"`
	LastUpdated   int64  `json:"last_updated,omitempty"`
	Redactions    int    `json:"redactions,omitempty"`
	// SafeSize is the offset just past the last complete line at index time.
	// A session file caught mid-write ends in a torn line; parsing skips it,
	// and the next append must resume from here or that message is lost.
	SafeSize int64 `json:"safe_size,omitempty"`
	// PrefixHash fingerprints the bytes up to SafeSize. Growth alone does not
	// prove the earlier bytes are untouched: agents rewind a session by
	// truncating and rewriting it, and once it grows past its old length that
	// looks exactly like an append. Without this the rewritten prefix keeps
	// its old text in the index and the new text is never read.
	PrefixHash uint64 `json:"prefix_hash,omitempty"`
}

type SessionMeta struct {
	ID, Harness, Project, Path, Title string
	Started, Updated                  time.Time
	Ord                               uint32
	// Asked holds hashes of the substantial questions this session opened with.
	// Hashes rather than text: the text of six questions per session would add
	// a third of a megabyte to a manifest that is read on every search, and the
	// only thing a caller needs from it is whether two sessions asked the same
	// thing. The text is recovered from the two or three sessions that matched,
	// which is a bounded cost paid once, on a screen a person is reading.
	Asked []uint64 `json:",omitempty"`
	// Touched holds the few files this session worked on most, so a caller can
	// ask about them without loading the session. Reading one back cost 130-220
	// ms, which is not a price worth paying for a footnote under a search hit.
	// The field is additive: a manifest written before it existed decodes with
	// it empty and the caller degrades to saying nothing.
	Touched []string `json:",omitempty"`
	// Shared marks a row that covers more than one conversation: two
	// transcripts wrote the same harness:id, so one row holds both. The build
	// says so once (#698); forget had no way to know, and dropping "1 session"
	// took two conversations from two projects (#970). Additive: a manifest
	// written before this decodes with it false.
	Shared bool `json:",omitempty"`
	// OrigID is the id a session had on the machine it came from. Import
	// renames every session to imported-<hash>, so a promoted note stopped
	// looking like one the moment it crossed a machine boundary and every rule
	// written for notes — newest correction first, the decision's state —
	// silently stopped applying (#975). Additive: older manifests decode with
	// it empty.
	OrigID string `json:",omitempty"`
	// Lifecycle is the state of a promoted note that arrived by sync. The
	// local states live in notes.jsonl, which the other machine does not have,
	// so a decision retracted there read as accepted here (#975).
	Lifecycle     string `json:",omitempty"`
	LifecycleNote string `json:",omitempty"`
	LifecycleAt   string `json:",omitempty"`
	// Hit holds hashes of the specific errors this session tripped over, so
	// the first screen can name a wall the machine keeps running into without
	// reading a single session. Same trade as Asked: the text is recovered
	// from the two or three sessions that matched.
	Hit []uint64 `json:",omitempty"`
}

// touchedFileCap bounds what goes in SessionMeta.Touched. Enough to say
// something useful about a session, small enough that a store of a thousand
// sessions pays kilobytes for it.
const touchedFileCap = 6

// askedQuestionCap bounds the hashes stored per session.
const askedQuestionCap = 8

// askedMaxRunes is how long a thing someone asked can be. Beyond this it is a
// report, a paste, or an instruction with a question buried in it.
const askedMaxRunes = 240

type Manifest struct {
	Version          int                    `json:"version"`
	Files            map[string]FileState   `json:"files"`
	Sessions         map[string]SessionMeta `json:"sessions"`
	BuiltAt          time.Time              `json:"built_at"`
	Generation       string                 `json:"generation,omitempty"`
	Scope            string                 `json:"scope"`
	Redacted         int                    `json:"redacted"`
	RedactionRules   map[string]int         `json:"redaction_rules,omitempty"`
	ExportWatermarks map[string]int64       `json:"export_watermarks,omitempty"`
	// ExportBoundary remembers which records were already sent at exactly
	// the watermark instant, so resuming is precise even when a harness
	// stamps a whole session with one timestamp.
	ExportBoundary  map[string][]uint64 `json:"export_boundary,omitempty"`
	ImportedRecords map[string]bool     `json:"imported_records,omitempty"`
	// RecordStrings interns the keys and source paths of records.bin; a
	// record stores an index into this table instead of repeating the
	// strings. Append-only, so an appended log keeps resolving.
	RecordStrings []string `json:"record_strings,omitempty"`
	// RecordsSize is records.bin's byte length when the manifest was committed.
	// A live index whose records.bin is shorter than this lost its tail to a
	// torn write and must be treated as corrupt.
	RecordsSize int64 `json:"records_size,omitempty"`
	// IngestHealth records, per harness, what ingestion skipped on the pass
	// that last touched it: malformed JSONL lines and files that failed to
	// parse. Silent loss must be diagnosable (`deja doctor --json`).
	IngestHealth map[string]HarnessIngest `json:"ingest_health,omitempty"`
}

// HarnessIngest is one harness's ingestion health from its last indexing pass.
type HarnessIngest struct {
	MalformedLines int    `json:"malformed_lines,omitempty"`
	FailedFiles    int    `json:"failed_files,omitempty"`
	LastError      string `json:"last_error,omitempty"`
}

type manifestCore struct {
	Version          int
	Files            map[string]FileState
	BuiltAt          time.Time
	Generation       string
	Scope            string
	Redacted         int
	RedactionRules   map[string]int
	ExportWatermarks map[string]int64
	ExportBoundary   map[string][]uint64
	ImportedRecords  map[string]bool
	RecordStrings    []string
	RecordsSize      int64
	IngestHealth     map[string]HarnessIngest
}

type RedactionStats struct {
	Total int
	Files map[string]int
	Rules map[string]map[string]int
}

type Record struct {
	Key        string
	SourcePath string
	Role       string
	Text       string
	Time       time.Time
	LowerText  string `json:"-"`
}

type OffsetRecord struct {
	Offset int64
	Record Record
}

type posting struct {
	Off int64
	Sid uint32
	// Tool marks a posting that came from a tool record — a command, a path, a
	// tool result — rather than from something a person or the agent said. The
	// bit rides in the posting because the per-session cap has to choose which
	// postings to keep *before* any record is read, which is the whole point of
	// the cap.
	Tool bool
}

type SearchResult struct {
	Sessions []model.Session
	Fuzzy    bool
	Stemmed  bool
	Variants map[string][]string
	Tier     string
	// Total is how many sessions the tier matched before its own window
	// trimmed them, and Capped whether that trimming withheld any. The
	// relevance tier is the one that needs them: it ranks and truncates
	// inside retrieval, so a caller counting Sessions is measuring the
	// window rather than the match. Zero Total means the tier reports no
	// figure of its own and the caller's count stands.
	Total  int
	Capped bool
}

func DefaultDir() string {
	if v := os.Getenv("DEJA_INDEX_DIR"); v != "" {
		return v
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".cache", "deja", "index.db")
}

const syncImportPath = "deja-sync-import"

type importedState struct {
	sessions   []model.Session
	watermarks map[string]int64
	boundary   map[string][]uint64
	dedupe     map[string]bool
}

type tokenJob struct {
	text   string
	offset int64
	sid    uint32
	when   time.Time
	tool   bool
}

type bucketPostings map[string]map[string][]posting

// msgSeen dedupes identical messages within a session across duplicate
// session objects in one indexing pass. Distinct messages (codex history
// accumulation) pass through; format twins (gemini .json/.jsonl, cursor
// multi-store composers) collapse.
type msgSeen map[string]bool

// recordWriter appends length-prefixed records through one buffer, tracking
// the file offset in memory: the hot rebuild path used to pay a Seek syscall
// per record, which dominated cold-rebuild profiles.
type recordWriter struct {
	f      *os.File
	w      *bufio.Writer
	off    int64
	tables *recordTables
}

type bucketEntry struct {
	tok string
	off uint64
	n   uint32
}

// LockWaitNotice is called once when a write path finds the index locked by
// another deja and is about to wait for it. Readers never reach it — they take
// a lock-free snapshot instead — so this is only the case where a command is
// about to sit still for the length of someone else's build.
var LockWaitNotice func()

// noteLockWait reports the wait at most once per process, so a command that
// takes several locks does not repeat itself.
func noteLockWait() {
	if LockWaitNotice == nil || lockWaitNoted {
		return
	}
	lockWaitNoted = true
	LockWaitNotice()
}

var lockWaitNoted bool
