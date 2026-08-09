package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSessionSetSource(t *testing.T) {
	local := Session{Project: "agent-fabric"}
	local.SetSource("workstation")
	if local.Source.Origin != "local" || local.Source.Instance != "workstation" {
		t.Fatalf("local source = %#v", local.Source)
	}
	imported := Session{Project: "imported:agent-fabric"}
	imported.SetSource("must-not-leak")
	if imported.Source.Origin != "imported" || imported.Source.Instance != "" {
		t.Fatalf("imported source = %#v", imported.Source)
	}
}

func TestUnsetSourceIsOmittedInsteadOfEmittedEmpty(t *testing.T) {
	b, err := json.Marshal(Session{ID: "x", Harness: "claude"})
	if err != nil || strings.Contains(string(b), `"source"`) {
		t.Fatalf("session JSON = %s, err = %v", b, err)
	}
}

func TestTouchSetsAndWidensWindow(t *testing.T) {
	base := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)

	var s Session
	s.Touch(base)
	if !s.Started.Equal(base) || !s.Updated.Equal(base) {
		t.Fatalf("first touch: started=%v updated=%v", s.Started, s.Updated)
	}

	earlier := base.Add(-time.Hour)
	s.Touch(earlier)
	if !s.Started.Equal(earlier) || !s.Updated.Equal(base) {
		t.Fatalf("earlier touch moved started only: started=%v updated=%v", s.Started, s.Updated)
	}

	later := base.Add(time.Hour)
	s.Touch(later)
	if !s.Started.Equal(earlier) || !s.Updated.Equal(later) {
		t.Fatalf("later touch moved updated only: started=%v updated=%v", s.Started, s.Updated)
	}

	// A time already inside the window changes nothing.
	s.Touch(base)
	if !s.Started.Equal(earlier) || !s.Updated.Equal(later) {
		t.Fatalf("inside-window touch changed the window: started=%v updated=%v", s.Started, s.Updated)
	}
}

func TestTouchIgnoresZero(t *testing.T) {
	s := Session{Started: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	before := s.Started
	s.Touch(time.Time{})
	if !s.Started.Equal(before) || !s.Updated.IsZero() {
		t.Fatalf("zero touch changed state: started=%v updated=%v", s.Started, s.Updated)
	}
}
