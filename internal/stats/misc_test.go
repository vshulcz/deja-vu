package stats

import (
	"encoding/json"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/usage"
	"reflect"
	"strings"
	"testing"
	"time"
)

// On a fresh machine `harnesses` marshalled as [] and `top_projects` as null,
// though both are declared as slices and the envelope is documented as a stable
// shape. A consumer iterating top_projects threw on the first run and worked
// ever after — the worst shape to diagnose (#1649).
func TestEmptyReportHasNoNullCollections(t *testing.T) {
	// The fresh-machine path: Build over no sessions at all.
	r := Build(nil, time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC))
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"harnesses", "top_projects", "monthly"} {
		v, ok := got[key]
		if !ok {
			t.Errorf("%s is absent from the envelope", key)
			continue
		}
		if v == nil {
			t.Errorf("%s is null on an empty report; an empty collection is []", key)
		}
	}
	// The control: the sibling scalars still read as zero rather than absent,
	// and nothing became a string.
	if !strings.Contains(string(b), `"total_sessions":0`) {
		t.Errorf("total_sessions changed shape: %s", b)
	}
}

// Month ticks are drawn one per week column, 13px apart, and a three-letter
// label is wider than one column. Two ticks in adjacent columns overprint on
// the card, which happened whenever the trailing year opened on a partial week
// from the month before.
func TestMonthTicksNeverShareAColumnEdge(t *testing.T) {
	for i := range 400 {
		now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).AddDate(0, 0, i)
		hm := buildHeatmap(map[string]int{}, now)
		for j := 1; j < len(hm.Months); j++ {
			if gap := hm.Months[j].Col - hm.Months[j-1].Col; gap < 2 {
				t.Fatalf("%s: %s(col %d) and %s(col %d) are %d column apart",
					now.Format("2006-01-02"), hm.Months[j-1].Label, hm.Months[j-1].Col,
					hm.Months[j].Label, hm.Months[j].Col, gap)
			}
		}
	}
}

// Dropping the crowded tick must keep the month that owns the columns after it,
// not the sliver of the month before.
func TestCrowdedTickKeepsTheMonthThatOwnsTheStrip(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	hm := buildHeatmap(map[string]int{}, now)
	if len(hm.Months) == 0 {
		t.Fatal("no month ticks")
	}
	if got := hm.Months[0]; got.Label != "Aug" || got.Col != 1 {
		t.Fatalf("first tick = %s(col %d), want Aug(col 1)", got.Label, got.Col)
	}
}

// collectJSONKeys gathers every json field name a type marshals, recursing
// through structs, slices and pointers. time.Time and the like carry no json
// tags on their exported fields, so they contribute nothing.
func collectJSONKeys(t reflect.Type, into map[string]bool) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name != "" {
			into[name] = true
		}
		collectJSONKeys(f.Type, into)
	}
}

// docs/json-output.md is the published contract for `deja stats --json`. This
// pins the emitted key set to it in both directions: a field added to the
// Report without a doc entry fails here, and a documented key that leaves the
// struct fails too. recall.raw_bytes and recall.since were emitted but
// undocumented until this pin caught them.
func TestStatsJSONKeysMatchTheDocumentedContract(t *testing.T) {
	documented := map[string]bool{
		// Report
		"schema_version": true, "total_sessions": true, "total_messages": true,
		"repeat_questions": true, "policy_withheld": true, "harnesses": true,
		"top_projects": true, "monthly": true, "sparkline": true, "date_range": true,
		"longest_session": true, "busiest_day": true, "recall": true,
		"week_recalls": true, "week_bytes": true, "week_injected": true,
		"handoffs_received": true, "agent_credits": true, "week_agent_credits": true,
		"used_not_credited": true, "week_used_not_credited": true,
		"sidecar_size": true, "spans": true, "span_files": true, "preserved_sessions": true,
		// HarnessStats / ProjectStats / MonthStats
		"harness": true, "sessions": true, "messages": true, "project": true, "month": true,
		// DateRangeStats / SessionStat / DayStat
		"start": true, "end": true, "id": true, "title": true, "date": true,
		// recall (usage.Summary)
		"recalls_served": true, "injections": true, "recall_sessions": true,
		"injected_sessions": true, "bytes": true, "injected_bytes": true,
		"raw_bytes": true, "dejavu_moments": true, "tool_lines": true, "fixes_served": true,
		"empty_result_rate": true, "since": true,
		// recall.compaction, documented in docs/json-output.md
		"compaction": true, "captures": true, "measured": true,
		"pending": true, "unmeasured": true, "median_actions": true, "p75_actions": true,
	}
	emitted := map[string]bool{}
	collectJSONKeys(reflect.TypeFor[Report](), emitted)
	// usage.Summary writes its own JSON so a zero timestamp does not print as
	// year 1 (#1874), and a key written that way is invisible to the tag walk
	// above. Marshal a filled one and take the keys from the output.
	filled := usage.Summary{Since: time.Unix(1, 0).UTC()}
	b, err := json.Marshal(filled)
	if err != nil {
		t.Fatal(err)
	}
	var written map[string]any
	if err := json.Unmarshal(b, &written); err != nil {
		t.Fatal(err)
	}
	for k := range written {
		emitted[k] = true
	}

	for k := range emitted {
		if !documented[k] {
			t.Errorf("stats.Report emits %q, missing from docs/json-output.md", k)
		}
	}
	for k := range documented {
		if !emitted[k] {
			t.Errorf("docs/json-output.md lists %q, no longer emitted by stats.Report", k)
		}
	}
}

