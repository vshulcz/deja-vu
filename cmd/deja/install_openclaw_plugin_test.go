package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

func TestInstallOpenClawPluginWritesWhatTheLoaderNeeds(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installOpenClawPlugin("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	dir := filepath.Join(sources.OpenClawStateDir(), "extensions", "deja")
	// Both files or neither: openclaw fails the load with a validation error
	// when one is missing rather than ignoring the plugin.
	for _, name := range []string{"package.json", "openclaw.plugin.json", "index.mjs"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("plugin missing %s: %v", name, err)
		}
	}
	var pkg struct {
		OpenClaw struct {
			Extensions []string `json:"extensions"`
		} `json:"openclaw"`
	}
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &pkg); err != nil {
		t.Fatalf("package.json is not valid JSON: %v", err)
	}
	if len(pkg.OpenClaw.Extensions) == 0 {
		t.Error("package.json declares no openclaw.extensions, which is the field " +
			"the installer rejects a plugin for")
	}

	// Discovery is not enough — the entry is what turns the plugin on.
	cfg := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
	body, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if !strings.Contains(string(body), `"deja"`) {
		t.Errorf("plugin not enabled in the config:\n%s", body)
	}
	// The user's trust list is theirs to write.
	if strings.Contains(string(body), `"allow"`) {
		t.Errorf("install wrote a plugins.allow entry:\n%s", body)
	}

	if _, err := installOpenClawPlugin("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("plugin survived uninstall: %v", err)
	}
	after, _ := os.ReadFile(cfg)
	if strings.Contains(string(after), "deja") {
		t.Errorf("uninstall left the plugin enabled:\n%s", after)
	}
}

// openclaw keeps other people's plugins in the same config file, so the entry
// deja adds and removes must not take theirs with it.
func TestInstallOpenClawPluginLeavesOtherPluginsAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	state := sources.OpenClawStateDir()
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(state, "openclaw.json")
	theirs := `{"plugins":{"allow":["theirs"],"entries":{"theirs":{"enabled":true}}}}` + "\n"
	if err := os.WriteFile(cfg, []byte(theirs), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installOpenClawPlugin("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := installOpenClawPlugin("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	var root map[string]any
	b, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("config is no longer valid JSON: %v", err)
	}
	plugins, _ := root["plugins"].(map[string]any)
	entries, _ := plugins["entries"].(map[string]any)
	if _, ok := entries["theirs"]; !ok {
		t.Errorf("their plugin entry is gone:\n%s", b)
	}
	if _, ok := plugins["allow"]; !ok {
		t.Errorf("their allow list is gone:\n%s", b)
	}
}

// The plugin's contract is narrow enough to be worth exercising: openclaw
// hands before_prompt_build the prompt and uses only prependContext and the
// system-context fields from what it returns. A handler that returns the
// recall in the wrong shape looks installed and injects nothing.
func TestOpenClawPluginRecallsForThePrompt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin openclaw would run")
	}
	home := t.TempDir()
	stub := filepath.Join(home, "deja")
	calls := filepath.Join(home, "calls")
	// Answers only the question the corpus knows about, so silence is checked
	// on the same stub rather than a second one.
	script := "#!/bin/sh\nin=$(cat)\nprintf '%s\\n' \"$in\" >> " + calls + "\n" +
		"case \"$in\" in *rotate*) printf 'RECALLED every 47 days' ;; esac\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(home, "index.mjs")
	if err := os.WriteFile(plugin, []byte(openclawPluginJS(stub)), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := `
import plugin from "` + plugin + `";
let handler;
plugin.register({ on: (name, fn) => { if (name === "before_prompt_build") handler = fn } });
if (!handler) { console.log("NOHOOK"); process.exit(0) }
const asked = await handler({ prompt: "how often does the token rotate?", messages: [] });
const quiet = await handler({ prompt: "something the history never saw", messages: [] });
console.log(JSON.stringify(asked));
console.log(JSON.stringify(quiet ?? null));
`
	run := filepath.Join(home, "drive.mjs")
	if err := os.WriteFile(run, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, run).CombinedOutput()
	if err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		t.Fatalf("driver said %q", out)
	}
	if lines[0] == "NOHOOK" {
		t.Fatal("the plugin registers no before_prompt_build handler, so it never " +
			"sees a prompt")
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("handler returned %s", lines[0])
	}
	if !strings.Contains(got["prependContext"], "RECALLED") {
		t.Errorf("recall did not come back as prependContext, which is the only "+
			"field openclaw reads here: %s", lines[0])
	}
	// A hook that talks on every prompt is wallpaper; deja stays silent when
	// the history has nothing.
	if lines[1] != "null" {
		t.Errorf("the plugin injected something for a prompt with no match: %s", lines[1])
	}
	body, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("the plugin never ran deja: %v", err)
	}
	if !strings.Contains(string(body), "how often does the token rotate?") {
		t.Errorf("the prompt never reached the hook:\n%s", body)
	}
}

// Compaction throws away the blocks a session was shown, and the list that
// stops them repeating outlives it. before_compaction fires in the plugin —
// measured on OpenClaw 2026.7.1-2, with the session id on the hook context.
func TestOpenClawPluginForgetsOnCompaction(t *testing.T) {
	js := openclawPluginJS("/bin/deja")
	if !strings.Contains(js, `"before_compaction"`) {
		t.Fatalf("nothing forgets after a compaction:\n%s", js)
	}
	// The session it sends is checked by driving the plugin
	// (TestOpenClawPluginStampsTheTranscriptID); here, that the capture is told
	// the file and whose store it is.
	if !strings.Contains(js, `ask(["hook-precompact"], {
          session_id: at.id,
          transcript_path: event?.sessionFile || "",`) ||
		!strings.Contains(js, `harness: "openclaw"`) {
		t.Fatalf("the forget is not the one deja means — no hook-precompact with this session:\n%s", js)
	}
}

