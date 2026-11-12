package index

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestGiveUpLineKeepsReportsAndDropsEverythingElse(t *testing.T) {
	reports := []string{
		"reverted the retry cap, it made the queue worse",
		"откатили пул соединений, стало хуже",
		"we backed out the migration and went with the view instead",
		"abandoned the sidecar approach and went back to the cron job",
		"gave up on the websocket path, polling it is",
	}
	for _, l := range reports {
		if _, ok := GiveUpLine(l); !ok {
			t.Errorf("a report of backing something out is not detected: %q", l)
		}
	}
	notReports := []string{
		// A question is the moment before the decision, not the decision.
		"should we have reverted the retry cap?",
		// Source that talks about reverting is code, not a report.
		`log.Printf("reverted %s", name)`,
		"// reverted in the previous release",
		// Too short to say anything, and too long to be a statement.
		"reverted",
		// Ordinary failure talk describes the world, not a decision about it —
		// this is how nearly every debugging session opens, and it must not
		// mark the session as a dead end.
		"the webhook retry doesn't work when the queue is full",
		"if that does not work, we can add an index on created_at",
		"the sidecar approach did not work, still digging",
		"не сработал ретрай при переполнении очереди, смотрю почему",
		"the build failed on the arm runner again",
	}
	for _, l := range notReports {
		if _, ok := GiveUpLine(l); ok {
			t.Errorf("this is not a report of backing something out: %q", l)
		}
	}
}

// Tool output is full of the word: git prints it, deja prints it, and a diff
// carries whatever the file said. Only what someone actually said counts.
func TestGaveUpReadsSpeechAndNotToolOutput(t *testing.T) {
	fromOutput := []model.Message{
		{Role: "tool-output", Text: "HEAD is now at 8f2c19a Revert \"cap retries\"\nreverted 1 commit"},
	}
	if gaveUp(fromOutput) {
		t.Error("git's own output marks the session as a dead end")
	}
	fromSpeech := []model.Message{
		{Role: "assistant", Text: "capped the retries\nreverted that, it made the queue worse"},
	}
	if !gaveUp(fromSpeech) {
		t.Error("the assistant saying it reverted the change is not detected")
	}
}

// P0-9/10/11: real prose reports carry quotes, issue refs and URLs, and can end
// in a follow-up question; a byte budget must not halve the room for Cyrillic.
func TestGiveUpLineDetectsRealProseReports(t *testing.T) {
	reports := []string{
		"reverted the change from #1143 and shipped the view instead",
		"rolled back after reading github.com/foo/bar//issues/3, it deadlocked",
		`reverted the "fast path" change because it broke ordering`,
		"откатили, но не помогло, что дальше?", // report + question
		"откатили миграцию пула соединений на постгресе, потому что она держала лок на всю таблицу и всё вставало намертво", // long Cyrillic
	}
	for _, l := range reports {
		if _, ok := GiveUpLine(l); !ok {
			t.Errorf("a real report was rejected: %q", l)
		}
	}
	notReports := []string{
		"should we revert this change?",       // pure question
		`log.Printf("reverted %s", name)`,     // code with string arg
		"// reverted in the previous release", // comment
	}
	for _, l := range notReports {
		if _, ok := GiveUpLine(l); ok {
			t.Errorf("a non-report was accepted: %q", l)
		}
	}
}

