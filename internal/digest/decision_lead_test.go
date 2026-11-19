package digest

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func conclusionSession(text string) model.Session {
	return model.Session{
		Harness:  "claude",
		Project:  "p",
		ID:       "s1",
		Messages: []model.Message{{Role: "assistant", Text: text}},
	}
}

// A reply is diagnosis-first, and the block quoted its opening. When the
// outcome is further in, that handed an agent the symptom and dropped what the
// session settled — 211 of 490 sessions on a real store (#2243).
func TestTheBlockQuotesTheConclusionAndNotOnlyTheDiagnosis(t *testing.T) {
	got := Conclusions(conclusionSession(
		"The pool kept dropping connections under load. "+
			"Both replicas showed the same pattern in the logs. "+
			"The fix was raising pgbouncer default_pool_size to 40."), 600, 3)
	if len(got) == 0 {
		t.Fatal("nothing was quoted")
	}
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "raising pgbouncer default_pool_size to 40") {
		t.Errorf("the outcome is not in the block:\n%s", joined)
	}
	if !CarriesDecision(joined) {
		t.Errorf("the block does not read as a conclusion:\n%s", joined)
	}
}

// The opening stays the default. A head that already concludes must not be
// traded for some later sentence that also happens to carry a marker — the
// first statement of the outcome is the one worth having.
func TestAHeadThatAlreadyConcludesIsKept(t *testing.T) {
	got := Conclusions(conclusionSession(
		"Connections were dropping under load. "+
			"The fix was pinning pgx to 5.4.3. "+
			"Later we also merged the CI image switch."), 600, 3)
	if len(got) == 0 {
		t.Fatal("nothing was quoted")
	}
	// Both sentences of the head, not the decision sentence lifted out of it:
	// the opening is what makes the outcome make sense, and a later sentence
	// carrying a marker of its own must not displace it.
	for _, want := range []string{"Connections were dropping under load", "pinning pgx to 5.4.3"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("the head was traded for a later sentence, %q is gone:\n%s", want, got[0])
		}
	}
}

// A message that settles nothing must not acquire a conclusion: the fallback
// is still its opening.
func TestAMessageWithNoOutcomeStillQuotesItsOpening(t *testing.T) {
	got := Conclusions(conclusionSession(
		"Looking at the pool metrics now. "+
			"The graph covers the last six hours. "+
			"I will check the replica logs next."), 600, 3)
	if len(got) == 0 {
		t.Fatal("a message that settles nothing was dropped entirely; its opening is still the answer")
	}
	if !strings.Contains(got[0], "Looking at the pool metrics") {
		t.Errorf("the opening was dropped for something else:\n%s", got[0])
	}
}

// The splitter has to agree with firstSentences on what a sentence is, or the
// two disagree about where a conclusion starts. A version number is not a
// sentence end; a CJK stop is, with no space after it.
func TestSentencesSplitWhereTheDigestAlreadySplits(t *testing.T) {
	got := sentencesOf("Pinned pgx to v5.4.3 and it held. Left a note.")
	if len(got) != 2 {
		t.Fatalf("split into %d sentences, want 2: %q", len(got), got)
	}
	if !strings.Contains(got[0], "v5.4.3 and it held") {
		t.Errorf("the version number was read as a sentence end: %q", got[0])
	}
	if n := len(sentencesOf("已固定 pgx 5.4.3。问题消失了。")); n != 2 {
		t.Errorf("CJK sentences split into %d, want 2", n)
	}
}

