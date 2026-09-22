package index

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A question that names one day is a different question from one that asks
// about a stretch of days: "what did I buy ten days ago" points at a session,
// "how much did I spend last month" counts over many. Only the first may move
// the ranking, because for the second the session nearest that day is not the
// answer — measured as four demotions on LongMemEval when spans were included.
func TestOnlyAQuestionNamingOneDayResolvesToADay(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC) // a Tuesday
	for _, c := range []struct {
		q    string
		want string // "" means it must not resolve
	}{
		{"what kitchen appliance did I buy 10 days ago", "2026-09-12"},
		{"I mentioned cooking something a couple of days ago", "2026-09-20"},
		{"what did I fix two weeks ago", "2026-09-08"},
		{"what did I read yesterday", "2026-09-21"},
		{"who did I meet with during the lunch last tuesday", "2026-09-15"},
		{"what did I work on last friday", "2026-09-18"},
		{"how many hours of jogging did I do last week", ""},
		// Counting questions stand down even when they do name a day: the
		// session nearest it holds one of the numbers being added up, not the
		// answer. Both of these are ranked right today and the guard is what
		// keeps them there.
		{"how much cashback did I earn at SaveMart last thursday", ""},
		{"how many pieces of writing have I completed since I started three weeks ago", ""},
		{"how many days ago did I buy a smoker", ""},
		{"how many total pieces of writing have I completed since I started", ""},
		{"what did I do in may", ""},
		{"what did I change", ""},
	} {
		got, ok := pointInTime(c.q, now)
		if c.want == "" {
			if ok {
				t.Errorf("%q resolved to %s, and it names no single day", c.q, got.Format("2006-01-02"))
			}
			continue
		}
		if !ok {
			t.Errorf("%q resolved to nothing, want %s", c.q, c.want)
			continue
		}
		if have := got.Format("2006-01-02"); have != c.want {
			t.Errorf("%q resolved to %s, want %s", c.q, have, c.want)
		}
	}
}

// "last tuesday" asked on a Tuesday means the Tuesday before, not today: a
// week back, not nothing back.
func TestLastWeekdayNeverResolvesToToday(t *testing.T) {
	tue := time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC)
	if got := lastWeekday(tue, "tuesday"); !got.Equal(tue.AddDate(0, 0, -7)) {
		t.Errorf("last tuesday on a Tuesday = %s, want a week back", got.Format("2006-01-02"))
	}
	if got := lastWeekday(tue, "sunday"); got.Weekday() != time.Sunday || !got.Before(tue) {
		t.Errorf("last sunday = %s (%s), want the most recent past Sunday", got.Format("2006-01-02"), got.Weekday())
	}
	if got := lastWeekday(tue, "sunday"); !got.Equal(tue.AddDate(0, 0, -2)) {
		t.Errorf("last sunday from Tuesday = %s, want two days back", got.Format("2006-01-02"))
	}
}

func sessionOn(id string, day time.Time) model.Session {
	return model.Session{ID: id, Started: day}
}

// Outside the span the pool covers, every candidate is equally far from the
// day, so the proximity order degenerates into "newest first" — an answer to a
// question nobody asked. The rule has to stand down there.
func TestTheDayRuleStandsDownOutsideThePoolsOwnSpan(t *testing.T) {
	base := time.Date(2023, 5, 10, 0, 0, 0, 0, time.UTC)
	ss := []model.Session{
		sessionOn("a", base),
		sessionOn("b", base.AddDate(0, 0, 5)),
		sessionOn("c", base.AddDate(0, 0, 10)),
	}
	if dayInsideRange(ss, base.AddDate(3, 0, 0)) {
		t.Error("a day three years past the newest session counts as inside the span")
	}
	if dayInsideRange(ss, base.AddDate(0, 0, -30)) {
		t.Error("a day a month before the oldest session counts as inside the span")
	}
	if !dayInsideRange(ss, base.AddDate(0, 0, 5)) {
		t.Error("a day the pool itself covers counts as outside the span")
	}
	// And the reordering itself leaves the pool alone out there.
	out := rerankByDayProximity(ss, base.AddDate(3, 0, 0))
	for i := range ss {
		if out[i].ID != ss[i].ID {
			t.Fatalf("order changed for a day outside the span: %s", sessionIDs(out))
		}
	}
}

// The day is a hint fused with the pool's order, not a sort key: a session the
// words put first does not lose its place to a closer date alone, but a closer
// date lifts a session the words ranked below it.
func TestTheClosestDayLiftsWithoutOverridingTheWords(t *testing.T) {
	day := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	ss := []model.Session{
		sessionOn("first-by-words", day.AddDate(0, 0, 9)),
		sessionOn("second-by-words", day.AddDate(0, 0, 8)),
		sessionOn("on-the-day", day),
		sessionOn("far", day.AddDate(0, 0, 40)),
	}
	got := sessionIDs(rerankByDayProximity(ss, day))
	if got[0] != "first-by-words" && got[0] != "on-the-day" {
		t.Fatalf("head is %q: the fusion should keep one of the two votes on top, got %v", got[0], got)
	}
	// The session sitting on the day must end up above the one the words put
	// last, and above its own starting place.
	pos := map[string]int{}
	for i, id := range got {
		pos[id] = i
	}
	if pos["on-the-day"] >= 2 {
		t.Errorf("the session on the named day stayed at %d: %v", pos["on-the-day"], got)
	}
	if pos["far"] != 3 {
		t.Errorf("the session 40 days off moved to %d: %v", pos["far"], got)
	}
}

// A session with no timestamp at all must not be treated as if it happened on
// the named day; it keeps its place from the words.
func TestASessionWithNoTimeIsNotNearAnyDay(t *testing.T) {
	day := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	ss := []model.Session{
		sessionOn("dated-far", day.AddDate(0, 0, 20)),
		{ID: "undated"},
		sessionOn("on-the-day", day),
	}
	got := sessionIDs(rerankByDayProximity(ss, day))
	if got[len(got)-1] != "undated" {
		t.Errorf("the undated session did not sink: %v", got)
	}
}
