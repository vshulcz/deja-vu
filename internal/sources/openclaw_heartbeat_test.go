package sources

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// A gateway heartbeat poll is a turn on agent:main:main that nobody asked for:
// `[OpenClaw heartbeat poll]` and a NO_REPLY. Read as the person's, a window
// that only got polls topped `deja last` under that title (#5319). The lines
// are OpenClaw 2026.10.4's, from a live gateway.
func TestOpenClawHeartbeatPollsAreNotTurns(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "main", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	poll := `{"type":"message","id":"%s","timestamp":"%s","message":{"role":"user","content":"[OpenClaw heartbeat poll]","provenance":{"kind":"internal_system","sourceTool":"heartbeat"},"__openclaw":{"senderIsOwner":false,"modelPromptProjection":{"version":1,"text":"Follow the heartbeat monitor scratch context when provided. If nothing needs attention, reply NO_REPLY."}}}}
{"type":"message","id":"%s-r","timestamp":"%s","message":{"role":"assistant","content":[{"type":"text","text":"NO_REPLY"}]}}
`
	only := write("hb.jsonl", `{"type":"session","version":4,"id":"hb","timestamp":"2026-10-18T05:42:12Z","cwd":"/work/app"}
`+fmt.Sprintf(poll, "u1", "2026-10-18T05:42:12Z", "u1", "2026-10-18T05:42:16Z"))
	main := write("dm.jsonl", `{"type":"session","version":4,"id":"dm","timestamp":"2026-10-18T05:00:00Z","cwd":"/work/app"}
{"type":"message","id":"a","timestamp":"2026-10-18T05:00:01Z","message":{"role":"user","content":"the eviction policy is LRU with a 512 MB cap"}}
{"type":"message","id":"b","timestamp":"2026-10-18T05:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"Noted."}]}}
`+fmt.Sprintf(poll, "u2", "2026-10-18T06:12:00Z", "u2", "2026-10-18T06:12:04Z")+
		`{"type":"message","id":"c","timestamp":"2026-10-18T06:42:00Z","message":{"role":"user","content":"[OpenClaw heartbeat poll]","provenance":{"kind":"internal_system","sourceTool":"heartbeat"}}}
{"type":"message","id":"d","timestamp":"2026-10-18T06:42:04Z","message":{"role":"assistant","content":[{"type":"text","text":"The nightly build failed on main."}]}}
`)
	got, err := ParseOpenClawFile(only)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range got {
		if len(s.Messages) > 0 {
			t.Errorf("a window holding only heartbeat polls reads as a session: %+v", s.Messages)
		}
	}
	got, err = ParseOpenClawFile(main)
	if err != nil || len(got) != 1 {
		t.Fatalf("dm: %v %v", got, err)
	}
	var texts []string
	for _, m := range got[0].Messages {
		texts = append(texts, m.Text)
	}
	want := []string{"the eviction policy is LRU with a 512 MB cap", "Noted.", "The nightly build failed on main."}
	if len(texts) != len(want) {
		t.Fatalf("dm messages = %q, want %q", texts, want)
	}
	for i := range want {
		if texts[i] != want[i] {
			t.Fatalf("dm messages = %q, want %q", texts, want)
		}
	}
}

// OpenClaw 2026.7's default heartbeat prompt says "reply HEARTBEAT_OK", not
// NO_REPLY, to the same poll; every ack stayed in the window as an assistant
// turn (#5326). An ack that carries a report is kept.
func TestOpenClawHeartbeatOKIsNotATurn(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "main", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	poll := `{"type":"message","id":"%s","timestamp":"2026-07-20T05:42:12Z","message":{"role":"user","content":"[OpenClaw heartbeat poll]"}}
{"type":"message","id":"%s-r","timestamp":"2026-07-20T05:42:16Z","message":{"role":"assistant","content":[{"type":"text","text":%q}]}}
`
	body := `{"type":"session","version":3,"id":"hb","timestamp":"2026-07-20T05:00:00Z","cwd":"/work/app"}
{"type":"message","id":"a","timestamp":"2026-07-20T05:00:01Z","message":{"role":"user","content":"the eviction policy is LRU with a 512 MB cap"}}
{"type":"message","id":"b","timestamp":"2026-07-20T05:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"Noted."}]}}
`
	for i, ack := range []string{"HEARTBEAT_OK", "**HEARTBEAT_OK**", "HEARTBEAT_OK.", "NO_REPLY NO_REPLY"} {
		body += fmt.Sprintf(poll, fmt.Sprint("u", i), fmt.Sprint("u", i), ack)
	}
	report := "HEARTBEAT_OK — but the nightly build failed on main."
	body += fmt.Sprintf(poll, "u9", "u9", report)
	p := filepath.Join(dir, "hb.jsonl")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ParseOpenClawFile(p)
	if err != nil || len(got) != 1 {
		t.Fatalf("hb: %v %v", got, err)
	}
	var texts []string
	for _, m := range got[0].Messages {
		texts = append(texts, m.Text)
	}
	want := []string{"the eviction policy is LRU with a 512 MB cap", "Noted.", report}
	if fmt.Sprint(texts) != fmt.Sprint(want) {
		t.Errorf("messages = %q, want %q", texts, want)
	}
}
