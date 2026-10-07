package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// openclawSession writes an OpenClaw 2026.7.1-2 session the way its local agent
// does: a pi-shaped JSONL under agents/<agent>/sessions/<id>.jsonl whose header
// records the agent's workspace, an exec call that failed, and the compaction
// entry a run appends once it has summarised the turns before it.
func openclawSession(t *testing.T, id, workspace string) string {
	t.Helper()
	cwd, _ := json.Marshal(workspace)
	path := filepath.Join(sources.OpenClawRoot(), "main", "sessions", id+".jsonl")
	compactionWrite(t, path, strings.Join([]string{
		`{"type":"session","version":3,"id":"` + id + `","timestamp":"2026-10-07T10:20:45.000Z","cwd":` + string(cwd) + `}`,
		`{"type":"model_change","id":"31fcff3c","parentId":null,"timestamp":"2026-10-07T10:20:45.010Z","provider":"stub","modelId":"stub-model"}`,
		`{"type":"message","id":"17cbcb01","parentId":"31fcff3c","timestamp":"2026-10-07T10:20:46.612Z","message":{"role":"user","content":"fix the parser test","timestamp":1791368445245}}`,
		`{"type":"message","id":"2b64c72e","parentId":"17cbcb01","timestamp":"2026-10-07T10:20:46.656Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"call_1","name":"exec","arguments":{"command":"go test ./parser/..."}}],"api":"openai-completions","provider":"stub","model":"stub-model","stopReason":"toolUse","timestamp":1791368446616}}`,
		`{"type":"message","id":"0596fb6b","parentId":"2b64c72e","timestamp":"2026-10-07T10:20:46.841Z","message":{"role":"toolResult","toolCallId":"call_1","toolName":"exec","content":[{"type":"text","text":"--- FAIL: TestParseSeed\nwant 3, got 4\nFAIL\n\n(Command exited with code 1)"}],"details":{"status":"completed","exitCode":1,"cwd":` + string(cwd) + `},"isError":true,"timestamp":1791368446840}}`,
		`{"type":"message","id":"28036307","parentId":"0596fb6b","timestamp":"2026-10-07T10:20:46.868Z","message":{"role":"assistant","content":[{"type":"text","text":"The parser test fails: want 3, got 4. I decided to fix parse.go next."}],"stopReason":"stop","timestamp":1791368446849}}`,
		`{"type":"compaction","id":"891d328a","parentId":"28036307","timestamp":"2026-10-07T10:20:46.908Z","summary":"done","firstKeptEntryId":"28036307","tokensBefore":31001,"details":{"readFiles":[],"modifiedFiles":[]},"fromHook":false}`,
	}, "\n")+"\n")
	return path
}

// OpenClaw compacts two ways, measured on 2026.7.1-2. `openclaw sessions
// compact` runs through the gateway, whose before_compaction names the session
// id and the workspace but no file; a run that fills its window compacts on its
// own and names the file and the session key only. Either way the session file
// still holds the turns, and the next turn gets the packet.
func TestOpenClawCompactionPacketReachesTheNextTurn(t *testing.T) {
	for name, file := range map[string]bool{"gateway, by id": false, "in the run, by file": true} {
		t.Run(name, func(t *testing.T) {
			hermeticEnv(t)
			hostsNoWarmup(t)
			t.Setenv("OPENCLAW_STATE_DIR", filepath.Join(t.TempDir(), "openclaw"))
			workspace := compactionGitRepo(t)
			dir := index.DefaultDir()
			path := openclawSession(t, "sc1", workspace)
			payload := map[string]any{"session_id": "sc1", "transcript_path": "", "cwd": workspace, "harness": "openclaw"}
			if file {
				payload["transcript_path"] = path
			}
			out := precompactThen(t, dir, workspace, payload, "")
			wantCompactionPacket(t, out)
			state, _, _ := index.Compaction(dir, "sc1", workspace)
			if state.Harness != "openclaw" {
				t.Errorf("the capture is filed under %q, want openclaw", state.Harness)
			}
		})
	}
}

// The plugin names the agent's workspace, which is what the session header
// records, rather than the gateway's own directory; and the compaction a run
// starts on its own, whose ctx holds only the session key, goes out under the
// session id and workspace that key last ran with.
func TestOpenClawPluginNamesTheSessionItCompacts(t *testing.T) {
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
	script := "#!/bin/sh\nin=$(cat)\nprintf '%s\\t%s\\n' \"$1\" \"$in\" >> " + calls + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(home, "index.mjs")
	if err := os.WriteFile(plugin, []byte(openclawPluginJS(stub)), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := `
import plugin from "` + plugin + `";
const h = {};
plugin.register({ on: (name, fn) => { h[name] = fn } });
const key = "agent:main:explicit:sa1";
await h.before_prompt_build({ prompt: "fix the parser test", messages: [] }, { sessionKey: key, sessionId: "sa1", workspaceDir: "/w/agent-workspace" });
await h.before_compaction({ messageCount: 4, messages: [], sessionFile: "/s/sa1.jsonl" }, { sessionKey: key });
`
	run := filepath.Join(home, "drive.mjs")
	if err := os.WriteFile(run, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, run).CombinedOutput(); err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, out)
	}
	b, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]map[string]any{}
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		sub, body, _ := strings.Cut(l, "\t")
		var m map[string]any
		if err := json.Unmarshal([]byte(body), &m); err != nil {
			t.Fatalf("payload %q: %v", l, err)
		}
		got[sub] = m
	}
	if cwd := got["hook-prompt"]["cwd"]; cwd != "/w/agent-workspace" {
		t.Errorf("hook-prompt was sent cwd %v, not the agent's workspace", cwd)
	}
	pre := got["hook-precompact"]
	if pre["session_id"] != "sa1" || pre["cwd"] != "/w/agent-workspace" || pre["transcript_path"] != "/s/sa1.jsonl" || pre["harness"] != "openclaw" {
		t.Errorf("the in-run compaction went out as %v", pre)
	}
}
