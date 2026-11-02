package index

import (
	"bufio"
	"os"
	"path/filepath"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

// HarnessCount is one harness's share of a build.
type HarnessCount struct {
	Name     string
	Sessions int
	Messages int
}

// BuildSummary describes the most recent (re)build in this process; the CLI
// uses it to greet a first-ever index with a summary instead of silence.
type BuildSummary struct {
	Initial  bool
	Sessions int
	Messages int
	// Dropped is how many parsed messages did not reach the index: deja's own
	// recall blocks, stripped before anything counts them, and turns that hold
	// nothing else. The two counts a build prints disagreed by exactly this and
	// nothing said why (#3386).
	Dropped    int
	Harnesses  int
	PerHarness []HarnessCount
	// Took is the build's wall time, set by the command that ran and timed it
	// (the build itself cannot see the parse that came before it); zero when
	// nothing timed it, and then no summary line states a time (#4630).
	Took time.Duration
}

// LastBuild is the summary of this process's most recent build.
var LastBuild BuildSummary

// SuppressHarnessNarration silences the per-harness progress lines for one
// build; the CLI sets it when it is about to greet a first index with the
// same numbers in the summary block.
var SuppressHarnessNarration bool

// FileState is what the index knows about one transcript file.
type FileState struct {
	Path          string `json:"path"`
	Size          int64  `json:"size"`
	MTime         int64  `json:"mtime"`
	MetadataSize  int64  `json:"metadata_size,omitempty"`
	MetadataMTime int64  `json:"metadata_mtime,omitempty"`
	CWDSize       int64  `json:"cwd_size,omitempty"`
	CWDMTime      int64  `json:"cwd_mtime,omitempty"`
	// WALSize and WALMTime are a SQLite store's <db>-wal: writes land there
	// and reach the database file only at a checkpoint, so a store whose
	// file never changed still has new sessions in it (#5140).
	WALSize     int64 `json:"wal_size,omitempty"`
	WALMTime    int64 `json:"wal_mtime,omitempty"`
	LastUpdated int64 `json:"last_updated,omitempty"`
	Redactions  int   `json:"redactions,omitempty"`
	// Kept marks a transcript its agent deleted and the index kept on
	// purpose. Without it the listing came up one short on every run, the
	// index never read as fresh, and every search served stale while a
	// background pass found nothing to do.
	Kept bool `json:"kept,omitempty"`
	// KeptAt is when an index pass first found the kept transcript gone, in
	// Unix seconds. No agent records when it deleted a transcript, and the pass
	// after it is the nearest deja can say; a row kept before this field
	// existed has zero, and no window claims it.
	KeptAt int64 `json:"kept_at,omitempty"`
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
	// WrittenBack marks a transcript `deja resume --write-back` rebuilt from
	// the index (#4617): it holds the conversation only, so a pass that reads
	// a session's other copies again carries this one's records instead of
	// reading it (#5358). It lasts while the file's size and mtime do.
	WrittenBack bool `json:"written_back,omitempty"`
}

// SessionMeta is the manifest's row for one indexed session.
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
	// Settled is what this session concluded, extracted once here for the same
	// reason Touched is: the point-of-action hook needs it before a command
	// runs, and finding it at that moment cost 133 ms a call against 21 ms —
	// a ranking pass plus a whole-session load, on a surface that fires on
	// every action (#3001, #3605). Bounded like the digest's own extraction,
	// and additive: a manifest written before it decodes with it empty and the
	// caller falls back to the search it used to do.
	Settled string `json:",omitempty"`
	// RanCommand is whether the session ran a command, which is when Settled
	// is worth extracting. An append sees only the new messages and needs to
	// know the ones before them did.
	RanCommand bool `json:",omitempty"`
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
	// SharedStarted and SharedUpdated are the span of the files that share
	// this row's id and do not own it, a Gemini resume stub beside its
	// transcript. A pass that reads only the owner builds the row from that
	// file again, and the stub's later Updated was gone until a rebuild
	// (#4574).
	SharedStarted time.Time `json:",omitzero"`
	SharedUpdated time.Time `json:",omitzero"`
	// Copies are the other transcripts that hold this id, which a pass that
	// replaces the session reads with the one that changed (#5358). Empty for
	// a session one file holds, which is nearly all of them.
	Copies []string `json:",omitempty"`
	// OrigID is the id a session had on the machine it came from. Import
	// renames every session to imported-<hash>, so a promoted note stopped
	// looking like one the moment it crossed a machine boundary and every rule
	// written for notes — newest correction first, the decision's state —
	// silently stopped applying (#975). Additive: older manifests decode with
	// it empty.
	OrigID string `json:",omitempty"`
	// RemoteID is the remote-control id of a Claude Code session, the one a
	// person copies from claude.ai/code. Additive like OrigID.
	RemoteID string `json:",omitempty"`
	// Kind, Parent and Agent describe a session an agent spawned: the
	// harness's own word for it, the session it was forked from, and the agent
	// that ran it. Only filled where the harness writes the edge itself.
	// Additive: an older manifest decodes with them empty and every surface
	// says what it said before.
	Kind   string `json:",omitempty"`
	Parent string `json:",omitempty"`
	Agent  string `json:",omitempty"`
	// Opening identifies the turn the session opens with. A fork copies the
	// turns it was forked from, times included, so a fork and its source open
	// alike, and recall reads that to keep a fork's source off its page (#4549).
	// Additive: an older manifest decodes with it zero, which matches nothing.
	Opening uint64 `json:",omitempty"`
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
	// NoText marks a row whose transcript had nothing left to index once
	// plumbing was stripped, so a transcript holding the conversation under
	// the same id takes the row instead of being reported as a clash (#4213).
	// Words cannot say it: a row of emoji has text and no words. Additive: an
	// older manifest decodes with it false, the answer it always gave.
	NoText bool `json:",omitempty"`
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
	// Rules are the check and build rules the person typed in this session
	// and did not take back in it, and Revokes the commands they took back
	// here, which may be another session's rule (user_rules.go). Additive: an
	// older manifest decodes with them empty, and no line is marked.
	Rules   []string `json:",omitempty"`
	Revokes []string `json:",omitempty"`
	// Planted are the orders the session's person turns carried that no
	// person typed (model.Session.Planted). Additive like Rules.
	Planted []string `json:",omitempty"`
}

