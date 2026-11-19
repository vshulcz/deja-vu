package search

import (
	"bytes"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"strings"
	"testing"
	"time"
	"unicode"
)

// longSessionAround builds a session whose query word is mentioned once in
// passing at the top, then not again until the exchange at the end that
// actually settles it, with enough conversation between the two to fill the
// context budget several times over.
func longSessionAround(turns int) model.Session {
	msgs := make([]model.Message, 0, 1+2*turns+2)
	msgs = append(msgs, model.Message{Role: "user", Text: "before we start, remind me about retries later"})
	for range turns {
		msgs = append(msgs,
			model.Message{Role: "user", Text: "unrelated question " + strings.Repeat("x", 200)},
			model.Message{Role: "assistant", Text: "unrelated answer " + strings.Repeat("y", 200)},
		)
	}
	msgs = append(msgs,
		model.Message{Role: "user", Text: "so what did we settle on for retries?"},
		model.Message{Role: "assistant", Text: "we decided to cap retries at 3 and give up after that"},
	)
	return model.Session{Harness: "claude", Project: "p", ID: "s1", Messages: msgs}
}

// The context is what gets handed to another agent, so it has to carry the
// turns that answer the query. Anchoring the budget on the first match let a
// word said once at the top pin the window there and spend all 8KB on the
// conversation that followed it.
func TestContextCarriesTheExchangeThatAnswers(t *testing.T) {
	var b bytes.Buffer
	PrintContext(&b, longSessionAround(60), "retries")
	if out := b.String(); !strings.Contains(out, "cap retries at 3") {
		t.Errorf("the turn that answers the query is missing from %d bytes of context", len(out))
	}
}

// The mirror image: the exchange that settles the question is at the top and
// the tail only mentions it in passing. A fix that simply favours the last
// match would lose the answer here instead.
func TestContextCarriesAnEarlyExchangeOverALaterMention(t *testing.T) {
	msgs := make([]model.Message, 0, 123)
	msgs = append(msgs,
		model.Message{Role: "user", Text: "what should we do about retries?"},
		model.Message{Role: "assistant", Text: "we decided to cap retries at 3 and give up after that"},
	)
	for range 60 {
		msgs = append(msgs,
			model.Message{Role: "user", Text: "unrelated question " + strings.Repeat("x", 200)},
			model.Message{Role: "assistant", Text: "unrelated answer " + strings.Repeat("y", 200)},
		)
	}
	msgs = append(msgs, model.Message{Role: "user", Text: "anyway, back to retries some other time"})

	var b bytes.Buffer
	PrintContext(&b, model.Session{Harness: "claude", Project: "p", ID: "s1", Messages: msgs}, "retries")
	if out := b.String(); !strings.Contains(out, "cap retries at 3") {
		t.Errorf("the turn that answers the query is missing from %d bytes of context", len(out))
	}
}

func TestContextWithNoQueryIncludesTailOfLongSession(t *testing.T) {
	var b bytes.Buffer
	PrintContext(&b, longSessionAround(60), "")
	out := b.String()
	if !strings.Contains(out, "before we start, remind me about retries later") {
		t.Errorf("opening turn missing from empty-query overview:\n%s", out)
	}
	if !strings.Contains(out, "cap retries at 3") {
		t.Errorf("tail resolution missing from empty-query overview:\n%s", out)
	}
	if !strings.Contains(out, "earlier turns omitted]") {
		t.Errorf("expected omission marker between head and tail in empty-query overview:\n%s", out)
	}
}

