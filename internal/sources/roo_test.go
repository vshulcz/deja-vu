package sources

import (
	"encoding/json"
	"github.com/vshulcz/deja-vu/internal/model"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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
	}), "")
	if len(spans) != 0 || len(wrote) != 0 {
		t.Errorf("a string use_regex was read as a literal: %q %q", spans, wrote)
	}

	spans, wrote = rooEditRecords(call("apply_diff", map[string]any{
		"path": "a\nb.go", "diff": "<<<<<<< SEARCH\nold enough to be evidence\n=======\nnew enough to be evidence\n>>>>>>> REPLACE",
	}), "")
	if len(spans) != 0 || len(wrote) != 0 {
		t.Errorf("a path with a newline produced a record: %q %q", spans, wrote)
	}

	long := strings.Repeat("x", editSpanMax+500)
	spans, _ = rooEditRecords(call("apply_diff", map[string]any{
		"path": "a/b.go", "diff": "<<<<<<< SEARCH\n" + long + "\n=======\nshort\n>>>>>>> REPLACE",
	}), "")
	if len(spans) != 1 {
		t.Fatalf("an oversized span produced %d records, want 1", len(spans))
	}
	if got := len(spans[0]) - len("a/b.go\n"); got != editSpanMax {
		t.Errorf("span kept %d bytes, want the %d-byte cap", got, editSpanMax)
	}
}

