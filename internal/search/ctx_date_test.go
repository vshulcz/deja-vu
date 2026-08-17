package search

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Every surface that prints a session's date prints the reader's — `deja show`,
// `deja last`, recall, resources/list — because an agent quoting it must not
// name a different day from the one on the user's screen. The ctx header
// formatted in UTC instead, so a reader far enough from it saw ctx name one day
// and everything else name the next for the same session. ctx is the briefing
// written to be handed to another agent, which makes its header the date most
// likely to be repeated back.
func TestContextHeaderDatesInTheReadersZone(t *testing.T) {
	// Fourteen hours ahead, so a late-UTC stamp lands on the next local day.
	zone, err := time.LoadLocation("Pacific/Kiritimati")
	if err != nil {
		t.Skip("zone database unavailable")
	}
	prev := time.Local
	time.Local = zone
	t.Cleanup(func() { time.Local = prev })

	updated := time.Date(2026, 6, 15, 20, 30, 0, 0, time.UTC)
	utcDay := updated.Format("2006-01-02")
	localDay := updated.In(zone).Format("2006-01-02")
	if utcDay == localDay {
		t.Fatalf("the fixture no longer straddles midnight (%s), so it cannot tell the two apart", utcDay)
	}

	s := model.Session{
		Harness: "claude", Project: "work", ID: "lt1",
		Updated:  updated,
		Messages: []model.Message{{Role: "user", Text: "the zibbleflux deadlock", Time: updated}},
	}
	// The prompt-hook digest carries the same date on the surface an agent
	// reads most often, so it is asserted here rather than left to drift.
	if d := AutoRecallDigestFor([]model.Session{s}, 4000, nil); d != "" {
		if strings.Contains(d, utcDay) && !strings.Contains(d, localDay) {
			t.Errorf("the prompt digest names the UTC day %s, not the reader's %s\n%s", utcDay, localDay, d)
		}
	}

	var b bytes.Buffer
	PrintContext(&b, s, "zibbleflux")
	head := strings.SplitN(b.String(), "\n", 2)[0]

	if strings.Contains(head, utcDay) {
		t.Errorf("the ctx header names the UTC day %s; the reader's screen says %s\n%s", utcDay, localDay, head)
	}
	if !strings.Contains(head, localDay) {
		t.Errorf("the ctx header does not name the reader's day %s\n%s", localDay, head)
	}
}
