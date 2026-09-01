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
// 23: the same shape again — #984 began recording an imported note's state on
// its manifest row without a bump, so a store that imported a batch before it
// kept a row with no state, and re-importing the batch adds nothing because it
// is deduped. The bump is what makes the one rebuild that re-derives it happen
// (#1049).
// 24: tokens() now folds NFD to NFC, so postings keys for accented words change
// shape. A store built before the fold keyed "café" decomposed; the rebuild
// re-derives every key on the composed form so an NFC query reaches it (#1098).
// 25: the fixes.gob and commands.gob sidecars are written only by a full
// rebuild, so a store built before they existed answered `deja fix` with a
// false "no session ran a command after that error" and the hook-tool command
// line with nothing — or, once the field was added, without the per-project
// session counts the trust policy needs. The bump forces the one rebuild that
// mines them both.
// 26: a session that opens with a greeting was titled "hi" — 17 rows of one
// real 800-session store, and the turn that says what the work was sits one
// line below (#790). Titles are part of this version for the reason 22 gives:
// incremental ingest reuses the row it already has, so nothing re-derives them
// without a bump.
// 27: the marks Japanese writes inside a word — ー above all, which appears in
// most loanwords — were not counted as CJK, so every such word was indexed as
// two runs and the bigrams came out of the pieces. A store built before this
// holds the split keys and a query built after it asks for the joined ones, so
// the bump is what makes the one rebuild that re-derives them happen (#1319).
// 28: what counts as a wall is bounded in characters rather than bytes, so a
// line written in Russian or Chinese is held to the same length as an English
// one. A store built before this holds fewer friction signatures, and nothing
// re-derives them without the bump — the same shape as 22 and 23 (#1319).
// 29: the prefixes a runner stamps on a line it did not write — pytest's `E `
// and a leading timestamp — are stripped before a line is hashed, so one error
// is one wall however it was printed. A store built before this holds both
// spellings under different signatures, and nothing re-derives them without
// the bump — the same shape as 27 and 28 (#1637).
// 30: a long digit run inside a line — a port, a pid, an epoch, a goroutine id
// — is masked before the line is hashed, so one failure is one wall across the
// numbers the machine hands out. A store built before this holds each run under
// its own signature, and nothing re-derives them without the bump — the same
// shape as 27, 28 and 29 (#2369).
// 31: what counts as a wall at all. A quote no longer drops a line as source
// (#2430), a generic opening no longer hides the phrase behind it (#2432), the
// list learned the walls a machine actually hits (#2434), a source line
// carrying an error is not one (#2436), and the line cap went from 120 to 200
// characters, which alone recovered a third again of what was recognised
// (#2438). Measured over a real store: 7,053 signatures before, 8,885 after. A
// store built under the old rules holds the old signatures and nothing
// re-derives them — `deja friction` reads the records and sees the difference,
// while `hook-tool-after` and the environment block read the manifest and stay
// silent, which is the state this bump exists to end (#2444).
// 32: goose sessions are kept unless goose says it wrote them for itself. The
// filter was an allow-list, so a way of reaching goose that it did not name
// was dropped. A store built under the old rules is missing those sessions,
// and the db path only re-reads what has been touched since its watermark, so
// without the bump they stay missing until each is used again — the same shape
// as 21, 22 and 23 (#2873).
const version = 32
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
	// PrefixSample fingerprints the same span without reading all of it: the
	// head, the bytes just before SafeSize, and SafeSize itself. The full hash
	// costs a read of the whole transcript on every search that finds the file
	// changed, which on a session being written right now is every search —
	// measured at 1.70s per call against a 250 MB transcript, growing with it.
	//
	// It catches what PrefixHash exists to catch. A rewind truncates and
	// rewrites from the truncation point, so either that point is past
	// SafeSize and the indexed bytes are genuinely untouched, or it is before
	// it and the window ending at SafeSize falls inside the rewritten region.
	//
	// PrefixHash stays for manifests written before this existed; they verify
	// the old way once, and the next walk records a sample.
	PrefixSample uint64 `json:"prefix_sample,omitempty"`
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
	// AgentTitle marks a title that came from the assistant's opening line
	// because the session holds no user turn (#692). The listing printed it in
	// the place of the reader's own question, so an assertion nobody made read
	// like something they said (#1100). Additive: an older manifest decodes
	// with it false, and the line then reads as it did before.
	AgentTitle bool `json:",omitempty"`
	// OrigID is the id a session had on the machine it came from. Import
	// renames every session to imported-<hash>, so a promoted note stopped
	// looking like one the moment it crossed a machine boundary and every rule
	// written for notes — newest correction first, the decision's state —
	// silently stopped applying (#975). Additive: older manifests decode with
	// it empty.
	OrigID string `json:",omitempty"`
	// Kind, Parent and Agent describe a session an agent spawned: the
	// harness's own word for it, the session it was forked from, and the agent
	// that ran it. Only filled where the harness writes the edge itself.
	// Additive: an older manifest decodes with them empty and every surface
	// says what it said before.
	Kind   string `json:",omitempty"`
	Parent string `json:",omitempty"`
	Agent  string `json:",omitempty"`
	// From is the machine this session was worked on. Every imported session
	// read as "from elsewhere" and nothing more, so with three machines
	// exchanging history there was no way to ask what the server did, and no
	// way to notice one of them had stopped sending. Additive: a manifest
	// written before it existed decodes with it empty, and a batch from an
	// older deja carries no origin, so both degrade to what they said before.
	From string `json:",omitempty"`
	// Lifecycle is the state of a promoted note that arrived by sync. The
	// local states live in notes.jsonl, which the other machine does not have,
	// so a decision retracted there read as accepted here (#975).
	Lifecycle     string `json:",omitempty"`
	LifecycleNote string `json:",omitempty"`
	LifecycleAt   string `json:",omitempty"`
	// GaveUp marks a session whose own text reports that something was tried
	// and backed out. It is evidence, not a lifecycle state — the states are
	// the user's judgement and stay theirs. Additive: an older manifest
	// decodes with it false and hits print as they did before.
	GaveUp bool `json:",omitempty"`
	// Words is the whole session's length in words. Ranking reads back only
	// the records that matched, so BM25 normalised by the size of the match
	// rather than by the size of the session. Additive: a manifest written
	// before it existed decodes with it zero and ranking falls back to what it
	// can see.
	Words int `json:",omitempty"`
	// Counted is how many of this session's messages the derived fields above
	// already include, and LastMsg fingerprints the newest of them.
	//
	// A transcript grows after being indexed — the ordinary case, not an edge
	// one — and the append path folded whatever it was handed. What it is
	// handed differs by store: a file yields only the region appended since
	// last time, while goose re-delivers a live session whole on every pass. A
	// count alone cannot tell those apart, so it either lost the tail or
	// counted the same messages again, and a session new to the index was
	// derived twice at once (#1304). Finding LastMsg in what arrived answers
	// it: what follows is new, and if it is not there at all, all of it is.
	//
	// A row written before this existed carries neither, which reads as
	// "nothing counted" — so the first delivery after an upgrade is folded
	// whole. That is right for the file stores, where a delivery is the
	// appended region; a store that re-delivers a live session counts it once
	// more and then settles, which is cheaper than a format bump that would
	// make every user rebuild.
	Counted int    `json:",omitempty"`
	LastMsg uint64 `json:",omitempty"`
	// TouchHits is how often each path in Touched was touched, in the same
	// order. Touched is a ranking, and a ranking cannot be merged without the
	// numbers behind it: a file touched once in a tail outranked one touched
	// throughout, because position was all the merge had to go on.
	TouchHits []int `json:",omitempty"`
	// Hit holds hashes of the specific errors this session tripped over, so
	// the first screen can name a wall the machine keeps running into without
	// reading a single session. Same trade as Asked: the text is recovered
	// from the two or three sessions that matched.
	Hit []uint64 `json:",omitempty"`
}