// Roo's tools take a path relative to the workspace, and the surfaces compare
// absolute ones: line-level blame matches a record to a file by their last two
// segments, so an edit at the root of a checkout — recorded as `loop.go` —
// could never match the file it changed. The workspace is in the task's own
// metadata, so the records carry the full path.
func TestRooRecordsResolveAgainstTheTaskWorkspace(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "tasks", "1788845325720")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `[
	 {"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"apply_diff","input":{"path":"loop.go","diff":"<<<<<<< SEARCH\n\tfor i := 0; i < attempts; i++ {\n=======\n\tfor i := 0; i <= attempts; i++ {\n>>>>>>> REPLACE"}}]}
	]`
	if err := os.WriteFile(filepath.Join(dir, "api_conversation_history.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := `{"id":"1788845325720","ts":1788845325718,"task":"fix the retry loop","workspace":"/checkout/retry"}`
	if err := os.WriteFile(filepath.Join(dir, "history_item.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}

	ss, err := ParseRooTask(filepath.Join(dir, "api_conversation_history.json"))
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	kinds := map[string]bool{}
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleEdit, RoleWrote, RoleFiles:
			kinds[m.Role] = true
			if !strings.HasPrefix(m.Text, "/checkout/retry/loop.go") {
				t.Errorf("%s record = %q, want the workspace in front of it", m.Role, m.Text)
			}
		}
	}
	for _, want := range []string{RoleEdit, RoleWrote, RoleFiles} {
		if !kinds[want] {
			t.Errorf("no %s record at all", want)
		}
	}

	// An absolute path is left alone, whichever machine's convention it is in.
	for _, p := range []string{"/already/abs.go", `C:\checkout\abs.go`, `\checkout\abs.go`} {
		if got := rooAbsPath(p, "/checkout/retry"); got != p {
			t.Errorf("rooAbsPath(%q) = %q, want it untouched", p, got)
		}
	}
	// And without a workspace the path stays as it was recorded rather than
	// being resolved against the wrong root.
	if got := rooAbsPath("loop.go", ""); got != "loop.go" {
		t.Errorf("rooAbsPath with no workspace = %q, want the recorded path", got)
	}
}

// The one shape that makes the `-------` rule ambiguous: a replaced span whose
// own first line is dashes. The rule counts as a header only under a line
// number, so this span keeps it.
func TestRooDiffKeepsDashesThatAreNotABlockHeader(t *testing.T) {
	replaced, written := rooDiffSides("<<<<<<< SEARCH\n-------\nold heading\n=======\n-------\nnew heading\n>>>>>>> REPLACE", rooMarkers)
	if len(replaced) != 1 || replaced[0] != "-------\nold heading" {
		t.Errorf("replaced = %q, want the dashes kept", replaced)
	}
	if len(written) != 1 || written[0] != "-------\nnew heading" {
		t.Errorf("written = %q, want the dashes kept", written)
	}
}

// Before native tool calling, Roo (v3.20, for one) and Cline kept a tool call
// as XML inside the assistant's text block and its result as user text blocks
// headed `[execute_command for '...'] Result:`. The readers took tool_use
// blocks only, so a task of that era had no command, files or edit record and
// its command output was indexed as the user's words (#4424). Shape from
// Roo's Task.ts and Cline's pushToolResult.
func TestRooLegacyXMLCallsLeaveRecords(t *testing.T) {
	const env = "<environment_details>\n# VSCode Visible Files\nretry/backoff.go\n</environment_details>"
	turn := func(role string, texts ...string) map[string]any {
		blocks := make([]any, 0, len(texts))
		for _, t := range texts {
			blocks = append(blocks, map[string]any{"type": "text", "text": t})
		}
		return map[string]any{"role": role, "content": blocks}
	}
	body, err := json.Marshal([]any{
		turn("user", "<task>\nthe backoff test overflows\n</task>", env),
		turn("assistant", "Running the tests.\n<execute_command>\n<command>go test ./retry/...</command>\n</execute_command>"),
		turn("user", "[execute_command for 'go test ./retry/...'] Result:",
			"Command executed in terminal within working directory '/w'. Exit code: 1\nOutput:\nFAIL retry_test.go:12 backoff overflow", env),
		turn("assistant", "<read_file>\n<args>\n  <file>\n    <path>retry/backoff.go</path>\n  </file>\n  <file>\n    <path>retry/clock.go</path>\n  </file>\n</args>\n</read_file>"),
		turn("user", "[read_file for 'retry/backoff.go, retry/clock.go'] Result:", "<files><file><path>retry/backoff.go</path><content>1 | package retry</content></file></files>"),
		turn("assistant", "Capping the shift.\n<apply_diff>\n<path>retry/backoff.go</path>\n<diff>\n<<<<<<< SEARCH\n:start_line:12\n-------\n\treturn base << attempt\n=======\n\treturn base << min(attempt, maxShiftForBackoff)\n>>>>>>> REPLACE\n</diff>\n</apply_diff>"),
		turn("user", "[apply_diff for 'retry/backoff.go'] Result:", "Changes successfully applied to retry/backoff.go", env),
		turn("assistant", "<ask_followup_question>\n<question>Cap at 30 or 60?</question>\n</ask_followup_question>"),
		turn("user", "[ask_followup_question for 'Cap at 30 or 60?'] Result:", "<answer>\nsixty, the old ceiling\n</answer>"),
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "tasks", "d0000000-0000-7000-8000-000000000001")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "api_conversation_history.json")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	h, ok := HashWrittenLine("\treturn base << min(attempt, maxShiftForBackoff)")
	if !ok {
		t.Fatal("the new line is too short to be evidence")
	}
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
		by := map[string][]string{}
		for _, m := range ss[0].Messages {
			by[m.Role] = append(by[m.Role], m.Text)
		}
		if got := by[RoleCommand]; len(got) != 1 || got[0] != "$ go test ./retry/..." {
			t.Errorf("%s: commands = %q", reader.name, got)
		}
		files := strings.Join(by[RoleFiles], "\n")
		for _, f := range []string{"retry/backoff.go", "retry/clock.go"} {
			if !strings.Contains(files, f) {
				t.Errorf("%s: no files record for %s: %q", reader.name, f, by[RoleFiles])
			}
		}
		if got := by[RoleEdit]; len(got) != 1 || got[0] != "retry/backoff.go\n\treturn base << attempt" {
			t.Errorf("%s: edits = %q", reader.name, got)
		}
		var wrote bool
		for _, rec := range by[RoleWrote] {
			if p, has := WroteRecordHas(rec, h); has && p == "retry/backoff.go" {
				wrote = true
			}
		}
		if !wrote {
			t.Errorf("%s: the written line is in no record: %q", reader.name, by[RoleWrote])
		}
		out := strings.Join(by[RoleToolOutput], "\n")
		if !strings.Contains(out, "FAIL retry_test.go:12 backoff overflow") || !strings.Contains(out, "Changes successfully applied") {
			t.Errorf("%s: tool output = %q", reader.name, by[RoleToolOutput])
		}
		// The person's words are the task and the answer they gave; neither
		// a result header nor a result body is one of them.
		users := strings.Join(by["user"], "\n---\n")
		if strings.Contains(users, "] Result:") || strings.Contains(users, "FAIL retry_test.go") || strings.Contains(users, "successfully applied") {
			t.Errorf("%s: a tool result was indexed as the user's words: %q", reader.name, by["user"])
		}
		if !strings.Contains(users, "the backoff test overflows") || !strings.Contains(users, "sixty, the old ceiling") {
			t.Errorf("%s: the user's own words are missing: %q", reader.name, by["user"])
		}
	}
}