// Key returns the session's canonical "harness:id" composite identifier.
func (m SessionMeta) Key() string { return model.SessionKey(m.Harness, m.ID) }

// IsImported reports whether the session arrived from another machine via sync.
func (m SessionMeta) IsImported() bool { return model.IsImportedProject(m.Project) }

// BareProject returns the session's project name without an "imported:" prefix.
func (m SessionMeta) BareProject() string { return model.BareProject(m.Project) }

// PromotedNoteID returns the session's canonical promoted-note id, or "".
func (m SessionMeta) PromotedNoteID() string {
	return model.CanonicalPromotedNoteID(m.Harness, m.ID, m.OrigID)
}

// IsPromotedNote reports whether the session is a personal or repo-scoped
// promoted note.
func (m SessionMeta) IsPromotedNote() bool {
	return m.PromotedNoteID() != ""
}

// PromotedNoteSourceKey returns the "harness:id" key of the session this note
// was promoted from, when m is a promoted note.
func (m SessionMeta) PromotedNoteSourceKey() (string, bool) {
	return model.PromotedNoteSourceKey(m.PromotedNoteID())
}

// CandidatePromotedNoteIDs returns the personal and repo-scoped promoted note
// ids that could refer to this session (including its pre-sync OrigID).
func (m SessionMeta) CandidatePromotedNoteIDs() []string {
	return model.CandidatePromotedNoteIDs(m.Harness, m.ID, m.OrigID)
}

// IsSpawned reports whether the session was spawned by another agent rather
// than started directly by a person.
func (m SessionMeta) IsSpawned() bool {
	return model.SpawnedKind(m.Kind)
}

// InParentFile reports whether the session is a sub-agent run kept inside its
// parent session's own transcript file (#5216).
func (m SessionMeta) InParentFile() bool {
	return model.InParentFile(m.Harness, m.Kind, m.Parent)
}

