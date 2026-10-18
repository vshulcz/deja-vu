package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRollout(t *testing.T, name string, lines []string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// A sub-agent spawned with fork_context starts with a copy of its parent's
// conversation, the parent's session_meta first, all stamped at spawn time.
// Read as the child's own, the child took the parent's first prompt as its
// title and every parent word as its own (#5210). Layout from codex-cli
// 0.149.0: the copy carries no thread_settings_applied, the child writes one
// before its first turn.
func TestCodexForkContextChildKeepsOnlyItsOwnTurns(t *testing.T) {
	const child, parent = "01a14d6f-9404-7821-a405-0a7adcaf41e9", "01a14d6c-c3d5-7f61-990c-0d5dd47a0c10"
	p := writeRollout(t, "rollout-2026-10-18T08-15-20-"+child+".jsonl", []string{
		`{"timestamp":"2026-10-18T05:15:20.470Z","type":"session_meta","payload":{"id":"` + child + `","forked_from_id":"` + parent + `","cwd":"/w/app","source":{"subagent":{"thread_spawn":{"parent_thread_id":"` + parent + `","depth":1}}}}}`,
		`{"timestamp":"2026-10-18T05:15:20.470Z","type":"session_meta","payload":{"id":"` + parent + `","cwd":"/w/app","source":"cli"}}`,
		`{"timestamp":"2026-10-18T05:15:20.470Z","type":"event_msg","payload":{"type":"task_started","turn_id":"t1"}}`,
		`{"timestamp":"2026-10-18T05:15:20.471Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"parent asks about EMU-E5"}]}}`,
		`{"timestamp":"2026-10-18T05:15:20.471Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"t1"}}`,
		`{"timestamp":"2026-10-18T05:15:20.472Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"parent spawns the helper"}]}}`,
		`{"timestamp":"2026-10-18T05:15:20.480Z","type":"event_msg","payload":{"type":"thread_settings_applied","thread_settings":{"model":"m"}}}`,
		`{"timestamp":"2026-10-18T05:15:20.481Z","type":"event_msg","payload":{"type":"task_started","turn_id":"t9"}}`,
		`{"timestamp":"2026-10-18T05:15:20.500Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"child task ECHIDNA-E7"}]}}`,
		`{"timestamp":"2026-10-18T05:15:20.510Z","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","call_id":"c1","input":"*** Begin Patch\n*** Update File: app.go\n@@\n+var echidnaLimit = 2048 // ECHIDNA-E7 child line\n*** End Patch\n"}}`,
		`{"timestamp":"2026-10-18T05:15:26.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}}`,
	})
	ss, err := ParseCodexRollout(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions = %d, err %v", len(ss), err)
	}
	s := ss[0]
	if s.ID != child || s.Kind != "subagent" || s.Parent != parent {
		t.Errorf("identity = %s %s %s, want the child under its parent", s.ID, s.Kind, s.Parent)
	}
	var users []string
	wrote := false
	for _, m := range s.Messages {
		if m.Role == "user" {
			users = append(users, m.Text)
		}
		wrote = wrote || m.Role == RoleWrote
	}
	if len(users) != 1 || users[0] != "child task ECHIDNA-E7" {
		t.Errorf("user turns = %q, want only the child's own task", users)
	}
	if !wrote {
		t.Error("the child's own edit lost its wrote record")
	}

	// Control: with no thread_settings_applied after the inherited meta, a
	// format that marks no boundary, nothing is dropped (#3933 shape).
	q := writeRollout(t, "rollout-2026-06-01T02-00-00-c.jsonl", []string{
		`{"timestamp":"2026-06-01T02:00:00Z","type":"session_meta","payload":{"id":"c","forked_from_id":"a","cwd":"/w"}}`,
		`{"timestamp":"2026-06-01T01:00:00Z","type":"session_meta","payload":{"id":"a","cwd":"/w"}}`,
		`{"timestamp":"2026-06-01T02:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"child question"}]}}`,
	})
	ss, err = ParseCodexRollout(q)
	if err != nil || len(ss) != 1 || len(ss[0].Messages) != 1 {
		t.Fatalf("unmarked fork: %+v, err %v, want its one turn kept", ss, err)
	}
}

// A model with no apply_patch tool runs apply_patch through exec_command, and
// Codex applies it all the same. Read as a command only, the edit left no
// files or wrote record and blame found no session for the line (#5211).
func TestCodexShellApplyPatchRecordsTheEdit(t *testing.T) {
	p := writeRollout(t, "rollout-2026-10-18T08-04-16-s.jsonl", []string{
		`{"timestamp":"2026-10-18T05:04:16Z","type":"session_meta","payload":{"id":"s","cwd":"/w/app"}}`,
		`{"timestamp":"2026-10-18T05:04:17Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"create parent.txt"}]}}`,
		`{"timestamp":"2026-10-18T05:04:18Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"c1","arguments":"{\"cmd\":\"apply_patch <<'EOF'\\n*** Begin Patch\\n*** Update File: parent.txt\\n@@\\n-QUOKKA-Q0 the line this edit replaces\\n+QUOKKA-Q1 written by the parent\\n*** End Patch\\nEOF\"}"}}`,
		`{"timestamp":"2026-10-18T05:04:19Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"Exit code: 0\nWall time: 0 seconds\nOutput:\nSuccess. Updated the following files:\nM parent.txt\n"}}`,
	})
	ss, err := ParseCodexRollout(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions = %d, err %v", len(ss), err)
	}
	byRole := map[string][]string{}
	for _, m := range ss[0].Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
	}
	if got := strings.Join(byRole[RoleFiles], "\n"); got != "/w/app/parent.txt" {
		t.Errorf("files = %q, want the patched file", got)
	}
	h, _ := HashWrittenLine("QUOKKA-Q1 written by the parent")
	if len(byRole[RoleWrote]) != 1 {
		t.Fatalf("wrote = %q, want one record", byRole[RoleWrote])
	}
	if path, has := WroteRecordHas(byRole[RoleWrote][0], h); !has || path != "/w/app/parent.txt" {
		t.Errorf("wrote = %q, want the added line under parent.txt", byRole[RoleWrote])
	}
	if len(byRole[RoleEdit]) != 1 || !strings.Contains(byRole[RoleEdit][0], "QUOKKA-Q0") {
		t.Errorf("edit = %q, want the replaced line", byRole[RoleEdit])
	}
}

