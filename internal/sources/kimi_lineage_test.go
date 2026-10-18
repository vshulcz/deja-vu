package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Shapes below are what kimi-code 2.2.0 wrote in a live run: `kimi fork`
// copies each agent's wire.jsonl and closes the copy with a forked record, and
// a coder sub-agent opens on the parent's prompt under origin
// system_trigger/subagent.

func kimiLineagePut(t *testing.T, p, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const kimiSourceWire = `{"type":"metadata","protocol_version":"1.5","created_at":1792304049712}
{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"text","text":"ticket srcneedle: create parent2.txt"}],"origin":{"kind":"user"}},"time":1792304049800}
{"type":"context.append_loop_event","agentId":"main","event":{"type":"tool.call","toolCallId":"c1","name":"Write","args":{"path":"/tmp/proj/kq-app/parent2.txt","content":"the parent agent wrote this particular line KQP-11\n"}},"time":1792304049900}
{"type":"context.append_loop_event","agentId":"main","event":{"type":"step.begin","uuid":"s1"},"time":1792304050000}
{"type":"context.append_loop_event","agentId":"main","event":{"type":"content.part","part":{"type":"text","text":"created parent2.txt"}},"time":1792304050100}
{"type":"context.append_loop_event","agentId":"main","event":{"type":"step.end","uuid":"s1"},"time":1792304050200}
`

func TestKimiForkKeepsOnlyItsOwnTurnsUnderItsSource(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_KIMI_ROOT", root)
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	ws := filepath.Join(root, "sessions", "wd_kq-app_ab")
	kimiLineagePut(t, filepath.Join(ws, "session_src", "state.json"), `{"id":"session_src","version":2,"cwd":"/tmp/proj/kq-app","createdAt":1792304049671,"updatedAt":1792304091777}`)
	src := kimiLineagePut(t, filepath.Join(ws, "session_src", "agents", "main", "wire.jsonl"), kimiSourceWire)
	kimiLineagePut(t, filepath.Join(ws, "session_fork", "state.json"), `{"id":"session_fork","version":2,"cwd":"/tmp/proj/kq-app","title":"Fork: session_src","forkedFrom":"session_src","createdAt":1792304312258,"updatedAt":1792304091777,"agents":{"main":{"type":"main"},"agent-0":{"type":"sub","parentAgentId":"main"}}}`)
	fork := kimiLineagePut(t, filepath.Join(ws, "session_fork", "agents", "main", "wire.jsonl"), kimiSourceWire+
		`{"type":"forked","agentId":"main","time":1792304312241}
{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"text","text":"forkneedle: only the fork asked this"}],"origin":{"kind":"user"}},"time":1792304313000}
`)
	// A sub-agent run of the source, copied into the fork and never resumed
	// there: nothing in it is the fork's.
	copiedChild := kimiLineagePut(t, filepath.Join(ws, "session_fork", "agents", "agent-0", "wire.jsonl"),
		`{"type":"metadata","protocol_version":"1.5","created_at":1792304049712}
{"type":"context.append_message","agentId":"agent-0","message":{"role":"user","content":[{"type":"text","text":"child task copied"}],"origin":{"kind":"system_trigger","name":"subagent"}},"time":1792304049850}
{"type":"forked","agentId":"agent-0","time":1792304312241}
`)

	ss, err := ParseKimiFile(fork)
	if err != nil || len(ss) != 1 {
		t.Fatalf("fork parsed %d sessions: %v", len(ss), err)
	}
	f := ss[0]
	if f.Kind != "fork" || f.Parent != "session_src" {
		t.Errorf("fork read as kind=%q parent=%q, want a fork of session_src", f.Kind, f.Parent)
	}
	for _, m := range f.Messages {
		if strings.Contains(m.Text, "srcneedle") || m.Role == RoleWrote {
			t.Errorf("fork holds the source's copied record %s %q", m.Role, m.Text)
		}
	}
	if len(f.Messages) != 1 || !strings.Contains(f.Messages[0].Text, "forkneedle") {
		t.Errorf("fork messages = %#v, want only its own turn", f.Messages)
	}
	if got := f.Started.UnixMilli(); got != 1792304313000 {
		t.Errorf("fork starts at %d, want its own first turn 1792304313000", got)
	}
	if ss, _ := ParseKimiFile(copiedChild); len(ss) != 0 {
		t.Errorf("a copied, unresumed sub-agent log reads as %d sessions of the fork: %#v", len(ss), ss)
	}

	// The source is untouched by the rule: it carries no forkedFrom.
	ss, err = ParseKimiFile(src)
	if err != nil || len(ss) != 1 || ss[0].Kind != "" || ss[0].Parent != "" {
		t.Fatalf("source parsed as %#v: %v", ss, err)
	}
	if !strings.Contains(ss[0].Messages[0].Text, "srcneedle") {
		t.Errorf("source lost its own turn: %#v", ss[0].Messages)
	}
}

func TestKimiSubagentKeepsItsTaskAndRelativePathsSitUnderTheCwd(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_KIMI_ROOT", root)
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	sess := filepath.Join(root, "sessions", "wd_kq-app_ab", "session_p")
	kimiLineagePut(t, filepath.Join(sess, "state.json"), `{"id":"session_p","version":2,"cwd":"/tmp/proj/kq-app","createdAt":1792304049671,"updatedAt":1792304091777}`)
	kimiLineagePut(t, filepath.Join(sess, "agents", "main", "wire.jsonl"), kimiSourceWire)
	child := kimiLineagePut(t, filepath.Join(sess, "agents", "agent-1", "wire.jsonl"),
		`{"type":"metadata","protocol_version":"1.5","created_at":1792304060000}
{"type":"context.append_message","agentId":"agent-1","message":{"role":"user","content":[{"type":"text","text":"Ticket KQC-13 child task: edit notes2.md"}],"origin":{"kind":"system_trigger","name":"subagent"}},"time":1792304060100}
{"type":"context.append_message","agentId":"agent-1","message":{"role":"user","content":[{"type":"text","text":"<system-reminder>\nAuto permission mode is active.\n</system-reminder>"}],"origin":{"kind":"injection","variant":"permission_mode"}},"time":1792304060110}
{"type":"context.append_loop_event","agentId":"agent-1","event":{"type":"tool.call","toolCallId":"e1","name":"Edit","args":{"path":"notes2.md","old_string":"the original retry budget line that the child will replace soon","new_string":"the retry budget line rewritten by the coder sub agent KQC-13"}},"time":1792304060200}
{"type":"context.append_loop_event","agentId":"agent-1","event":{"type":"tool.call","toolCallId":"e2","name":"Write","args":{"path":"~/notes/home.txt","content":"a line written under the home directory by the sub agent\n"}},"time":1792304060300}
{"type":"context.append_loop_event","agentId":"agent-1","event":{"type":"tool.call","toolCallId":"e4","name":"Write","args":{"path":"~tilde.txt","content":"a file whose name starts with a tilde, under the cwd\n"}},"time":1792304060350}
{"type":"context.append_loop_event","agentId":"agent-1","event":{"type":"tool.call","toolCallId":"e3","name":"Write","args":{"path":"/tmp/elsewhere/abs.txt","content":"an absolute line written by the coder sub agent here\n"}},"time":1792304060400}
`)
	ss, err := ParseKimiFile(child)
	if err != nil || len(ss) != 1 {
		t.Fatalf("child parsed %d sessions: %v", len(ss), err)
	}
	s := ss[0]
	if s.Kind != "subagent" || s.Parent != "session_p" {
		t.Errorf("child read as kind=%q parent=%q", s.Kind, s.Parent)
	}
	if len(s.Messages) == 0 || s.Messages[0].Role != "user" || !strings.Contains(s.Messages[0].Text, "KQC-13 child task") {
		t.Errorf("child's first message = %#v, want its task as a user turn", s.Messages)
	}
	for _, m := range s.Messages {
		if strings.Contains(m.Text, "Auto permission mode") {
			t.Errorf("the host's reminder came through as %s: %q", m.Role, m.Text)
		}
	}
	paths := map[string][]string{}
	for _, m := range s.Messages {
		switch m.Role {
		case RoleFiles, RoleEdit, RoleWrote:
			p, _, _ := strings.Cut(m.Text, "\n")
			paths[m.Role] = append(paths[m.Role], p)
		}
	}
	want := map[string][]string{
		RoleFiles: {"/tmp/proj/kq-app/notes2.md", "~/notes/home.txt", "/tmp/proj/kq-app/~tilde.txt", "/tmp/elsewhere/abs.txt"},
		RoleEdit:  {"/tmp/proj/kq-app/notes2.md"},
		RoleWrote: {"/tmp/proj/kq-app/notes2.md", "~/notes/home.txt", "/tmp/proj/kq-app/~tilde.txt", "/tmp/elsewhere/abs.txt"},
	}
	for role, w := range want {
		if strings.Join(paths[role], "|") != strings.Join(w, "|") {
			t.Errorf("%s paths = %v, want %v", role, paths[role], w)
		}
	}
}

// The explore profile puts Kimi's <git-context> block ahead of the parent's
// prompt, under the same origin; kept, it titled the run "<git-context>
// Working directory: …".
func TestKimiExploreTaskLeavesKimisGitContextOut(t *testing.T) {
	for name, block := range map[string]string{
		"repo":       `<git-context>\nWorking directory: /tmp/proj/kq-app\nBranch: master\nDirty files (1):\n ?? main.go\n</git-context>\n\n`,
		"not a repo": `<git-context status=\"unavailable\" reason=\"not-a-repo\"/>\n\n`,
	} {
		root := t.TempDir()
		t.Setenv("DEJA_KIMI_ROOT", root)
		t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
		sess := filepath.Join(root, "sessions", "wd_kq-app_ab", "session_p")
		kimiLineagePut(t, filepath.Join(sess, "state.json"), `{"id":"session_p","version":2,"cwd":"/tmp/proj/kq-app"}`)
		child := kimiLineagePut(t, filepath.Join(sess, "agents", "agent-0", "wire.jsonl"),
			`{"type":"metadata","protocol_version":"1.5","created_at":1792304060000}
{"type":"context.append_message","agentId":"agent-0","message":{"role":"user","content":[{"type":"text","text":"`+block+`find the retry notes"}],"origin":{"kind":"system_trigger","name":"subagent"}},"time":1792304060100}
`)
		ss, err := ParseKimiFile(child)
		if err != nil || len(ss) != 1 || len(ss[0].Messages) != 1 {
			t.Fatalf("%s: parsed %#v: %v", name, ss, err)
		}
		if got := ss[0].Messages[0].Text; got != "find the retry notes" {
			t.Errorf("%s: task = %q, want the parent's prompt alone", name, got)
		}
	}
}

// kimi-code writes a fork's wire copies before its state.json; a pass in that
// gap stored the copy as the fork's own, and a tail holding only the forked
// record could not take it back. That tail is read whole.
func TestKimiTailWithTheForkedRecordIsReadWhole(t *testing.T) {
	p := kimiLineagePut(t, filepath.Join(t.TempDir(), "session_fork", "agents", "main", "wire.jsonl"), kimiSourceWire)
	offset := int64(len(kimiSourceWire))
	add := func(line string) {
		f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.WriteString(line)
		_ = f.Close()
	}
	add(`{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"text","text":"next"}],"origin":{"kind":"user"}},"time":1792304060000}` + "\n")
	if !kimiTailResumes(p, offset) {
		t.Fatal("control: an ordinary tail is not appended")
	}
	add(`{"type":"forked","agentId":"main","time":1792304312241}` + "\n")
	if kimiTailResumes(p, offset) {
		t.Error("a tail holding the forked record is appended to the stored copy")
	}
}

// A line a Kimi sub-agent wrote had no author unless DEJA_INCLUDE_SUBAGENTS=1:
// the logs were skipped whole (#5229). They come in by default the way
// Claude's and Qwen's do, as the task, the last turns and every change.
func TestKimiSubagentIsReadByDefaultAsTaskAnswerAndChanges(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_KIMI_ROOT", root)
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")
	sess := filepath.Join(root, "sessions", "wd_kq-app_ab", "session_p")
	kimiLineagePut(t, filepath.Join(sess, "state.json"), `{"id":"session_p","version":2,"cwd":"/tmp/proj/kq-app"}`)
	kimiLineagePut(t, filepath.Join(sess, "agents", "main", "wire.jsonl"), kimiSourceWire)
	var b strings.Builder
	b.WriteString(`{"type":"context.append_message","agentId":"agent-0","message":{"role":"user","content":[{"type":"text","text":"Ticket KQC-12 child task: create child2.txt"}],"origin":{"kind":"system_trigger","name":"subagent"}},"time":1792304060100}` + "\n")
	b.WriteString(`{"type":"context.append_loop_event","agentId":"agent-0","event":{"type":"tool.call","toolCallId":"w1","name":"Write","args":{"path":"child2.txt","content":"the coder sub agent wrote this particular line KQC-12\n"}},"time":1792304060200}` + "\n")
	for i := 0; i < 8; i++ {
		b.WriteString(`{"type":"context.append_loop_event","agentId":"agent-0","event":{"type":"tool.result","toolCallId":"r","result":{"output":"middleneedle reading"}},"time":1792304060300}` + "\n")
	}
	b.WriteString(`{"type":"context.append_message","agentId":"agent-0","message":{"role":"assistant","content":[{"type":"text","text":"created child2.txt"}]},"time":1792304060400}` + "\n")
	child := kimiLineagePut(t, filepath.Join(sess, "agents", "agent-0", "wire.jsonl"), b.String())

	found := false
	for _, f := range KimiSessionFiles() {
		found = found || f == child
	}
	if !found {
		t.Fatal("the sub-agent log is not read by default")
	}
	ss, err := ParseKimiFile(child)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parsed %d sessions: %v", len(ss), err)
	}
	var roles []string
	wrote := false
	for _, m := range ss[0].Messages {
		roles = append(roles, m.Role)
		wrote = wrote || m.Role == RoleWrote && strings.HasPrefix(m.Text, "/tmp/proj/kq-app/child2.txt\n")
	}
	if ss[0].Messages[0].Role != "user" || !wrote || ss[0].Messages[len(ss[0].Messages)-1].Text != "created child2.txt" {
		t.Errorf("default read kept %v, want the task, the write and the answer", roles)
	}
	if n := len(ss[0].Messages); n > SubagentTailKept+3 {
		t.Errorf("default read kept %d messages, want the middle cut: %v", n, roles)
	}
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	if ss, _ := ParseKimiFile(child); len(ss) != 1 || len(ss[0].Messages) < 12 {
		t.Errorf("DEJA_INCLUDE_SUBAGENTS=1 does not keep the whole run: %d", len(ss[0].Messages))
	}
}
