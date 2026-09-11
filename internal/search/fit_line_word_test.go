package search

import (
	"strings"
	"testing"
)

// The width cut is a second cut, after the excerpt window: on 400 real messages
// rendered at 118 columns it ended inside a word in 56% of the lines it
// shortened, even with the window's own edges snapped.
func TestTheWidthCutEndsOnAWholeWord(t *testing.T) {
	line := "the exporter retried without any backoff at all, and the fourth attempt went out immediately after the third one"
	got := fitLine(line, 60)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("the line was not cut at all: %q", got)
	}
	body := strings.TrimSuffix(got, "…")
	last := strings.Fields(body)[len(strings.Fields(body))-1]
	if !strings.Contains(line, last+" ") && !strings.Contains(line, last+",") {
		t.Errorf("the line ended on the fragment %q: %q", last, got)
	}
}

// A word longer than the walk keeps its prefix: a path or an identifier is worth
// more cut than dropped, and dropping it would spend a third of the line on
// nothing.
func TestALongTokenIsCutRatherThanDropped(t *testing.T) {
	line := "the chart is kube-prometheus-stack-prometheus-operator-admission-webhook-patch"
	got := fitLine(line, 50)
	if !strings.Contains(got, "kube-prometheus") {
		t.Errorf("the long token was dropped instead of cut: %q", got)
	}
}
