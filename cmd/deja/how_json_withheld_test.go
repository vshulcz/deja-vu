package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// howEntries carries the withheld count for a stated reason: filtering on its
// own turns a leak into a confident "no command mentions that" over records a
// rule hid. The prose path says so in a sentence, and a script cannot read a
// sentence — so `commands: []` alone would mean both "nothing matched" and
// "everything that matched was withheld".
func TestHowJSONSaysWhatARuleWithheld(t *testing.T) {
	dir := importedCommandStore(t)

	// Control: with no rule in force the command is answered, or the arm below
	// is measuring an empty store rather than a policy.
	var open bytes.Buffer
	if err := runHow(dir, []string{"--json", "kafkactl"}, &open); err != nil {
		t.Fatal(err)
	}
	var before howJSON
	if err := json.Unmarshal(open.Bytes(), &before); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, open.String())
	}
	if len(before.Commands) == 0 {
		t.Fatalf("nothing matched without a policy, so this test measures nothing:\n%s", open.String())
	}

	writePolicy(t, `{"activations":{"search":{"imported":false}}}`)

	var buf bytes.Buffer
	if err := runHow(dir, []string{"--json", "kafkactl"}, &buf); err != nil {
		t.Fatal(err)
	}
	var got howJSON
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if len(got.Commands) != 0 {
		t.Fatalf("the policy should have withheld everything, got %d rows", len(got.Commands))
	}
	if got.Withheld < 1 {
		t.Errorf("withheld=%d — an empty result reads as \"nothing matched\":\n%s", got.Withheld, buf.String())
	}
}

// The ignore rule is the other half, and it is a separate number: the trust
// policy is about where a session came from, the ignore rule about a path the
// reader named. A consumer that can see only one of them still cannot tell an
// empty answer from a hidden one.
func TestHowJSONSaysWhatTheIgnoreRuleHid(t *testing.T) {
	dir := importedCommandStore(t)
	writePolicy(t, `{"ignore":["*projx*"]}`)

	var buf bytes.Buffer
	if err := runHow(dir, []string{"--json", "kafkactl"}, &buf); err != nil {
		t.Fatal(err)
	}
	var got howJSON
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if len(got.Commands) != 0 {
		t.Fatalf("the ignore rule should have taken the only match, got %d rows", len(got.Commands))
	}
	if got.Ignored < 1 {
		t.Errorf("ignored=%d — the rule hid the answer and the envelope says nothing:\n%s", got.Ignored, buf.String())
	}
}

// importedCommandStore holds one imported session that ran a command, so a
// local-only policy has something to withhold.
func importedCommandStore(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	var batch []byte
	for i, rec := range []index.SyncRecord{
		{Harness: "claude", SessionID: "peer1", Project: "tmp/projx", Role: "user",
			Text: "how do we drive kafka here"},
		{Harness: "claude", SessionID: "peer1", Project: "tmp/projx", Role: "command",
			Text: "kafkactl consume orders --from-beginning"},
	} {
		b, err := json.Marshal(rec)
		if err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
		batch = append(append(batch, b...), '\n')
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), batch, 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatalf("import: %v (%s)", err, strings.TrimSpace(out))
	}
	return dir
}
