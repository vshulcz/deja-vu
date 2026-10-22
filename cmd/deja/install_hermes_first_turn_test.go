package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The plugin's first turn ran hook-context with nothing on stdin, so the
// session was never stamped live and the deja tool answered that turn with
// the session asking it (#4246).
func TestHermesFirstTurnNamesTheSessionToHookContext(t *testing.T) {
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
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s|' \"$*\" >> "+log+"\ncat >> "+log+"\necho >> "+log+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	hookDir := filepath.Join(root, "plugins", "deja")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hookDir, "__init__.py"), []byte(hermesPluginPy(fake)), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "import importlib.util\n" +
		"spec = importlib.util.spec_from_file_location('dejahook', 'plugins/deja/__init__.py')\n" +
		"m = importlib.util.module_from_spec(spec)\n" +
		"spec.loader.exec_module(m)\n" +
		"m.recall(session_id='20261001_182154_69936a', user_message='look up the retry loop', is_first_turn=True)\n"
	cmd := exec.Command(py, "-c", script)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("plugin: %v\n%s", err, out)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		if strings.HasPrefix(line, "hook-context") {
			if !strings.Contains(line, `"session_id": "20261001_182154_69936a"`) {
				t.Errorf("hook-context was not told which session is starting: %q", line)
			}
			return
		}
	}
	t.Fatalf("the first turn never ran hook-context:\n%s", b)
}
