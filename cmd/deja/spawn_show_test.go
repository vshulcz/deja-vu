package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// After a hit on a subagent's session, the line that matters is which session
// asked for it; after a hit on the parent, which sessions it spawned (#1385).
func TestShowNamesTheSessionsAroundASpawn(t *testing.T) {
	old := ChildrenOfSession
	t.Cleanup(func() { ChildrenOfSession = old })
	ChildrenOfSession = func(dir, id string) ([]model.Session, error) {
		if id != "01a00a65-7a72-7102-9574-0de51ab0d0ee" {
			return nil, nil
		}
		return []model.Session{{ID: "01a00a65-child-one"}, {ID: "01a00a65-child-two"}}, nil
	}

	var w bytes.Buffer
	printSpawnEdges(&w, "", model.Session{
		ID:     "01a00a65-child-one",
		Kind:   "subagent_fork",
		Parent: "01a00a65-7a72-7102-9574-0de51ab0d0ee",
		Agent:  "general-purpose",
	})
	got := w.String()
	if !strings.Contains(got, "spawned from 01a00a65") || !strings.Contains(got, "general-purpose") {
		t.Errorf("the child does not name what asked for it:\n%s", got)
	}

	w.Reset()
	printSpawnEdges(&w, "", model.Session{ID: "01a00a65-7a72-7102-9574-0de51ab0d0ee"})
	got = w.String()
	if !strings.Contains(got, "spawned 2 sessions") {
		t.Errorf("the parent does not list its children:\n%s", got)
	}

	// A subagent whose harness recorded no parent gets a line about what it is
	// and no guess about who started it.
	w.Reset()
	printSpawnEdges(&w, "", model.Session{ID: "01a00a65-orphan", Kind: "subagent"})
	got = w.String()
	if !strings.Contains(got, "records no parent") {
		t.Errorf("an orphan subagent says nothing about itself:\n%s", got)
	}
	if strings.Contains(got, "spawned from") {
		t.Errorf("an edge was invented:\n%s", got)
	}

	// An ordinary session gains no line at all.
	w.Reset()
	printSpawnEdges(&w, "", model.Session{ID: "plain-session"})
	if w.Len() != 0 {
		t.Errorf("a session nobody spawned still printed:\n%s", w.String())
	}
}