// The year screen says "N of M questions were asked more than once", so the
// two numbers have to come off one population: the same question twice in one
// session is one question, and what RepeatQuestions refuses to count as a
// question cannot appear in the denominator either.
func TestQuestionSpreadCountsOneQuestionOnce(t *testing.T) {
	q := "why does the scheduler run tasks twice?"
	other := "where does the importer write its watermark?"
	ss := []model.Session{
		{Harness: "claude", Project: "app", ID: "a", Messages: []model.Message{
			{Role: "user", Text: q},
			{Role: "user", Text: q}, // the same question again in one session
			{Role: "assistant", Text: "because the lease is not held?"},
			{Role: "user", Text: "ok?"},         // under the floor
			{Role: "user", Text: "the plan is"}, // no question at all
		}},
		{Harness: "claude", Project: "app", ID: "b", Messages: []model.Message{
			{Role: "user", Text: q},
			{Role: "user", Text: other},
		}},
	}

	distinct, repeated := QuestionSpread(ss)
	if distinct != 2 {
		t.Errorf("distinct questions = %d, want 2 — %q and %q", distinct, q, other)
	}
	if repeated != 1 {
		t.Errorf("repeated questions = %d, want 1 — only %q was asked in both sessions", repeated, q)
	}
	if got := RepeatQuestions(ss); got != repeated {
		t.Errorf("RepeatQuestions says %d and the spread says %d — the screen would show a count out of a smaller total", got, repeated)
	}
}

// Chinese, Japanese and Korean write no whitespace, and the denominator has
// the same floor as the numerator.
func TestQuestionSpreadCountsCJK(t *testing.T) {
	distinct, repeated := QuestionSpread(askedTwice("调度器为什么会重复执行任务？"))
	if distinct != 1 || repeated != 1 {
		t.Errorf("CJK spread = %d distinct, %d repeated; want 1 and 1", distinct, repeated)
	}
	if distinct, repeated = QuestionSpread(askedTwice("好吗？")); distinct != 0 || repeated != 0 {
		t.Errorf("an acknowledgement counted: %d distinct, %d repeated", distinct, repeated)
	}
}

func TestQuestionSpreadIsEmptyWithoutQuestions(t *testing.T) {
	distinct, repeated := QuestionSpread(nil)
	if distinct != 0 || repeated != 0 {
		t.Errorf("no sessions gave %d distinct and %d repeated", distinct, repeated)
	}
}

func TestAskedByAPersonSeparatesQuestionsFromPlumbing(t *testing.T) {
	for _, s := range []string{
		"why does the pool exhaust under load?",
		"сколько по времени займут такие прогоны?",
		"how do we handle the retry budget",
	} {
		if !AskedByAPerson(s) {
			t.Errorf("%q is a question a person asked", s)
		}
	}
	for _, s := range []string{
		"Use the deja recall tool to search for: openclaw harness",
		"Reply with only the raw JSON returned by the tool",
		"<system-reminder>something</system-reminder>",
		"The following tool was executed by the user",
		"Continue if you have next steps, or stop and ask for clarification",
		strings.Repeat("a long pasted report ", 40) + "why is it slow?",
		"",
	} {
		if AskedByAPerson(s) {
			t.Errorf("%q should not count as a question someone asked", s)
		}
	}
}

