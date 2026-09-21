package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Roo's editor sends a SEARCH/REPLACE block, so the reader took the path a
// call named and neither side of the change: blame could say a session touched
// the file and nothing about the line. Shape from Roo's own diff strategy —
// the same one the fixture in roo_work_records_test.go came from.
func writeRooEditTask(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, "tasks", "1788845325719")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `[
	 {"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"apply_diff","input":{"path":"internal/retry/loop.go","diff":"<<<<<<< SEARCH\n:start_line:42\n-------\n\tfor i := 0; i < attempts; i++ {\n=======\n\tfor i := 0; i <= attempts; i++ {\n>>>>>>> REPLACE\n<<<<<<< SEARCH\n\treturn errors.New(\"gave up\")\n=======\n\treturn fmt.Errorf(\"gave up after %d attempts\", attempts)\n>>>>>>> REPLACE"}}]},
	 {"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"search_and_replace","input":{"path":"internal/retry/backoff.go","search":"time.Sleep(time.Second * delay)","replace":"time.Sleep(backoff.Next(delay))"}}]},
	 {"role":"assistant","content":[{"type":"tool_use","id":"t3","name":"search_and_replace","input":{"path":"internal/retry/names.go","search":"attempt([0-9]+)","replace":"attempt_$1","use_regex":true}}]},
	 {"role":"assistant","content":[{"type":"tool_use","id":"t4","name":"write_to_file","input":{"path":"internal/retry/clock.go","content":"package retry\n\n// Clock is the part of time this package needs, so a test can stop it.\ntype Clock interface{ Now() time.Time }\n"}}]},
	 {"role":"assistant","content":[{"type":"tool_use","id":"t5","name":"apply_diff","input":{"path":"internal/retry/half.go","diff":"<<<<<<< SEARCH\n\tthis block never closes\n"}}]},
	 {"role":"assistant","content":[{"type":"tool_use","id":"t6","name":"apply_diff","input":{"path":"internal/retry/open.go","diff":"<<<<<<< SEARCH\n\tif attempts == 0 { return nil }\n=======\n\tif attempts <= 0 { return ErrNoAttemptsRequested }\n"}}]}
	]`
	path := filepath.Join(dir, "api_conversation_history.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRooDiffBecomesEditAndWroteRecords(t *testing.T) {
	path := writeRooEditTask(t, t.TempDir())
	for _, reader := range []struct {
		name  string
		parse func(string) ([]model.Session, error)
	}{
		{"roo", ParseRooTask},
		{"kilocode", ParseKiloTask},
		{"cline legacy", parseClineLegacyTask},
	} {
		ss, err := reader.parse(path)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%s parse: %v, %d sessions", reader.name, err, len(ss))
		}
		var edits, wrote []string
		for _, m := range ss[0].Messages {
			switch m.Role {
			case RoleEdit:
				edits = append(edits, m.Text)
			case RoleWrote:
				wrote = append(wrote, m.Text)
			}
		}
		// Both blocks of the first call, the literal search, and neither side
		// of the regex one.
		want := []string{
			"internal/retry/loop.go\n\tfor i := 0; i < attempts; i++ {",
			"internal/retry/loop.go\n\treturn errors.New(\"gave up\")",
			"internal/retry/backoff.go\ntime.Sleep(time.Second * delay)",
		}
		if len(edits) != len(want) {
			t.Fatalf("%s: %d edit records, want %d: %q", reader.name, len(edits), len(want), edits)
		}
		for i, w := range want {
			if edits[i] != w {
				t.Errorf("%s: edit %d = %q, want %q", reader.name, i, edits[i], w)
			}
		}
		for _, span := range edits {
			if strings.Contains(span, "attempt([0-9]+)") {
				t.Errorf("%s: a regex was recorded as text the file held: %q", reader.name, span)
			}
			if strings.Contains(span, "SEARCH") || strings.Contains(span, "=======") {
				t.Errorf("%s: a marker line reached the span: %q", reader.name, span)
			}
			if strings.Contains(span, ":start_line:") || strings.Contains(span, "-------") {
				t.Errorf("%s: the block header reached the span: %q", reader.name, span)
			}
		}
		// The written side has to carry the replacement and the whole-file
		// write, and a line only the new side holds must match.
		joined := strings.Join(wrote, "\n")
		for _, f := range []string{"internal/retry/loop.go", "internal/retry/clock.go"} {
			if !strings.Contains(joined, f+"\n") {
				t.Errorf("%s: no written record for %s: %q", reader.name, f, wrote)
			}
		}
		h, ok := HashWrittenLine("\treturn fmt.Errorf(\"gave up after %d attempts\", attempts)")
		if !ok {
			t.Fatal("the new line is too short to be evidence — pick another")
		}
		var found bool
		for _, rec := range wrote {
			if p, has := WroteRecordHas(rec, h); has && p == "internal/retry/loop.go" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: the line the diff wrote is in no record: %q", reader.name, wrote)
		}
		// An unterminated block records nothing rather than guessing where it
		// closed — whichever of the two markers is the one missing.
		all := strings.Join(edits, "\n") + joined
		for _, f := range []string{"internal/retry/half.go", "internal/retry/open.go"} {
			if strings.Contains(all, f) {
				t.Errorf("%s: the unclosed block in %s produced a record: %q %q",
					reader.name, f, edits, wrote)
			}
		}
	}
}

