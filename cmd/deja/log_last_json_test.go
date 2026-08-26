package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// `--json` has to answer in JSON, including when the answer is that there is
// nothing. The array form beside this one is `[]` and never null for the reason
// the document gives — it is the output a script polls — while `--last --json`
// returned the human sentence and a parse error with it (#1975).
func TestTheLastDigestAnswersInJSONWhenThereIsNone(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()

	for _, c := range []struct{ name, body string }{
		{"no file at all", ""},
		{"one truncated line", `{"t":"2026-08-25T10:00:00Z","kind":"hook","digest":"the block`},
		{"a line with no digest", `{"t":"2026-08-25T10:00:00Z","kind":"hook","bytes":10}`},
		{"blank lines only", "\n\n\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.body == "" {
				_ = os.Remove(usage.SnapshotPath(dir))
			} else if err := os.WriteFile(usage.SnapshotPath(dir), []byte(c.body), 0o600); err != nil {
				t.Fatal(err)
			}
			var b strings.Builder
			if err := runLogTo(&b, dir, []string{"--last", "--json"}); err != nil {
				t.Fatal(err)
			}
			var v any
			if err := json.Unmarshal([]byte(b.String()), &v); err != nil {
				t.Fatalf("not JSON: %q", strings.TrimSpace(b.String()))
			}
			if v != nil {
				t.Errorf("want null for a digest that is not there, got %#v", v)
			}
		})
	}
}

// The human form is unchanged: a person reading the screen gets the sentence,
// not the word null.
func TestTheLastDigestStillSaysSoInWords(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	_ = os.Remove(usage.SnapshotPath(dir))

	var b strings.Builder
	if err := runLogTo(&b, dir, []string{"--last"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "no injected digests recorded yet") {
		t.Errorf("the human form changed: %q", b.String())
	}
}

// And a digest that is there still comes back whole, so the branch above did
// not take the ordinary answer with it.
func TestTheLastDigestIsStillReturned(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	usage.RecordDigestPolicy(dir, usage.KindHook, "the injected block", 2, 4000, "local-only")

	var b strings.Builder
	if err := runLogTo(&b, dir, []string{"--last", "--json"}); err != nil {
		t.Fatal(err)
	}
	var got usage.Snapshot
	if err := json.Unmarshal([]byte(b.String()), &got); err != nil {
		t.Fatal(err)
	}
	if got.Digest != "the injected block" {
		t.Errorf("digest = %q", got.Digest)
	}
}
