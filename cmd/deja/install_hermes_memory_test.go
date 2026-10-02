package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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
	// The Python construct, not the bare word: the launcher path the plugin
	// now names carries the test's own directory, and this test's name has
	// "MemoryProvider" in it — so the loose check failed on its own name
	// (#3682).
	for _, forbidden := range []string{"(MemoryProvider)", "import MemoryProvider", "register_memory_provider("} {
		if strings.Contains(string(hook), forbidden) {
			t.Fatalf("hook plugin carries %q, which turns it into an exclusive plugin the general loader skips", forbidden)
		}
	}
	// With the provider active, the hook stays silent: the provider injects
	// the same recall before each turn.
	for _, want := range []string{`"memory", "provider") != "deja-memory"`, `"deja-memory", "__init__.py"`} {
		if !strings.Contains(string(hook), want) {
			t.Fatalf("hook plugin does not step aside for a provider that exists (%q):\n%s", want, hook)
		}
	}
	if _, err := installHermesAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("uninstall left the provider directory: %v", err)
	}
}

// A MEMORY.md entry is often a bullet ("- user prefers X"). Passed bare, deja
// read it as a flag and the note was lost without a word.
func TestHermesMemoryWritePassesDashTextAfterSeparator(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub deja is a shebang script")
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	dir := t.TempDir()
	argvFile := filepath.Join(dir, "argv.json")
	stub := filepath.Join(dir, "deja")
	stubSrc := "#!" + py + "\nimport json, sys\njson.dump(sys.argv[1:], open(" + strconv.Quote(argvFile) + ", 'w'))\n"
	if err := os.WriteFile(stub, []byte(stubSrc), 0o755); err != nil {
		t.Fatal(err)
	}
	// Just enough of Hermes for the provider to import.
	if err := os.MkdirAll(filepath.Join(dir, "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent", "__init__.py"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent", "memory_provider.py"), []byte("class MemoryProvider:\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "provider.py"), []byte(hermesMemoryPy(stub)), 0o644); err != nil {
		t.Fatal(err)
	}
	run := "import provider\nprovider.DejaMemoryProvider().on_memory_write('add', 'user', '- prefers tabs')\n"
	cmd := exec.Command(py, "-c", run)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("provider: %v\n%s", err, out)
	}
	got, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("stub deja was not called: %v", err)
	}
	want := `["remember", "--tag", "hermes-user", "--", "- prefers tabs"]`
	if string(got) != want {
		t.Fatalf("argv = %s, want %s", got, want)
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