func TestRepeatQuestionsCountsOnlyRealRepeats(t *testing.T) {
	q := "why does the connection pool exhaust under load?"
	tmpl := "Use the build tool with scope all and report back"
	mk := func(id string, texts ...string) model.Session {
		s := model.Session{ID: id, Harness: "claude"}
		for _, t := range texts {
			s.Messages = append(s.Messages, model.Message{Role: "user", Text: t})
		}
		return s
	}
	ss := []model.Session{mk("a", q, tmpl), mk("b", q, tmpl), mk("c", tmpl)}
	if got := RepeatQuestions(ss); got != 1 {
		t.Fatalf("got %d, want the one question and not the template", got)
	}
}

// `stats --impact` said memory was credited aloud 143 times out of 6,650 and
// nothing else: the 2% folds together injections the model rightly ignored and
// injections it used without saying so, and only the second is a problem worth
// wording changes (#3079). This counts the second, from the id the reply
// carries.
func TestUsedNotCreditedCountsTheRepliesThatNameASessionAndSayNothing(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	old := now.Add(-30 * 24 * time.Hour)
	fresh := now.Add(-24 * time.Hour)
	ss := []model.Session{{
		Harness: "claude", ID: "s1",
		Messages: []model.Message{
			// Credited: counted by AgentCredits, not here.
			{Role: "assistant", Text: "déjà vu: the retry budget (claude, Aug 2, deja:abc12345) — reusing it.", Time: fresh},
			// Used and not said: the id is in the reply, the line is not.
			{Role: "assistant", Text: "the budget stays at five, from deja:abc12345", Time: fresh},
			{Role: "assistant", Text: "as in deja:99ff0011, one shard", Time: old},
			// Nothing to do with a recall.
			{Role: "assistant", Text: "running the tests now", Time: fresh},
			// A person quoting an id is not the agent crediting itself.
			{Role: "user", Text: "look at deja:abc12345", Time: fresh},
			// "deja:" with nothing that looks like a session id is prose.
			{Role: "assistant", Text: "the deja: prefix is what the block uses", Time: fresh},
		},
	}}

	total, week := UsedNotCredited(ss, now)
	if total != 2 {
		t.Fatalf("total = %d, want the two replies that named a session without the line", total)
	}
	if week != 1 {
		t.Fatalf("week = %d, want only the recent one", week)
	}
	// And the credited reply still counts as credited, in the other counter.
	if credits, _ := AgentCredits(ss, now); credits != 1 {
		t.Fatalf("credits = %d, want the one reply that said the line", credits)
	}
}

// A session stamped 22:00 UTC is 01:00 tomorrow for a reader east of UTC. The
// brief has rendered in the reader's zone since #767; stats kept using the
// timestamp's, so two screens of one tool dated the same session a day apart
// (#849).
func TestDateRangeUsesTheReadersZone(t *testing.T) {
	east := time.FixedZone("east", 3*60*60)
	stamp := time.Date(2026, 7, 1, 22, 0, 0, 0, time.UTC)
	ss := []model.Session{{
		ID: "late", Harness: "claude", Project: "p",
		Started: stamp, Updated: stamp,
		Messages: []model.Message{{Role: "user", Text: "late evening session", Time: stamp}},
	}}

	local := stamp.In(east).Format("2006-01-02")
	if local != "2026-07-02" {
		t.Fatalf("fixture zone is wrong: %s", local)
	}

	// Build takes "now", and the zone deja renders in is that instant's.
	got := Build(ss, time.Date(2026, 7, 5, 12, 0, 0, 0, east))
	if got.DateRange.Start != local || got.DateRange.End != local {
		t.Errorf("range = %s → %s, want %s on both ends", got.DateRange.Start, got.DateRange.End, local)
	}
	if got.BusiestDay.Date != local {
		t.Errorf("busiest day = %s, want %s", got.BusiestDay.Date, local)
	}

	// West of UTC the same instant falls on the previous day, and the range
	// has to follow the reader there too.
	west := time.FixedZone("west", -8*60*60)
	if got := Build(ss, time.Date(2026, 7, 5, 12, 0, 0, 0, west)); !strings.HasPrefix(got.DateRange.Start, "2026-07-01") {
		t.Errorf("west of UTC: range starts %s, want 2026-07-01", got.DateRange.Start)
	}
}