// OpenClaw's own docs call before_agent_start a compatibility-only combined
// phase and ask new plugins to use the explicit ones. agent_turn_prepare is
// also where queued next-turn injections are drained, so the digest sits where
// that seam would arrive if it ever starts delivering.
func TestOpenClawPluginUsesThePhaseHooks(t *testing.T) {
	js := openclawPluginJS("/bin/deja")
	if strings.Contains(js, `"before_agent_start"`) {
		t.Fatalf("the digest is still on the compatibility hook:\n%s", js)
	}
	for _, want := range []string{`"agent_turn_prepare"`, `"before_prompt_build"`} {
		if !strings.Contains(js, want) {
			t.Fatalf("missing %s:\n%s", want, js)
		}
	}
}

// deja's receipt reaches the person in a 1:1 chat with the owner: the prompt
// hook asks once for the JSON reply, the model gets what --plain prints, and
// the reply on its way to Telegram carries one short receipt line as a context
// block. A group chat, the TUI ("webchat") and receipt:false stay --plain and
// leave the reply alone, and the session-start digest is never asked for its
// notes on a channel.
func TestOpenClawPluginShowsTheReceiptInAnOwnerDM(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin openclaw would run")
	}
	home := t.TempDir()
	stub := filepath.Join(home, "deja")
	calls := filepath.Join(home, "calls")
	reply := `{"systemMessage":"deja-vu — you have been here: \"zorbit drain\" (Oct 7 · via: zorbit, drain)","hookSpecificOutput":{"additionalContext":"RECALLED drain with --grace 40"}}`
	script := "#!/bin/sh\ncat >/dev/null\nprintf '%s\\n' \"$*\" >> " + calls + "\n" +
		"case \"$*\" in\n" +
		"  'hook-prompt --plain') printf 'RECALLED drain with --grace 40' ;;\n" +
		"  hook-prompt) printf '%s\\n' '" + reply + "' ;;\n" +
		"esac\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(home, "index.mjs")
	if err := os.WriteFile(plugin, []byte(openclawPluginJS(stub)), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := `
const cases = {
  dm: { channel: "telegram", isGroup: false },
  group: { channel: "telegram", isGroup: true },
  tui: { channel: "webchat", isGroup: false },
  off: { channel: "telegram", isGroup: false, pluginConfig: { receipt: false } },
};
const out = {};
for (const [name, c] of Object.entries(cases)) {
  const { default: plugin } = await import("` + plugin + `?" + name);
  const hooks = {};
  plugin.register({
    on: (n, fn) => { hooks[n] = fn },
    pluginConfig: c.pluginConfig || {},
    config: { channels: { telegram: { allowFrom: ["4242"] } } },
  });
  const key = "agent:main:" + name;
  await hooks.before_dispatch?.({ sessionKey: key, channel: c.channel, senderId: "4242", isGroup: c.isGroup }, {});
  const built = await hooks.before_prompt_build({ prompt: "what did we decide about the zorbit drain", messages: [] },
    { sessionKey: key, sessionId: "s-" + name, channel: c.channel, senderId: "4242", trigger: "user" });
  const sent = hooks.reply_payload_sending
    ? await hooks.reply_payload_sending({ kind: "final", channel: c.channel, sessionKey: key, payload: { text: "Use --grace 40." } }, {})
    : null;
  out[name] = { context: built?.prependContext ?? null, sent: sent ?? null };
}
console.log(JSON.stringify(out));
`
	run := filepath.Join(home, "drive.mjs")
	if err := os.WriteFile(run, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command(node, run).CombinedOutput()
	if err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, raw)
	}
	var got map[string]struct {
		Context string `json:"context"`
		Sent    *struct {
			Payload struct {
				Text         string `json:"text"`
				Presentation struct {
					Blocks []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"blocks"`
				} `json:"presentation"`
			} `json:"payload"`
		} `json:"sent"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("driver said %s", raw)
	}
	for name, c := range got {
		if c.Context != "RECALLED drain with --grace 40" {
			t.Errorf("%s: the model was not given what --plain prints: %q", name, c.Context)
		}
	}
	dm := got["dm"]
	if dm.Sent == nil {
		t.Fatalf("the reply in the owner's DM went out without the receipt:\n%s", raw)
	}
	blocks := dm.Sent.Payload.Presentation.Blocks
	want := `_↺ you've been here: "zorbit drain" · Oct 7 · deja_`
	if dm.Sent.Payload.Text != "Use --grace 40." || len(blocks) != 1 || blocks[0].Type != "context" || blocks[0].Text != want {
		t.Errorf("want one context block %q under the reply:\n%s", want, raw)
	}
	for _, name := range []string{"group", "tui", "off"} {
		if got[name].Sent != nil {
			t.Errorf("%s: the reply was changed:\n%s", name, raw)
		}
	}
	body, _ := os.ReadFile(calls)
	if n := strings.Count(string(body), "hook-prompt\n"); n != 1 {
		t.Errorf("want one JSON hook-prompt spawn (the DM), got %d:\n%s", n, body)
	}
	if n := strings.Count(string(body), "hook-prompt --plain\n"); n != 3 {
		t.Errorf("want the group, TUI and receipt:false turns on --plain, got %d:\n%s", n, body)
	}
}
