package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Hermes runs cron jobs with memory on and tells the plugin and the provider
// the platform is "cron". The same prompt every fire ran hook-context and
// hook-prompt each time and put their answer in front of the job; a person's
// session on the same machine still recalls.
func TestHermesCronRunRecallsNothingOnItsOwn(t *testing.T) {
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
	if werr := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" >> "+log+"\ncat >/dev/null\necho recalled\n"), 0o755); werr != nil {
		t.Fatal(werr)
	}
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if merr := os.MkdirAll(filepath.Dir(p), 0o755); merr != nil {
			t.Fatal(merr)
		}
		if werr := os.WriteFile(p, []byte(body), 0o644); werr != nil {
			t.Fatal(werr)
		}
	}
	write("plugins/deja/__init__.py", hermesPluginPy(fake))
	write("agent/__init__.py", "")
	write("agent/memory_provider.py", "class MemoryProvider:\n    pass\n")
	home := filepath.Join(root, "hermes")
	t.Setenv("HERMES_HOME", home)
	hermeticEnv(t)
	t.Setenv("HERMES_HOME", home)
	if _, ierr := installHermesMemoryProvider(fake, false); ierr != nil {
		t.Fatal(ierr)
	}
	b, err := os.ReadFile(filepath.Join(home, "plugins", "deja-memory", "__init__.py"))
	if err != nil {
		t.Fatal(err)
	}
	write("plugins/deja_memory/__init__.py", string(b))

	run := func(platform string) (string, []string) {
		t.Helper()
		_ = os.Remove(log)
		script := "import importlib.util, sys\n" +
			"sys.path.insert(0, '.')\n" +
			"def load(name, path):\n" +
			"    spec = importlib.util.spec_from_file_location(name, path)\n" +
			"    m = importlib.util.module_from_spec(spec)\n" +
			"    spec.loader.exec_module(m)\n" +
			"    return m\n" +
			"hook = load('dejahook', 'plugins/deja/__init__.py')\n" +
			"hook._provider_active = lambda: False\n" +
			"out = []\n" +
			"for first in (True, False):\n" +
			"    r = hook.recall(session_id='s1', user_message='look up the retry loop', is_first_turn=first, platform='" + platform + "')\n" +
			"    out.append('hook:' + (r or {}).get('context', '').strip())\n" +
			"prov = load('dejamem', 'plugins/deja_memory/__init__.py')\n" +
			"p = [v for v in vars(prov).values() if isinstance(v, type) and v.__name__ == 'DejaMemoryProvider'][0]()\n" +
			"p.initialize('s2', platform='" + platform + "')\n" +
			"out.append('provider:' + p.prefetch('look up the retry loop').strip())\n" +
			"print('|'.join(out))\n"
		cmd := exec.CommandContext(t.Context(), py, "-c", script)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", platform, err, out)
		}
		calls, _ := os.ReadFile(log)
		return strings.TrimSpace(string(out)), strings.Fields(string(calls))
	}

	got, calls := run("cron")
	if got != "hook:|hook:|provider:" || len(calls) != 0 {
		t.Errorf("a cron run recalled: %q, deja ran %v", got, calls)
	}
	// The control: the same calls from a person's session do recall, so the
	// silence above is the platform's doing and not a broken stand.
	got, calls = run("cli")
	if !strings.Contains(got, "hook:recalled") || !strings.Contains(got, "provider:recalled") || len(calls) == 0 {
		t.Errorf("a cli session did not recall: %q, deja ran %v", got, calls)
	}
}
