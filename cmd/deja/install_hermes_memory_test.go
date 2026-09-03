package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Hermes lists a memory provider at `hermes memory setup` when a directory
// under ~/.hermes/plugins/ has an __init__.py that mentions MemoryProvider;
// that directory must not be the hook plugin's, which the general loader
// would then skip as "exclusive" and lose the hook and /deja with it.
func TestInstallHermesWritesAMemoryProviderBesideTheHookPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_HERMES_HOME", "")
	if _, err := installHermesAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	dir := filepath.Join(home, ".hermes", "plugins", "deja-memory")
	manifest, err := os.ReadFile(filepath.Join(dir, "plugin.yaml"))
	if err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	// `hermes memory setup` runs the dependency check before activating.
	for _, want := range []string{"name: deja-memory", "check: \"deja --version\"", "brew install deja-vu"} {
		if !strings.Contains(string(manifest), want) {
			t.Fatalf("manifest missing %q:\n%s", want, manifest)
		}
	}
	code, err := os.ReadFile(filepath.Join(dir, "__init__.py"))
	if err != nil {
		t.Fatalf("provider code missing: %v", err)
	}
	src := string(code)
	for _, want := range []string{
		"class DejaMemoryProvider(MemoryProvider)", // what the discovery scan looks for
		"ctx.register_memory_provider(",
		`"deja_recall"`, `"deja_fix"`, `"deja_blame"`,
		"hook-context", "hook-prompt", // the same recall the hooks inject elsewhere
		`"/bin/deja"`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("provider missing %q:\n%s", want, src)
		}
	}
	// The hook plugin must not mention MemoryProvider anywhere, or Hermes
	// reclassifies it and stops loading its hook.
	hook, err := os.ReadFile(filepath.Join(home, ".hermes", "plugins", "deja", "__init__.py"))
	if err != nil {
		t.Fatalf("hook plugin missing: %v", err)
	}
	if strings.Contains(string(hook), "MemoryProvider") {
		t.Fatalf("hook plugin mentions MemoryProvider, which turns it into an exclusive plugin the general loader skips")
	}
	// With the provider active, the hook stays silent: the provider injects
	// the same recall before each turn.
	if !strings.Contains(string(hook), `"memory", "provider") == "deja-memory"`) {
		t.Fatalf("hook plugin does not step aside for the provider:\n%s", hook)
	}
	if _, err := installHermesAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("uninstall left the provider directory: %v", err)
	}
}

// The generated Python has to at least parse; a template slip here ships to
// every Hermes user as a provider that fails to import.
func TestHermesGeneratedPythonCompiles(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	dir := t.TempDir()
	for name, src := range map[string]string{
		"provider.py": hermesMemoryPy(`C:\Program Files\deja "x".exe`),
		"hook.py":     hermesPluginPy(`C:\Program Files\deja "x".exe`),
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(py, "-m", "py_compile", path).CombinedOutput(); err != nil {
			t.Fatalf("%s does not compile: %v\n%s", name, err, out)
		}
	}
}
