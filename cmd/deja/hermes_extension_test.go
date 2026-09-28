package main

import (
	"os"
	"path/filepath"
	"testing"
)

// extensions/hermes is what the Hermes plugin catalog installs, and it has to
// be the provider deja install writes, or a fix lands in one and not the
// other. DEJA_WRITE_EXTENSIONS=1 rewrites it from the generator.
func TestHermesExtensionIsTheProviderInstallWrites(t *testing.T) {
	want := map[string]string{
		"plugin.yaml": hermesMemoryManifest,
		"__init__.py": hermesCatalogPy(),
	}
	dir := filepath.Join("..", "..", "extensions", "hermes")
	for name, body := range want {
		path := filepath.Join(dir, name)
		if os.Getenv("DEJA_WRITE_EXTENSIONS") == "1" {
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != body {
			t.Errorf("extensions/hermes/%s differs from what deja install writes — DEJA_WRITE_EXTENSIONS=1 go test ./cmd/deja -run TestHermesExtension", name)
		}
	}
}

// A provider `hermes plugins install` put in ~/.hermes/plugins/deja-memory is
// Hermes': its install record names it, and deja must neither rewrite its
// files nor delete the directory on uninstall.
func TestDejaLeavesAHermesInstalledProviderAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_HERMES_HOME", "")
	plugins := filepath.Join(home, ".hermes", "plugins")
	dir := filepath.Join(plugins, hermesMemoryDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	catalog := hermesCatalogPy()
	if err := os.WriteFile(filepath.Join(dir, "__init__.py"), []byte(catalog), 0o644); err != nil {
		t.Fatal(err)
	}
	record := `{"deja-memory": {"source": "https://github.com/vshulcz/deja-vu.git", "subdir": "extensions/hermes"}}`
	if err := os.WriteFile(filepath.Join(plugins, ".install-metadata.json"), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := installHermesMemoryProvider("/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "__init__.py")); string(got) != catalog {
		t.Fatalf("install rewrote the provider Hermes installed (action %q)", res.Action)
	}
	if _, err := installHermesMemoryProvider("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "__init__.py")); err != nil {
		t.Fatalf("uninstall removed the provider Hermes installed: %v", err)
	}

	// The control: without the record the directory is deja's, and uninstall
	// takes it.
	if err := os.Remove(filepath.Join(plugins, ".install-metadata.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := installHermesMemoryProvider("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("uninstall left deja's own provider: %v", err)
	}
}
