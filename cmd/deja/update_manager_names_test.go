package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The upgrade line exists so the user runs it instead of --force, so a name
// that resolves to nothing sends them straight back to overwriting a managed
// binary: `npm update -g deja-vu` answered 404 while the published package is
// `@vshulcz/deja-vu` (#915). These names live in the shipped manifests, so the
// advice is checked against them rather than against a second copy of the
// string.
func TestManagerAdviceNamesThePackageTheManifestsShip(t *testing.T) {
	repo := filepath.Join("..", "..")

	scoop, err := filepath.Glob(filepath.Join(repo, "packaging", "scoop", "*.json"))
	if err != nil || len(scoop) != 1 {
		t.Fatalf("scoop manifests = %v (%v), want exactly one", scoop, err)
	}
	app := strings.TrimSuffix(filepath.Base(scoop[0]), ".json")
	if _, cmd := packageManagerOwning(`C:\Users\v\scoop\apps\deja\current\deja.exe`); cmd != "scoop update "+app {
		t.Errorf("scoop advice = %q, manifest ships %q", cmd, app)
	}

	winget, err := os.ReadFile(filepath.Join(repo, "packaging", "winget", "vshulcz.deja-vu.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	id := ""
	for _, line := range strings.Split(string(winget), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "PackageIdentifier:"); ok {
			id = strings.TrimSpace(rest)
			break
		}
	}
	if id == "" {
		t.Fatal("no PackageIdentifier in the winget manifest")
	}
	if _, cmd := packageManagerOwning(`C:\Users\v\AppData\Local\winget\packages\vshulcz.deja-vu\deja.exe`); cmd != "winget upgrade "+id {
		t.Errorf("winget advice = %q, manifest ships %q", cmd, id)
	}

	// npm's name is the one in the package it publishes, scope included.
	pkg, err := os.ReadFile(filepath.Join(repo, "npm", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	name := ""
	for _, line := range strings.Split(string(pkg), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), `"name":`); ok {
			name = strings.Trim(strings.TrimSpace(rest), `",`)
			break
		}
	}
	if name == "" {
		t.Fatal("no name in npm/package.json")
	}
	if _, cmd := packageManagerOwning("/usr/lib/node_modules/deja-vu/bin/deja"); cmd != "npm update -g "+name {
		t.Errorf("npm advice = %q, the package publishes %q", cmd, name)
	}
}
