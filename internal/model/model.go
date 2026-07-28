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
