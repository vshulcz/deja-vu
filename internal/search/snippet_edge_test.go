package search

import (
	"strings"
	"testing"
	"unicode"
)

// The excerpt window starts a fixed distance before the match, so on a long line
// it used to open in the middle of whatever word sat there. Over 400 real
// messages that was 35% of excerpts; the first words are the ones a reader uses
// to decide whether to read on.
func TestAnExcerptDoesNotOpenInTheMiddleOfAWord(t *testing.T) {
	// A long line with no sentence break anywhere near the left edge, so the
	// word snap is what has to do the work.
	lead := strings.Repeat("configuration reconciliation telemetry ", 12)
	text := lead + "the exporter retried without any backoff at all " + strings.Repeat("and more words after it ", 20)

	sn := snippet(text, "backoff", nil)
	body := strings.TrimSuffix(strings.TrimPrefix(sn, "… "), " …")
	if !strings.Contains(body, "backoff") {
		t.Fatalf("the excerpt lost its own match: %q", sn)
	}
	first := strings.Fields(body)[0]
	if !strings.Contains(lead+" ", " "+first+" ") && !strings.HasPrefix(text, first) {
		t.Errorf("the excerpt opened on a word fragment %q:\n%s", first, sn)
	}
	last := strings.Fields(body)[len(strings.Fields(body))-1]
	if !strings.Contains(text, last+" ") {
		t.Errorf("the excerpt closed on a word fragment %q:\n%s", last, sn)
	}
}

// A sentence start within reach of the left edge is worth more than a word
// start: it is where the thought begins.
func TestAnExcerptPrefersToOpenOnASentence(t *testing.T) {
	lead := "Вчера мы долго ходили вокруг этого места и ничего не поняли толком совсем. " +
		"Причина оказалась в том, что бэкофф считался от нуля, поэтому четвёртая попытка шла сразу же после третьей."
	text := lead + " " + strings.Repeat("дальше идёт ещё текст про другое ", 20)

	sn := snippet(text, "четвёртая", nil)
	body := strings.TrimSuffix(strings.TrimPrefix(sn, "… "), " …")
	if !strings.HasPrefix(body, "Причина оказалась") {
		t.Errorf("the excerpt did not open on the sentence that carries the answer:\n%s", sn)
	}
	if r := []rune(body); len(r) > 0 && !unicode.IsUpper(r[0]) {
		t.Errorf("the excerpt opened mid-sentence: %q", body)
	}
}
