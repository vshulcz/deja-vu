package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111  deja-vu_1.2.3_windows_amd64.zip
bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222  deja-vu_1.2.3_windows_arm64.zip
cccc3333cccc3333cccc3333cccc3333cccc3333cccc3333cccc3333cccc3333  deja-vu_1.2.3_linux_amd64.tar.gz
`

// A manifest with one stale hash installs a mismatched binary for half the
// users and passes every other check we have, so a missing checksum has to stop
// the run rather than leave the old value in place.
func TestParseRefusesAnIncompleteChecksumFile(t *testing.T) {
	p, err := parse("1.2.3", sample)
	if err != nil {
		t.Fatal(err)
	}
	if p.amd64 != "aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111" {
		t.Fatalf("amd64 = %q", p.amd64)
	}
	if p.arm64 != "bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222" {
		t.Fatalf("arm64 = %q", p.arm64)
	}

	onlyOne := strings.SplitN(sample, "\n", 2)[0]
	if _, err := parse("1.2.3", onlyOne); err == nil {
		t.Fatal("a checksums file missing arm64 was accepted")
	}
	// A file for a different version must not half-match: every name carries
	// the version, so nothing should be picked up at all.
	if _, err := parse("9.9.9", sample); err == nil {
		t.Fatal("checksums for another version were accepted")
	}
}

// The two hashes are written in file order. If that mapping ever slipped, the
// arm64 entry would carry the amd64 hash and every arm64 install would fail its
// integrity check with no clue why.
func TestEditKeepsHashesWithTheirArchitectures(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "installer.yaml")
	original := `PackageIdentifier: vshulcz.deja-vu
PackageVersion: 0.0.1
Installers:
- Architecture: x64
  InstallerUrl: https://github.com/vshulcz/deja-vu/releases/download/v0.0.1/deja-vu_0.0.1_windows_amd64.zip
  InstallerSha256: 1111111111111111111111111111111111111111111111111111111111111111
- Architecture: arm64
  InstallerUrl: https://github.com/vshulcz/deja-vu/releases/download/v0.0.1/deja-vu_0.0.1_windows_arm64.zip
  InstallerSha256: 2222222222222222222222222222222222222222222222222222222222222222
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := parse("1.2.3", sample)
	if err != nil {
		t.Fatal(err)
	}
	out, err := edit(path, p)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)

	x64 := strings.Index(got, "Architecture: x64")
	arm := strings.Index(got, "Architecture: arm64")
	amdHash := strings.Index(got, strings.ToUpper(p.amd64))
	armHash := strings.Index(got, strings.ToUpper(p.arm64))
	if amdHash < x64 || amdHash > arm {
		t.Fatalf("amd64 hash did not land in the x64 block:\n%s", got)
	}
	if armHash < arm {
		t.Fatalf("arm64 hash did not land in the arm64 block:\n%s", got)
	}
	// Version and every URL move together; a leftover 0.0.1 anywhere means a
	// user downloads one release and validates against another.
	if strings.Contains(got, "0.0.1") {
		t.Fatalf("stale version left behind:\n%s", got)
	}
	if !strings.Contains(got, "PackageVersion: 1.2.3") {
		t.Fatalf("version not updated:\n%s", got)
	}
}

// A reordered file would silently pair the wrong hash with an architecture, so
// an unexpected count is an error rather than a best effort.
func TestEditRefusesAnUnexpectedNumberOfHashes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "installer.yaml")
	if err := os.WriteFile(path, []byte(`PackageVersion: 0.0.1
Installers:
- Architecture: x64
  InstallerSha256: 1111111111111111111111111111111111111111111111111111111111111111
- Architecture: arm64
  InstallerSha256: 2222222222222222222222222222222222222222222222222222222222222222
- Architecture: x86
  InstallerSha256: 3333333333333333333333333333333333333333333333333333333333333333
`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, _ := parse("1.2.3", sample)
	if _, err := edit(path, p); err == nil {
		t.Fatal("three hashes against two architectures were rewritten anyway")
	}
}

// The generator is what -check compares against, so it has to be deterministic:
// otherwise CI fails on a correctly pinned repository.
func TestRenderScoopIsStableAndComplete(t *testing.T) {
	p, _ := parse("1.2.3", sample)
	first, err := renderScoop(p)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := renderScoop(p)
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(first) {
			t.Fatal("renderScoop output varies between calls")
		}
	}
	var m map[string]any
	if err := json.Unmarshal(first, &m); err != nil {
		t.Fatalf("not json: %v", err)
	}
	if m["version"] != "1.2.3" || m["bin"] != "deja.exe" || m["checkver"] != "github" {
		t.Fatalf("manifest lost a field: %v", m)
	}
	arch := m["architecture"].(map[string]any)
	if arch["64bit"].(map[string]any)["hash"] != p.amd64 {
		t.Fatal("64bit hash is not the amd64 one")
	}
	if arch["arm64"].(map[string]any)["hash"] != p.arm64 {
		t.Fatal("arm64 hash is not the arm64 one")
	}
}
