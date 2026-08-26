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
