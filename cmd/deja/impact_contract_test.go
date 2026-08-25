package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/usage"
)

// `deja stats --impact --json` is a machine surface that was documented
// nowhere, and it grew `since` in #1890 while `credited_aloud` vanished for one
// commit in the same change. `stats --json` has had its keys pinned to the
// document since #1710; this is that pin for the screen beside it (#1898).
func TestImpactJSONKeysMatchTheDocumentedContract(t *testing.T) {
	doc, err := os.ReadFile("../../docs/json-output.md")
	if err != nil {
		t.Fatal(err)
	}
	section := string(doc)
	i := strings.Index(section, "## `deja stats --impact --json`")
	if i < 0 {
		t.Fatal("docs/json-output.md does not describe `deja stats --impact --json`")
	}
	section = section[i:]
	if end := strings.Index(section[3:], "\n## "); end > 0 {
		section = section[:end+3]
	}

	var out strings.Builder
	full := usage.ImpactReport{
		Recalls: 3, Injections: 2, ServedBytes: 100, RawBytes: 1000,
		ReusedTwice: 1, DejaVuMoments: 1, Since: time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC),
	}
	if err := printImpact(&out, full, 2, true); err != nil {
		t.Fatal(err)
	}
	var emitted map[string]any
	if err := json.Unmarshal([]byte(out.String()), &emitted); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	for k := range emitted {
		if !strings.Contains(section, "`"+k+"`") {
			t.Errorf("--impact --json emits %q, missing from its section of docs/json-output.md", k)
		}
	}
	// And the other way: a key the document names must still be emitted, so a
	// field that leaves the struct cannot leave the document behind.
	for _, k := range []string{"recalls", "injections", "served_bytes", "raw_bytes",
		"reused_twice", "dejavu_moments", "since", "credited_aloud"} {
		if !strings.Contains(section, "`"+k+"`") {
			continue
		}
		if _, ok := emitted[k]; !ok {
			t.Errorf("docs/json-output.md names %q, no longer emitted", k)
		}
	}
}