// Rendering a digest ran two regexes over every line of every turn, so a
// 30000-turn session spent 22.6 s producing 8 KB (#1742). Counting how many
// lines reach the engine says that directly, and says it the same on any
// machine — a wall-clock bound does not.
func TestOrdinaryProseNeverReachesTheRegexpEngine(t *testing.T) {
	build := func(n int) model.Session {
		s := model.Session{Harness: "claude", ID: "big", Project: "proj", Updated: time.Now()}
		for i := range n {
			role := "user"
			if i%2 == 1 {
				role = "assistant"
			}
			s.Messages = append(s.Messages, model.Message{
				Role: role,
				Text: fmt.Sprintf("shard %d\n", i) + strings.Repeat("payload words here\n", 40),
			})
		}
		return s
	}

	before := toolDumpEngineCalls.Load()
	var out bytes.Buffer
	PrintContext(&out, build(2000), "grumbleflux")
	if got := toolDumpEngineCalls.Load() - before; got != 0 {
		t.Errorf("%d lines of ordinary prose were handed to the regexp engine", got)
	}
	if out.Len() == 0 || out.Len() > 9000 {
		t.Fatalf("digest is %d bytes", out.Len())
	}

	// A line that could match still reaches it — the prefilter decides nothing
	// on its own.
	s := model.Session{Harness: "claude", ID: "dump", Project: "proj", Updated: time.Now(),
		Messages: []model.Message{{Role: "user", Text: "goroutine 1 [running]:\nnpm ERR! code E404\nplain line"}}}
	before = toolDumpEngineCalls.Load()
	var dump bytes.Buffer
	PrintContext(&dump, s, "goroutine")
	if got := toolDumpEngineCalls.Load() - before; got != 2 {
		t.Errorf("lines carrying a tool-dump literal reached the engine %d times, want 2", got)
	}
}

// `deja ctx <id>` on a session holding one very long message printed 8 KB that
// ended mid-word, dropped the final turn and said nothing, so an agent took it
// for the whole session (#5392).
func TestACutContextSaysWhatItLeftOut(t *testing.T) {
	long := strings.Repeat("filler ", 50_000) // ~350 KB in one message
	s := model.Session{Harness: "claude", ID: "sess-5392", Project: "app", Updated: time.Now(),
		Messages: []model.Message{
			{Role: "user", Text: "here is the build log\n" + long},
			{Role: "assistant", Text: "the root cause is the stale lockfile"},
		}}
	var b bytes.Buffer
	PrintContext(&b, s, "")
	out := b.String()
	if len(out) > ContextBudget+200 {
		t.Errorf("the digest grew to %d bytes", len(out))
	}
	if !strings.Contains(out, "this turn is cut here") || !strings.Contains(out, "`deja show sess-5392`") {
		t.Errorf("the cut turn is not marked:\n%s", out[len(out)-400:])
	}
	if !strings.Contains(out, "filler …\n[this turn is cut here") {
		t.Errorf("the cut fell mid-word:\n%s", out[len(out)-400:])
	}
	if !strings.Contains(out, "1 of 2 turns shown, the last turn not among them") {
		t.Errorf("the dropped final turn is not reported:\n%s", out[len(out)-400:])
	}

	// The control: a session that fits is printed with no marker at all.
	small := s
	small.Messages = []model.Message{{Role: "user", Text: "why does it fail"}, {Role: "assistant", Text: "the stale lockfile"}}
	b.Reset()
	PrintContext(&b, small, "")
	if strings.Contains(b.String(), "turns shown") || strings.Contains(b.String(), "cut here") {
		t.Errorf("a whole session was marked as cut:\n%s", b.String())
	}
}

// A window that keeps the final turn but leaves earlier ones out says how many
// it showed, and does not call the last one missing.
func TestAWindowThatLeavesOutEarlierTurnsCountsThem(t *testing.T) {
	msgs := make([]model.Message, 0, 41)
	for range 40 {
		msgs = append(msgs, model.Message{Role: "user", Text: strings.Repeat("an ordinary question about nothing much ", 8)})
	}
	msgs = append(msgs, model.Message{Role: "assistant", Text: "the quokkabloom retry is capped at three"})
	s := model.Session{Harness: "codex", ID: "sess-window", Messages: msgs}
	var b bytes.Buffer
	PrintContext(&b, s, "quokkabloom")
	out := b.String()
	if !strings.Contains(out, "quokkabloom retry is capped") {
		t.Fatalf("the matched turn is not in the window:\n%s", out)
	}
	if !strings.Contains(out, " of 41 turns shown — `deja show sess-window` prints every turn]") {
		t.Errorf("the turns left out are not counted:\n%s", out[max(0, len(out)-300):])
	}
	if strings.Contains(out, "not among them") {
		t.Errorf("the final turn was shown and still reported missing")
	}
}

