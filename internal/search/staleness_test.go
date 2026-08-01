package search

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func stalenessSession(id, project, text string, updated time.Time) model.Session {
	return model.Session{
		ID: id, Harness: "claude", Project: project, Updated: updated,
		Messages: []model.Message{{Role: "user", Text: text, Time: updated}},
	}
}

func TestMarkEarlierAttempts(t *testing.T) {
	now := time.Now()
	old := stalenessSession("old1", "api", "connection pool exhausted under load, pgx acquire leak", now.AddDate(0, 0, -30))
	fresh := stalenessSession("new1", "api", "connection pool exhausted under load fixed with acquire timeout", now.AddDate(0, 0, -1))
	other := stalenessSession("oth1", "web", "connection pool exhausted under load in the browser", now.AddDate(0, 0, -30))

	hits, err := Run([]model.Session{old, fresh, other}, Options{Query: "connection pool exhausted", All: true})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Hit{}
	for _, h := range hits {
		byID[h.Session.ID] = h
	}
	if byID["old1"].Superseded == "" {
		t.Fatalf("old same-project hit not marked: %+v", byID["old1"])
	}
	if byID["old1"].Superseded != fresh.Updated.Format("2006-01-02") {
		t.Fatalf("superseded date = %q", byID["old1"].Superseded)
	}
	if byID["new1"].Superseded != "" {
		t.Fatalf("newest hit marked superseded: %+v", byID["new1"])
	}
	if byID["oth1"].Superseded != "" {
		t.Fatalf("different-project hit marked superseded: %+v", byID["oth1"])
	}
}

func TestMarkEarlierAttemptsNeedsOverlap(t *testing.T) {
	now := time.Now()
	old := stalenessSession("old2", "api", "jwt refresh rotation broke login jwks cache stale", now.AddDate(0, 0, -30))
	fresh := stalenessSession("new2", "api", "docker jwt oom killed the build runner memory limit", now.AddDate(0, 0, -1))
	hits, err := Run([]model.Session{old, fresh}, Options{Query: "jwt", All: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Superseded != "" {
			t.Fatalf("low-overlap hit marked superseded: %+v", h)
		}
	}
}

func TestPrintShowsEarlierAttempt(t *testing.T) {
	now := time.Now()
	old := stalenessSession("old3", "api", "rate limiter lets bursts through token bucket clock", now.AddDate(0, 0, -20))
	fresh := stalenessSession("new3", "api", "rate limiter lets bursts through fixed with monotonic token bucket", now.AddDate(0, 0, -1))
	hits, err := Run([]model.Session{old, fresh}, Options{Query: "rate limiter bursts", All: true})
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	Print(&b, hits, Options{Query: "rate limiter bursts"})
	if !strings.Contains(b.String(), "earlier attempt") {
		t.Fatalf("print output missing earlier-attempt note: %q", b.String())
	}
}

// A day of notes is one session, so two days about the same decision overlap
// by construction — and the reader was told most of their own decision log
// was work the project had moved past (#863).
func TestNotesAreNotEarlierAttempts(t *testing.T) {
	now := time.Now()
	oldNote := stalenessSession("deja-day1-api", "api", "decided to keep the connection pool at 50", now.AddDate(0, 0, -30))
	oldNote.Harness = notesHarness
	newNote := stalenessSession("deja-day2-api", "api", "decided to keep the connection pool at 50 after review", now.AddDate(0, 0, -1))
	newNote.Harness = notesHarness
	// A transcript in the same project still supersedes another transcript:
	// the exclusion is about notes, not about the whole project.
	oldSess := stalenessSession("s-old", "api", "decided to keep the connection pool at 50", now.AddDate(0, 0, -30))
	// A transcript whose only newer rival is a note: writing a note about a
	// decision is not the project moving past the session that made it.
	lonely := stalenessSession("s-lonely", "web", "decided to keep the connection pool at 50", now.AddDate(0, 0, -30))
	lonelyNote := stalenessSession("deja-day2-web", "web", "decided to keep the connection pool at 50 after review", now.AddDate(0, 0, -1))
	lonelyNote.Harness = notesHarness
	newSess := stalenessSession("s-new", "api", "decided to keep the connection pool at 50 after review", now.AddDate(0, 0, -1))

	hits, err := Run([]model.Session{oldNote, newNote, oldSess, newSess, lonely, lonelyNote}, Options{Query: "connection pool", All: true})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Hit{}
	for _, h := range hits {
		byID[h.Session.ID] = h
	}
	if got := byID["deja-day1-api"].Superseded; got != "" {
		t.Errorf("note labelled an earlier attempt: %q", got)
	}
	if byID["s-old"].Superseded == "" {
		t.Error("an older transcript stopped being marked")
	}
	if got := byID["s-lonely"].Superseded; got != "" {
		t.Errorf("transcript marked as superseded by a note: %q", got)
	}
}
