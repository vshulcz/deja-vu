package model

import (
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"
)

// Canonical message roles stored in transcripts and the index.
const (
	RoleUser       = "user"
	RoleAssistant  = "assistant"
	RoleTool       = "tool"
	RoleToolOutput = "tool-output"
	RoleFiles      = "files"
	RoleCommand    = "command"
	RoleEdit       = "edit"
	RoleSummary    = "summary"
	RoleWrote      = "wrote"
)

// ImportedPrefix marks a session project name that arrived via peer sync.
const ImportedPrefix = "imported:"

// RepoOriginPrefix marks a repo-scoped origin or project prefix.
const (
	OriginRepo        = "repo"
	RepoOriginPrefix  = "repo:"
	RepoProjectPrefix = RepoOriginPrefix
)

// NotesHarness and promoted-note prefixes define the canonical identity shape
// of personal and repo-scoped promoted notes across the index, search, and sync.
const (
	NotesHarness           = "deja"
	PromotedNotePrefix     = "deja-note-"
	RepoPromotedNotePrefix = "deja-repo-note-"
)

// Canonical lifecycle states of a promoted decision.
const (
	LifecycleAccepted   = "accepted"
	LifecycleRejected   = "rejected"
	LifecycleSuperseded = "superseded"
	LifecycleStale      = "stale"
)

// Canonical session lineage kinds recorded by harnesses.
const (
	KindSubagent     = "subagent"
	KindSidechain    = "sidechain"
	KindSubagentFork = "subagent_fork"
	KindFork         = "fork"
)

// IsWorkRole reports whether a message role records what an agent did rather
// than what was said. Tool and file-activity records are bulk-repetitive by
// nature and are excluded from conversational context windows and prose
// extractors unless explicitly requested.
func IsWorkRole(role string) bool {
	switch role {
	case RoleToolOutput, RoleFiles, RoleCommand, RoleEdit, RoleSummary, RoleWrote:
		return true
	}
	return false
}

// RoleMatches reports whether a stored message role satisfies a --role filter.
// `--role tool` is what `deja help` documents while "tool-output" is what is
// stored, so both spellings match.
func RoleMatches(stored, want string) bool {
	if stored == want {
		return true
	}
	return want == RoleTool && stored == RoleToolOutput
}

// SessionKey returns the canonical "harness:id" composite identifier used
// across the manifest, seen ledgers, and dedupe maps.
func SessionKey(harness, id string) string {
	return harness + ":" + id
}

// IsImportedProject reports whether a project name carries the synced
// "imported:" prefix.
func IsImportedProject(project string) bool {
	return strings.HasPrefix(project, ImportedPrefix)
}

// BareProject strips the "imported:" or "repo:" prefix from a project name if present.
func BareProject(project string) string {
	if rest, ok := strings.CutPrefix(project, ImportedPrefix); ok {
		return rest
	}
	return strings.TrimPrefix(project, RepoOriginPrefix)
}

// IsRepoProject reports whether project names a repo-scoped origin ("repo" or "repo:...").
func IsRepoProject(project string) bool {
	return project == OriginRepo || strings.HasPrefix(project, RepoOriginPrefix)
}

// PromotedNoteID returns the session id a personal promoted note is indexed
// under for sourceSession ("harness:id").
func PromotedNoteID(sourceSession string) string {
	return PromotedNotePrefix + strings.ReplaceAll(sourceSession, ":", "-")
}

// RepoPromotedNoteID returns the session id a repo-scoped promoted note is
// indexed under for sourceSession ("harness:id").
func RepoPromotedNoteID(sourceSession string) string {
	return RepoPromotedNotePrefix + strings.ReplaceAll(sourceSession, ":", "-")
}

// IsPromotedNoteID reports whether id has a personal ("deja-note-") or
// repo-scoped ("deja-repo-note-") promoted note prefix.
func IsPromotedNoteID(id string) bool {
	return strings.HasPrefix(id, PromotedNotePrefix) || strings.HasPrefix(id, RepoPromotedNotePrefix)
}

// CanonicalPromotedNoteID returns the promoted note id of a session on the
// machine that created it, or "" when the session is not a promoted note.
// After sync the local id is "imported-<hash>" while OrigID preserves the
// original "deja-note-..." or "deja-repo-note-..." id (#975, #2833).
func CanonicalPromotedNoteID(harness, id, origID string) string {
	if harness != NotesHarness {
		return ""
	}
	if IsPromotedNoteID(id) {
		return id
	}
	if IsPromotedNoteID(origID) {
		return origID
	}
	return ""
}

