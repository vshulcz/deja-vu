package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Each host's payload, as captured on its stand, finds the session the way
// Claude's transcript_path does: Qwen sends only session_id, Kimi sessionId,
// Copilot names the session's directory, and Cursor, CodeBuddy and Grok send
// a transcript path whose name is the id.
func TestStatuslinePayloadsOfEveryHostFindTheSession(t *testing.T) {
	dir := seedTouchedIndex(t, 3, "/w/t/pool.go")
	for name, payload := range map[string]string{
		"qwen":      `{"session_id":"t02","version":"0.20.0","workspace":{"current_dir":"/w/t"}}`,
		"kimi":      `{"model":"m","cwd":"/w/t","sessionId":"t02","version":"2.1.1"}`,
		"copilot":   `{"cwd":"/w/t","session_id":"t02","transcript_path":"/h/.copilot/session-state/t02"}`,
		"cursor":    `{"session_id":"x","transcript_path":"/h/.cursor/projects/w-t/agent-transcripts/t02/t02.jsonl","cwd":"/w/t"}`,
		"codebuddy": `{"hook_event_name":"Status","session_id":"t02","transcript_path":"/h/.codebuddy/projects/w-t/t02.jsonl"}`,
	} {
		in := readStatuslineInput(strings.NewReader(payload))
		if m, ok := statuslineMemory(dir, in); !ok || m.Path != "/w/t/pool.go" {
			t.Errorf("%s: the payload did not find the session (%+v, %v)", name, m, ok)
		}
	}
	// CodeBuddy before the first prompt, Kimi before the first message.
	for _, payload := range []string{`{"session_id":"unknown"}`, `{"sessionId":""}`} {
		if _, ok := statuslineMemory(dir, readStatuslineInput(strings.NewReader(payload))); ok {
			t.Errorf("%s named a session", payload)
		}
	}
}

func hostStatuslineCases(home string) []struct {
	target, path string
	read         func(t *testing.T, path string) string
} {
	jsonAt := func(keys ...string) func(t *testing.T, path string) string {
		return func(t *testing.T, path string) string {
			t.Helper()
			b, err := os.ReadFile(path)
			if err != nil {
				return ""
			}
			var root map[string]any
			if err := json.Unmarshal(b, &root); err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			cur := root
			for _, k := range keys {
				next, _ := cur[k].(map[string]any)
				cur = next
			}
			cmd, _ := cur["command"].(string)
			return cmd
		}
	}
	tomlAt := func(table string) func(t *testing.T, path string) string {
		return func(t *testing.T, path string) string {
			b, _ := os.ReadFile(path)
			return strings.Trim(tomlTableValue(string(b), table, "command"), `"`)
		}
	}
	return []struct {
		target, path string
		read         func(t *testing.T, path string) string
	}{
		{"cursor-auto", filepath.Join(home, ".cursor", "cli-config.json"), jsonAt("statusLine")},
		{"copilot-auto", filepath.Join(home, ".copilot", "settings.json"), jsonAt("statusLine")},
		{"qwen-auto", filepath.Join(home, ".qwen", "settings.json"), jsonAt("ui", "statusLine")},
		{"codebuddy-auto", filepath.Join(home, ".codebuddy", "settings.json"), jsonAt("statusLine")},
		{"grok-auto", filepath.Join(home, ".grok", "config.toml"), tomlAt("ui.status_line")},
		{"kimi-auto", filepath.Join(home, ".kimi-code", "tui.toml"), tomlAt("status_line")},
		{"trae-auto", filepath.Join(home, ".trae", "traecli.toml"), tomlAt("tui.statusline")},
	}
}

// Every host with a command status line gets deja's from its -auto target,
// in the file and under the key its stand read it from, and uninstall takes
// it back out.
func TestAutoTargetsWireTheHostsStatusLine(t *testing.T) {
	hermeticEnv(t)
	home := sources.Home()
	want := hookRun(hookExeFor("/opt/deja/bin/deja", false), "statusline")
	for _, c := range hostStatuslineCases(home) {
		if _, err := installTarget(c.target, "/opt/deja/bin/deja", false); err != nil {
			t.Fatalf("%s: %v", c.target, err)
		}
		if got := c.read(t, c.path); got != want {
			t.Errorf("%s: status line command = %q in %s, want %q", c.target, got, c.path, want)
		}
		before, _ := os.ReadFile(c.path)
		if _, err := installTarget(c.target, "/opt/deja/bin/deja", false); err != nil {
			t.Fatal(err)
		}
		if after, _ := os.ReadFile(c.path); string(after) != string(before) {
			t.Errorf("%s: a second install changed %s:\n%s\n---\n%s", c.target, c.path, before, after)
		}
		if _, err := installTarget(c.target, "/opt/deja/bin/deja", true); err != nil {
			t.Fatal(err)
		}
		if got := c.read(t, c.path); got != "" {
			t.Errorf("%s: uninstall left %q", c.target, got)
		}
		// A line deja wrote from a binary that has since moved is deja's, and
		// is rewritten rather than kept as somebody else's.
		seedStatusline(t, c.target, c.path, "/gone/bin/deja statusline")
		if _, err := installTarget(c.target, "/opt/deja/bin/deja", false); err != nil {
			t.Fatal(err)
		}
		if got := c.read(t, c.path); got != want {
			t.Errorf("%s: a moved binary's line stayed %q", c.target, got)
		}
	}
}

func seedStatusline(t *testing.T, target, path, cmd string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	q, _ := json.Marshal(cmd)
	var seed string
	switch {
	case strings.HasSuffix(path, "tui.toml"):
		seed = "[status_line]\ncommand = " + string(q) + "\n"
	case strings.HasSuffix(path, "traecli.toml"):
		seed = "[tui.statusline]\ntype = \"command\"\ncommand = " + string(q) + "\n"
	case strings.HasSuffix(path, ".toml"):
		seed = "[ui.status_line]\ntype = \"command\"\ncommand = " + string(q) + "\n"
	case target == "qwen-auto":
		seed = `{"ui":{"statusLine":{"type":"command","command":` + string(q) + `}}}`
	default:
		seed = `{"statusLine":{"type":"command","command":` + string(q) + `}}`
	}
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A status line somebody set up is theirs: install leaves it and says how to
// run both, and uninstall does not take it.
func TestAutoTargetsLeaveSomeoneElsesStatusLine(t *testing.T) {
	hermeticEnv(t)
	home := sources.Home()
	for _, c := range hostStatuslineCases(home) {
		seedStatusline(t, c.target, c.path, "~/bin/mine.sh")
		r, err := installTarget(c.target, "/opt/deja/bin/deja", false)
		if err != nil {
			t.Fatalf("%s: %v", c.target, err)
		}
		if got := c.read(t, c.path); got != "~/bin/mine.sh" {
			t.Errorf("%s: install replaced the reader's status line with %q", c.target, got)
		}
		if !strings.Contains(r.Note, "mine.sh") {
			t.Errorf("%s: install did not say how to run both: %+v", c.target, r)
		}
		if _, err := installTarget(c.target, "/opt/deja/bin/deja", true); err != nil {
			t.Fatal(err)
		}
		if got := c.read(t, c.path); got != "~/bin/mine.sh" {
			t.Errorf("%s: uninstall took the reader's status line", c.target)
		}
	}
}
