package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Hermes gives a general plugin no compaction hook, and the memory-provider
// route takes over the reader's memory.provider. The plugin hermes-auto writes
// notices the summary appear in the history pre_api_request and pre_llm_call
// see, and hands deja the turns from the call before it.
func TestHermesPluginCapturesACompactionWithoutTheProvider(t *testing.T) {
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
	script := "#!/bin/sh\nprintf '%s|' \"$*\" >> " + log + "\ncat >> " + log + "\necho >> " + log + "\nexit 0\n"
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
	probe := "import importlib.util\n" +
		"spec = importlib.util.spec_from_file_location('dejahook', 'plugins/deja/__init__.py')\n" +
		"m = importlib.util.module_from_spec(spec)\n" +
		"spec.loader.exec_module(m)\n" +
		"hooks = []\n" +
		"class Ctx:\n" +
		"    def register_hook(self, name, fn): hooks.append(name)\n" +
		"    def register_command(self, *a, **k): pass\n" +
		"m.register(Ctx())\n" +
		"assert 'pre_api_request' in hooks and 'post_llm_call' in hooks, hooks\n" +
		"full = [{'role': 'user', 'content': 'fix the flaky retry in queue.go'}, {'role': 'assistant', 'content': 'raised the backoff cap'}]\n" +
		"m.watch(session_id='s1', conversation_history=full)\n" +
		"m.watch(session_id='s1', conversation_history=full + [{'role': 'user', 'content': 'go on'}])\n" +
		"cut = [{'role': 'user', 'content': '[CONTEXT COMPACTION — REFERENCE ONLY] Earlier turns were compacted', '_compressed_summary': True}]\n" +
		"m.watch(session_id='s1', conversation_history=cut)\n" +
		"m.watch(session_id='s1', conversation_history=cut + [{'role': 'assistant', 'content': 'next'}])\n"
	cmd := exec.Command(py, "-c", probe)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("plugin: %v\n%s", err, out)
	}
	b, _ := os.ReadFile(log)
	calls := strings.Count(string(b), "hook-precompact|")
	if calls != 1 {
		t.Fatalf("hook-precompact ran %d times, want once:\n%s", calls, b)
	}
	for _, want := range []string{`"session_id": "s1"`, `"harness": "hermes"`, "raised the backoff cap", "go on"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the packet payload lacks %q:\n%s", want, b)
		}
	}
	if strings.Contains(string(b), "CONTEXT COMPACTION") {
		t.Errorf("the payload carried the summary instead of the turns before it:\n%s", b)
	}
}