// Every user turn was kept whatever it held, so a session whose transcript
// carries the harness talking to itself — Claude Code's teammate messages,
// task notifications, idle-notification JSON — spent the context budget on
// that and never reached the turns about the question. On this machine's
// index, ten real questions came back 38% envelope, three of them 89% (#3323).
func TestContextSkipsTurnsThatAreOnlyTheHostTalking(t *testing.T) {
	long := strings.Repeat("Review of the damaged-index JSON fix, point four. ", 120)
	s := model.Session{Harness: "claude", Project: "p", ID: "id", Messages: []model.Message{
		{Role: "user", Text: "Another Claude session sent a message:\n<teammate-message teammate_id=\"rev354\">\n" + long + "\n</teammate-message>"},
		{Role: "user", Text: "<teammate-message teammate_id=\"rev354\">\n{\"type\":\"idle_notification\",\"from\":\"rev354\"}\n</teammate-message>"},
		{Role: "user", Text: "follow HERMES_HOME, the variable Hermes itself reads"},
		{Role: "assistant", Text: "the hermes reader now takes HERMES_HOME before the profiles root"},
	}}

	var b bytes.Buffer
	PrintContext(&b, s, "HERMES_HOME")
	got := b.String()
	if strings.Contains(got, "idle_notification") {
		t.Errorf("an idle notification is in the context handed to another agent:\n%s", got)
	}
	if strings.Contains(got, "teammate_id") {
		t.Errorf("a teammate envelope is in the context handed to another agent:\n%s", got)
	}
	if !strings.Contains(got, "HERMES_HOME") {
		t.Errorf("the turns about the question never made it:\n%s", got)
	}
}

// A turn that is the person's words with an envelope appended is the person's
// turn and stays whole — the words are what the reader asked about.
func TestContextKeepsATurnThatHasWordsBesideTheEnvelope(t *testing.T) {
	s := model.Session{Harness: "claude", Project: "p", ID: "id", Messages: []model.Message{
		{Role: "user", Text: "why does the retry loop drop the last attempt\n<system-reminder>the file was edited</system-reminder>"},
		{Role: "assistant", Text: "because the counter is compared with < instead of <="},
	}}
	var b bytes.Buffer
	PrintContext(&b, s, "retry loop")
	if !strings.Contains(b.String(), "why does the retry loop drop the last attempt") {
		t.Errorf("the person's words went with the envelope:\n%s", b.String())
	}
}

// The fallback overview, printed when no turn carries the whole query, is
// context too: it must not be an agent's idle notifications either.
func TestContextOverviewSkipsTheHostsOwnTurns(t *testing.T) {
	s := model.Session{Harness: "claude", Project: "p", ID: "id", Messages: []model.Message{
		{Role: "user", Text: "<task-notification>agent qa-zed2 finished</task-notification>"},
		{Role: "user", Text: "index the copied Zed store and compare the counts"},
	}}
	var b bytes.Buffer
	PrintContext(&b, s, "a query that matches nothing at all here")
	if strings.Contains(b.String(), "task-notification") {
		t.Errorf("the overview is the host's own turn:\n%s", b.String())
	}
}

// The strip must not take a person's words with it. An unclosed tag runs to
// the end of the text under the prompt hook's rule, and a person who quotes a
// tag name in a question opens one without closing it — the review of #3323
// found both, on the shapes Codex and Copilot write.
func TestContextKeepsTheQuestionUnderAnUnclosedTag(t *testing.T) {
	s := model.Session{Harness: "codex", Project: "p", ID: "id", Messages: []model.Message{
		{Role: "user", Text: "<environment_context>\n  <cwd>/repo</cwd>\nCan you fix the failing test in parser_test.go?"},
		{Role: "user", Text: "why does <system-reminder> show up in my prompt when I did not add it?"},
	}}
	var b bytes.Buffer
	PrintContext(&b, s, "parser_test.go")
	got := b.String()
	if !strings.Contains(got, "Can you fix the failing test in parser_test.go?") {
		t.Errorf("the question under an unclosed tag was cut:\n%s", got)
	}
	if !strings.Contains(got, "show up in my prompt when I did not add it?") {
		t.Errorf("a question that names a tag lost its words:\n%s", got)
	}
}

