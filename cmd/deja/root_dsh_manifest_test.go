package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The repository root carries a package.json that re-exports extensions/dsh.
// It exists for one reader: the DeepSeek Harness catalogs, whose admission
// bots take "repository = plugin" and look for `dsh.bundle` in the root
// manifest — a monorepo with the plugin in a subdirectory was filed with no
// npm name by one bot, marked "unavailable" by another and refused by a third
// for that alone. The root manifest has to stay a faithful pointer at the real
// one, or the catalogs install something the npm package is not.
func TestRootManifestMirrorsTheDshPlugin(t *testing.T) {
	root := filepath.Join("..", "..")
	read := func(p string) map[string]any {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		return m
	}
	top := read("package.json")
	sub := read("extensions/dsh/package.json")

	for _, key := range []string{"name", "version", "license", "engines", "dependencies"} {
		a, _ := json.Marshal(top[key])
		b, _ := json.Marshal(sub[key])
		if string(a) != string(b) {
			t.Errorf("root package.json %s = %s, extensions/dsh has %s", key, a, b)
		}
	}
	if top["private"] != true {
		t.Error("the root manifest must be private: the package on npm is extensions/dsh, not the repository")
	}

	// What the admission bots resolve: dsh.bundle is a non-empty object, and
	// every ./path it names is a file in the tree.
	bundle, _ := top["dsh"].(map[string]any)["bundle"].(map[string]any)
	if len(bundle) == 0 {
		t.Fatal("root package.json declares no dsh.bundle")
	}
	for key, v := range bundle {
		p, _ := v.(string)
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil {
			t.Errorf("dsh.bundle.%s names %q, which is not in the repository: %v", key, p, err)
		}
	}
	for _, key := range []string{"main"} {
		p, _ := top[key].(string)
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil {
			t.Errorf("root package.json %s names %q, which is not in the repository: %v", key, p, err)
		}
	}
}
