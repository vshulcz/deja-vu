package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/jsonout"
)

// `deja how` answers "how do I run this here", and its answer is the single
// output most worth piping somewhere else — yet it was the one command in this
// family without `--json` (#1931).

func TestHowJSONIsAVersionedEnvelope(t *testing.T) {
	root := frictionEnv(t)
	writeFrictionCorpus(t, root, index.FrictionMinSessions)
	var buf bytes.Buffer
	if err := runHow(index.DefaultDir(), []string{"--json", "go"}, &buf); err != nil {
		t.Fatalf("how --json refused: %v", err)
	}
	var got howJSON
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if got.SchemaVersion != jsonout.Version {
		t.Fatalf("schema_version = %d, want %d", got.SchemaVersion, jsonout.Version)
	}
	if got.Commands == nil {
		t.Fatal("commands is null; an empty result must still be an array")
	}
}

// The empty case keeps the shape. The prose path answers with a sentence naming
// the terms, and separately with a policy note when a rule withheld everything;
// a script can branch on neither.
func TestHowJSONKeepsItsShapeWhenNothingMatches(t *testing.T) {
	root := frictionEnv(t)
	writeFrictionCorpus(t, root, index.FrictionMinSessions)
	_ = root
	var buf bytes.Buffer
	if err := runHow(index.DefaultDir(), []string{"--json", "nothing-matches-this-xyzzy"}, &buf); err != nil {
		t.Fatal(err)
	}
	var got howJSON
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if len(got.Commands) != 0 || got.Found != 0 || got.Truncated {
		t.Fatalf("want an empty result, got %+v", got)
	}
}

// `found` and `truncated` exist because the cap note goes to STDERR. A caller
// reading stdout alone could not otherwise tell eight ways to run the tests
// from thirteen.
func TestHowJSONReportsWhatTheLimitHid(t *testing.T) {
	root := frictionEnv(t)
	writeFrictionCorpus(t, root, index.FrictionMinSessions)
	_ = root
	var buf bytes.Buffer
	if err := runHow(index.DefaultDir(), []string{"--json", "--limit", "1", "go"}, &buf); err != nil {
		t.Fatal(err)
	}
	var got howJSON
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if len(got.Commands) > 1 {
		t.Fatalf("--limit 1 returned %d commands", len(got.Commands))
	}
	if got.Found < len(got.Commands) {
		t.Fatalf("found %d is below the rows returned %d", got.Found, len(got.Commands))
	}
	if got.Truncated != (got.Found > len(got.Commands)) {
		t.Fatalf("truncated=%v disagrees with found=%d rows=%d", got.Truncated, got.Found, len(got.Commands))
	}
}

// A command with no search terms is still a usage error, and the flag must not
// become the term — the shape `fix` was refused over.
func TestHowJSONIsNotSearchedFor(t *testing.T) {
	root := frictionEnv(t)
	writeFrictionCorpus(t, root, index.FrictionMinSessions)
	_ = root
	var buf bytes.Buffer
	if err := runHow(index.DefaultDir(), []string{"--json"}, &buf); err == nil {
		t.Fatal("how --json with no terms was accepted; the flag became the query")
	}
}

// Answering one flag must not reopen the dropped-on-the-floor behaviour the
// unknown-flag guard closed.
func TestHowStillRefusesAnUnknownFlag(t *testing.T) {
	root := frictionEnv(t)
	writeFrictionCorpus(t, root, index.FrictionMinSessions)
	_ = root
	var buf bytes.Buffer
	if err := runHow(index.DefaultDir(), []string{"--jsonn", "go"}, &buf); err == nil {
		t.Fatal("how accepted --jsonn")
	}
}