// Two different envelopes standing apart are two blocks, not one: the words
// between them are a person's. And what a nested block leaves behind — an
// orphan closing tag, Cursor's wrapper — does not reach the agent reading the
// context (second review of #3323).
func TestContextKeepsWordsBetweenTwoEnvelopes(t *testing.T) {
	s := model.Session{Harness: "claude", Project: "p", ID: "id", Messages: []model.Message{
		{Role: "user", Text: "<meta>\nsome session meta\n</meta>\nI wanted to also ask: why did the deploy fail last night?\n<deja-recall>\nan old block\n</deja-recall>"},
		{Role: "user", Text: "<additional_data>\nsome file dump\n<attached_files>\nfile1.go\n</attached_files>\n</additional_data>\nplease review the retry loop off-by-one"},
		{Role: "user", Text: "<user_query>\nwhy is the retry loop dropping the last attempt\n</user_query>"},
	}}
	var b bytes.Buffer
	PrintContext(&b, s, "deploy")
	got := b.String()
	if !strings.Contains(got, "why did the deploy fail last night?") {
		t.Errorf("the sentence between two envelopes was eaten:\n%s", got)
	}
	if strings.Contains(got, "</additional_data>") || strings.Contains(got, "<user_query>") {
		t.Errorf("a bare tag reached the context:\n%s", got)
	}
	if !strings.Contains(got, "why is the retry loop dropping the last attempt") {
		t.Errorf("the words inside Cursor's wrapper are gone:\n%s", got)
	}
}

// Naming a tag is not writing one: a sentence that quotes Cursor's wrapper
// keeps its words, where a global strip of the bare tag gutted it (third
// review of #3323).
func TestContextKeepsASentenceThatNamesTheWrapper(t *testing.T) {
	s := model.Session{Harness: "claude", Project: "p", ID: "id", Messages: []model.Message{
		{Role: "user", Text: "the three things Cursor wraps around a user message: `<user_query>`, `<additional_data>`, `<attached_files>`"},
	}}
	var b bytes.Buffer
	PrintContext(&b, s, "cursor wraps")
	if !strings.Contains(b.String(), "`<user_query>`") {
		t.Errorf("the sentence lost the tag it was naming:\n%s", b.String())
	}
}

// Each close pairs with the nearest open before it. A turn that opens one
// wrapper and never closes it, then closes a later one, used to hand the stray
// tag back inside the text (third review of #3323).
func TestContextPairsEachWrapperWithTheNearestOpen(t *testing.T) {
	s := model.Session{Harness: "cursor", Project: "p", ID: "id", Messages: []model.Message{
		{Role: "user", Text: "<user_query>open one never closed about the retry loop, then <user_query>why does it drop the last attempt</user_query> after it"},
	}}
	var b bytes.Buffer
	PrintContext(&b, s, "retry loop")
	got := b.String()
	if strings.Contains(got, "</user_query>") {
		t.Errorf("a closing tag was left where its pair was unwrapped:\n%s", got)
	}
	if !strings.Contains(got, "why does it drop the last attempt") {
		t.Errorf("the question inside the closed wrapper is gone:\n%s", got)
	}
	// The open with no close is the person naming the tag, and stays where it
	// is — the same rule every other tag gets here.
	if strings.Count(got, "<user_query>") != 1 {
		t.Errorf("the unclosed tag a person typed was taken out:\n%s", got)
	}
	// And it is the one they left open, at the head — not the wrapper's own,
	// which the close paired with.
	if !strings.Contains(got, "then why does it drop the last attempt") {
		t.Errorf("the close paired with the wrong open, so the tag sits against the question:\n%s", got)
	}
}

