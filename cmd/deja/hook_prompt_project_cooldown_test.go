package main

import (
	"os"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func sessionsWithIDs(ids ...string) []model.Session {
	out := make([]model.Session, 0, len(ids))
	for _, id := range ids {
		out = append(out, model.Session{ID: id})
	}
	return out
}

// The per-prompt cooldown only ever counted the agent session it was called
// from, so every new agent session started blank and the same past session was
// served again. Measured over six weeks of a real log: 937 injections drawn
// from 74 distinct sessions, 92% repeats, ten sessions carrying 80% of the
// total (#2038).
//
// This pins the part that was missing rather than the ranking: what one agent
// session was shown has to be visible to the next one working in the same
// project.
func TestTheCooldownOutlivesTheAgentSession(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	const project = "deja-vu"

	rememberInjectedFor(dir, "agent-one", project, sessionsWithIDs("marathon", "other"))

	// The next agent session knows nothing of the first one — that is the bug.
	if got := recentlyInjected(dir, "agent-two", injectionCooldown); len(got) != 0 {
		t.Fatalf("a fresh agent session already had a cooldown: %v", got)
	}

	got := recentlyInjectedInProject(dir, project, injectionCooldown)
	if !got["marathon"] || !got["other"] {
		t.Errorf("the project cooldown lost what the previous agent session was shown: %v", got)
	}

	// A different project is not the repetition being fixed, and must not be
	// suppressed by it.
	if other := recentlyInjectedInProject(dir, "some-other-repo", injectionCooldown); len(other) != 0 {
		t.Errorf("the cooldown reached into another project: %v", other)
	}
}

// Lines written before the project field existed carry three fields. They have
// to be read past rather than guessed at, or every old entry would match the
// first project asked about.
func TestOldSeenLinesCarryNoProject(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	if err := os.WriteFile(dir+".hookseen", []byte("agent-one marathon 2026-08-01T00:00:00Z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := recentlyInjectedInProject(dir, "deja-vu", injectionCooldown); len(got) != 0 {
		t.Errorf("a line with no project matched a project: %v", got)
	}
	// The per-session cooldown still reads it, so nothing regresses for the
	// agent session that wrote it.
	if got := recentlyInjected(dir, "agent-one", injectionCooldown); !got["marathon"] {
		t.Errorf("the old line stopped working for its own agent session: %v", got)
	}
}

// The window is a window: a session shown long enough ago comes back, because
// the same past work answers many questions across a day.
func TestTheProjectCooldownIsAWindowNotABan(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	const project = "deja-vu"
	rememberInjectedFor(dir, "agent-one", project, sessionsWithIDs("marathon"))
	for i := 0; i < injectionCooldown; i++ {
		rememberInjectedFor(dir, "agent-one", project, sessionsWithIDs("filler"))
	}
	if got := recentlyInjectedInProject(dir, project, injectionCooldown); got["marathon"] {
		t.Error("a session pushed out of the window is still being skipped")
	}
}

func TestTheSeenLineStaysFieldSeparated(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	rememberInjectedFor(dir, "agent-one", "deja-vu", sessionsWithIDs("marathon"))
	b, err := os.ReadFile(dir + ".hookseen")
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(b))
	if n := len(strings.Fields(line)); n != 4 {
		t.Fatalf("the line has %d fields, not four: %q", n, line)
	}
}