// ParentKey returns the "harness:parent" composite key of the session that
// spawned or forked this session, or "" when Parent is unset.
func (m SessionMeta) ParentKey() string {
	if m.Parent == "" {
		return ""
	}
	return model.SessionKey(m.Harness, m.Parent)
}

// OrigKey returns the "harness:orig_id" composite key from the peer machine
// before sync renamed the session, or "" when OrigID is unset.
func (m SessionMeta) OrigKey() string {
	if m.OrigID == "" {
		return ""
	}
	return model.SessionKey(m.Harness, m.OrigID)
}

// Key returns the finding's canonical "harness:id" session identifier.
func (f SecretFinding) Key() string { return model.SessionKey(f.Harness, f.ID) }

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

// Manifest describes an index: its layout, the files it read and the
// sessions they hold.
type Manifest struct {
	Version int `json:"version"`
	// Format is the on-disk layout this store was written with. Absent on a
	// store written before the field existed, which reads as unknown: a reader
	// that cannot tell declines rather than guessing.
	Format   int                    `json:"format,omitempty"`
	Files    map[string]FileState   `json:"files"`
	Sessions map[string]SessionMeta `json:"sessions"`
	BuiltAt  time.Time              `json:"built_at"`
	// SourcesReadAt is when deja last walked this machine's stores. It is not
	// BuiltAt: an import from a peer writes the manifest, and the manifest's
	// build time with it, without looking at a single local transcript. On a
	// machine that syncs on a timer that made every store look freshly read —
	// `deja doctor` counts a store stale by comparing its newest file against
	// the build time, so it reported nothing to do while local transcripts sat
	// unread (#3747). Absent on a store written before this field, which reads
	// as zero and falls back to BuiltAt.
	SourcesReadAt    time.Time        `json:"sources_read_at"`
	Generation       string           `json:"generation,omitempty"`
	Scope            string           `json:"scope"`
	Redacted         int              `json:"redacted"`
	RedactionRules   map[string]int   `json:"redaction_rules,omitempty"`
	ExportWatermarks map[string]int64 `json:"export_watermarks,omitempty"`
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
	// DeadRecords are the offsets, ascending, of records a later pass
	// replaced. A pass that replaces a few sessions appends their new records
	// and lists the old ones here rather than writing the log again, and every
	// walk of the log and every postings read skips them. DeadBytes is what
	// they hold; past a quarter of the log the next replacing pass writes the
	// log again without them (#5387).
	DeadRecords []int64 `json:"dead_records,omitempty"`
	DeadBytes   int64   `json:"dead_bytes,omitempty"`
	// BucketFiles is how many postings files buckets/ held when the manifest
	// was committed. Losing the whole directory was already caught (#946), but
	// a partial copy or an interrupted sync leaves some files behind rather
	// than none, and each one that goes missing takes every result for the
	// tokens it held — silently, with `doctor --deep` still reporting the
	// index healthy (#1088). Additive: a manifest written before this decodes
	// with it zero, and the check is skipped.
	BucketFiles int `json:"bucket_files,omitempty"`
	// bucketsLeaving is how many of the postings files on disk the pass
	// committing this manifest removes right after: the count above leaves
	// them out (#5426).
	bucketsLeaving int
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
	// AliasFingerprint is the same for the project alias file
	// (sources.ProjectAliasFingerprint): aliases name sessions at ingest, so an
	// edit renames nothing already indexed until a rebuild.
	AliasFingerprint string `json:"alias_fingerprint,omitempty"`
	// ToolFingerprint records which external CLIs were available when this
	// index was built. A store deja could not read for want of sqlite3 or zstd
	// is not stale by its file state — the transcripts did not change — so
	// installing the tool and running `deja index`, which is what doctor tells
	// the reader to do, reported "index is up to date" and left the store out
	// of recall (#1760). Additive like the field above: empty means "not
	// recorded", which is what an index from an older deja carries.
	ToolFingerprint string `json:"tool_fingerprint,omitempty"`
	// Compactions holds bounded continuation packets captured when a harness
	// compacts a live session. It lives in manifest.gob so rebuild swaps carry
	// it with the existing local index, while keeping it out of searchable
	// transcript records.
	Compactions map[string]CompactionState `json:"compactions,omitempty"`
	// KeptDated says the transcripts this store lost were dated as they went:
	// a pass of a build that stamps KeptAt wrote it. A store from before that
	// holds rows for transcripts already gone, unmarked (0.21 had no Kept), and
	// the first pass to find them must not date months of deletions to today.
	KeptDated bool `json:"kept_dated,omitempty"`
	// ReadZone names the time zone this machine's stores were last read in
	// (zoneFingerprint). aider headers carry no zone and are read in the
	// local one, so a pass in another zone rereads them (#5053). Empty on a
	// store from before the field, which says nothing.
	ReadZone string `json:"read_zone,omitempty"`
	// Deferred holds the transcripts a pass left unread because zstd was
	// missing, with the state its walk saw. Not in Files: Files says what is
	// indexed, and a rollout held there would read as unchanged the day zstd
	// arrives. Left out of both, it read as changed on every pass, and every
	// search rewrote the manifest for 1500 rollouts it still could not read
	// (#5105). Additive: an older store decodes with it empty.
	Deferred map[string]FileState `json:"deferred,omitempty"`
	// PartialOf is set on the early index a big first build publishes before
	// it finishes: how many sessions that build found, of which this index
	// holds only the newest. An answer from it has to say the older ones are
	// missing, not the newest (#5390). Zero on every complete index.
	PartialOf int `json:"partial_of,omitempty"`
	// SearchOnly says the build skipped the derived tables — fixes,
	// co-occurrence, commands, command failures, session facts — because
	// DEJA_SEARCH_ONLY or "search_only" asked it to (#5395). A reader of one
	// of those tables says so rather than answering empty, and a pass whose
	// setting differs rebuilds once.
	SearchOnly bool `json:"search_only,omitempty"`
	// Empty holds the rows of sessions with no record, a transcript that had
	// nothing to index, out of Sessions so no reader lists them (#868,
	// #5563). A pass takes one back when its id is read again: a Gemini
	// resume stub's row carries its span and file to the transcript that
	// takes the id later, as a rebuild reading both gives (#4213).
	Empty map[string]SessionMeta `json:"empty,omitempty"`
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
	// ClippedSessions splits Clipped by session id. Zed keeps every thread in
	// one threads.db, so the file's count alone put the note on every thread
	// in it (#4340). Kept out of the JSON: the contract is the per-file count.
	ClippedSessions map[string]int `json:"-"`
	// Reason says why the last unusable record was skipped, for a store whose
	// records are rows rather than lines a reader can go and look at (#4341).
	Reason string `json:"reason,omitempty"`
	// Unusable is those records by id. A store read from its watermark hands
	// back only what changed, so a pass carries the rows it did not re-read
	// instead of reporting them gone. Out of the JSON: the count is the contract.
	Unusable map[string]string `json:"-"`
	// Walk marks an Error the store walk found rather than a parser: a
	// directory it could not list or a transcript it may not read. Only those
	// are cleared by a later walk that reads the path, because a parse error
	// on a file that has not changed is still the file's (#5393).
	Walk bool `json:"-"`
}

