package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Hermes had no after-failure line: pre_llm_call runs before the turn, so the
// pair for a command that just failed waited for the next user message. The
// plugin now answers transform_tool_result, whose string replaces the result
// the model reads.
func TestHermesPluginAppendsTheFixLineToAFailedCommand(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in deja is a shell script")
	}
	root := t.TempDir()
	log := filepath.Join(root, "calls")
	fake := filepath.Join(root, "deja")
	script := "#!/bin/sh\nprintf '%s|' \"$*\" >> " + log + "\ncat >> " + log + "\necho >> " + log + "\n" +
		"[ \"$1\" = hook-tool-after ] && echo '<deja-recall>ran make clean after this before</deja-recall>'\nexit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "plugins", "deja")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "__init__.py"), []byte(hermesPluginPy(fake)), 0o644); err != nil {
		t.Fatal(err)
	}
	probe := "import importlib.util, json\n" +
		"spec = importlib.util.spec_from_file_location('dejahook', 'plugins/deja/__init__.py')\n" +
		"m = importlib.util.module_from_spec(spec)\n" +
		"spec.loader.exec_module(m)\n" +
		"hooks = []\n" +
		"class Ctx:\n" +
		"    def register_hook(self, name, fn): hooks.append(name)\n" +
		"    def register_command(self, *a, **k): pass\n" +
		"m.register(Ctx())\n" +
		"failed = json.dumps({'output': 'panic: sql: database is closed', 'exit_code': 2, 'error': None})\n" +
		"passed = json.dumps({'output': 'ok', 'exit_code': 0, 'error': None})\n" +
		"print(json.dumps({\n" +
		"  'hooks': hooks,\n" +
		"  'failed': m.repair(tool_name='terminal', result=failed, session_id='s1'),\n" +
		"  'passed': m.repair(tool_name='terminal', result=passed, session_id='s1'),\n" +
		"  'other': m.repair(tool_name='read_file', result=failed, session_id='s1'),\n" +
		"}))\n"
	cmd := exec.Command(py, "-c", probe)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("plugin: %v\n%s", err, out)
	}
	var got struct {
		Hooks  []string `json:"hooks"`
		Failed *string  `json:"failed"`
		Passed *string  `json:"passed"`
		Other  *string  `json:"other"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("probe output %q: %v", out, err)
	}
	if !strings.Contains(strings.Join(got.Hooks, ","), "transform_tool_result") {
		t.Fatalf("register() did not answer transform_tool_result: %v", got.Hooks)
	}
	if got.Failed == nil || !strings.HasPrefix(*got.Failed, `{"output": "panic`) ||
		!strings.HasSuffix(*got.Failed, "\n\n<deja-recall>ran make clean after this before</deja-recall>") {
		t.Fatalf("the failed result did not come back with the line after it: %v", got.Failed)
	}
	if got.Passed != nil || got.Other != nil {
		t.Fatalf("a passing command or another tool was changed: passed=%v other=%v", got.Passed, got.Other)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(calls), "hook-tool-after --plain|"); n != 1 {
		t.Fatalf("deja was asked %d times, want once, for the failed command only:\n%s", n, calls)
	}
	if !strings.Contains(string(calls), `"session_id": "s1"`) || !strings.Contains(string(calls), `"tool_name": "terminal"`) {
		t.Fatalf("hook-tool-after was not told the tool and the session:\n%s", calls)
	}
	if !strings.Contains(hermesPluginManifest, "transform_tool_result") {
		t.Fatal("the manifest does not list transform_tool_result")
	}
}

// What the plugin hands deja is Hermes's own terminal result: a JSON document
// inside a string, with the output under "output". The fix pair has to come
// out of that shape.
func TestFixPairReadsHermesTerminalResult(t *testing.T) {
	seedFixPair(t, "panic: sql: database is closed", "make clean && make CGO_ENABLED=0")
	result, _ := json.Marshal(map[string]any{
		"output":    "goroutine 1 [running]:\npanic: sql: database is closed",
		"exit_code": 2,
		"error":     nil,
	})
	payload, _ := json.Marshal(map[string]any{
		"tool_name":     "terminal",
		"tool_response": string(result),
		"session_id":    "hermes-1",
		"cwd":           "/work/app",
	})
	var out bytes.Buffer
	if err := runHookToolAfterMode(os.Getenv("DEJA_INDEX_DIR"), bytes.NewReader(payload), &out, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "make clean && make CGO_ENABLED=0") {
		t.Fatalf("no fix line for a Hermes terminal failure:\n%s", out.String())
	}
}
