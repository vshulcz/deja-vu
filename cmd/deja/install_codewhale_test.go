package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CodeWhale reads `servers` in mcp.json and starts a server only when
// tools.always_load names its tool; with both, a 0.10.0 stand had deja's tool
// in the request. Uninstall takes both out and leaves the reader's config.
func TestInstallCodeWhaleWritesTheServerAndLoadsItsTool(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("CODEWHALE_HOME", "")
	home := os.Getenv("HOME")
	cfg := filepath.Join(home, ".codewhale", "config.toml")
	theirs := "provider = \"openai\"\n\n[tools]\nalways_load = [\"git_show\"]\n"
	writeFileMkdir(t, cfg, theirs)

	if _, err := captureRun(t, "install", "codewhale-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	// The test binary is deja.test, which is deja's only by the wiring record,
	// and the record is cached per process: read before this install wrote it,
	// the cache failed the check whenever an earlier test had filled it.
	forgetWrittenExes()
	var mcp struct {
		Servers map[string]map[string]any `json:"servers"`
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(home, ".codewhale", "mcp.json"))), &mcp); err != nil {
		t.Fatal(err)
	}
	if !entryRunsDeja(mcp.Servers["deja"]) {
		t.Errorf("mcp.json has no deja server: %+v", mcp.Servers)
	}
	got := readFile(t, cfg)
	if !strings.Contains(got, `always_load = ["git_show", "mcp_deja_deja"]`) {
		t.Errorf("always_load not extended:\n%s", got)
	}
	for _, ev := range codewhaleHookEvents {
		if !strings.Contains(got, "event = \""+ev+"\"") || !strings.Contains(got, "hook-codewhale "+ev) {
			t.Errorf("no %s hook:\n%s", ev, got)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "deja-history", "SKILL.md")); err != nil {
		t.Errorf("no shared skill: %v", err)
	}

	if _, err := captureRun(t, "install", "codewhale-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if again := readFile(t, cfg); again != got {
		t.Errorf("second install changed config.toml:\n%s", again)
	}

	if _, err := captureRun(t, "uninstall", "codewhale-auto"); err != nil {
		t.Fatal(err)
	}
	if after := readFile(t, cfg); after != theirs {
		t.Errorf("uninstall left config.toml as\n%q\nwant\n%q", after, theirs)
	}
}

func TestTomlListedUnder(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "[tools]\nalways_load = [\"x\"]\n"},
		{"a = 1\n", "a = 1\n\n[tools]\nalways_load = [\"x\"]\n"},
		{"[tools]\nb = 2\n", "[tools]\nalways_load = [\"x\"]\nb = 2\n"},
		{"[tools]\nalways_load = [\"y\"] # mine\n[other]\n", "[tools]\nalways_load = [\"y\", \"x\"]\n[other]\n"},
		{"[tools]\nalways_load = [\"x\"]\n", "[tools]\nalways_load = [\"x\"]\n"},
	}
	for _, c := range cases {
		got, err := tomlListedUnder(c.in, "tools", "always_load", "x", false)
		if err != nil || got != c.want {
			t.Errorf("add to %q = %q, %v; want %q", c.in, got, err, c.want)
		}
		back, err := tomlListedUnder(got, "tools", "always_load", "x", true)
		if err != nil {
			t.Fatal(err)
		}
		if c.in != "[tools]\nalways_load = [\"x\"]\n" && strings.Contains(back, `"x"`) {
			t.Errorf("remove from %q left %q", got, back)
		}
	}
	if _, err := tomlListedUnder("[tools]\nalways_load = [\n  \"y\",\n]\n", "tools", "always_load", "x", false); err == nil {
		t.Error("a multi-line array was edited")
	}
	if _, err := tomlListedUnder("tools.always_load = []\n", "tools", "always_load", "x", false); err == nil {
		t.Error("a dotted key was not refused")
	}
}

// message_submit answers with the person's text first and deja's additions
// framed after it; nothing at all when deja has nothing to add, so CodeWhale
// keeps the message as typed.
func TestHookCodeWhaleMessageSubmitKeepsTheTextFirst(t *testing.T) {
	hermeticEnv(t)
	dir := t.TempDir()
	var out bytes.Buffer
	in := `{"event":"message_submit","text":"fix the flaky test","session_id":"sess_1","workspace":"/w"}`
	if err := runHookCodeWhale(dir, []string{"message_submit"}, strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if s := strings.TrimSpace(out.String()); s != "" {
		var resp struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(s), &resp); err != nil {
			t.Fatalf("not JSON: %q", s)
		}
		if !strings.HasPrefix(resp.Text, "fix the flaky test\n\n<deja-recall>") {
			t.Errorf("text = %q", resp.Text)
		}
	}
	if err := runHookCodeWhale(dir, []string{"nope"}, strings.NewReader(""), &out); err == nil {
		t.Error("an unknown event was accepted")
	}
}

// tool_call_before reads the tool from the environment and never sends a
// decision: allow and none are the same to CodeWhale.
func TestHookCodeWhaleToolCallBeforeSendsNoDecision(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEEPSEEK_TOOL_NAME", "edit")
	t.Setenv("DEEPSEEK_TOOL_ARGS", `{"path":"main.go"}`)
	t.Setenv("DEEPSEEK_SESSION_ID", "sess_1")
	t.Setenv("DEEPSEEK_WORKSPACE", t.TempDir())
	var out bytes.Buffer
	if err := runHookCodeWhale(t.TempDir(), []string{"tool_call_before"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "decision") {
		t.Errorf("hook sent a decision: %s", out.String())
	}
}
