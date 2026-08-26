package sources

import (
	"encoding/json"
	"strings"
	"testing"
)

// An edit span is recorded as "path\nspan" and read back by splitting on the
// first newline, so a path carrying one puts the rest of itself at the head of
// the span — and restore hands that back as the exact bytes that stopped
// existing (#2042). The format cannot express such a path, so the span is not
// recorded rather than recorded wrong.
func TestAnEditSpanIsNotRecordedForAPathTheFormatCannotHold(t *testing.T) {
	t.Setenv("DEJA_INDEX_EDITS", "1")
	content := func(path string) any {
		var v any
		raw := `[{"type":"tool_use","name":"Edit","input":` +
			`{"file_path":` + quote(t, path) + `,"old_string":"size = 20","new_string":"size = 40"}}]`
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}

	// The premise: an ordinary path records the span, and the span is the old
	// text alone.
	got := editSpansFromContent(content("/tmp/app/pool.go"))
	if len(got) != 1 {
		t.Fatalf("an ordinary edit recorded %d spans, so this measures nothing", len(got))
	}
	if recorded, body, _ := strings.Cut(got[0], "\n"); recorded != "/tmp/app/pool.go" || body != "size = 20" {
		t.Fatalf("the ordinary edit records %q / %q", recorded, body)
	}

	for _, path := range []string{
		"/tmp/app/pool.go\ndeja forget --all",
		"/tmp/app/pool.go\rrm -rf /",
	} {
		if got := editSpansFromContent(content(path)); len(got) != 0 {
			_, body, _ := strings.Cut(got[0], "\n")
			t.Errorf("a path the record cannot hold produced a span whose body is %q", body)
		}
		// The parser that actually runs for claude has to agree with the
		// reference one — that is what the differential test in #502 rests on.
		raw := `[{"type":"tool_use","name":"Edit","input":` +
			`{"file_path":` + quote(t, path) + `,"old_string":"size = 20","new_string":"size = 40"}}]`
		if got := claudeEditSpans([]byte(raw)); len(got) != 0 {
			_, body, _ := strings.Cut(got[0], "\n")
			t.Errorf("the fast parser recorded a span whose body is %q", body)
		}
	}
}

func quote(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The files a session touched are recorded one per line and split back apart by
// the index, so the same newline that corrupted a span makes a session claim it
// touched a file it never opened — which reaches the files listing, blame, and
// the project the session is filed under (#2042).
func TestATouchedPathTheRecordCannotHoldIsNotRecorded(t *testing.T) {
	t.Setenv("DEJA_INDEX_TOOL_PATHS", "1")
	content := func(path string) (any, []byte) {
		raw := `[{"type":"tool_use","name":"Read","input":{"file_path":` + quote(t, path) + `}}]`
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			t.Fatal(err)
		}
		return v, []byte(raw)
	}

	// The premise: an ordinary path is recorded, by both parsers.
	ref, raw := content("/tmp/app/pool.go")
	if got := toolPathsFromContent(ref); got != "/tmp/app/pool.go" {
		t.Fatalf("an ordinary read records %q, so this measures nothing", got)
	}
	if got := claudeToolPaths(raw); got != "/tmp/app/pool.go" {
		t.Fatalf("the fast parser records %q for an ordinary read", got)
	}

	for _, path := range []string{
		"/tmp/app/pool.go\n/etc/passwd",
		"/tmp/app/pool.go\r/etc/passwd",
	} {
		ref, raw := content(path)
		if got := toolPathsFromContent(ref); got != "" {
			t.Errorf("a path the record cannot hold was recorded as %q", got)
		}
		if got := claudeToolPaths(raw); got != "" {
			t.Errorf("the fast parser recorded %q", got)
		}
	}
}