// PrintContext heads its output with the session's project and id — transcript
// text a harness wrote. The body is SafeText'd; the header must be too, or
// `deja show`, `deja ctx` and the MCP context tools print an escape or a bidi
// run straight to a terminal or the agent (#1090).
func TestPrintContextSanitisesTheHeader(t *testing.T) {
	var b bytes.Buffer
	s := model.Session{Harness: "claude", Project: "proj\x1b[31m", ID: "id\u202eevil"}
	PrintContext(&b, s, "")
	header, _, _ := strings.Cut(b.String(), "\n")
	for _, r := range header {
		if unicode.IsControl(r) {
			t.Fatalf("context header carried a control rune %U: %q", r, header)
		}
	}
	if strings.ContainsRune(header, '\u202e') {
		t.Fatalf("context header carried a bidi override: %q", header)
	}
	if !strings.Contains(header, "proj") {
		t.Fatalf("header lost the project text: %q", header)
	}
}

// mixedContextSession is the shape that broke rendering only the window in
// #1742: prose next to tool dumps and numbered output, so collapsing changes
// sizes, plus a fenced block and an escape byte that the renderers treat
// differently from ordinary text.
func mixedContextSession(turns int) model.Session {
	base := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	s := model.Session{Harness: "claude", Project: "proj", ID: "mixed", Updated: base}
	for i := range turns {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		var text string
		switch i % 5 {
		case 0:
			text = fmt.Sprintf("turn %d: the resharding question came back today", i)
		case 1:
			text = fmt.Sprintf("  %d | 0x%04x  some tool dump line\n  %d | 0x%04x  another\nwe decided to cap retries at 3", i, i, i+1, i+1)
		case 2:
			text = fmt.Sprintf("```go\nfunc turn%d() {}\n```", i)
		case 3:
			text = strings.Repeat(fmt.Sprintf("turn %d prose that runs long enough to matter. ", i), 40)
		case 4:
			text = fmt.Sprintf("turn %d\x1b[31m coloured \x1b[0m and \t tabbed\n\n\n  spaced   out  ", i)
		}
		s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: base.Add(time.Duration(i) * time.Minute)})
	}
	return s
}

// The digest is the same bytes however many cores render it: turns are written
// back by index, so parallel rendering cannot reorder or drop one (#1790).
func TestContextIsTheSameBytesOnOneCoreAndMany(t *testing.T) {
	s := mixedContextSession(200)
	for _, query := range []string{"", "resharding", "retries", "nothing matches this"} {
		var one, many bytes.Buffer
		withRenderWorkers(t, 1, func() { PrintContext(&one, s, query) })
		withRenderWorkers(t, 8, func() { PrintContext(&many, s, query) })
		if one.String() != many.String() {
			t.Errorf("query %q rendered differently on 8 cores than on 1:\n--- one ---\n%s\n--- many ---\n%s", query, one.String(), many.String())
		}
		if one.Len() == 0 {
			t.Errorf("query %q rendered nothing, so this proves nothing", query)
		}
	}
}

// Below the threshold the turns render in place; the output still has to match
// what the pool produces.
func TestShortSessionsRenderInPlaceAndMatch(t *testing.T) {
	s := mixedContextSession(6)
	var small, pooled bytes.Buffer
	withRenderWorkers(t, 8, func() { PrintContext(&small, s, "resharding") })
	withRenderWorkers(t, 1, func() { PrintContext(&pooled, s, "resharding") })
	if small.String() != pooled.String() {
		t.Errorf("a six-turn session rendered differently:\n%s\n---\n%s", small.String(), pooled.String())
	}
	if !strings.Contains(small.String(), "resharding") {
		t.Errorf("the matched turn is missing:\n%s", small.String())
	}
}

func withRenderWorkers(t *testing.T, n int, f func()) {
	t.Helper()
	prev := renderContextTurnsWorkers
	renderContextTurnsWorkers = func() int { return n }
	defer func() { renderContextTurnsWorkers = prev }()
	f()
}