func TestConclusionsSkipsCelebratoryBoilerplateIntoSummary(t *testing.T) {
	got := Conclusions(conclusionSession(
		"Perfect! I've successfully fixed the bug where Foo crashed on empty input.\n\n"+
			"## Summary\n"+
			"**Root Cause**: Missing nil guard in parser.go when buffer is empty.\n"+
			"**Solution**: Return early on empty slice before indexing."), 600, 3)
	if len(got) == 0 {
		t.Fatal("nothing was quoted")
	}
	joined := strings.Join(got, " ")
	if strings.Contains(joined, "Perfect! I've successfully") {
		t.Errorf("celebratory boilerplate was quoted instead of root cause:\n%s", joined)
	}
	for _, want := range []string{"Root Cause", "Missing nil guard"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %q in conclusion block, got:\n%s", want, joined)
		}
	}

	gotSummary := Conclusions(conclusionSession(
		"## Summary\n\n"+
			"Here's what I found and fixed:\n\n"+
			"### The Problem\n"+
			"`parse_token` in `pkg/auth/tokens.py` raised `ValueError` instead of returning `None` when no session token exists.\n\n"+
			"### The Fix\n"+
			"Updated `_parse_token_header` to guard the signature check."), 600, 3)
	if len(gotSummary) == 0 {
		t.Fatal("nothing was quoted for summary heading case")
	}
	joinedSummary := strings.Join(gotSummary, " ")
	for _, bad := range []string{"Here's what I found", "The Problem"} {
		if strings.Contains(joinedSummary, bad) {
			t.Errorf("unexpected meta-intro or structural heading %q in conclusion block:\n%s", bad, joinedSummary)
		}
	}
	for _, want := range []string{"parse_token", "tokens.py"} {
		if !strings.Contains(joinedSummary, want) {
			t.Errorf("expected %q in conclusion block, got:\n%s", want, joinedSummary)
		}
	}
}

func TestStripAssistantBoilerplateSkipsTransitionChatter(t *testing.T) {
	if !IsCelebratoryBoilerplate("The fix works! Now let me run pytest on the suite.") {
		t.Fatal("expected transition chatter sentence to be flagged as boilerplate")
	}
	if !IsCelebratoryBoilerplate("I'll help you fix this bug related to widget rendering.") {
		t.Fatal("expected opening helper chatter to be flagged as boilerplate")
	}
	if !IsCelebratoryBoilerplate("Let me start by exploring the repository.") {
		t.Fatal("expected opening exploration chatter to be flagged as boilerplate")
	}
	if IsCelebratoryBoilerplate("Root Cause: validate_unique skipped inherited constraint checks.") {
		t.Fatal("expected technical root-cause sentence not to be flagged as boilerplate")
	}
	stripped := StripAssistantBoilerplate(
		"The fix works. Now let me create a test script to verify the edge case.\n\n" +
			"## Bug Summary\n" +
			"Root Cause: validate_unique skipped inherited constraint checks.",
	)
	if strings.Contains(stripped, "The fix works") || strings.Contains(stripped, "Now let me create") || strings.Contains(stripped, "Bug Summary") {
		t.Fatalf("expected transition chatter and structural heading stripped, got %q", stripped)
	}
	if !strings.Contains(stripped, "Root Cause: validate_unique skipped inherited constraint checks.") {
		t.Fatalf("expected technical explanation preserved, got %q", stripped)
	}
}

func TestSubstantialConclusionsPrefersRootCauseAndFixOverProblemSection(t *testing.T) {
	msg := "All tests pass!\n\n" +
		"## Problem\n" +
		"Saving a child object after assigning an unsaved parent failed silently.\n" +
		"Stale foreign keys remained empty until a second reload.\n\n" +
		"## Root Cause\n" +
		"In models/base.py, _prepare_related_fields_for_save() skipped updating cached related attributes.\n\n" +
		"## The Fix\n" +
		"Cleared the stale descriptor cache inside _prepare_related_fields_for_save()."
	got := SubstantialConclusions(conclusionSession(msg), 320, 1)
	if len(got) == 0 {
		t.Fatal("expected root-cause conclusion")
	}
	if !strings.Contains(got[0], "_prepare_related_fields_for_save") {
		t.Fatalf("expected root cause symbol preserved, got %q", got[0])
	}
	if strings.Contains(got[0], "failed silently") {
		t.Fatalf("expected problem restatement skipped in favor of root cause, got %q", got[0])
	}

	inlineMsg := "**Problem:** Widget rendering raised an error under nested groups.\n" +
		"**Root Cause:** format_widget_group() omitted the parent prefix during normalization."
	gotInline := SubstantialConclusions(conclusionSession(inlineMsg), 320, 1)
	if len(gotInline) == 0 || !strings.Contains(gotInline[0], "format_widget_group") {
		t.Fatalf("expected inline Root Cause preserved, got %#v", gotInline)
	}

	codeOnlyFix := "## Problem\nToken expiry check allowed negative offsets in auth/tokens.py.\n\n" +
		"## Root Cause\n```python\nreturn max(0, ttl)\n```"
	gotFallback := SubstantialConclusions(conclusionSession(codeOnlyFix), 320, 1)
	if len(gotFallback) == 0 || !strings.Contains(gotFallback[0], "Token expiry check") {
		t.Fatalf("expected fallback to prose section when outcome section only has a code fence, got %#v", gotFallback)
	}
}