// IsPromotedNote reports whether (harness, id, origID) identifies a personal
// or repo-scoped promoted note, locally or after sync.
func IsPromotedNote(harness, id, origID string) bool {
	return CanonicalPromotedNoteID(harness, id, origID) != ""
}

// PromotedNoteSourceKey extracts the "harness:id" source key from a promoted
// note id ("deja-note-<harness>-<id>" or "deja-repo-note-<harness>-<id>").
// Hyphenated harness names ("copilot-chat", "claude-history") are matched before
// falling back to splitting on the first hyphen.
func PromotedNoteSourceKey(noteID string) (string, bool) {
	rest, ok := strings.CutPrefix(noteID, RepoPromotedNotePrefix)
	if !ok {
		rest, ok = strings.CutPrefix(noteID, PromotedNotePrefix)
	}
	if !ok || rest == "" {
		return "", false
	}
	for _, h := range []string{"copilot-chat", "claude-history"} {
		if id, ok := strings.CutPrefix(rest, h+"-"); ok && id != "" {
			return SessionKey(h, id), true
		}
	}
	return strings.Replace(rest, "-", ":", 1), true
}

// CandidatePromotedNoteIDs returns the personal and repo-scoped promoted note
// session ids that could have been promoted from (harness, id, origID).
func CandidatePromotedNoteIDs(harness, id, origID string) []string {
	localKey := SessionKey(harness, id)
	if origID == "" || origID == id {
		return []string{
			PromotedNoteID(localKey),
			RepoPromotedNoteID(localKey),
		}
	}
	origKey := SessionKey(harness, origID)
	return []string{
		PromotedNoteID(localKey),
		PromotedNoteID(origKey),
		RepoPromotedNoteID(localKey),
		RepoPromotedNoteID(origKey),
	}
}

// Message is one turn of a session.
type Message struct {
	Role string    `json:"role"`
	Text string    `json:"text"`
	Time time.Time `json:"time"`
}

// IsWorkRecord reports whether m records tool or file activity rather than
// conversational speech.
func (m Message) IsWorkRecord() bool { return IsWorkRole(m.Role) }

// IsSpoken reports whether m is conversational speech rather than a work record.
func (m Message) IsSpoken() bool { return !IsWorkRole(m.Role) }

// MarshalJSON leaves the time out of a message the transcript never stamped.
//
// The zero time marshals as "0001-01-01T00:00:00Z", which reads as a date
// rather than as the absence of one: a consumer sorting by it puts the message
// before everything that ever happened, and one bucketing by month gets a
// bucket in the year one. Every surface deja prints already refuses it — the
// listing shows "-" because "0001-01-01 reads as corrupted data rather than as
// a missing field" (#765) — and this is the surface a machine reads (#2113).
func (m Message) MarshalJSON() ([]byte, error) {
	// The alias sheds the method, so this does not call itself; the outer Time
	// shadows the embedded one, which is how a field is dropped without
	// restating the rest of the shape.
	type message Message
	if m.Time.IsZero() {
		return json.Marshal(struct {
			message
			Time *time.Time `json:"time,omitempty"`
		}{message(m), nil})
	}
	return json.Marshal(message(m))
}

