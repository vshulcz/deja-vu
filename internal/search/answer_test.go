package search

import (
	"github.com/vshulcz/deja-vu/internal/model"
	"strings"
	"testing"
	"unicode/utf8"
)

// clip cuts the answer line at the last word boundary before the cap. Chinese,
// Japanese and Korean put no spaces between words, so the walk back found no
// boundary at all, fell through to the cap, and split the character sitting on
// it — an invalid byte in the `→ ` line of a recall payload an agent reads.
func TestAnswerClipCutsOnACharacter(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{"chinese", strings.Repeat("重试队列在预发环境卡住了工作进程同时醒来", 12)},
		{"japanese", strings.Repeat("リトライキューがステージングで詰まりワーカーが同時に起きる", 12)},
		{"korean", strings.Repeat("재시도큐가스테이징에서멈추고워커가동시에깨어난다", 12)},
		{"latin", strings.Repeat("the retry queue stalls on staging and the workers wake together ", 8)},
		{"mixed", strings.Repeat("重试队列 stalls 工作进程同时醒来 needs jitter ", 8)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := clip(tc.text)
			if !utf8.ValidString(got) {
				t.Errorf("clip produced invalid UTF-8: %q", got[max(0, len(got)-24):])
			}
			if len(got) > answerCap+len("…") {
				t.Errorf("clip returned %d bytes, cap is %d", len(got), answerCap)
			}
			if !strings.HasSuffix(got, "…") {
				t.Errorf("the cut is not marked: %q", got[max(0, len(got)-24):])
			}
			// What comes back is a prefix of the input, mark aside: a cut
			// through a character would be the right length and the wrong text.
			if !strings.HasPrefix(tc.text, strings.TrimSuffix(got, "…")) {
				t.Errorf("clip returned text that is not a prefix of its input: %q", got)
			}
		})
	}
}

// A line inside the cap is returned whole, with no mark.
func TestAnswerClipLeavesShortLines(t *testing.T) {
	const short = "重试队列卡住了，我们加了抖动。"
	if got := clip(short); got != short {
		t.Errorf("clip(%q) = %q", short, got)
	}
}

// The word boundary still wins where there is one: Latin answers are cut at a
// space, not mid-word.
func TestAnswerClipStillPrefersAWordBoundary(t *testing.T) {
	// The cap has to land inside a word, or the cut is at a boundary by
	// accident and the test says nothing: 35 repeats is 245 bytes, and the
	// long word after them runs across byte 260.
	text := strings.Repeat("stalls ", 35) + "backoffjitterhandler spreads the wakeups over a second"
	if text[answerCap] == ' ' || text[answerCap-1] == ' ' {
		t.Fatalf("wrong fixture: byte %d is at a word boundary", answerCap)
	}
	got := strings.TrimSuffix(clip(text), "…")
	if len(got) >= len(text) {
		t.Fatalf("wrong fixture, nothing was cut")
	}
	// The character the cut landed on has to be the space between two words:
	// anything else means it landed inside one.
	if c := text[len(got)]; c != ' ' {
		t.Errorf("cut inside a word at byte %d (%q): …%q", len(got), string(c), got[max(0, len(got)-24):])
	}
	if got[len(got)-1] == ' ' {
		t.Errorf("trailing space survived: %q", got[max(0, len(got)-24):])
	}
}

// Whatever comes in, what goes out is readable. Stored text is valid UTF-8, so
// the case below is defensive — but the old cut answered it by handing the
// agent 260 unreadable bytes.
func TestAnswerClipNeverEmitsInvalidUTF8(t *testing.T) {
	for _, name := range []string{"continuation bytes", "truncated character"} {
		var in string
		switch name {
		case "continuation bytes":
			in = strings.Repeat("\x80", 300)
		default:
			in = strings.Repeat("重", 100)[:299]
		}
		got := clip(in)
		if !utf8.ValidString(got) {
			t.Errorf("%s: clip returned invalid UTF-8: %q", name, got)
		}
		if len(got) > answerCap+len("…") {
			t.Errorf("%s: clip returned %d bytes", name, len(got))
		}
	}
	// And a real line keeps its content: the guard above must not be reached
	// by ordinary text.
	ordinary := strings.Repeat("重试队列在预发环境卡住了工作进程同时醒来", 12)
	if got := clip(ordinary); len(got) < answerCap-8 {
		t.Errorf("an ordinary line came back as %d bytes: %q", len(got), got)
	}
}

// The "→ " line under a recall hit is the one structured thing the answer adds,
// and it picked its sentence with an English phrase list while every block deja
// injects used the shared recogniser — which knows both languages and tells a
// plan from an outcome. Counted over 1265 distinct assistant lines on a real
// store: the list marks 34, the shared rule 60, and 59 of those are lines the
// list never sees.
func TestTheAnswerLineUsesTheSharedRecogniser(t *testing.T) {
	// Each reply opens with chatter and records its outcome in a later
	// sentence, so picking the sentence and falling through to the whole reply
	// are told apart: the fallback would carry the wanted words too.
	for _, c := range []struct{ reply, want, notWant string }{
		{"Смотрю дальше по логам и жду ответа. Причина в конфиге: `my.aeza.net` не подпадает ни под одно правило.",
			"Причина в конфиге", "Смотрю дальше"},
		{"Проверил оба пути входа и перезагрузил демон. Состояние стало пустым после повторного off.",
			"стало пустым", "Проверил оба пути"},
		{"Долго искал и перебрал три гипотезы. В итоге решили держать single-writer.",
			"решили держать single-writer", "Долго искал"},
		// The English shapes the phrase list holds on its own still work.
		{"Looked at the options and weighed them. We pinned pgx to 5.4.3 for now.",
			"We pinned pgx", "Looked at the options"},
		{"Dug through the handler for a while. Root cause was the stale etag reuse.",
			"Root cause", "Dug through"},
	} {
		got := DecisionText(c.reply)
		if !strings.Contains(got, c.want) {
			t.Errorf("the answer line picked %q, which does not carry %q", got, c.want)
		}
		if strings.Contains(got, c.notWant) {
			t.Errorf("the answer line quoted the whole reply rather than its outcome: %q", got)
		}
	}
}

