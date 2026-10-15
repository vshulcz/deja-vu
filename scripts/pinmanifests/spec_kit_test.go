package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Spec Kit catalog entry names the version inside deja-spec-kit.zip, and
// the release packs the zip after this pin. Only the extension's own version
// moves: the deja range under requires.tools is a separate promise.
func TestSpecKitExtensionVersionIsPinned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "extension.yml")
	before := `schema_version: "1.0"

extension:
  id: "deja-vu"
  version: "0.18.0"

requires:
  speckit_version: ">=0.10.0"
  tools:
    - name: "deja"
      version: ">=0.18.0"
      required: true
`
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := renderYAMLExtensionVersion(path)(pins{version: "1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "\n  version: \"1.2.3\"\n") {
		t.Fatalf("the extension version was not pinned:\n%s", got)
	}
	if !strings.Contains(got, `version: ">=0.18.0"`) {
		t.Fatalf("the deja range under requires.tools moved:\n%s", got)
	}

	if err := os.WriteFile(path, []byte("extension:\n  id: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := renderYAMLExtensionVersion(path)(pins{version: "1.2.3"}); err == nil {
		t.Fatal("a manifest with no extension version was accepted")
	}

	if _, ok := targets()[specKitExtension]; !ok {
		t.Fatalf("%s is not in the pinned set", specKitExtension)
	}
	if _, err := os.Stat(filepath.Join("..", "..", specKitExtension)); err != nil {
		t.Fatalf("%s is pinned but not in the repository: %v", specKitExtension, err)
	}
}
