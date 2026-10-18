package sources

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Qwen Code 0.20 writes every sub-agent run, foreground, background or fork,
// to subagents/<session>/agent-<id>.jsonl. The reader took them only under
// DEJA_INCLUDE_SUBAGENTS=1, so by default nothing in the index had written the
// file a sub-agent wrote and blame said nobody did (#5223). They are read the
// way Claude's are now: the task, the edits and the last turns.
func TestQwenSubagentRunIsReadByDefault(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", filepath.Join(root, "home"))
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(root, "qwen"))
	project := filepath.Join(root, "qwen", "projects", "-w-my-app")
	put := func(p, body string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	const parent = "8bde8fee-08e8-4950-b83b-2e23c486eb7d"
	put(filepath.Join(project, "chats", parent+".jsonl"),
		`{"type":"user","sessionId":"`+parent+`","timestamp":"2026-10-18T06:02:40Z","cwd":"/w/my-app","message":{"role":"user","parts":[{"text":"delegate the notes"}]}}`+"\n")
	rec := func(typ, at, msg string) string {
		return `{"sessionId":"` + parent + `","timestamp":"2026-10-18T06:10:` + at + `Z","type":"` + typ + `","cwd":"/w/my-app","agentId":"fork-call_f7a","agentName":"fork","isSidechain":true,` + msg + "}\n"
	}
	// A fork child: the parent's history it inherits sits in a system
	// agent_bootstrap record, then the directive, then its own work.
	body := `{"sessionId":"` + parent + `","timestamp":"2026-10-18T06:10:00Z","type":"system","subtype":"agent_bootstrap","agentId":"fork-call_f7a","agentName":"fork","isSidechain":true,"systemPayload":{"kind":"fork","history":[{"role":"user","parts":[{"text":"PARENTONLY delegate the notes"}]}]}}` + "\n" +
		rec("user", "01", `"message":{"role":"user","parts":[{"text":"write fork.txt with the NUMBAT5 line"}]}`) +
		rec("assistant", "02", `"message":{"role":"model","parts":[{"functionCall":{"id":"c1","name":"write_file","args":{"file_path":"/w/my-app/fork.txt","content":"NUMBAT5 fork child line written by the forked sub-agent"}}}]}`) +
		rec("tool_result", "03", `"message":{"role":"user","parts":[{"functionResponse":{"id":"c1","name":"write_file","response":{"output":"Successfully created and wrote to new file: /w/my-app/fork.txt."}}}]}`)
	for i := 0; i < 8; i++ {
		body += rec("assistant", fmt.Sprintf("%02d", 10+i), `"message":{"role":"model","parts":[{"text":"MIDDLEPROSE step `+fmt.Sprint(i)+`"}]}`)
	}
	body += rec("assistant", "30", `"message":{"role":"model","parts":[{"text":"fork.txt written"}]}`)
	child := put(filepath.Join(project, "subagents", parent, "agent-fork-call_f7a.jsonl"), body)

	has := func(p string) bool {
		for _, f := range QwenSessionFiles() {
			if f == p {
				return true
			}
		}
		return false
	}
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")
	if !has(child) {
		t.Fatal("a sub-agent log is left out by default")
	}
	ss, err := ParseQwenFile(child)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parsed %d sessions: %v", len(ss), err)
	}
	s := ss[0]
	if s.Kind != "subagent" || s.Parent != parent || s.Agent != "fork" {
		t.Errorf("child read as kind=%q parent=%q agent=%q, want a fork sub-agent of %s", s.Kind, s.Parent, s.Agent, parent)
	}
	var text []string
	wrote := false
	for _, m := range s.Messages {
		text = append(text, m.Text)
		if m.Role == RoleWrote && strings.HasPrefix(m.Text, "/w/my-app/fork.txt") {
			wrote = true
		}
	}
	all := strings.Join(text, "\n")
	if !strings.HasPrefix(s.Messages[0].Text, "write fork.txt") {
		t.Errorf("first turn is %q, want the task", s.Messages[0].Text)
	}
	if !wrote {
		t.Errorf("the child's write of fork.txt is not kept: %q", all)
	}
	if strings.Contains(all, "PARENTONLY") {
		t.Error("the parent's history copied into the fork is read as the child's")
	}
	if strings.Contains(all, "MIDDLEPROSE step 0") || !strings.Contains(all, "fork.txt written") {
		t.Errorf("default read is not task, edits and tail: %q", all)
	}

	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	if ss, _ := ParseQwenFile(child); len(ss) != 1 || !strings.Contains(fmt.Sprint(ss[0].Messages), "MIDDLEPROSE step 0") {
		t.Error("DEJA_INCLUDE_SUBAGENTS=1 does not keep the whole run")
	}
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "0")
	if has(child) {
		t.Error("DEJA_INCLUDE_SUBAGENTS=0 still reads the sub-agent log")
	}
}
