package sources

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// claudeContentCases are content fields with the shapes the readers branch on:
// every item type, a type key spelled in capitals or given twice, a type that
// is not a string, items that are not objects, and content that is not an
// array at all.
var claudeContentCases = []string{
	`"plain text"`,
	`{"not":"an array"}`,
	`[1, "two", null]`,
	`[{"type":"text","text":"hello"},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./..."}}]`,
	`[{"type":"tool_use","id":"t2","name":"Edit","input":{"file_path":"/tmp/app/a.go","old_string":"x := 1","new_string":"x := 2"}}]`,
	`[{"type":"tool_use","id":"t3","name":"Write","input":{"file_path":"/tmp/app/b.go","content":"package b\n"}}]`,
	`[{"type":"tool_use","id":"t4","name":"MultiEdit","input":{"file_path":"/tmp/app/c.go","edits":[{"old_string":"a","new_string":"b"}]}}]`,
	`[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"Exit code 2\nFAIL"}]`,
	`[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"ok"}]}]`,
	`[{"Type":"tool_use","id":"t5","name":"Bash","input":{"command":"make build"}}]`,
	`[{"TYPE":"tool_result","tool_use_id":"t5","is_error":true,"content":"Exit code 1"}]`,
	`[{"type":"text","type":"tool_use","id":"t6","name":"Bash","input":{"command":"ls -la"}}]`,
	`[{"type":"tool_use","type":"text","text":"twice"}]`,
	`[{"type":7,"id":"t7","name":"Bash","input":{"command":"npm test"}}]`,
	`[{"type":"tool_use","id":"t8","name":"Bash","input":"not an object"}]`,
	`[{"type":"tool_result","tool_use_id":"t8","is_error":"yes","content":"Exit code 3"}]`,
	`[{"type":"tool_use","name":"Read","input":{"file_path":"/tmp/app/d.go"}},{"type":"tool_use","name":"NotebookEdit","input":{"notebook_path":"/tmp/n.ipynb","new_source":"x","edit_mode":"replace"}}]`,
}

// Splitting the content once must not change what any reader takes from it:
// the reference is the same content with no item's type known, which skips
// nothing and is how each reader decoded every item before (#5586).
func TestSplitContentReadsWhatEachReaderReadAlone(t *testing.T) {
	for _, raw := range claudeContentCases {
		fresh := func() *claudeContent {
			c := splitClaudeContent(json.RawMessage(raw))
			c.kind, c.known = make([]string, len(c.items)), make([]bool, len(c.items))
			return c
		}
		c := splitClaudeContent(json.RawMessage(raw))
		gotText, gotTool := claudeTextKindIn(c)
		wantText, wantTool := claudeTextKindIn(fresh())
		checks := []struct {
			name      string
			got, want any
		}{
			{"text", []any{gotText, gotTool}, []any{wantText, wantTool}},
			{"paths", claudeToolPathsIn(c), claudeToolPathsIn(fresh())},
			{"edits", claudeEditSpansIn(c), claudeEditSpansIn(fresh())},
			{"wrote", claudeWroteRecordsIn(c), claudeWroteRecordsIn(fresh())},
			{"commands", claudeCommandsIn(c), claudeCommandsIn(fresh())},
			{"outcomes", claudeToolOutcomesIn(c), claudeToolOutcomesIn(fresh())},
		}
		for _, ch := range checks {
			if !reflect.DeepEqual(ch.got, ch.want) {
				t.Errorf("%s of %s = %#v, want %#v", ch.name, raw, ch.got, ch.want)
			}
		}
	}
}

// A megabyte line buffer per transcript was 11.7 GB of a 12k-session cold
// build (#5586). Reading many small transcripts allocates far less than one
// buffer each now.
func TestReadingTranscriptsReusesTheLineBuffer(t *testing.T) {
	if raceEnabled {
		t.Skip("sync.Pool drops buffers on purpose under -race")
	}
	dir := t.TempDir()
	paths := make([]string, 0, 64)
	for i := range 64 {
		p := filepath.Join(dir, fmt.Sprintf("r%d.jsonl", i))
		line := `{"type":"response_item","payload":{"type":"message","role":"user","content":"hi"}}` + "\n"
		if err := os.WriteFile(p, []byte(strings.Repeat(line, 3)), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for _, p := range paths {
		if err := scanJSONLFromOffset(p, 0, func(map[string]any) {}); err != nil {
			t.Fatal(err)
		}
	}
	runtime.ReadMemStats(&after)
	if per := (after.TotalAlloc - before.TotalAlloc) / uint64(len(paths)); per > 256<<10 {
		t.Fatalf("%d bytes allocated per transcript of three short lines", per)
	}
}

// Naming the store a kind belongs to is asked per session; it used to build
// the whole registry to answer.
func TestHarnessForKindAllocatesNothing(t *testing.T) {
	if HarnessForKind("codex") != "codex" {
		t.Fatal("codex is not its own store")
	}
	if n := testing.AllocsPerRun(50, func() { _ = HarnessForKind("claude-history") }); n != 0 {
		t.Fatalf("HarnessForKind allocated %v times", n)
	}
}