// summarySession is a session whose window holds a compaction summary — the
// longest message a session ever has, naming everything it did — beside the
// turns that actually discussed the subject.
func summarySession() model.Session {
	summary := "Summary:\n1. **Primary Request and Intent:**\n\n" +
		"   The standing instruction is to keep improving the tool.\n" +
		"2. **Key Technical Concepts:**\n   - the retry budget, the scheduler, the failover\n" +
		strings.Repeat("   - a line about something else entirely\n", 120)
	now := time.Now()
	return model.Session{
		Harness: "claude", ID: "s1", Project: "app", Updated: now,
		Messages: []model.Message{
			{Role: "user", Text: summary, Time: now},
			{Role: "user", Text: "what did we settle about the retry budget?", Time: now.Add(time.Minute)},
			{Role: "assistant", Text: "We capped the retry budget at three attempts and let the fourth fail loudly.", Time: now.Add(2 * time.Minute)},
		},
	}
}

// A compaction summary names everything the session did, so it matches almost
// any question and then fills the window on its own. Measured over eight
// questions an agent would ask, on a real store: three answers came back
// carrying one, at 95% of the answer each — 44% of every byte recall_context
// served, none of it an answer.
func TestAContextWindowIsNotFilledByACompactionSummary(t *testing.T) {
	var b bytes.Buffer
	PrintContext(&b, summarySession(), "retry budget")
	got := b.String()

	if !strings.Contains(got, "capped the retry budget at three") {
		t.Errorf("the turn that answers is missing:\n%s", got)
	}
	if strings.Contains(got, "a line about something else entirely") {
		t.Errorf("the summary was printed whole (%d bytes):\n%s", len(got), got[:min(len(got), 400)])
	}
	if !strings.Contains(got, "compaction summary") {
		t.Errorf("nothing says the summary was cut:\n%s", got)
	}
	// The lines of it that do carry the query are what it contributes.
	if !strings.Contains(got, "the retry budget, the scheduler, the failover") {
		t.Errorf("the matching line of the summary is missing:\n%s", got)
	}
}

// Asked nothing — `deja ctx <id>`, the MCP resource read — there is nothing to
// match, and the summary is the best account of the session there is.
func TestWithNoQueryTheSummaryIsKept(t *testing.T) {
	var b bytes.Buffer
	PrintContext(&b, summarySession(), "")
	if !strings.Contains(b.String(), "a line about something else entirely") {
		t.Error("a session read with no query lost its summary")
	}
}

// A turn that merely opens with the word is a person writing, not a harness.
func TestAnOrdinaryTurnCalledSummaryIsUntouched(t *testing.T) {
	now := time.Now()
	s := model.Session{
		Harness: "claude", ID: "s2", Project: "app", Updated: now,
		Messages: []model.Message{
			{Role: "user", Text: "Summary: the retry budget is three and the scheduler is single-writer, which is what we agreed.", Time: now},
		},
	}
	var b bytes.Buffer
	PrintContext(&b, s, "retry budget")
	if got := b.String(); !strings.Contains(got, "single-writer") {
		t.Errorf("an ordinary turn was clipped as a compaction summary:\n%s", got)
	}
}

// A long session where the query's common word is dense at the top and its
// identifying word appears only late. The window must land on the identifying
// word: that is the one that picked this session out of the index.
func TestContextWindowPrefersIdentifyingWord(t *testing.T) {
	msgs := make([]model.Message, 0, 41)
	for i := range 40 {
		msgs = append(msgs, model.Message{
			Role: "user",
			Text: fmt.Sprintf("turn %d: sessions sessions sessions %s", i, strings.Repeat("filler ", 40)),
		})
	}
	msgs = append(msgs, model.Message{
		Role: "assistant",
		Text: "the prime-agent source reads PRIME_AGENT_SESSION_DIR " + strings.Repeat("body ", 40),
	})
	s := model.Session{Harness: "claude", Project: "p", ID: "id", Messages: msgs}

	var b bytes.Buffer
	PrintContext(&b, s, "prime-agent sessions")
	if !strings.Contains(b.String(), "prime-agent") {
		t.Fatalf("digest never reached the identifying word:\n%s", b.String()[:400])
	}
}