// touchedFileCap bounds what goes in SessionMeta.Touched.
//
// Six was enough to say something about a session and wrong for the thing that
// reads it: the line before an edit asks how many sessions touched this file,
// and a session that worked on forty recorded six. Measured over 334 distinct
// files an agent really edited on this machine, 257 of them had been touched by
// no session at all as far as the index knew, while 268 were spoken about in
// sessions the store holds. The surface was blind to three files in four.
//
// Forty, from the three points measured: files with no history 257 -> 218 -> 190
// at 6, 20 and 40, and files the hook can speak about 26 -> 30 -> 36. It costs
// 700 KB of a 195 MB index and 3 ms on the hook that reads it — measured on the
// same store, where the line went from silent on 12 of 12 real edits to
// speaking on 4.
const touchedFileCap = 40

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
	// BucketFiles is how many postings files buckets/ held when the manifest
	// was committed. Losing the whole directory was already caught (#946), but
	// a partial copy or an interrupted sync leaves some files behind rather
	// than none, and each one that goes missing takes every result for the
	// tokens it held — silently, with `doctor --deep` still reporting the
	// index healthy (#1088). Additive: a manifest written before this decodes
	// with it zero, and the check is skipped.
	BucketFiles int `json:"bucket_files,omitempty"`
	// IngestHealth records, per harness, what ingestion skipped on the pass
	// that last touched it: malformed JSONL lines and files that failed to
	// parse. Silent loss must be diagnosable (`deja doctor --json`).
	IngestHealth map[string]HarnessIngest `json:"ingest_health,omitempty"`
	// IngestFiles is the same story per file, and it is the one that survives:
	// a store's counts are the sum of its files, so a pass that re-reads one
	// transcript cannot forget what a different one could not read (#2015).
	// Sparse — only files with something to report are in it.
	IngestFiles map[string]FileIngest `json:"ingest_files,omitempty"`
	// ExcludeFingerprint identifies the exclusion patterns in force when this
	// index was built. They apply at ingest, so a pattern added later leaves
	// what is already indexed searchable and exportable — silently, until this
	// told `deja index` to say so (#1307). Additive: a manifest written before
	// it decodes with it empty, and an empty one means "not recorded", which
	// says nothing rather than guessing.
	ExcludeFingerprint string `json:"exclude_fingerprint,omitempty"`
	// ToolFingerprint records which external CLIs were available when this
	// index was built. A store deja could not read for want of sqlite3 or zstd
	// is not stale by its file state — the transcripts did not change — so
	// installing the tool and running `deja index`, which is what doctor tells
	// the reader to do, reported "index is up to date" and left the store out
	// of recall (#1760). Additive like the field above: empty means "not
	// recorded", which is what an index from an older deja carries.
	ToolFingerprint string `json:"tool_fingerprint,omitempty"`
}