type manifestCore struct {
	Version int
	// Format: see Manifest. Written since onDiskFormat existed; absent on an
	// older store, which reads as zero and means "cannot say".
	Format           int
	Files            map[string]FileState
	BuiltAt          time.Time
	SourcesReadAt    time.Time
	Generation       string
	Scope            string
	Redacted         int
	RedactionRules   map[string]int
	ExportWatermarks map[string]int64
	ExportBoundary   map[string][]uint64
	ImportedRecords  map[string]bool
	RecordStrings    []string
	RecordsSize      int64
	DeadRecords      []int64
	DeadBytes        int64
	BucketFiles      int
	IngestHealth     map[string]HarnessIngest
	IngestFiles      map[string]FileIngest
	// ExcludeFingerprint, ToolFingerprint: see Manifest.
	ExcludeFingerprint string
	AliasFingerprint   string
	ToolFingerprint    string
	Compactions        map[string]CompactionState
	KeptDated          bool
	ReadZone           string
	Deferred           map[string]FileState
	PartialOf          int
	SearchOnly         bool
	Empty              map[string]SessionMeta
}

// RedactionStats counts what redaction masked, in total, per file and per
// rule.
type RedactionStats struct {
	Total int
	Files map[string]int
	Rules map[string]map[string]int
}

