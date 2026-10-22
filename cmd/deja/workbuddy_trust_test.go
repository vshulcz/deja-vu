package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The key WorkBuddy AI 5.6.2 wrote into mcp-approvals.json when Trust was
// clicked for a server running "/tmp/hhsbx/workbuddy-gui/deja mcp" (#4740).
func TestWorkBuddyApprovalKeyMatchesTheApp(t *testing.T) {
	got := workBuddyApprovalKey("deja", "/tmp/hhsbx/workbuddy-gui/deja", []string{"mcp"}, nil, "")
	want := "2e57a628dff522acd0dd45f93882c0ae52b7e7a3d20e52cea3d1240ede48420c::deja"
	if got != want {
		t.Fatalf("key = %s, want %s", got, want)
	}
	entry := map[string]any{"command": "/tmp/hhsbx/workbuddy-gui/deja", "args": []any{"mcp"}}
	if got := workBuddyEntryApprovalKey("deja", entry); got != want {
		t.Fatalf("entry key = %s, want %s", got, want)
	}
	// Env keys and cwd are part of what the app hashes.
	entry["env"] = map[string]any{"X": "1"}
	if workBuddyEntryApprovalKey("deja", entry) == want {
		t.Fatal("env keys did not change the key")
	}
}

// workBuddyAIHome makes a WorkBuddy AI home the way the app leaves one.
func workBuddyAIHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(hermeticEnv(t), "home")
	wb := filepath.Join(home, ".workbuddy-ai")
	if err := os.MkdirAll(filepath.Join(wb, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wb, "workbuddy.db"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return wb
}

// approveDeja writes the approval the app writes for deja's entry as mcp.json
// holds it.
func approveDeja(t *testing.T, wb string) {
	t.Helper()
	servers, _ := readCodeBuddyJSON(t, filepath.Join(wb, "mcp.json"))["mcpServers"].(map[string]any)
	entry, _ := servers["deja"].(map[string]any)
	if entry == nil {
		t.Fatalf("no deja entry in %v", servers)
	}
	command, _ := entry["command"].(string)
	raw, ok := entry["args"].([]any)
	if !ok {
		t.Fatalf("deja entry args are not a list: %v", entry)
	}
	args := make([]string, 0, len(raw))
	for _, a := range raw {
		arg, ok := a.(string)
		if !ok {
			t.Fatalf("deja entry arg is not a string: %v", a)
		}
		args = append(args, arg)
	}
	b, err := json.Marshal(map[string]int64{workBuddyApprovalKey("deja", command, args, nil, ""): 1759600000000})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(wb, "mcp-approvals.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Install wires the server and says the one click it still needs; once the
// app holds an approval for that exact command, it says nothing more.
func TestInstallWorkBuddySaysToTrust(t *testing.T) {
	wb := workBuddyAIHome(t)
	dir := index.DefaultDir()
	for _, target := range []string{"workbuddy", "workbuddy-auto"} {
		out := captureStdout(t, func() {
			if err := runInstall(dir, []string{target, "--no-index", "--no-guidance"}, false); err != nil {
				t.Fatal(err)
			}
		})
		if !strings.Contains(out, workBuddyTrustNote) {
			t.Fatalf("%s: no trust line in:\n%s", target, out)
		}
	}
	approveDeja(t, wb)
	out := captureStdout(t, func() {
		if err := runInstall(dir, []string{"workbuddy", "--no-index", "--no-guidance"}, false); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, workBuddyTrustNote) {
		t.Fatalf("approved entry still told to trust:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(wb, "mcp-approvals.json")); err != nil {
		t.Fatal(err)
	}
}

// Install never writes the approval itself, and the workbuddy.cn home, read
// from another edition, gets no line.
func TestInstallWorkBuddyLeavesApprovalsAlone(t *testing.T) {
	wb := workBuddyAIHome(t)
	if _, err := installTarget("workbuddy", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wb, "mcp-approvals.json")); !os.IsNotExist(err) {
		t.Fatalf("install wrote mcp-approvals.json: %v", err)
	}

	home := filepath.Join(hermeticEnv(t), "home")
	if err := os.MkdirAll(filepath.Join(home, ".workbuddy", "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("workbuddy", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if n := workBuddyUntrustedNote(); n != "" {
		t.Fatalf("~/.workbuddy got %q", n)
	}
}

// Doctor reads the approval: untrusted with the fix until the app has one for
// the entry as written, wired after.
func TestDoctorWorkBuddyTrust(t *testing.T) {
	wb := workBuddyAIHome(t)
	if _, err := installTarget("workbuddy", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	row := func() doctorMCPStatus {
		for _, r := range collectDoctorMCP() {
			if r.Name == "workbuddy" {
				return r
			}
		}
		t.Fatal("no workbuddy row")
		return doctorMCPStatus{}
	}
	text := func() string {
		var buf bytes.Buffer
		doctorMCP(&buf)
		for line := range strings.SplitSeq(buf.String(), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "workbuddy ") {
				return line
			}
		}
		return ""
	}

	var buf bytes.Buffer
	doctorMCP(&buf)
	if !strings.Contains(text(), "untrusted") || !strings.Contains(buf.String(), workBuddyTrustNote) {
		t.Fatalf("untrusted entry reported as:\n%s", buf.String())
	}
	if r := row(); r.State != "untrusted" || r.Note != workBuddyTrustNote {
		t.Fatalf("json row = %+v", r)
	}

	approveDeja(t, wb)
	buf.Reset()
	doctorMCP(&buf)
	if !strings.Contains(text(), " wired ") || strings.Contains(buf.String(), workBuddyTrustNote) {
		t.Fatalf("approved entry reported as:\n%s", buf.String())
	}
	if r := row(); r.State != "wired" || r.Note != "" {
		t.Fatalf("json row = %+v", r)
	}

	// An approval for another command is not one for this entry.
	b, err := json.Marshal(map[string]int64{workBuddyApprovalKey("deja", "/old/deja", []string{"mcp"}, nil, ""): 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(wb, "mcp-approvals.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if r := row(); r.State != "untrusted" {
		t.Fatalf("stale approval read as trusted: %+v", r)
	}
}