// Covering the whole query still beats covering part of it. Relaxing the old
// all-words rule must not cost the turn that says every word of the question.
// One turn saying a single query word over and over must not outweigh the turn
// that answers the whole question. Rarity alone does not settle this: repetition
// of one rare word can out-sum three of them said once each, and the two turns
// sit far enough apart that only one fits the budget.
func TestContextWindowPrefersTheTurnCoveringTheWholeQuery(t *testing.T) {
	filler := func(tag string, n int) []model.Message {
		out := make([]model.Message, 0, n)
		for i := range n {
			out = append(out, model.Message{
				Role: "user",
				Text: fmt.Sprintf("%s %d: %s", tag, i, strings.Repeat("unrelated ", 60)),
			})
		}
		return out
	}
	msgs := make([]model.Message, 0, 82)
	msgs = append(msgs, model.Message{
		Role: "assistant",
		Text: "http " + strings.Repeat("http ", 50),
	})
	msgs = append(msgs, filler("mid", 40)...)
	msgs = append(msgs, model.Message{
		Role: "assistant",
		Text: "we capped retries on the http client",
	})
	msgs = append(msgs, filler("tail", 40)...)
	s := model.Session{Harness: "claude", Project: "p", ID: "id", Messages: msgs}

	var b bytes.Buffer
	PrintContext(&b, s, "http client retries")
	out := b.String()
	if !strings.Contains(out, "capped retries on the http client") {
		t.Fatalf("window sat on the loud single word instead of the answer:\n%s", out[:min(len(out), 400)])
	}
}

// The context digest is the conversation, not the machinery under it. What an
// agent did — the files it touched, the spans it replaced, the commands it ran
// — is indexed and searchable by role, and before those roles were labelled
// honestly (#560) they arrived as `user` and filled this with blocks nobody
// meant by context.
//
// Measured by removing the filter: with isWorkRecord returning false, the
// digest grew "## files" and "## edit" blocks and no test in this package
// failed. This is that test.
func TestContextDigestLeavesTheMachineryOut(t *testing.T) {
	s := model.Session{
		Harness: "claude", Project: "work", ID: "w1",
		Messages: []model.Message{
			{Role: "user", Text: "the retry queue stalls on staging and every worker wakes at once"},
			{Role: roleToolOutput, Text: "npm ERR! code ELIFECYCLE\nnpm ERR! errno 1"},
			{Role: "command", Text: "$ npm test"},
			{Role: "files", Text: "/w/app/retry.go\n/w/app/queue.go"},
			{Role: "edit", Text: "/w/app/retry.go\nthe body that was replaced"},
			{Role: "assistant", Text: "We spread the wakeups over a second, bounded by the poll interval."},
		},
	}
	var b strings.Builder
	PrintContext(&b, s, "retry")
	out := b.String()

	// The conversation is there, or the assertions below pass on an empty
	// digest.
	if !strings.Contains(out, "the retry queue stalls on staging") {
		t.Fatalf("wrong fixture, the conversation is missing:\n%s", out)
	}
	if !strings.Contains(out, "We spread the wakeups over a second") {
		t.Fatalf("wrong fixture, the answer is missing:\n%s", out)
	}

	for _, marker := range []string{"## files", "## edit", "## tool-output", "## command"} {
		if strings.Contains(out, marker) {
			t.Errorf("the digest carries a %s block:\n%s", marker, out)
		}
	}
	// And not just the headings: the bodies must not arrive under another
	// role either. These are what actually bite — a stubbed isWorkRecord
	// leaks the files and edit blocks, while tool output and commands are
	// held back by the query filter above as well, so their absence here is
	// weaker evidence than it looks.
	for _, body := range []string{"/w/app/queue.go", "the body that was replaced", "npm ERR!", "$ npm test"} {
		if strings.Contains(out, body) {
			t.Errorf("the digest carries %q:\n%s", body, out)
		}
	}
}