// Codex applies a shell apply_patch in the call's workdir, not the session's
// cwd: filed under the cwd, a patch run from a subdirectory named a file that
// does not exist and blame found no writer.
func TestCodexShellApplyPatchResolvesAgainstItsWorkdir(t *testing.T) {
	p := writeRollout(t, "rollout-2026-10-18T08-04-16-w.jsonl", []string{
		`{"timestamp":"2026-10-18T05:04:16Z","type":"session_meta","payload":{"id":"w","cwd":"/w/app"}}`,
		`{"timestamp":"2026-10-18T05:04:17Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"edit sub/parent.txt"}]}}`,
		`{"timestamp":"2026-10-18T05:04:18Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"c1","arguments":"{\"cmd\":\"apply_patch <<'EOF'\\n*** Begin Patch\\n*** Update File: parent.txt\\n@@\\n-QUOKKA-W0 the line this edit replaces\\n+QUOKKA-W1 written from sub\\n*** End Patch\\nEOF\",\"workdir\":\"sub\"}"}}`,
		`{"timestamp":"2026-10-18T05:04:19Z","type":"response_item","payload":{"type":"function_call","name":"shell_command","call_id":"c2","arguments":"{\"command\":\"apply_patch <<'EOF'\\n*** Begin Patch\\n*** Add File: other.txt\\n+QUOKKA-W2 written from /w/lib\\n*** End Patch\\nEOF\",\"workdir\":\"/w/lib\"}"}}`,
	})
	ss, err := ParseCodexRollout(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions = %d, err %v", len(ss), err)
	}
	var files []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleFiles {
			files = append(files, m.Text)
		}
	}
	if got := strings.Join(files, "\n"); got != "/w/app/sub/parent.txt\n/w/lib/other.txt" {
		t.Errorf("files = %q, want each patch under its call's workdir", got)
	}
}
