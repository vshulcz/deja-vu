package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The harness plugins install the deja binary package when there is no deja on
// PATH, and the pin is a caret on a 0.x version — which npm reads as "this
// minor and no further". opencode-deja asked for ^0.18.0 through two releases,
// so the one user who needed the fallback got a binary from two releases back
// and nothing said so (#2993).
func TestNpmDepIsPinnedToTheRelease(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.json")
	before := `{
  "name": "opencode-deja",
  "version": "0.18.0",
  "keywords": ["memory"],
  "dependencies": {
    "@opencode-ai/plugin": ">=1.0.0",
    "@vshulcz/deja-vu": "^0.18.0"
  }
}
`
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := renderNpmDejaDep(path)(pins{version: "1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, `"@vshulcz/deja-vu": "^1.2.3"`) {
		t.Fatalf("the dependency was not pinned to the release:\n%s", got)
	}
	// Only that line: these manifests carry keywords, engines and a description
	// no release derives, and the package's own version is set at publish time
	// from its own line.
	if !strings.Contains(got, `"@opencode-ai/plugin": ">=1.0.0"`) || !strings.Contains(got, `"version": "0.18.0"`) {
		t.Fatalf("something other than the deja dependency moved:\n%s", got)
	}
}

// A package that stopped depending on deja is a change worth stopping for: the
// silent alternative is a pin that quietly covers nothing.
func TestNpmDepRefusesAManifestWithoutIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.json")
	if err := os.WriteFile(path, []byte(`{"name":"x","dependencies":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := renderNpmDejaDep(path)(pins{version: "1.2.3"}); err == nil {
		t.Fatal("a manifest with no deja dependency was accepted")
	}
}

// The writer and the check read one list. They used to carry a copy each, so a
// target added to one and not the other was either pinned and never verified or
// verified and never pinned.
func TestEveryHarnessPackageIsATarget(t *testing.T) {
	got := targets()
	// Named here rather than read from the list under test: a list that lost an
	// entry would otherwise check nothing and pass.
	for _, name := range []string{"dsh", "opencode", "openclaw", "pi"} {
		path := filepath.Join("extensions", name, "package.json")
		if _, ok := got[path]; !ok {
			t.Fatalf("%s is not pinned", path)
		}
		if _, err := os.Stat(filepath.Join("..", "..", path)); err != nil {
			t.Fatalf("%s is pinned but not in the repository: %v", path, err)
		}
	}
	// And the manifests that were already covered stay covered.
	for _, path := range []string{scoopPath, installer, codexPlugin, kimiConst, agentPlugin} {
		if _, ok := got[path]; !ok {
			t.Fatalf("%s dropped out of the pinned set", path)
		}
	}
}