// Session is one transcript as deja indexes it.
type Session struct {
	ID       string    `json:"id"`
	Harness  string    `json:"harness"`
	Project  string    `json:"project"`
	Path     string    `json:"path,omitempty"`
	Title    string    `json:"title,omitempty"`
	Started  time.Time `json:"started"`
	Updated  time.Time `json:"updated"`
	Messages []Message `json:"messages,omitempty"`
	Source   *Source   `json:"source,omitempty"`
	// GaveUp marks a session that says, in its own words, that something was
	// tried and backed out. Parsers do not set it; the index fills it from
	// what it read, and it is false for a store indexed before it existed.
	GaveUp bool `json:"gave_up,omitempty"`
	// Words is how long the whole session is, in words. Search sees only the
	// messages that matched a query, so it had no way to tell a short session
	// that is about the query from a marathon that mentions it once — the two
	// look the same size from inside a result. Parsers do not set it; the index
	// fills it from what it counted at build time, and it is zero for a store
	// indexed before it existed.
	Words int `json:"words,omitempty"`
	// Held is how many messages the session holds when Messages carries only
	// some of them: a search that reads the matching records alone hands over
	// a part, and a count taken from that part says the session is as short
	// as its matches. Zero when Messages is the whole session.
	Held int `json:"-"`
	// Touched lists the few files this session worked on most. Parsers do not
	// set it; the index fills it from what it stored, so a caller holding a
	// search result can ask a cheap question about those files without reading
	// the session back.
	Touched []string `json:"touched,omitempty"`
	// AgentTitle marks a Title taken from the assistant's opening line because
	// the session holds no user turn (#692). Surfaces that print titles in the
	// place of the reader's own question need to say so (#1100).
	AgentTitle bool `json:"agent_title,omitempty"`
	// ProjectFromDir marks a Project that is only the directory the transcript
	// was started from, with no files of the session's own to say otherwise.
	// An appended tail with a few turns and no file activity says nothing about
	// where the work was, so the index keeps the project it has.
	ProjectFromDir bool `json:"-"`
	// OrigID is the id this session had on the machine it came from, when it
	// arrived by sync. Import renames sessions to imported-<hash>, so a
	// promoted note stopped looking like one across a machine boundary and the
	// rules written for notes stopped applying to it (#975).
	OrigID string `json:"orig_id,omitempty"`
	// RemoteID is the id Claude Code's remote control gives this session —
	// the session_01… in claude.ai/code/session_01…, which is what a person
	// copies from the phone or the browser. Nothing on disk is named by it, so
	// "claude --resume session_01…" found nothing (#4667).
	RemoteID string `json:"remote_id,omitempty"`
	// FormerID is the id an older deja gave this session, when a parser has
	// since changed how it names them. Only the tombstone check reads it: a
	// session forgotten under the old id stays forgotten under the new one.
	FormerID string `json:"-"`
	// From is the machine this session was worked on, when it arrived by sync.
	// Empty for local work and for batches written by a deja that did not
	// stamp an origin.
	From string `json:"from,omitempty"`
	// Lifecycle carries the state of a promoted note that arrived by sync: the
	// states live in the other machine's notes.jsonl, which never travels
	// (#975).
	Lifecycle     string `json:"lifecycle,omitempty"`
	LifecycleNote string `json:"lifecycle_note,omitempty"`
	LifecycleAt   string `json:"lifecycle_at,omitempty"`
	// Kind, Parent and Agent describe a session an agent spawned rather than a
	// person: the harness's own word for it ("subagent", "subagent_fork"), the
	// session it was forked from when the harness records one, and the name of
	// the agent that ran it. Only harnesses that write the edge themselves fill
	// these — deja does not infer a parent from timing or naming (#1385).
	Kind   string `json:"kind,omitempty"`
	Parent string `json:"parent,omitempty"`
	Agent  string `json:"agent,omitempty"`
	// Typed is what a person typed in this session, one entry per turn, with
	// pasted text cut out. A parser fills it only where the transcript can
	// tell: a turn a person typed into an interactive session, not a
	// sub-agent's prompt, a turn a script sent, or a tool result. The index
	// reads the check and build rules out of it; it is never stored.
	Typed []string `json:"-"`
	// UserRules are the check and build rules the person typed in this
	// session, filled by the surface that shows it, and only for a local
	// session of the project the reader works in (index.MarkUserRules).
	// TakenBack are the commands the person took back, there or later.
	UserRules []string `json:"-"`
	TakenBack []string `json:"-"`
	// Planted are the orders a person's turn carried that the person did not
	// type: a paste, or every turn of a headless run (claude -p). Foreign is
	// a session outside the reader's project. Recall cuts orders from both.
	Planted []string `json:"-"`
	Foreign bool     `json:"-"`
}

// Source identifies where a session entered this deja index. Instance is an
// operator-configured stable name for the local store set; imported sessions
// deliberately omit it until sync carries peer provenance of its own.
type Source struct {
	Origin   string `json:"origin"`
	Instance string `json:"instance,omitempty"`
}

// Key returns the session's canonical "harness:id" composite identifier.
func (s *Session) Key() string { return SessionKey(s.Harness, s.ID) }

// IsImported reports whether the session arrived from another machine via sync.
func (s *Session) IsImported() bool { return IsImportedProject(s.Project) }

// BareProject returns the session's project name without an "imported:" prefix.
func (s *Session) BareProject() string { return BareProject(s.Project) }

