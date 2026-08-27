package model

import (
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"
)

type Message struct {
	Role string    `json:"role"`
	Text string    `json:"text"`
	Time time.Time `json:"time"`
}

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
	// Kind, Parent and Agent describe a session an agent spawned rather than a
	// person: the harness's own word for it ("subagent", "subagent_fork"), the
	// session it was forked from when the harness records one, and the name of
	// the agent that ran it. Only harnesses that write the edge themselves fill
	// these — deja does not infer a parent from timing or naming (#1385).
	Kind   string `json:"kind,omitempty"`
	Parent string `json:"parent,omitempty"`
	Agent  string `json:"agent,omitempty"`
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
