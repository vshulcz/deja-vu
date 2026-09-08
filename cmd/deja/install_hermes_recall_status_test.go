package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Hermes 0.17 has MemoryProvider and no RecallStatus, so the provider deja
// writes failed to import there: Hermes reported it unavailable while the hook
// had already stood down for it, and the turn got memory from neither (#3202).
func TestTheProviderImportsOnAHermesWithoutRecallStatus(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The 0.17 interface: the base class, nothing else.
	stub := "from abc import ABC\n\n\nclass MemoryProvider(ABC):\n    pass\n"
	if err := os.WriteFile(filepath.Join(agentDir, "memory_provider.py"), []byte(stub), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "__init__.py"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	provider := filepath.Join(root, "deja_memory.py")
	if err := os.WriteFile(provider, []byte(hermesMemoryPy(`"/bin/deja"`)), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(py, "-c", "import deja_memory; p = deja_memory.DejaMemoryProvider(); print(p.recall_status())")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the provider does not import on a Hermes without RecallStatus: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "None" {
		t.Errorf("recall_status() = %q, want None with nothing recalled yet", got)
	}
}

// A provider directory that exists but does not load is not a provider. The
// hook has to keep answering, or the turn has no memory at all.
func TestTheHookDoesNotStandDownForAProviderThatCannotLoad(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	root := t.TempDir()
	plugins := filepath.Join(root, "plugins")
	hookDir := filepath.Join(plugins, "deja")
	broken := filepath.Join(plugins, "deja-memory")
	for _, d := range []string{hookDir, broken} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(hookDir, "__init__.py"), []byte(hermesPluginPy(`"/bin/deja"`)), 0o644); err != nil {
		t.Fatal(err)
	}
	// What an older Hermes gives the provider: a module that raises on import.
	if err := os.WriteFile(filepath.Join(broken, "__init__.py"),
		[]byte("from agent.memory_provider import MemoryProvider, RecallStatus\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The config says deja-memory is the provider.
	cfg := filepath.Join(root, "hermes_cli")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "__init__.py"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "config.py"),
		[]byte("def load_config():\n    return {}\n\n\ndef cfg_get(cfg, *keys):\n    return \"deja-memory\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	script := "import importlib.util, sys\n" +
		"spec = importlib.util.spec_from_file_location('dejahook', 'plugins/deja/__init__.py')\n" +
		"m = importlib.util.module_from_spec(spec)\n" +
		"spec.loader.exec_module(m)\n" +
		"print(m._provider_active())\n"
	cmd := exec.Command(py, "-c", script)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hook plugin does not run: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "False" {
		t.Errorf("_provider_active() = %q, want False for a provider that cannot load", got)
	}
}