// A task of the native era calls its tools as tool_use blocks, so XML in an
// assistant's text there is something it showed, not something the client
// ran: a reply explaining the old format, a snippet in a code fence. And a
// result of the XML era that carries the person's feedback keeps that
// feedback as their words.
func TestRooXMLOnlyInXMLEraTasks(t *testing.T) {
	write := func(turns []any) string {
		body, err := json.Marshal(turns)
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(t.TempDir(), "tasks", "d0000000-0000-7000-8000-000000000002")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "api_conversation_history.json")
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	text := func(role string, texts ...string) map[string]any {
		blocks := make([]any, 0, len(texts))
		for _, s := range texts {
			blocks = append(blocks, map[string]any{"type": "text", "text": s})
		}
		return map[string]any{"role": role, "content": blocks}
	}
	native := write([]any{
		text("user", "<task>\nhow did the old roo call tools?\n</task>"),
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": "t1", "name": "read_file", "input": map[string]any{"path": "README.md"}},
		}},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": "# readme"},
		}},
		text("assistant", "It wrote the call as XML in its reply:\n```xml\n<execute_command>\n<command>go test ./retry/... -run Backoff</command>\n</execute_command>\n```"),
	})
	denied := write([]any{
		text("user", "<task>\nclean the build\n</task>"),
		// The client of that era ran one call per message and answered the
		// rest "was not executed because a tool has already been used".
		text("assistant", "<execute_command>\n<command>rm -rf build</command>\n</execute_command>\n<execute_command>\n<command>go build ./... -o build/out</command>\n</execute_command>"),
		text("user", "[execute_command for 'rm -rf build'] Result:",
			"The user denied this operation and provided the following feedback:\n<feedback>\nkeep build/cache, only drop build/out\n</feedback>"),
	})
	for _, reader := range []struct {
		name  string
		parse func(string) ([]model.Session, error)
	}{
		{"roo", ParseRooTask},
		{"kilocode", ParseKiloTask},
		{"cline legacy", parseClineLegacyTask},
	} {
		ss, err := reader.parse(native)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%s parse: %v, %d sessions", reader.name, err, len(ss))
		}
		for _, m := range ss[0].Messages {
			if m.Role == RoleCommand {
				t.Errorf("%s: XML shown in a native-era reply became a command: %q", reader.name, m.Text)
			}
		}
		ss, err = reader.parse(denied)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%s parse: %v, %d sessions", reader.name, err, len(ss))
		}
		var users []string
		for _, m := range ss[0].Messages {
			if m.Role == "user" {
				users = append(users, m.Text)
			}
			if m.Role == RoleCommand && strings.Contains(m.Text, "go build") {
				t.Errorf("%s: the second call of a message, which the client never ran, became a command", reader.name)
			}
		}
		if !strings.Contains(strings.Join(users, "\n"), "keep build/cache, only drop build/out") {
			t.Errorf("%s: the feedback the person typed is not among their words: %q", reader.name, users)
		}
	}
}