// And a reply that only announces what it is about to do is not an answer: the
// shared recogniser rejects it, which the phrase list could not see either way.
func TestTheAnswerLineDoesNotQuoteAPlanAsAnOutcome(t *testing.T) {
	msgs := []model.Message{
		{Role: "user", Text: "why does the exporter retry so hard"},
		{Role: "assistant", Text: "Проверяю, что экспортер теперь перестал ретраить без паузы."},
		{Role: "assistant", Text: "Причина найдена: бэкофф считался от нуля, поэтому четвёртая попытка шла сразу."},
	}
	got := AnswerAfter(msgs, 0)
	if strings.Contains(got, "Проверяю") {
		t.Errorf("a plan was attached as the answer: %q", got)
	}
	if !strings.Contains(got, "Причина найдена") {
		t.Errorf("the outcome was not attached: %q", got)
	}
}

func TestAnswerAfterUsesTurnSynthesisAfterEditsInsteadOfOpeningPledge(t *testing.T) {
	msgs := make([]model.Message, 0, 25)
	msgs = append(msgs,
		model.Message{Role: "user", Text: "Please fix AdminReadonlyField URL resolution when current_app is nested."},
		model.Message{Role: "assistant", Text: "I'll help you fix this bug! Let me start by exploring the repository layout."},
	)
	for range 20 {
		msgs = append(msgs, model.Message{Role: "files", Text: "/repo/contrib/admin/helpers.py"})
	}
	msgs = append(msgs,
		model.Message{Role: "edit", Text: "/repo/contrib/admin/helpers.py\nget_admin_url"},
		model.Message{Role: "command", Text: "pytest -q → exit 0"},
		model.Message{
			Role: "assistant",
			Text: "## Problem\nAdmin URLs inside custom sites ignored the active admin namespace.\n\n" +
				"## Root Cause\nAdminReadonlyField.get_admin_url() failed to pass self.model_admin.admin_site.name as current_app.\n\n" +
				"## The Fix\nUpdated get_admin_url() to supply current_app to reverse().",
		},
	)
	got := AnswerAfter(msgs, 0)
	if !strings.Contains(got, "AdminReadonlyField.get_admin_url") {
		t.Fatalf("expected turn synthesis root cause, got %q", got)
	}
	if strings.Contains(got, "I'll help you") || strings.Contains(got, "Let me start by") {
		t.Fatalf("expected opening helper chatter skipped, got %q", got)
	}
}

// The reply is attached by the recall path rather than by scoring, because
// search returns only the messages that matched — see attachAnswers in
// cmd/deja. These cover the pieces it builds on.

func TestAnswerAfterOnlyTakesTheReplyToThisTurn(t *testing.T) {
	msgs := []model.Message{
		{Role: "user", Text: "first question"},
		{Role: "user", Text: "second question before any answer"},
		{Role: "assistant", Text: "We pinned the driver."},
	}
	// The user spoke twice; the reply belongs to the second turn, not the first.
	if got := AnswerAfter(msgs, 0); got != "" {
		t.Fatalf("attached a reply across an intervening user turn: %q", got)
	}
	if got := AnswerAfter(msgs, 1); !strings.Contains(got, "pinned") {
		t.Fatalf("missed the reply that follows: %q", got)
	}
	// Nothing follows the last message.
	if got := AnswerAfter(msgs, 2); got != "" {
		t.Fatalf("invented a reply after the last message: %q", got)
	}
}

func TestDecisionTextPrefersTheOutcomeSentence(t *testing.T) {
	long := "The stack trace points at the pool. Metrics show it saturating at 40 connections. " +
		"Root cause was a pool sized from the wrong config key. We set it from the pod limit instead."
	got := DecisionText(long)
	if !strings.Contains(got, "Root cause") {
		t.Fatalf("did not pick the outcome sentence: %q", got)
	}
	// A version number is not a sentence boundary.
	if got = DecisionText("We pinned pgx to 5.4.3 and moved on."); !strings.Contains(got, "5.4.3 and moved on") {
		t.Fatalf("split on a version number: %q", got)
	}
	// With no decision language, the opening of the reply is a fair fallback.
	if got = DecisionText("Looks like the proxy closes idle connections earlier than the client expects."); !strings.HasPrefix(got, "Looks like") {
		t.Fatalf("fallback lost the opening: %q", got)
	}
	if DecisionText("   ") != "" {
		t.Fatal("whitespace produced an answer")
	}
	// Long replies are clipped on a word boundary rather than mid-token.
	got = DecisionText(strings.Repeat("word ", 200))
	if len(got) > answerCap+3 || strings.HasSuffix(got, "wo…") {
		t.Fatalf("clip landed badly: %d chars, %q", len(got), got[max(0, len(got)-20):])
	}
}
