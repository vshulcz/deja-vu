package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A question carrying one rare word the store holds is answered — and only
// when the words it stands among are words the corpus knows. That second half
// is what makes the first safe: among words nothing has ever written, a single
// surviving anchor is as likely to be a typo as a question, and the tier owes
// silence rather than a guess. Both halves are one condition in
// relevanceSearch (`len(terms) >= 3 && termsKnown >= 2`) and neither was
// visible from outside.
func TestALoneRareAnchorIsServedOnlyAmongWordsTheCorpusKnows(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "projects", "-tmp-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(sid, text string, age time.Duration) {
		rec := claudeRecord(t, map[string]any{
			"type": "user", "sessionId": sid, "cwd": "/tmp/app",
			"timestamp": time.Now().Add(-age).UTC().Format(time.RFC3339),
			"message":   map[string]any{"role": "user", "content": text},
		})
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for i, text := range []string{
		"deployed the service and watched the dashboards for a while",
		"the on-call rotation was quiet, wrote some docs instead",
		"refactored the client, moved the retries into the transport",
		"upgraded the database and ran the migrations in the morning",
		"cleaned up the logging, too many fields in every line",
		"the release to production went out on friday without a hitch",
		"took a break after the incident review, everyone was tired",
	} {
		write(fmt.Sprintf("f%d", i), text, time.Duration(i+3)*time.Hour)
	}
	// The one session that says the rare word, worded so that no question
	// below can reach it by an AND over its words.
	write("target", "the worker died because the retry_budget was exhausted", 2*time.Hour)

	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	// Asked among words this corpus uses — "break" and "production" are in it
	// — the rare word carries the question on its own, and it is the relevance
	// tier that carries it. The tier matters as much as the hit: the rungs
	// above it would mean the answer came from a spelling guess.
	out, _ := captureRun(t, "search", "--json", "why did retry_budget break production")
	if !strings.Contains(out, "target") {
		t.Errorf("a question standing on one rare word the store holds found nothing:\n%s", out)
	}
	if !strings.Contains(out, `"tier":"relevance"`) {
		t.Errorf("the question was answered by another rung, so this measures something else:\n%s", out)
	}
	// Asked among words nothing has written, the same anchor is withheld: at
	// that point deja cannot tell a question from a typo.
	if out, _ := captureRun(t, "search", "why did retry_budget zzqx wwvv"); strings.Contains(out, "target") {
		t.Errorf("answered on one anchor among words the corpus never saw:\n%s", out)
	}
	// The other half of the same condition, and a separate rule: a short query
	// keeps the silence contract whatever its words are, because one lucky
	// word out of two is not a question.
	if out, _ := captureRun(t, "search", "retry_budget kkjh"); strings.Contains(out, "target") {
		t.Errorf("a two-word query was answered on one of them:\n%s", out)
	}
	// And the word on its own still answers exactly, which is the tier below.
	if out, _ = captureRun(t, "search", "retry_budget"); !strings.Contains(out, "target") {
		t.Fatalf("the rare word alone finds nothing, so this measures nothing:\n%s", out)
	}
}
