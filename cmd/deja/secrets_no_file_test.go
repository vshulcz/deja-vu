package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The report closes with "the files above are yours to edit or delete", and the
// rows from a database store have no file above. Measured on this machine, 36
// of 83 findings were in one — nearly half the report, silently left with no
// action and no reason (#3823).
func TestTheSessionsWithNoFileAreAccountedFor(t *testing.T) {
	when := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	scan := index.SecretScan{
		Sessions: 2,
		Findings: []index.SecretFinding{
			{Kind: "github-token", Harness: "claude", ID: "a", Project: "app",
				Path: "/tmp/a.jsonl", When: when, Count: 1},
			{Kind: "openai-key", Harness: "zed", ID: "b", Project: "app", When: when, Count: 1},
		},
	}
	var out bytes.Buffer
	printSecrets(&out, scan, 10)
	got := out.String()

	if !strings.Contains(got, "a.jsonl") {
		t.Errorf("the row with a file does not name it:\n%s", got)
	}
	if !strings.Contains(got, "1 of the sessions above keep their history in a database") {
		t.Errorf("the row with no file is not accounted for:\n%s", got)
	}
	// And the sentence stays off a report where every row has a file, where it
	// would be a line about nothing.
	var all bytes.Buffer
	scan.Findings = scan.Findings[:1]
	printSecrets(&all, scan, 10)
	if strings.Contains(all.String(), "keep their history in a database") {
		t.Errorf("the sentence fired with no database row to explain:\n%s", all.String())
	}
}