// Three edge shapes of one call, checked on the records directly: the regex
// flag arrives as a string from the XML the model writes as well as a bool
// from the stored history, a path carrying a newline cannot be held by the
// record format, and a span longer than the cap is cut rather than dropped.
func TestRooEditRecordEdges(t *testing.T) {
	call := func(name string, in map[string]any) []any {
		return []any{map[string]any{"type": "tool_use", "name": name, "input": in}}
	}

	spans, wrote := rooEditRecords(call("search_and_replace", map[string]any{
		"path": "a/b.go", "search": "attempt([0-9]+)", "replace": "attempt_$1", "use_regex": "true",
	}))
	if len(spans) != 0 || len(wrote) != 0 {
		t.Errorf("a string use_regex was read as a literal: %q %q", spans, wrote)
	}

	spans, wrote = rooEditRecords(call("apply_diff", map[string]any{
		"path": "a\nb.go", "diff": "<<<<<<< SEARCH\nold enough to be evidence\n=======\nnew enough to be evidence\n>>>>>>> REPLACE",
	}))
	if len(spans) != 0 || len(wrote) != 0 {
		t.Errorf("a path with a newline produced a record: %q %q", spans, wrote)
	}

	long := strings.Repeat("x", editSpanMax+500)
	spans, _ = rooEditRecords(call("apply_diff", map[string]any{
		"path": "a/b.go", "diff": "<<<<<<< SEARCH\n" + long + "\n=======\nshort\n>>>>>>> REPLACE",
	}))
	if len(spans) != 1 {
		t.Fatalf("an oversized span produced %d records, want 1", len(spans))
	}
	if got := len(spans[0]) - len("a/b.go\n"); got != editSpanMax {
		t.Errorf("span kept %d bytes, want the %d-byte cap", got, editSpanMax)
	}
}

// The one shape that makes the `-------` rule ambiguous: a replaced span whose
// own first line is dashes. The rule counts as a header only under a line
// number, so this span keeps it.
func TestRooDiffKeepsDashesThatAreNotABlockHeader(t *testing.T) {
	replaced, written := rooDiffSides("<<<<<<< SEARCH\n-------\nold heading\n=======\n-------\nnew heading\n>>>>>>> REPLACE")
	if len(replaced) != 1 || replaced[0] != "-------\nold heading" {
		t.Errorf("replaced = %q, want the dashes kept", replaced)
	}
	if len(written) != 1 || written[0] != "-------\nnew heading" {
		t.Errorf("written = %q, want the dashes kept", written)
	}
}