// PromotedNoteID returns the session's canonical promoted-note id (handling
// both personal/repo notes and synced OrigID), or "" if not a promoted note.
func (s *Session) PromotedNoteID() string {
	return CanonicalPromotedNoteID(s.Harness, s.ID, s.OrigID)
}

// IsPromotedNote reports whether the session is a personal or repo-scoped
// promoted note.
func (s *Session) IsPromotedNote() bool {
	return s.PromotedNoteID() != ""
}

// PromotedNoteSourceKey returns the "harness:id" key of the session this note
// was promoted from, when s is a promoted note.
func (s *Session) PromotedNoteSourceKey() (string, bool) {
	return PromotedNoteSourceKey(s.PromotedNoteID())
}

// CandidatePromotedNoteIDs returns the personal and repo-scoped promoted note
// ids that could refer to this session (including its pre-sync OrigID).
func (s *Session) CandidatePromotedNoteIDs() []string {
	return CandidatePromotedNoteIDs(s.Harness, s.ID, s.OrigID)
}

// IsSpawned reports whether the session was spawned by another agent rather
// than started directly by a person.
func (s *Session) IsSpawned() bool {
	return SpawnedKind(s.Kind)
}

// InParentFile reports whether the session is a sub-agent run kept inside its
// parent session's own transcript file (#5216).
func (s *Session) InParentFile() bool {
	return InParentFile(s.Harness, s.Kind, s.Parent)
}

// ParentKey returns the "harness:parent" composite key of the session that
// spawned or forked this session, or "" when Parent is unset.
func (s *Session) ParentKey() string {
	if s.Parent == "" {
		return ""
	}
	return SessionKey(s.Harness, s.Parent)
}

// OrigKey returns the "harness:orig_id" composite key from the peer machine
// before sync renamed the session, or "" when OrigID is unset.
func (s *Session) OrigKey() string {
	if s.OrigID == "" {
		return ""
	}
	return SessionKey(s.Harness, s.OrigID)
}

// FormerKey returns the "harness:former_id" composite key from an older parser
// naming scheme, or "" when FormerID is unset.
func (s *Session) FormerKey() string {
	if s.FormerID == "" {
		return ""
	}
	return SessionKey(s.Harness, s.FormerID)
}

// SetSource fills the machine-facing provenance fields without changing the
// project string retained for human compatibility.
func (s *Session) SetSource(localInstance string) {
	if s.IsImported() {
		s.Source = &Source{Origin: "imported"}
		return
	}
	if s.From == OriginRepo || IsRepoProject(s.Project) {
		s.Source = &Source{Origin: OriginRepo}
		return
	}
	s.Source = &Source{Origin: "local", Instance: localInstance}
}

// Touch widens the session's Started..Updated span to cover t.
func (s *Session) Touch(t time.Time) {
	if t.IsZero() {
		return
	}
	if s.Started.IsZero() || t.Before(s.Started) {
		s.Started = t
	}
	if s.Updated.IsZero() || t.After(s.Updated) {
		s.Updated = t
	}
}

// LoggedID is a session id as a JSON log holds it. encoding/json replaces every
// byte that is not valid UTF-8 with U+FFFD, so an id carrying one — a project
// directory named with a stray byte, which ext4 allows — comes back from the
// usage log as a different string and matched no session in the index (#2199).
// Anything comparing an id against one that has been through JSON has to
// compare the same form.
//
// Byte for byte what the encoder does, rather than strings.ToValidUTF8, which
// collapses a run of bad bytes into one replacement where the encoder writes
// one per byte.
func LoggedID(id string) string {
	if utf8.ValidString(id) {
		return id
	}
	var b strings.Builder
	b.Grow(len(id))
	for i := 0; i < len(id); {
		r, size := utf8.DecodeRuneInString(id[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteRune(utf8.RuneError)
			i++
			continue
		}
		b.WriteString(id[i : i+size])
		i += size
	}
	return b.String()
}

// SpawnedKind reports whether a session's kind is the harness's word for an
// agent another session spawned.
func SpawnedKind(kind string) bool {
	switch kind {
	case KindSidechain, KindSubagent, KindSubagentFork:
		return true
	}
	return false
}

// InParentFile reports whether a session is a sub-agent run its harness keeps
// inside the parent's own log: Copilot CLI's task runs (#5216).
func InParentFile(harness, kind, parent string) bool {
	return harness == "copilot" && kind == KindSidechain && parent != ""
}