// HarnessIngest is one harness's ingestion health from its last indexing pass.
type HarnessIngest struct {
	MalformedLines int `json:"malformed_lines,omitempty"`
	// ClippedMessages counts messages stored short of what the transcript
	// holds, because a single message ran past maxIndexedText. The text is
	// there and the tail is not, so a search over the tail answers "no
	// matches" — the same silent loss as an unparseable line (#1093).
	ClippedMessages int    `json:"clipped_messages,omitempty"`
	FailedFiles     int    `json:"failed_files,omitempty"`
	LastError       string `json:"last_error,omitempty"`
}

// FileIngest is what one file cost the last pass that read it: lines it could
// not parse, and the error if the file itself would not open. Held per file so
// that re-reading one transcript cannot erase what another one reported.
type FileIngest struct {
	Malformed int    `json:"malformed,omitempty"`
	Error     string `json:"error,omitempty"`
	// Clipped counts messages stored short of what this file holds. Here for
	// the same reason as the other two: a pass that reads one transcript must
	// not speak for what another one holds (#2022).
	Clipped int `json:"clipped,omitempty"`
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
	BucketFiles      int
	IngestHealth     map[string]HarnessIngest
	IngestFiles      map[string]FileIngest
	// ExcludeFingerprint, ToolFingerprint: see Manifest.
	ExcludeFingerprint string
	ToolFingerprint    string
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
	// Neighbour says the swap came from the co-occurrence map rather than from
	// a word form: "login" answered by "jwks" is not a spelling of it, and
	// calling it a word form reads as a typo correction the reader did not
	// make (#1786).
	Neighbour bool
	Variants  map[string][]string
	Tier      string
	// Total is how many sessions the tier matched before its own window
	// trimmed them, and Capped whether that trimming withheld any. The
	// relevance tier is the one that needs them: it ranks and truncates
	// inside retrieval, so a caller counting Sessions is measuring the
	// window rather than the match. Zero Total means the tier reports no
	// figure of its own and the caller's count stands.
	//
	// "Matched" means different things per tier, and the number a reader sees
	// in "showing 33 of 414" is this one. Measured on a 1,365-session store,
	// for "what did we decide about the retry budget": 2 sessions hold every
	// term, 819 hold at least one, and 414 cleared the relevance tier's own
	// bar — so Total counts what the tier was willing to rank, which is why
	// the line above it says the answer is ranked by relevance rather than
	// matched (#2612).
	Total  int
	Capped bool
	// TermIDF is what the relevance ranking judged each query term to be worth.
	// It travels with the answer so the caller choosing which message to show
	// can weigh the words the same way the ranking weighed the session. Without
	// it that choice was made by counting terms, one each, and the two disagreed
	// exactly when one rare word carried the session. Empty on every other tier.
	TermIDF map[string]float64 `json:",omitempty"`
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
