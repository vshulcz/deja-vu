package search

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// An hour repeats when the clocks go back, and both halves render as the same
// minute — so an hour of conversation reads as a duplicated message. Both
// stamps are right, which is why the reader cannot tell them apart (#1788).
func TestTheRepeatedHourSaysWhichSideItIsOn(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata here")
	}
	at := func(utc string) time.Time {
		ts, perr := time.Parse(time.RFC3339, utc)
		if perr != nil {
			t.Fatal(perr)
		}
		return ts.In(ny)
	}
	s := model.Session{Harness: "claude", Project: "proj", ID: "dst", Messages: []model.Message{
		{Role: "user", Text: "before the change", Time: at("2026-11-01T05:30:00Z")},
		{Role: "assistant", Text: "an hour later", Time: at("2026-11-01T06:30:00Z")},
		{Role: "user", Text: "after it", Time: at("2026-11-01T07:30:00Z")},
	}}
	var b bytes.Buffer
	PrintSession(&b, s)
	out := b.String()
	if strings.Count(out, "01:30 -04:00") != 1 || strings.Count(out, "01:30 -05:00") != 1 {
		t.Errorf("the two halves of the repeated hour are not told apart:\n%s", out)
	}
	if !strings.Contains(out, "02:30") {
		t.Errorf("the turn after the change lost its stamp:\n%s", out)
	}

	// An ordinary session keeps the format it has: this happens twice a year
	// and must not cost every other transcript a wider column.
	plain := model.Session{Harness: "claude", Project: "proj", ID: "p", Messages: []model.Message{
		{Role: "user", Text: "one", Time: at("2026-06-01T05:30:00Z")},
		{Role: "assistant", Text: "two", Time: at("2026-06-01T06:30:00Z")},
	}}
	var pb bytes.Buffer
	PrintSession(&pb, plain)
	if strings.Contains(pb.String(), "-04:00") {
		t.Errorf("an ordinary session gained an offset:\n%s", pb.String())
	}
}

// Two turns at the same instant are not a repeated hour — a burst of messages
// in one minute is ordinary — and a minute that comes back three times still
// says which is which.
func TestRepeatedStampDetection(t *testing.T) {
	base, err := time.Parse(time.RFC3339, "2026-11-01T05:30:00Z")
	if err != nil {
		t.Fatal(err)
	}
	same := repeatedStamps([]model.Message{{Time: base}, {Time: base}, {Time: base}})
	if len(same) != 0 {
		t.Errorf("messages at one instant were called a repeated hour: %v", same)
	}
	// The same wall-clock minute from three different instants: one entry, and
	// every one of them gets the offset when it prints.
	thrice := repeatedStamps([]model.Message{
		{Time: base}, {Time: base.Add(time.Hour)}, {Time: base.Add(2 * time.Hour)},
	})
	if len(thrice) != 0 {
		t.Errorf("UTC has no repeated hour here: %v", thrice)
	}
	if none := repeatedStamps([]model.Message{{}, {}}); len(none) != 0 {
		t.Errorf("sessions without stamps produced %v", none)
	}
}