// Precision review: a reversal that did not happen — negated, conditional,
// future, or the reflexive Russian verb — must not mark the session.
func TestGiveUpLineRejectsUnrealisedReversals(t *testing.T) {
	notReports := []string{
		// Negation.
		"we haven't reverted the migration yet",
		"we never gave up on the retry approach",
		"не отказались от идеи с пулом соединений",
		"он сказал не откатывать это ни в коем случае",
		// Conditional / future.
		"if this fails we just roll it back, no big deal",
		"we could roll it back but we won't do that now",
		"I'll be reverting the config change tomorrow",
		// Question with an internal separator.
		"did we roll it back, or is it still live?",
		"should we have reverted, or patched forward?",
		// Reflexive Russian verb (all genders/numbers), not a reversal action.
		"мяч откатился под диван и застрял там надолго",
		"деталь откатилась назад и упала со стола",
		"колесо откатилось в сторону от станка",
		"камни откатились вниз по склону после дождя",
		// A diff removed-line is not a report of a reversal.
		"- reverted the old migration helper",
		// A copula question without a "?" is still a question, not a report.
		"was that rolled back",
		"is that reverted yet",
		"were those changes reverted",
		// Metalinguistic mentions of reversal tokens in backticks or quotes.
		"the filter checks for `reverted` or «откатили» in assistant turns",
		`do not match "rolled back" when quoted as a literal token`,
	}
	for _, l := range notReports {
		if _, ok := GiveUpLine(l); ok {
			t.Errorf("an unrealised or non-reversal was marked: %q", l)
		}
	}
	// And genuine reports still pass, including a report that ends in a
	// separate follow-up question.
	reports := []string{
		"reverted the retry cap, it made the queue worse",
		"откатили, но не помогло, что дальше?",
		"we backed out the migration and went with the view instead",
		"* reverted the caching layer, it broke ordering",
		"> rolled back the migration after the DBA complained",
		// A modal AFTER the phrase belongs to another verb — still a report.
		"reverted it since it would corrupt state otherwise",
		"reverted the index because a full scan could deadlock",
		// Subject-elided passive report is not a question.
		"was rolled back after review, all good now",
	}
	for _, l := range reports {
		if _, ok := GiveUpLine(l); !ok {
			t.Errorf("a genuine report was dropped: %q", l)
		}
	}
}

func TestGiveUpRejectsParserProseAndTestSoundnessRollbacks(t *testing.T) {
	rejects := []string{
		// Algorithmic parser prose ("отбросил интервал") is not an engineering rollback.
		"Старый extractLineRanges отбросил интервал L10-L20 из-за пустой строки",
		// Temporary test-soundness and scratchpad rollbacks must not fire dead-end warnings.
		"Each new test failed with its fix reverted before passing.",
		"Verified by temporarily reverting the mcp.go hunk there: TestBlame fails.",
		"Against the reverted tree all four assertions fail.",
		"Test is sound. Reverting the fix fails TestRelayOutboxBatch.",
		"Fail-before confirmed on the reverted copy in /tmp/scratch.",
	}
	for _, s := range rejects {
		if got := GiveUpExcerpts(s); len(got) > 0 {
			t.Errorf("expected non-dead-end prose %q to be rejected, got %v", s, got)
		}
	}

	accepts := []string{
		"Отбросили этот вариант кеширования, потому что он ломает порядок событий.",
		"Отбросил подход с глобальным мьютексом из-за дедлока в воркере.",
	}
	for _, s := range accepts {
		if got := GiveUpExcerpts(s); len(got) == 0 {
			t.Errorf("expected genuine Russian dead-end %q to be accepted", s)
		}
	}
}

func TestGiveUpExcerptsExtractsTwoSentenceWindowFromLongParagraph(t *testing.T) {
	// Over giveUpLineMax (300 runes), with the target symbol in the opening
	// sentence and the rollback + causal reason in later sentences.
	longPara := "Confirmed: `blameTextResult` returns JSON via `mustMarshalBlame` because the protocol requires a structured array shape across all callers in the workspace and downstream clients depend on exact field names in every response payload. " +
		"Wrapping `blameTextResult` in markdown prose would break that contract for strict JSON consumers. " +
		"My framing attempt was wrong — reverted."
	got := GiveUpExcerpts(longPara)
	if len(got) == 0 {
		t.Fatalf("expected GiveUpExcerpts to extract a 2-sentence window from >300-rune paragraph")
	}
	joined := got[0]
	if _, ok := GiveUpLine(joined); !ok {
		t.Errorf("extracted window %q did not pass GiveUpLine", joined)
	}
	if len(got[0]) < 40 {
		t.Errorf("extracted window unexpectedly short: %q", joined)
	}
}