func TestSubstantialConclusionsTrajectoryVerifiedSummaryHeadingWithoutSubheading(t *testing.T) {
	s := model.Session{
		Harness: "claude",
		Project: "p",
		ID:      "traj-summary-heading",
		Messages: []model.Message{
			{
				Role: "user",
				Text: "Fix widget alignment in ui/layout.py",
			},
			{
				Role: "assistant",
				Text: "I thought the bug was in ui/theme.py because padding looked off.",
			},
			{
				Role: "edit",
				Text: "ui/layout.py",
			},
			{
				Role: "command",
				Text: "pytest tests/test_layout.py → exit 0",
			},
			{
				Role: "assistant",
				Text: "## Summary\n\nUpdated `ui/layout.py` in `compute_row_offsets` to subtract border width from cell span.",
			},
		},
	}
	got := SubstantialConclusions(s, 320, 1)
	if len(got) != 1 {
		t.Fatalf("expected 1 conclusion, got %d: %#v", len(got), got)
	}
	if !strings.Contains(got[0], "compute_row_offsets") {
		t.Fatalf("expected post-edit summary symbol compute_row_offsets, got %q", got[0])
	}
	if strings.Contains(got[0], "ui/theme.py") {
		t.Fatalf("expected pre-edit guess to lose to post-edit trajectory synthesis, got %q", got[0])
	}
}

func TestConclusionsRussianOutcomeHeadingsAndNonStructuralHeadings(t *testing.T) {
	ruMsg := "Готово! Все тесты проходят.\n\n" +
		"Вот что было сделано:\n\n" +
		"## Проблема\n" +
		"Запрос падал при пустом токене.\n" +
		"Кэш возвращал устаревшую сессию.\n\n" +
		"## Причина\n" +
		"Функция validateSession не проверяла срок действия ключа перед выдачей ответа.\n\n" +
		"## Исправление\n" +
		"Добавили проверку expiresAt в validateSession и обновили тесты."
	gotRU := SubstantialConclusions(conclusionSession(ruMsg), 400, 1)
	if len(gotRU) == 0 {
		t.Fatal("expected Russian conclusion")
	}
	if !strings.Contains(gotRU[0], "validateSession") {
		t.Fatalf("expected Russian outcome section content, got %q", gotRU[0])
	}
	if strings.Contains(gotRU[0], "Готово!") || strings.Contains(gotRU[0], "Вот что было сделано") {
		t.Fatalf("expected Russian boilerplate stripped, got %q", gotRU[0])
	}

	gotHeading := StripAssistantBoilerplate("### **Subsystem Cache Invalidation**\n\n**Cache Timeout Note**\n\n### Trailing.\n\nUpdated cache TTL to 30s.")
	if !strings.Contains(gotHeading, "Subsystem Cache Invalidation.") {
		t.Fatalf("expected non-structural bold-wrapped heading terminated with period, got %q", gotHeading)
	}
	if !strings.Contains(gotHeading, "Cache Timeout Note.") {
		t.Fatalf("expected standalone bold non-structural heading terminated with period, got %q", gotHeading)
	}
	if !strings.Contains(gotHeading, "Trailing.") {
		t.Fatalf("expected punctuated heading preserved as-is, got %q", gotHeading)
	}
	if ensureHeadingTerminator("") != "" {
		t.Fatal("expected empty heading terminator to stay empty")
	}
}

