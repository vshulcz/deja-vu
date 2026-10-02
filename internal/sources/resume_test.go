package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A line longer than the chunk the walk reads back is still handed over
// whole, and the walk stops where fn says.
func TestEachLineBeforeReadsLongLinesWhole(t *testing.T) {
	long := strings.Repeat("x", 70<<10)
	body := "first\n" + long + "\nthird\n"
	p := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var got []string
	eachLineBefore(p, int64(len(body)), func(line []byte) bool {
		got = append(got, string(line))
		return true
	})
	if len(got) != 3 || got[0] != "third" || got[1] != long || got[2] != "first" {
		t.Fatalf("got %d lines, first %.10q", len(got), got)
	}

	got = nil
	eachLineBefore(p, int64(len("first\n")+len(long)+1), func(line []byte) bool {
		got = append(got, string(line))
		return false
	})
	if len(got) != 1 || got[0] != long {
		t.Fatalf("stopped walk got %d lines", len(got))
	}
}

// tailFile writes head and tail as one transcript and returns the offset
// between them, where the stored part ends.
func tailFile(t *testing.T, name, head, tail string) (string, int64) {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(head+tail), 0o644); err != nil {
		t.Fatal(err)
	}
	return p, int64(len(head))
}

// A file that is gone, or an offset at its start, walks nothing.
func TestEachLineWalksStopAtAMissingFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone.jsonl")
	called := false
	eachLineBefore(missing, 10, func([]byte) bool { called = true; return true })
	eachLineFrom(missing, 0, func([]byte) bool { called = true; return true })
	if called {
		t.Error("a missing file handed over a line")
	}
	p, _ := tailFile(t, "t.jsonl", "a\nb\n", "")
	var got []string
	eachLineFrom(p, 0, func(line []byte) bool { got = append(got, string(line)); return false })
	if len(got) != 1 || got[0] != "a" {
		t.Errorf("stopped walk from the start got %q, want only a", got)
	}
	if decodeJSONLine([]byte(`[1]`)) != nil || decodeJSONLine([]byte(`{"n":1}`))["n"] != json.Number("1") {
		t.Error("decodeJSONLine wants an object, numbers kept as json.Number")
	}
}

// The answering rule only knows the calls the tail makes. A failed exit for
// a call stored already sends the file back; one for a call in the same tail,
// a clean exit, or a line that does not decode does not (#4443).
func TestCodexAndCopilotResumesOnlyOnAFailureForAStoredCall(t *testing.T) {
	codexCall := func(id string) string {
		return `{"timestamp":"2026-07-31T00:00:02Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"` + id + `","arguments":"{\"cmd\":\"go test ./...\"}"}}` + "\n"
	}
	codexOut := func(id, code string) string {
		return `{"timestamp":"2026-07-31T00:00:03Z","type":"response_item","payload":{"type":"function_call_output","call_id":"` + id + `","output":"Process exited with code ` + code + `\nOutput:\nFAIL\n"}}` + "\n"
	}
	copilotCall := func(id string) string {
		return `{"type":"tool.execution_start","timestamp":"2026-09-05T10:00:01Z","data":{"toolName":"bash","toolCallId":"` + id + `","arguments":{"command":"go vet ./..."}}}` + "\n"
	}
	copilotOut := func(id, code string) string {
		return `{"type":"tool.execution_complete","timestamp":"2026-09-05T10:00:02Z","data":{"toolCallId":"` + id + `","success":true,"result":{"content":"vet\n<shellId: 0 completed with exit code ` + code + `>"}}}` + "\n"
	}
	for _, c := range []struct {
		name       string
		resumes    func(string, int64) bool
		head, tail string
		want       bool
	}{
		{"codex failed exit for a stored call", codexResumes, codexCall("c1"), codexOut("c1", "2"), false},
		{"codex clean exit for a stored call", codexResumes, codexCall("c1"), codexOut("c1", "0"), true},
		{"codex call and failure both in the tail", codexResumes, "", codexCall("c2") + codexOut("c2", "1"), true},
		{"codex half-written answer", codexResumes, codexCall("c1"), `{"type":"response_item","payload":{"type":"function_call_output","call_id":"c1","out` + "\n", true},
		{"codex failure then more", codexResumes, codexCall("c1"), codexOut("c1", "1") + codexCall("c3") + codexOut("c3", "0"), false},
		{"copilot failed exit for a stored call", copilotResumes, copilotCall("k1"), copilotOut("k1", "1"), false},
		{"copilot clean exit for a stored call", copilotResumes, copilotCall("k1"), copilotOut("k1", "0"), true},
		{"copilot call and failure both in the tail", copilotResumes, "", copilotCall("k2") + copilotOut("k2", "1"), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			head := `{"type":"session_meta"}` + "\n" + c.head
			p, off := tailFile(t, "t.jsonl", head, c.tail)
			if got := c.resumes(p, off); got != c.want {
				t.Errorf("resumes = %v, want %v", got, c.want)
			}
			if !c.resumes(p, 0) {
				t.Error("a read from the start resumes nothing, so it must say yes")
			}
		})
	}
}

