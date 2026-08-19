package model

import (
	"strings"
	"time"
)

type Message struct {
	Role string    `json:"role"`
	Text string    `json:"text"`
	Time time.Time `json:"time"`
}

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
	// Touched lists the few files this session worked on most. Parsers do not
	// set it; the index fills it from what it stored, so a caller holding a
	// search result can ask a cheap question about those files without reading
	// the session back.
	Touched []string `json:"touched,omitempty"`
	// AgentTitle marks a Title taken from the assistant's opening line because
	// the session holds no user turn (#692). Surfaces that print titles in the
	// place of the reader's own question need to say so (#1100).
	AgentTitle bool `json:"agent_title,omitempty"`
	// OrigID is the id this session had on the machine it came from, when it
	// arrived by sync. Import renames sessions to imported-<hash>, so a
	// promoted note stopped looking like one across a machine boundary and the
	// rules written for notes stopped applying to it (#975).
	OrigID string `json:"orig_id,omitempty"`
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
}

// Source identifies where a session entered this deja index. Instance is an
// operator-configured stable name for the local store set; imported sessions
// deliberately omit it until sync carries peer provenance of its own.
type Source struct {
	Origin   string `json:"origin"`
	Instance string `json:"instance,omitempty"`
}

// SetSource fills the machine-facing provenance fields without changing the
// project string retained for human compatibility.
func (s *Session) SetSource(localInstance string) {
	if strings.HasPrefix(s.Project, "imported:") {
		s.Source = &Source{Origin: "imported"}
		return
	}
	s.Source = &Source{Origin: "local", Instance: localInstance}
}

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
