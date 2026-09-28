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