// grok joins a reply's chunks by promptId and a prompt's by promptIndex. The
// tail is read on its own unless its first chunk carries the key the stored
// part ended on (#4445).
func TestGrokResumesUnlessTheTailJoinsTheLastChunk(t *testing.T) {
	user := func(i, text string) string {
		return `{"timestamp":1782900001,"method":"session/update","params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"` + text + `"},"_meta":{"promptIndex":` + i + `}}}}` + "\n"
	}
	agent := func(id, text string) string {
		return `{"timestamp":1782900002,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"` + text + `"}},"_meta":{"promptId":"` + id + `"}}}` + "\n"
	}
	tool := `{"timestamp":1782900003,"method":"session/update","params":{"update":{"sessionUpdate":"tool_call","toolCallId":"t1","title":"bash"}}}` + "\n"
	for _, c := range []struct {
		name, head, tail string
		want             bool
	}{
		{"reply goes on past a tool", user("0", "find it") + agent("p1", "first "), tool + agent("p1", "answer"), false},
		{"next prompt", user("0", "find it") + agent("p1", "done"), user("1", "thanks") + agent("p2", "ok"), true},
		{"empty chunk is not the reply's", user("0", "find it") + agent("p1", "done"), agent("p1", "") + user("1", "next"), true},
		{"chunk without a prompt id", agent("", "loose"), agent("", "more"), true},
		{"nothing stored to join", tool, agent("p1", "answer"), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, off := tailFile(t, "updates.jsonl", c.head, c.tail)
			if got := GrokResumes(p, off); got != c.want {
				t.Errorf("GrokResumes = %v, want %v", got, c.want)
			}
		})
	}
	if p, _ := tailFile(t, "updates.jsonl", agent("p1", "a"), agent("p1", "b")); !GrokResumes(p, 0) {
		t.Error("offset 0 must resume")
	}
}

// kiro-cli streams a reply as AssistantMessage records under one message_id.
func TestKiroCLIResumesUnlessTheTailContinuesTheReply(t *testing.T) {
	prompt := `{"version":"v1","kind":"Prompt","data":{"message_id":"m1","content":[{"kind":"text","data":"fix the retry loop"}],"meta":{"timestamp":1790848800}}}` + "\n"
	reply := func(id, text string) string {
		return `{"version":"v1","kind":"AssistantMessage","data":{"message_id":"` + id + `","content":[{"kind":"text","data":"` + text + `"}]}}` + "\n"
	}
	results := `{"version":"v1","kind":"ToolResults","data":{"message_id":"m3","content":[]}}` + "\n"
	for _, c := range []struct {
		name, head, tail string
		want             bool
	}{
		{"reply goes on", prompt + reply("m2", "Checking"), reply("m2", " the loop."), false},
		{"reply goes on past a result", prompt + reply("m2", "Checking"), results + `{"version":"v1"}` + "\n" + reply("m2", " more"), false},
		{"next reply", prompt + reply("m2", "Done."), reply("m4", "Next."), true},
		{"next prompt", prompt + reply("m2", "Done."), prompt, true},
		{"stored part ends on a prompt", prompt, reply("m2", "Checking"), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, off := tailFile(t, "s.jsonl", c.head, c.tail)
			if got := KiroCLIResumes(p, off); got != c.want {
				t.Errorf("KiroCLIResumes = %v, want %v", got, c.want)
			}
		})
	}
}

// A record without a stamp takes the last stamped one's before the offset;
// with none stamped, or nothing before the offset, there is none to take.
func TestKiroCLITimeBefore(t *testing.T) {
	stamped := `{"version":"v1","kind":"Prompt","data":{"message_id":"m1","content":[{"kind":"text","data":"q"}],"meta":{"timestamp":1790848800}}}` + "\n"
	bare := `{"version":"v1","kind":"AssistantMessage","data":{"message_id":"m2","content":[{"kind":"text","data":"a"}]}}` + "\n"
	p, off := tailFile(t, "s.jsonl", stamped+bare, bare)
	if got := kiroCLITimeBefore(p, off); got.Unix() != 1790848800 {
		t.Errorf("time before = %v, want the prompt's stamp", got)
	}
	p, off = tailFile(t, "s.jsonl", bare+bare, bare)
	if got := kiroCLITimeBefore(p, off); !got.IsZero() {
		t.Errorf("unstamped records gave %v, want none", got)
	}
	if got := kiroCLITimeBefore(p, 0); !got.IsZero() {
		t.Errorf("offset 0 gave %v, want none", got)
	}
}

// A read under LimitReads stops at the end the pass recorded, so a line
// written while the pass ran is left for the next one (#4442). Holds nest:
// releasing the inner one keeps the outer bound.
func TestLimitReadsHoldsTheEndUntilTheLastRelease(t *testing.T) {
	first := `{"n":1}` + "\n"
	p, end := tailFile(t, "t.jsonl", first, `{"n":2}`+"\n")
	count := func() int {
		n := 0
		if err := scanJSONLFromOffset(p, 0, func(map[string]any) { n++ }); err != nil {
			t.Fatal(err)
		}
		return n
	}
	outer := LimitReads(map[string]int64{p: end})
	inner := LimitReads(map[string]int64{p: end})
	if got := count(); got != 1 {
		t.Errorf("held read saw %d lines, want 1", got)
	}
	inner()
	if got := count(); got != 1 {
		t.Errorf("after the inner release a read saw %d lines, want the outer bound's 1", got)
	}
	if err := scanJSONLFromOffset(p, end+5, func(map[string]any) { t.Error("a read past the bound got a line") }); err != nil {
		t.Fatal(err)
	}
	outer()
	if got := count(); got != 2 {
		t.Errorf("released read saw %d lines, want 2", got)
	}
}