// Record is one message as the record log stores it.
type Record struct {
	Key        string
	SourcePath string
	Role       string
	Text       string
	Time       time.Time
	LowerText  string `json:"-"`
}

// OffsetRecord is a Record with its offset in the record log.
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

// SearchResult is a search's sessions and how it found them.
type SearchResult struct {
	Sessions []model.Session
	Fuzzy    bool
	Stemmed  bool
	// Neighbour says the swap came from the co-occurrence map rather than from
	// a word form: "login" answered by "jwks" is not a spelling of it, and
	// calling it a word form reads as a typo correction the reader did not
	// make (#1786).
	Neighbour bool
	// LoneWord says a two-word question was answered on one of its words
	// (loneWordSearch): leads, not an answer, so the page keeps them short.
	LoneWord bool
	Variants map[string][]string
	Tier     string
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
	// Strict is how many of Sessions matched rather than ranked: they hold
	// every word of the query, in the forms Variants names when the answer
	// carries any. Only withRelevanceTail sets it: that is where
	// a strict answer too thin to trust on its own gets the relevance
	// ranking hung underneath it and the whole thing is labelled relevance,
	// which the tier contract defines as nothing matched. Measured over 93
	// two-word queries on a 2,422-session store, all 20 answers labelled
	// relevance had a strict head of 1 to 9 sessions and none of them had
	// matched nothing (#3815).
	Strict int
	// Directory is set when the answer leads with sessions about the asked
	// file's directory because none named the file (#4762).
	Directory string `json:",omitempty"`
	// StrictIDs is which of them, keyed harness+":"+id — the order of
	// Sessions is the merged ranking's, so the head is not the first Strict
	// of them. A caller telling a reader what one session is needs the
	// answer for that session, not the count.
	StrictIDs map[string]bool `json:",omitempty"`
	// TermIDF is what the relevance ranking judged each query term to be worth.
	// It travels with the answer so the caller choosing which message to show
	// can weigh the words the same way the ranking weighed the session. Without
	// it that choice was made by counting terms, one each, and the two disagreed
	// exactly when one rare word carried the session. Empty on every other tier.
	TermIDF map[string]float64 `json:",omitempty"`
	// Unread is set when Options.Window let the exact tier read only the
	// sessions that can reach the page. The caller hands it to ranking.
	Unread *query.Unread `json:",omitempty"`
	// Fell is set (query.FellNoPostings, query.FellNoText) when every rung
	// above the ranked tier came back empty; see Options.FellThrough.
	Fell int `json:",omitempty"`
}

// IsStrict says whether this session holds every word of the query rather
// than having been ranked into the answer underneath the ones that do.
func (r SearchResult) IsStrict(s model.Session) bool {
	return r.StrictIDs[s.Key()]
}

// DefaultDir is the index directory: DEJA_INDEX_DIR, or
// ~/.cache/deja/index.db.
func DefaultDir() string {
	if v := os.Getenv("DEJA_INDEX_DIR"); v != "" {
		return v
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".cache", "deja", "index.db")
}

const syncImportPath = "deja-sync-import"

type importedState struct {
	sessions    []model.Session
	watermarks  map[string]int64
	boundary    map[string][]uint64
	dedupe      map[string]bool
	compactions map[string]CompactionState
	// partialOf: see Manifest.PartialOf.
	partialOf int
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
type msgSeen map[seenK]bool

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

// LockWaitDone is called when the wait LockWaitNotice announced is over, so
// a notice that keeps moving while it waits knows when to stop.
var LockWaitDone func()

// noteLockWait reports the wait at most once per process, so a command that
// takes several locks does not repeat itself. The caller runs what it returns
// once it holds the lock.
func noteLockWait() (ended func()) {
	if LockWaitNotice == nil || lockWaitNoted {
		return func() {}
	}
	lockWaitNoted = true
	LockWaitNotice()
	return func() {
		if LockWaitDone != nil {
			LockWaitDone()
		}
	}
}

var lockWaitNoted bool
