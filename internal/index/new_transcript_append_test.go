package index

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// A new conversation is a new file, so the commonest update there is was the
// one that rewrote the whole store: `canAppendIncremental` refused any path it
// had not seen, and the replacement path reads every surviving record back
// through the tokenizer. Measured on a synthetic Codex store, one new
// one-message transcript cost 1.19s at 42.9 MB of records and 4.76s at
// 171.4 MB, against 0.11s and 0.30s for a message appended to a file already
// indexed — the cost followed the store, not the new file (#3500).
//
// The progress line is the assertion because it names the path taken: the
// append path says what it updated, the replacement path prints its
// changed/removed counts.
func TestANewTranscriptIsAppendedNotRewritten(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	first := filepath.Join(claude, "p", "first.jsonl")
	write(t, first, claudeLine("s-first", "2026-01-02T03:04:05Z", "the exporter retries without a pause"))
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	write(t, filepath.Join(claude, "p", "second.jsonl"),
		claudeLine("s-second", "2026-01-02T05:00:00Z", "the billing webhook retries twice"))
	var progress bytes.Buffer
	if err := Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	if got := progress.String(); !strings.Contains(got, "updated 1 file") {
		t.Errorf("a new transcript did not take the append path: %q", got)
	}

	for query, want := range map[string]string{
		"billing webhook retries": "s-second",
		"exporter retries":        "s-first",
	} {
		ss, err := Search(dir, search.Options{Query: query, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(ss) != 1 || ss[0].ID != want {
			t.Fatalf("%q found %#v, want the session %s", query, ss, want)
		}
	}
}

// The append path reads a file from an offset, which only a kind with a resume
// parser can do. A new file of any other kind has to keep taking the
// replacement path: appending it would mark the file read and index none of
// its sessions.
func TestANewFileOfAKindThatCannotResumeIsRewritten(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	write(t, filepath.Join(claude, "p", "first.jsonl"),
		claudeLine("s-first", "2026-01-02T03:04:05Z", "the exporter retries without a pause"))
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	gemini := filepath.Join(os.Getenv("DEJA_GEMINI_ROOT"), "tmp", "gid", "chats", "g.json")
	write(t, gemini, `{"sessionId":"g-new","startTime":"2026-01-03T00:00:00Z","messages":`+
		`[{"type":"user","content":"the invoice job hammers the billing API"}]}`)
	var progress bytes.Buffer
	if err := Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	if got := progress.String(); strings.Contains(got, "updated 1 file") {
		t.Errorf("a kind with no resume parser took the append path: %q", got)
	}
	ss, err := Search(dir, search.Options{Query: "invoice job hammers", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "g-new" {
		t.Fatalf("the new gemini session is not indexed: %#v", ss)
	}
}