// Current Roo offers apply_patch, search_replace, edit_file and edit next to
// apply_diff, and the last three name the file `file_path`. The reader knew
// none of them, so a task edited through one had no files, edit or wrote
// record (#4419). Shapes from the Roo CLI 0.1.17 bundle,
// core/prompts/tools/native-tools.
func TestRooNativeEditToolsLeaveRecords(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks", "1788845325720")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const old, neu = "max_backoff_seconds = 30  # cap for the retry loop", "max_backoff_seconds = 120  # cap for the retry loop"
	body := `[
	 {"role":"user","content":[{"type":"text","text":"<task>\nraise the retry backoff cap\n</task>"}]},
	 {"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"apply_patch","input":{"patch":"*** Begin Patch\n*** Update File: conf/a.cfg\n@@\n-` + old + `\n+` + neu + `\n*** End Patch"}}]},
	 {"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"search_replace","input":{"file_path":"/w/conf/b.cfg","old_string":"` + old + `","new_string":"` + neu + `"}}]},
	 {"role":"assistant","content":[{"type":"tool_use","id":"t3","name":"edit_file","input":{"file_path":"/w/conf/c.cfg","old_string":"` + old + `","new_string":"` + neu + `","expected_replacements":1}}]},
	 {"role":"assistant","content":[{"type":"tool_use","id":"t4","name":"edit","input":{"file_path":"/w/conf/d.cfg","old_string":"` + old + `","new_string":"` + neu + `","replace_all":false}}]}
	]`
	path := filepath.Join(dir, "api_conversation_history.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "history_item.json"), []byte(`{"id":"1788845325720","ts":1788845325720,"task":"raise the cap","workspace":"/w"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	h, ok := HashWrittenLine(neu)
	if !ok {
		t.Fatal("the new line is too short to be evidence")
	}
	for _, reader := range []struct {
		name  string
		parse func(string) ([]model.Session, error)
	}{
		{"roo", ParseRooTask},
		{"kilocode", ParseKiloTask},
	} {
		ss, err := reader.parse(path)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%s parse: %v, %d sessions", reader.name, err, len(ss))
		}
		files, edits, wrote := map[string]bool{}, map[string]bool{}, map[string]bool{}
		for _, m := range ss[0].Messages {
			switch m.Role {
			case RoleFiles:
				for p := range strings.SplitSeq(m.Text, "\n") {
					files[p] = true
				}
			case RoleEdit:
				p, span, _ := strings.Cut(m.Text, "\n")
				if span == old {
					edits[p] = true
				}
			case RoleWrote:
				if p, has := WroteRecordHas(m.Text, h); has {
					wrote[p] = true
				}
			}
		}
		for _, f := range []string{"/w/conf/a.cfg", "/w/conf/b.cfg", "/w/conf/c.cfg", "/w/conf/d.cfg"} {
			if !files[f] {
				t.Errorf("%s: no files record for %s: %v", reader.name, f, files)
			}
			if !edits[f] {
				t.Errorf("%s: no edit record for %s: %v", reader.name, f, edits)
			}
			if !wrote[f] {
				t.Errorf("%s: no wrote record for %s: %v", reader.name, f, wrote)
			}
		}
	}
}

// When the model answers without a tool call, Roo sends an automated user turn
// (formatResponse.noToolsUsed). Roo's own view shows it as an error row; deja
// indexed it as the person's words, 36 of 37 user turns on one live task
// (#4421). Text from the Roo CLI 0.1.17 bundle.
func TestRooNoToolsRetryPromptIsNotTheUsersWords(t *testing.T) {
	const prompt = "[ERROR] You did not use a tool in your previous response! Please retry with a tool use.\n\n" +
		"# Reminder: Instructions for Tool Use\n\nTools are invoked using the platform's native tool calling mechanism.\n\n" +
		"# Next Steps\n\nIf you have completed the user's task, use the attempt_completion tool.\n" +
		"If you require additional information from the user, use the ask_followup_question tool.\n" +
		"Otherwise, if you have not completed the task and do not need additional information, then proceed with the next step of the task.\n" +
		"(This is an automated message, so do not respond to it conversationally.)"
	const env = "<environment_details>\n# VSCode Visible Files\nretry.cfg\n</environment_details>"
	const mine = "why does deja show \"[ERROR] You did not use a tool in your previous response!\" as mine"
	turn := func(role string, texts ...string) map[string]any {
		blocks := make([]any, 0, len(texts))
		for _, t := range texts {
			blocks = append(blocks, map[string]any{"type": "text", "text": t})
		}
		return map[string]any{"role": role, "content": blocks}
	}
	body, err := json.Marshal([]any{
		turn("user", "<user_message>\nSet retries to 5 in retry.cfg.\n</user_message>", env),
		turn("assistant", "Done, retries is 5."),
		turn("user", prompt, env),
		turn("assistant", "retry.cfg updated."),
		turn("user", prompt),
		turn("user", mine),
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "tasks", "1788845325721")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "api_conversation_history.json")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
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
		var users []string
		for _, m := range ss[0].Messages {
			if m.Role == "user" {
				users = append(users, m.Text)
			}
		}
		// The control: a person quoting the prompt inside a sentence keeps it.
		want := []string{"Set retries to 5 in retry.cfg.", mine}
		if strings.Join(users, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("%s: user turns = %q, want %q", reader.name, users, want)
		}
	}
}

