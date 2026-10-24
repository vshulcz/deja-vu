package sources

import (
	"strings"
	"testing"
)

// A shell apply_patch that opens with `cd sub &&` edits sub/<header>, and the
// reader filed it under the call's workdir or the session's cwd instead, so
// blame credited a file of the same name there (#5306).
func TestCodexShellApplyPatchFollowsALeadingCD(t *testing.T) {
	p := writeRollout(t, "rollout-2026-10-18T08-04-16-cd.jsonl", []string{
		`{"timestamp":"2026-10-18T05:04:16Z","type":"session_meta","payload":{"id":"cd","cwd":"/w/app"}}`,
		`{"timestamp":"2026-10-18T05:04:17Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"edit sub/parent.txt"}]}}`,
		`{"timestamp":"2026-10-18T05:04:18Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"c1","arguments":"{\"cmd\":\"cd sub && apply_patch <<'EOF'\\n*** Begin Patch\\n*** Update File: parent.txt\\n@@\\n-WOMBAT-C0 the line this edit replaces\\n+WOMBAT-C1 written after cd\\n*** End Patch\\nEOF\"}"}}`,
		`{"timestamp":"2026-10-18T05:04:19Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"c2","arguments":"{\"cmd\":\"apply_patch <<'EOF'\\n*** Begin Patch\\n*** Update File: parent.txt\\n@@\\n-WOMBAT-C1\\n+WOMBAT-C2 written with workdir\\n*** End Patch\\nEOF\",\"workdir\":\"sub\"}"}}`,
		`{"timestamp":"2026-10-18T05:04:20Z","type":"response_item","payload":{"type":"function_call","name":"shell_command","call_id":"c3","arguments":"{\"command\":\"cd /w/lib && apply_patch <<'EOF'\\n*** Begin Patch\\n*** Add File: other.txt\\n+WOMBAT-C3 written in /w/lib\\n*** End Patch\\nEOF\"}"}}`,
		`{"timestamp":"2026-10-18T05:04:21Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"c4","arguments":"{\"cmd\":\"cd \\\"deep dir\\\" && apply_patch <<'EOF'\\n*** Begin Patch\\n*** Add File: inner.txt\\n+WOMBAT-C4 written in a quoted dir under the workdir\\n*** End Patch\\nEOF\",\"workdir\":\"sub\"}"}}`,
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
	want := "/w/app/sub/parent.txt\n/w/app/sub/parent.txt\n/w/lib/other.txt\n/w/app/sub/deep dir/inner.txt"
	if got := strings.Join(files, "\n"); got != want {
		t.Errorf("files =\n%s\nwant each patch where its cd left it:\n%s", got, want)
	}
}

func TestShellPatchDir(t *testing.T) {
	tests := []struct {
		name, cmd, dir, want string
	}{
		{"no cd", "apply_patch <<'EOF'", "/w/app", "/w/app"},
		{"relative", "cd sub && apply_patch <<'EOF'", "/w/app", "/w/app/sub"},
		{"absolute", "cd /w/lib && apply_patch", "/w/app", "/w/lib"},
		{"semicolon", "cd sub; apply_patch", "/w/app", "/w/app/sub"},
		{"chained", "cd sub && cd inner && apply_patch", "/w/app", "/w/app/sub/inner"},
		{"double quoted", `cd "a b" && apply_patch`, "/w/app", "/w/app/a b"},
		{"single quoted", `cd 'a b' && apply_patch`, "/w/app", "/w/app/a b"},
		{"leading blanks", "  cd sub && apply_patch", "/w/app", "/w/app/sub"},
		{"parent", "cd .. && apply_patch", "/w/app/sub", "/w/app"},
		{"home is not followed", "cd ~ && apply_patch", "/w/app", "/w/app"},
		{"variable is not followed", "cd $TMP && apply_patch", "/w/app", "/w/app"},
		{"dash is not followed", "cd - && apply_patch", "/w/app", "/w/app"},
		{"cd with no command after", "cd sub", "/w/app", "/w/app"},
		{"cd not chained", "cd sub || apply_patch", "/w/app", "/w/app"},
		{"unclosed quote", `cd "sub && apply_patch`, "/w/app", "/w/app"},
		{"cd later in the command", "echo hi && cd sub && apply_patch", "/w/app", "/w/app"},
		{"a word starting with cd", "cdx sub && apply_patch", "/w/app", "/w/app"},
		{"windows dir", `cd sub && apply_patch`, `C:\w\app`, `C:\w\app\sub`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ShellPatchDir(tc.cmd, tc.dir)
			if tc.name == "windows dir" {
				got = strings.ReplaceAll(got, "/", `\`)
			}
			if got != tc.want {
				t.Errorf("ShellPatchDir(%q, %q) = %q, want %q", tc.cmd, tc.dir, got, tc.want)
			}
		})
	}
}
