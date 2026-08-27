package sources

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A cline or roo task is one JSON document: if it will not unmarshal, nothing
// from that file is indexed. Counting it as one malformed *line* said "1 line
// could not be read" about three thousand turns, while the bucket that fits —
// a path deja could not read, with the parser's words beside it — was right
// there (#2232).
func TestAWholeTaskThatWillNotParseIsReportedAsAPath(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("[")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, `{"ts":%d,"type":"say","say":"text","text":"turn %d about the pool"},`, 1782900000+i, i)
	}
	truncated := strings.TrimSuffix(b.String(), ",") // no closing bracket

	sessions := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	modern := filepath.Join(sessions, "abc.messages.json")
	if err := os.WriteFile(modern, []byte(truncated), 0o644); err != nil {
		t.Fatal(err)
	}
	task := filepath.Join(dir, "tasks", "123")
	if err := os.MkdirAll(task, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(task, "api_conversation_history.json")
	if err := os.WriteFile(legacy, []byte(truncated), 0o644); err != nil {
		t.Fatal(err)
	}
	rooTask := filepath.Join(dir, "roo", "tasks", "456")
	if err := os.MkdirAll(rooTask, 0o755); err != nil {
		t.Fatal(err)
	}
	roo := filepath.Join(rooTask, "api_conversation_history.json")
	if err := os.WriteFile(roo, []byte(truncated), 0o644); err != nil {
		t.Fatal(err)
	}

	DiagSnapshot() // start from nothing
	if _, err := ParseClineFile(modern); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseClineFile(legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseRooTask(roo); err != nil {
		t.Fatal(err)
	}
	malformed, failed := DiagSnapshot()

	for _, p := range []string{modern, legacy, roo} {
		if n := malformed[p]; n != 0 {
			t.Errorf("%s: counted as %d malformed line(s); it is a whole file", filepath.Base(p), n)
		}
		if failed[p] == "" {
			t.Errorf("%s: not reported as a path deja could not read", filepath.Base(p))
			continue
		}
		// The parser's own words travel with it, so `doctor --json` says what
		// was wrong rather than only that something was.
		if !strings.Contains(failed[p], "JSON") && !strings.Contains(failed[p], "json") {
			t.Errorf("%s: the recorded reason says nothing about the parse: %q", filepath.Base(p), failed[p])
		}
	}
}