func TestDecisionLeadBoilerplateSecondaryOutcomeAndBudgetCut(t *testing.T) {
	gotSkip := SubstantialConclusions(conclusionSession(
		"Perfect! Reviewing the worker queue logs carefully across all partitions now. "+
			"The backlog grew slowly during the morning traffic window. "+
			"Fixed the same way. "+
			"In the end we raised worker pool capacity from 16 to 64 threads for steady throughput."), 400, 1)
	if len(gotSkip) == 0 {
		t.Fatal("expected conclusion after skipping celebratory prefix and thin sentence")
	}
	if !strings.Contains(gotSkip[0], "64 threads") {
		t.Fatalf("expected substantial decision sentence over thin opener, got %q", gotSkip[0])
	}

	// Verify decisionLeadFromClean skips embedded celebratory sentences directly.
	cleanLead := decisionLeadFromClean(
		"Perfect! Reviewing the worker queue logs carefully across all partitions. "+
			"In the end we raised worker pool capacity to 64 threads.",
		true, false)
	if !strings.Contains(cleanLead, "64 threads") || strings.Contains(cleanLead, "Perfect!") {
		t.Fatalf("expected decisionLeadFromClean to skip celebratory prefix, got %q", cleanLead)
	}

	// Short preamble (<2 sentences) keeps full preamble when budget permits,
	// and falls back to the secondary Fix section when tight budget overflows.
	secMsg := "Queue backpressure stalled replication workers across three zones for over an hour before recovery.\n\n" +
		"## Исправление\n" +
		"Capped retry batch size inside worker/pool.go to 64 entries."
	fullBudget := SubstantialConclusions(conclusionSession(secMsg), 400, 1)
	if len(fullBudget) == 0 || !strings.Contains(fullBudget[0], "Queue backpressure") {
		t.Fatalf("expected short preamble kept within generous budget, got %#v", fullBudget)
	}
	tightBudget := SubstantialConclusions(conclusionSession(secMsg), 90, 1)
	if len(tightBudget) == 0 || !strings.Contains(tightBudget[0], "worker/pool.go") {
		t.Fatalf("expected secondary outcome fallback under tight budget, got %#v", tightBudget)
	}

	// Single over-budget sentence uses markedCut when budget allows readable prefix
	// and yields empty when budget is too small.
	longSent := conclusionSession("We decided to partition the shard routing table across sixteen independent ring buckets.")
	if cut := Conclusions(longSent, 55, 1); len(cut) != 1 || !strings.HasSuffix(cut[0], "…") {
		t.Fatalf("expected ellipsis cut for tight readable budget, got %#v", cut)
	}
	if tiny := Conclusions(longSent, 5, 1); len(tiny) != 0 {
		t.Fatalf("expected no unreadable stub for tiny budget, got %#v", tiny)
	}

	// Over-budget substantial conclusion trims to first substantial sentence.
	twoSubstantial := conclusionSession(
		"Fixed the worker pool deadlock on shutdown. " +
			"Also updated the replica supervisor drain timeout to match graceful stop.")
	if trimmed := SubstantialConclusions(twoSubstantial, 55, 1); len(trimmed) != 1 || !strings.Contains(trimmed[0], "worker pool deadlock") {
		t.Fatalf("expected first substantial sentence when two sentences exceed budget, got %#v", trimmed)
	}

	// Mid-line meta intro clause, invalid standalone bold spans, and heading-0 root cause.
	if got := StripAssistantBoilerplate("Patched lease check. Here's what changed:\n**a** and **b**\n****\nAdded unit test."); !strings.Contains(got, "Patched lease check.") {
		t.Fatalf("expected mid-line meta intro stripped leaving first sentence, got %q", got)
	}
	if got := SubstantialConclusions(conclusionSession("# **Root Cause**\n\nIn cache/store.go, missed lock release stalled writers."), 300, 1); len(got) != 1 || !strings.Contains(got[0], "missed lock release") {
		t.Fatalf("expected heading-0 bold Root Cause extracted, got %#v", got)
	}
}
