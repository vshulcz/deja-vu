package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Copilot CLI sub-agent's records sit in its parent's events.jsonl and are
// read as a session of their own. Forgetting the parent left them searchable
// (#5216).
func TestForgetCopilotParentTakesItsInlineSubagent(t *testing.T) {
	h := newTwoWayEnv(t)
	root := filepath.Join(h.tmp, "copilot")
	t.Setenv("DEJA_COPILOT_ROOT", root)
	const parent, child, other = "f67048ad-042c-42f4-ac33-5db51c4d48b0", "717532a2-bf69-4164-be09-03daacd1762f", "aaaa1111-0000-4000-8000-000000000000"
	write := func(id string, lines ...string) {
		dir := filepath.Join(root, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	start := func(id string) string {
		return `{"type":"session.start","data":{"sessionId":"` + id + `","startTime":"2026-10-18T05:31:46.707Z","context":{"cwd":"/Users/x/work/app"}},"timestamp":"2026-10-18T05:31:46.707Z"}`
	}
	write(parent, start(parent),
		`{"type":"user.message","data":{"content":"write the notes with a helper"},"timestamp":"2026-10-18T05:31:47.305Z"}`,
		`{"type":"subagent.started","agentId":"`+child+`","data":{"agentName":"general-purpose"},"timestamp":"2026-10-18T05:31:47.356Z"}`,
		`{"type":"user.message","agentId":"`+child+`","data":{"content":"draft the zanzibarquill notes"},"timestamp":"2026-10-18T05:31:47.399Z"}`,
		`{"type":"assistant.message","agentId":"`+child+`","data":{"content":"drafted the zanzibarquill notes"},"timestamp":"2026-10-18T05:31:49.063Z"}`,
	)
	write(other, start(other), `{"type":"user.message","data":{"content":"unrelated cache work"},"timestamp":"2026-10-18T06:00:00.000Z"}`)
	dir := filepath.Join(h.tmp, "a.db")
	if err := Ensure(dir, "copilot", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := FindByID(dir, child); !ok {
		t.Fatalf("the sub-agent is not indexed as its own session")
	}
	res, err := Forget(dir, ForgetOptions{Session: parent})
	if err != nil {
		t.Fatal(err)
	}
	if res.Sessions != 2 {
		t.Errorf("forgot %d sessions (%v), want the parent and its sub-agent", res.Sessions, res.Keys)
	}
	if res.Attached != 1 {
		t.Errorf("attached = %d, want the one sub-agent run, which the selector did not name", res.Attached)
	}
	if _, ok, _ := FindByID(dir, child); ok {
		t.Errorf("the sub-agent's run is still indexed after its parent was forgotten")
	}
	if _, ok, _ := FindByID(dir, other); !ok {
		t.Errorf("an unrelated session went too")
	}
	if res.Tombstones != 1 {
		t.Errorf("tombstones = %d, want only the parent's, which covers its runs", res.Tombstones)
	}

	// A run the forgotten session starts later stays out with it.
	const later = "abcdef01-0000-4000-8000-000000000000"
	write(parent, start(parent),
		`{"type":"user.message","data":{"content":"write the notes with a helper"},"timestamp":"2026-10-18T05:31:47.305Z"}`,
		`{"type":"subagent.started","agentId":"`+child+`","data":{"agentName":"general-purpose"},"timestamp":"2026-10-18T05:31:47.356Z"}`,
		`{"type":"user.message","agentId":"`+child+`","data":{"content":"draft the zanzibarquill notes"},"timestamp":"2026-10-18T05:31:47.399Z"}`,
		`{"type":"assistant.message","agentId":"`+child+`","data":{"content":"drafted the zanzibarquill notes"},"timestamp":"2026-10-18T05:31:49.063Z"}`,
		`{"type":"subagent.started","agentId":"`+later+`","data":{"agentName":"explore"},"timestamp":"2026-10-18T07:00:00.000Z"}`,
		`{"type":"user.message","agentId":"`+later+`","data":{"content":"zebrafinch lookup"},"timestamp":"2026-10-18T07:00:01.000Z"}`,
	)
	if err := Ensure(dir, "copilot", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := FindByID(dir, later); ok {
		t.Errorf("a run the forgotten session started later was indexed")
	}

	// Unforgetting the parent brings its runs back.
	if _, err := Unforget(dir, "copilot:"+parent, nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{parent, child, later} {
		if _, ok, _ := FindByID(dir, id); !ok {
			t.Errorf("%s is still gone after its parent was unforgotten", id)
		}
	}
}

// Matched by --project along with its parent, the run still takes no
// tombstone of its own, so unforgetting the parent brings it back.
func TestForgetCopilotByProjectLeavesTheRunToItsParent(t *testing.T) {
	h := newTwoWayEnv(t)
	root := filepath.Join(h.tmp, "copilot")
	t.Setenv("DEJA_COPILOT_ROOT", root)
	const parent, child = "f67048ad-042c-42f4-ac33-5db51c4d48b0", "717532a2-bf69-4164-be09-03daacd1762f"
	dir0 := filepath.Join(root, parent)
	if err := os.MkdirAll(dir0, 0o755); err != nil {
		t.Fatal(err)
	}
	body := strings.Join([]string{
		`{"type":"session.start","data":{"sessionId":"` + parent + `","startTime":"2026-10-18T05:31:46.707Z","context":{"cwd":"/Users/x/work/app"}},"timestamp":"2026-10-18T05:31:46.707Z"}`,
		`{"type":"user.message","data":{"content":"write the notes with a helper"},"timestamp":"2026-10-18T05:31:47.305Z"}`,
		`{"type":"user.message","agentId":"` + child + `","data":{"content":"draft the notes"},"timestamp":"2026-10-18T05:31:47.399Z"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir0, "events.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(h.tmp, "a.db")
	if err := Ensure(dir, "copilot", false, nil); err != nil {
		t.Fatal(err)
	}
	res, err := Forget(dir, ForgetOptions{Project: "work/app"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Sessions != 2 || res.Tombstones != 1 || res.Attached != 1 {
		t.Errorf("sessions/tombstones/attached = %d/%d/%d, want 2/1/1", res.Sessions, res.Tombstones, res.Attached)
	}
	if _, err := Unforget(dir, "copilot:"+parent, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := FindByID(dir, child); !ok {
		t.Errorf("the run stayed forgotten after its parent came back")
	}
}