func TestParseRooTask(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	path := filepath.Join(root, "fixtures", "registry", "roo", "tasks", "1767225700000", "api_conversation_history.json")
	ss, err := ParseRooTask(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d", len(ss))
	}
	s := ss[0]
	if s.ID != "roo-task-1767225700000" || s.Harness != "roo" {
		t.Fatalf("identity: %+v", s)
	}
	if s.Title != "Fix the flaky test" {
		t.Fatalf("title = %q", s.Title)
	}
	if s.Project == "roo" {
		t.Fatalf("project must come from history_item workspace, got %q", s.Project)
	}
	if s.Messages[0].Text != "Fix the flaky test" {
		t.Fatalf("envelope not unwrapped: %q", s.Messages[0].Text)
	}
}

func TestRooRootsOverride(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	t.Setenv("DEJA_ROO_ROOTS", filepath.Join(root, "fixtures", "registry", "roo"))
	if files := RooTaskFiles(); len(files) != 1 {
		t.Fatalf("files = %v", files)
	}
	if ss := LoadRoo(); len(ss) != 1 {
		t.Fatalf("sessions = %d", len(ss))
	}
}

// Roo and Cline write the same filename in the same layout. Cline's kind is
// registered first, so without a root check it claims every Roo task and the
// history is filed under the wrong harness — which is what deja did until a
// real roo CLI session showed up as cline.
func TestRooTasksAreNotClaimedByCline(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_ROO_ROOTS", "")
	t.Setenv("DEJA_CLINE_ROOTS", "")
	cli := filepath.Join(home, ".vscode-mock", "global-storage")
	t.Setenv("DEJA_ROO_CLI_ROOT", cli)
	task := filepath.Join(cli, "tasks", "019fa464", "api_conversation_history.json")
	if err := os.MkdirAll(filepath.Dir(task), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(task, []byte(`[{"role":"user","content":[{"type":"text","text":"hello"}]}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := KindForPath(task); got != "roo" {
		t.Fatalf("KindForPath(%s) = %q, want roo", task, got)
	}
}

// Roo lets a user move its whole store with the roo-cline.customStoragePath
// setting. deja read only the default location, so for those users it indexed
// nothing at all and said the harness was missing.
func TestRooRootsFollowsCustomStoragePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_ROO_ROOTS", "")
	t.Setenv("DEJA_ROO_CLI_ROOT", filepath.Join(home, "absent"))
	// The settings file lives somewhere different on each platform; ask the
	// resolver rather than hardcoding the mac path, which is how this test
	// passed locally and failed on linux.
	settingsPath := vsCodeUserSettingsPaths()[0]
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(home, "relocated")
	if err := os.MkdirAll(filepath.Join(store, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Written the way VS Code writes it: comments and a trailing comma. A
	// strict decode treats this as invalid and silently finds nothing.
	settings := "{\n  // moved to an external disk\n  \"roo-cline.customStoragePath\": " +
		strconv.Quote(store) + ",\n}\n"
	if err := os.WriteFile(settingsPath, []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	roots := RooRoots()
	found := false
	for _, r := range roots {
		if r == store {
			found = true
		}
	}
	if !found {
		t.Fatalf("relocated store not in roots: %v", roots)
	}
}

// Zoo Code (zoocodeorganization.zoo-code) is a Roo Code fork writing the
// identical task layout under its own extension ID and customStoragePath key (#6044).
func TestRooRootsFindsZooCodeExtensionAndCustomStoragePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DEJA_ROO_ROOTS", "")
	t.Setenv("DEJA_ROO_CLI_ROOT", filepath.Join(home, "absent"))

	zooGlobal := filepath.Join(vsCodeUserDir("Code"), "globalStorage", ZooExtensionID)
	task := filepath.Join(zooGlobal, "tasks", "1767225700001", "api_conversation_history.json")
	if err := os.MkdirAll(filepath.Dir(task), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(task, []byte(`[{"role":"user","content":[{"type":"text","text":"zoo code task"}]}]`), 0o644); err != nil {
		t.Fatal(err)
	}

	settingsPath := vsCodeUserSettingsPaths()[0]
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	zooCustom := filepath.Join(home, "zoo-custom")
	if err := os.MkdirAll(filepath.Join(zooCustom, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"zoo-code.customStoragePath": `+strconv.Quote(zooCustom)+`}`), 0o644); err != nil {
		t.Fatal(err)
	}

	roots := RooRoots()
	var sawGlobal, sawCustom bool
	for _, r := range roots {
		if r == zooGlobal {
			sawGlobal = true
		}
		if r == zooCustom {
			sawCustom = true
		}
	}
	if !sawGlobal || !sawCustom {
		t.Fatalf("Zoo Code roots missing (global=%v custom=%v): %v", sawGlobal, sawCustom, roots)
	}
	if got := KindForPath(task); got != "roo" {
		t.Fatalf("KindForPath(%s) = %q, want roo", task, got)
	}
}

func TestStripJSONCHandlesCommentsAndCommas(t *testing.T) {
	in := []byte("{\n  \"a\": \"http://x/y\", // trailing note\n  /* block */ \"b\": [1,2,],\n}")
	var got map[string]any
	if err := json.Unmarshal(dropTrailingCommas(stripJSONCComments(in)), &got); err != nil {
		t.Fatalf("jsonc not accepted: %v", err)
	}
	// A // inside a string is part of the value, not a comment.
	if got["a"] != "http://x/y" {
		t.Fatalf("a = %v", got["a"])
	}
}

// Zoo Code (zoocodeorganization.zoo-code) continues Roo Code with the same
// task layout under its own globalStorage directory (#6044). Tasks there must
// be discovered alongside legacy rooveterinaryinc.roo-cline tasks and claimed
// by "roo" rather than "cline".
func TestRooRootsDiscoversZooCodeAndDedupsCustomStoragePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DEJA_ROO_ROOTS", "")
	t.Setenv("DEJA_CLINE_ROOTS", "")
	t.Setenv("DEJA_ROO_CLI_ROOT", filepath.Join(home, "absent"))

	rooRoot := filepath.Join(vsCodeUserDir("Code"), "globalStorage", RooExtensionID)
	zooRoot := filepath.Join(vsCodeUserDir("Cursor"), "globalStorage", ZooExtensionID)
	rooTask := filepath.Join(rooRoot, "tasks", "1767225700001", "api_conversation_history.json")
	zooTask := filepath.Join(zooRoot, "tasks", "1767225700002", "api_conversation_history.json")
	for _, p := range []string{rooTask, zooTask} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(`[{"role":"user","content":[{"type":"text","text":"zoo check"}]}]`), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Both extensions pointing customStoragePath at the same directory must
	// surface that root once, not twice.
	sharedCustom := filepath.Join(home, "shared-custom")
	if err := os.MkdirAll(filepath.Join(sharedCustom, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := vsCodeUserSettingsPaths()[0]
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := "{\n  \"roo-cline.customStoragePath\": " + strconv.Quote(sharedCustom) + ",\n" +
		"  \"zoo-code.customStoragePath\": " + strconv.Quote(sharedCustom) + ",\n}\n"
	if err := os.WriteFile(settingsPath, []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}

	roots := RooRoots()
	counts := map[string]int{}
	for _, r := range roots {
		counts[r]++
	}
	if counts[rooRoot] != 1 {
		t.Fatalf("rooRoot count = %d in %v", counts[rooRoot], roots)
	}
	if counts[zooRoot] != 1 {
		t.Fatalf("zooRoot count = %d in %v", counts[zooRoot], roots)
	}
	if counts[sharedCustom] != 1 {
		t.Fatalf("sharedCustom count = %d in %v (want 1 deduplicated)", counts[sharedCustom], roots)
	}
	if got := KindForPath(zooTask); got != "roo" {
		t.Fatalf("KindForPath(%s) = %q, want roo", zooTask, got)
	}
	if ss := LoadRoo(); len(ss) != 2 {
		t.Fatalf("LoadRoo() = %d sessions, want 2", len(ss))
	}
}

// Roo puts a failing command's output in the tool_result block of the next
// user turn; the reader took text blocks only, so the error reached neither
// search nor the fix pairs (#3269).
func TestRooToolResultIsIndexedAsToolOutput(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks", "1788845325718")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `[
	 {"role":"user","content":[{"type":"text","text":"<task>\nrun go test and fix whatever fails in widgetd\n</task>"},{"type":"text","text":"<environment_details>\n# Current Cost\n$0.04\n</environment_details>"}]},
	 {"role":"assistant","content":[{"type":"text","text":"I'll run the tests first."},{"type":"tool_use","id":"toolu_01roo","name":"execute_command","input":{"command":"go test ./..."}}]},
	 {"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01roo","content":"Command executed in terminal within working directory '/Users/qa/proj/widgetd'. Command execution was not successful, inspect the cause and adjust as needed.\nExit code: 1\nOutput:\n# widgetd/pkg\npkg/parser.go:42:9: undefined: frobnicateWidget\nFAIL\twidgetd/pkg [build failed]\nFAIL"},{"type":"text","text":"<environment_details>\n# Current Cost\n$0.05\n</environment_details>"}]},
	 {"role":"assistant","content":[{"type":"text","text":"frobnicateWidget was renamed to frobnicate; I'll fix the call site."},{"type":"tool_use","id":"toolu_02roo","name":"apply_diff","input":{"path":"pkg/parser.go","diff":"x"}}]}
	]`
	path := filepath.Join(dir, "api_conversation_history.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseRooTask(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	var toolOut []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleToolOutput {
			toolOut = append(toolOut, m.Text)
		}
		if m.Role == "user" && strings.Contains(m.Text, "Exit code") {
			t.Errorf("tool output indexed as the user's words: %q", m.Text)
		}
	}
	if len(toolOut) != 1 || !strings.Contains(toolOut[0], "undefined: frobnicateWidget") {
		t.Fatalf("tool output = %q, want the failing command's output", toolOut)
	}
}

// The legacy Cline task file has the same shape and shared the same gap.
func TestClineLegacyToolResultIsIndexedAsToolOutput(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "tasks", "1788845325718")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `[
	 {"role":"user","content":[{"type":"text","text":"<task>\nrun go test in widgetd\n</task>"}]},
	 {"role":"assistant","content":[{"type":"text","text":"Running."},{"type":"tool_use","id":"t1","name":"execute_command","input":{"command":"go test ./..."}}]},
	 {"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"Exit code: 1\nOutput:\npkg/parser.go:42:9: undefined: frobnicateWidget\nFAIL"}]}
	]`
	path := filepath.Join(dir, "api_conversation_history.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := parseClineLegacyTask(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	var toolOut []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleToolOutput {
			toolOut = append(toolOut, m.Text)
		}
	}
	if len(toolOut) != 1 || !strings.Contains(toolOut[0], "undefined: frobnicateWidget") {
		t.Fatalf("tool output = %q, want the failing command's output", toolOut)
	}
}

// A build log of a few megabytes is capped like every other harness's tool
// output.
func TestRooToolResultIsCapped(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks", "1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("line of build output\n", maxParsedMessage/10)
	quoted := mustJSON(t, big)
	body := `[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":` + string(quoted) + `}]}]`
	path := filepath.Join(dir, "api_conversation_history.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseRooTask(path)
	if err != nil || len(ss) != 1 || len(ss[0].Messages) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	if n := len(ss[0].Messages[0].Text); n > maxParsedMessage+64 {
		t.Errorf("tool output not capped: %d bytes", n)
	}
}

// Roo writes a `ts` on every turn of api_conversation_history.json. The reader
// stamped turn N at history_item's ts plus N seconds, and that ts is when the
// task was last touched, so an hour-long task read as four seconds starting at
// its end (#4420).
func TestRooTurnsKeepTheirOwnTime(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks", "b0000000")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	at := func(hm string) time.Time {
		v, err := time.Parse(time.RFC3339, "2026-10-01T"+hm+":00Z")
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	ms := func(hm string) string { return itoa64(at(hm).UnixMilli()) }
	body := `[
	 {"role":"user","ts":` + ms("08:00") + `,"content":[{"type":"text","text":"<task>\nwhy does the retry loop spin\n</task>"}]},
	 {"role":"assistant","ts":` + ms("08:10") + `,"content":[{"type":"text","text":"Checking the loop."},{"type":"tool_use","id":"t1","name":"execute_command","input":{"command":"go test ./retry/..."}}]},
	 {"role":"user","ts":` + ms("08:20") + `,"content":[{"type":"tool_result","tool_use_id":"t1","content":"FAIL retry_test.go:12"}]},
	 {"role":"assistant","content":[{"type":"text","text":"No ts on this one."}]},
	 {"role":"assistant","ts":` + ms("09:00") + `,"content":[{"type":"text","text":"The cap was never applied."}]}
	]`
	path := filepath.Join(dir, "api_conversation_history.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "history_item.json"), []byte(`{"id":"b0000000","ts":`+ms("09:00")+`,"task":"why does the retry loop spin"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, reader := range []struct {
		name  string
		parse func(string) ([]model.Session, error)
	}{
		{"roo", ParseRooTask},
		{"kilocode", ParseKiloTask},
	} {
		ss, err := reader.parse(path)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%s parse: %v, %d sessions", reader.name, err, len(ss))
		}
		want := map[string]time.Time{
			"why does the retry loop spin": at("08:00"),
			"Checking the loop.":           at("08:10"),
			"$ go test ./retry/...":        at("08:10"),
			"FAIL retry_test.go:12":        at("08:20"),
			// A turn without its own ts keeps the old scheme.
			"No ts on this one.":         at("09:00").Add(3 * time.Second),
			"The cap was never applied.": at("09:00"),
		}
		for _, m := range ss[0].Messages {
			w, ok := want[m.Text]
			if !ok {
				continue
			}
			if !m.Time.Equal(w) {
				t.Errorf("%s: %q at %s, want %s", reader.name, m.Text, m.Time.UTC().Format(time.RFC3339), w.Format(time.RFC3339))
			}
			delete(want, m.Text)
		}
		if len(want) != 0 {
			t.Errorf("%s: messages not found: %v", reader.name, want)
		}
		if !ss[0].Started.Equal(at("08:00")) {
			t.Errorf("%s: started %s, want 08:00", reader.name, ss[0].Started.UTC())
		}
	}
}

// Roo and the legacy Cline extension name their tools execute_command,
// read_file, write_to_file, apply_diff / replace_in_file; the readers took the
// assistant's prose and none of the calls, so no command or file record ever
// came out of them (#3295). Shape from Roo's own Task.ts.
func writeRooTask(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, "tasks", "1788845325718")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `[
	 {"role":"user","content":[{"type":"text","text":"<task>\nrun go test and fix whatever fails\n</task>"}]},
	 {"role":"assistant","content":[{"type":"text","text":"Running the tests."},{"type":"tool_use","id":"t1","name":"execute_command","input":{"command":"go test ./..."}}]},
	 {"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"Exit code: 1\nOutput:\npkg/parser.go:42:9: undefined: frobnicateWidget\nFAIL"}]},
	 {"role":"assistant","content":[{"type":"text","text":"Renaming the call."},{"type":"tool_use","id":"t2","name":"read_file","input":{"path":"pkg/parser.go"}}]},
	 {"role":"assistant","content":[{"type":"tool_use","id":"t3","name":"apply_diff","input":{"path":"pkg/parser.go","diff":"<<<<<<< SEARCH\n\tout := frobnicateWidget(w)\n=======\n\tout := frobnicate(w)\n>>>>>>> REPLACE"}}]}
	]`
	path := filepath.Join(dir, "api_conversation_history.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRooToolCallsBecomeCommandAndFileRecords(t *testing.T) {
	path := writeRooTask(t, t.TempDir())
	ss, err := ParseRooTask(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	checkRooWork(t, "roo", ss[0].Messages)
	ss, err = parseClineLegacyTask(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("legacy parse: %v, %d sessions", err, len(ss))
	}
	checkRooWork(t, "cline legacy", ss[0].Messages)
}

func checkRooWork(t *testing.T, reader string, ms []model.Message) {
	t.Helper()
	var cmds, files []string
	for _, m := range ms {
		switch m.Role {
		case RoleCommand:
			cmds = append(cmds, m.Text)
		case RoleFiles:
			files = append(files, m.Text)
		}
	}
	if len(cmds) != 1 || cmds[0] != "$ go test ./..." {
		t.Errorf("%s: commands = %q, want the execute_command", reader, cmds)
	}
	if len(files) == 0 || !strings.Contains(strings.Join(files, "\n"), "pkg/parser.go") {
		t.Errorf("%s: files = %q, want pkg/parser.go", reader, files)
	}
}
