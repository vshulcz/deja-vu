package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// docSection is one heading's text, up to the next heading of the same or a
// shallower level — a subsection ends where its parent does. That is what makes
// these tests a check on where a key is documented rather than on the document
// as a whole: cutting at the same level only, the last `###` in a file ran to
// the end of it (#1951).
func docSection(t *testing.T, doc, heading string) string {
	t.Helper()
	// A heading is matched as a whole line, and a Windows checkout ends every
	// line with a carriage return, so the boundary test below never held and
	// eleven of these tests failed at once without naming the reason (#2081).
	// `.gitattributes` keeps a fresh clone at LF; this keeps an existing
	// working tree that already has CRLF from failing the same way.
	doc = strings.ReplaceAll(doc, "\r\n", "\n")
	// A heading is a whole line. Matching anywhere would let prose that names a
	// heading — a pointer to it, a sentence about the format — start the
	// section somewhere in the middle of a paragraph, and matching only its
	// start would take a sentence that opens with one.
	i := -1
	for at := 0; ; {
		k := strings.Index(doc[at:], heading)
		if k < 0 {
			break
		}
		k += at
		at = k + len(heading)
		startsLine := k == 0 || doc[k-1] == '\n'
		endsLine := at == len(doc) || doc[at] == '\n'
		if startsLine && endsLine {
			i = k
			break
		}
	}
	if i < 0 {
		t.Fatalf("docs/json-output.md has no line %q", heading)
	}
	rest := doc[i+len(heading):]
	level := strings.Count(strings.SplitN(heading, " ", 2)[0], "#")
	fenced := false
	for at := 0; ; {
		nl := strings.IndexByte(rest[at:], '\n')
		if nl < 0 {
			return rest
		}
		at += nl + 1
		line := rest[at:]
		// A `#` inside an example is a comment, not a heading — `deja log`
		// output starts its rows with one. Sections here end on headings only.
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		hashes := len(line) - len(strings.TrimLeft(line, "#"))
		if hashes > 0 && hashes <= level && strings.HasPrefix(line[hashes:], " ") {
			return rest[:at-1]
		}
	}
}

// jsonKeys is every key a value emits, nested ones included, once each.
func jsonKeys(v any, into map[string]bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, inner := range t {
			into[k] = true
			jsonKeys(inner, into)
		}
	case []any:
		for _, inner := range t {
			jsonKeys(inner, into)
		}
	}
}

// `deja show --json` and `deja last --json` are documented in prose and pinned
// by nothing, while `stats --json` has been pinned key by key since #1710 and
// `--impact --json` since #1900. A field added to the session object reaches
// three commands' output at once, so this is the check that a document entry
// comes with it.
//
// Each command is checked against its own section plus the shared "session
// object" table, which is where the keys their sections do not name live. Not
// the whole document: a key documented for `search` alone would otherwise pass
// as documented here.
//
// It pins what this corpus emits, which is the always-present half. The
// optional keys — agent_title, touched, gave_up, orig_id, from, lifecycle,
// kind, parent, agent — are in that table today and a fixture would have to
// grow a case each to hold them there.
func TestSessionJSONKeysAreDocumented(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for i := 0; i < 4; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		lines = append(lines, `{"type":"`+role+`","sessionId":"sess1","cwd":"/proj","timestamp":"2026-08-20T10:0`+
			string(rune('0'+i))+`:00Z","message":{"role":"`+role+`","content":"the parser retried"}}`)
	}
	if err := os.WriteFile(filepath.Join(store, "sess1.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	_ = tmp

	doc, err := os.ReadFile("../../docs/json-output.md")
	if err != nil {
		t.Fatal(err)
	}
	documented := string(doc)

	for _, c := range []struct {
		name    string
		args    []string
		section string
		least   int
	}{
		{"show", []string{"show", "sess1", "--harness", "claude", "--json"},
			"## `deja show <exact-id> --harness <name> --json`", 18},
		{"last", []string{"last", "3", "--json"}, "## `deja last --json`", 10},
	} {
		out, err := captureRun(t, c.args...)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var body any
		if err := json.Unmarshal([]byte(out), &body); err != nil {
			t.Fatalf("%s: %v: %s", c.name, err, out)
		}
		keys := map[string]bool{}
		jsonKeys(body, keys)
		if len(keys) < c.least {
			t.Fatalf("%s emitted %d keys, fewer than the %d this corpus should produce: %v",
				c.name, len(keys), c.least, keys)
		}
		// The command's own section plus the shared table, not the whole
		// document: a key documented for `search` alone would otherwise read as
		// documented here.
		where := docSection(t, documented, c.section) + docSection(t, documented, "### The session object")
		var missing []string
		for k := range keys {
			if !strings.Contains(where, "`"+k+"`") && !strings.Contains(where, `"`+k+`"`) {
				missing = append(missing, k)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("deja %s --json emits %v, absent from its section of docs/json-output.md and from the session object table",
				c.name, missing)
		}
	}
}
