package main

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func templateJSON(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "packaging", "mcpb", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A bundle carries one binary. If its manifest claimed every platform, the host
// would install it happily on Linux and then fail to start a Mach-O binary.
func TestRenderDeclaresOnlyTheePlatformItShips(t *testing.T) {
	tmpl := templateJSON(t)
	for _, tc := range targets {
		out, err := render(tmpl, tc, "1.2.3")
		if err != nil {
			t.Fatalf("%s: %v", tc.suffix, err)
		}
		var m map[string]any
		if err := json.Unmarshal(out, &m); err != nil {
			t.Fatalf("%s: rendered manifest is not json: %v", tc.suffix, err)
		}
		if m["version"] != "1.2.3" {
			t.Fatalf("%s: version = %v", tc.suffix, m["version"])
		}
		platforms := m["compatibility"].(map[string]any)["platforms"].([]any)
		if len(platforms) != 1 || platforms[0] != tc.platform {
			t.Fatalf("%s: platforms = %v, want exactly [%s]", tc.suffix, platforms, tc.platform)
		}
		server := m["server"].(map[string]any)
		wantEntry := "server/" + tc.exe
		if server["entry_point"] != wantEntry {
			t.Fatalf("%s: entry_point = %v, want %s", tc.suffix, server["entry_point"], wantEntry)
		}
		cfg := server["mcp_config"].(map[string]any)
		if cfg["command"] != "${__dirname}/"+wantEntry {
			t.Fatalf("%s: command = %v", tc.suffix, cfg["command"])
		}
		// Windows entries must carry the extension: the format only appends
		// .exe automatically for some hosts, and a missing binary is silent.
		if tc.platform == "win32" && filepath.Ext(wantEntry) != ".exe" {
			t.Fatalf("%s: windows entry point has no .exe", tc.suffix)
		}
	}
}

// The whole reason for per-target bundles is that amd64 and arm64 are not
// interchangeable. A substring match that confused them would produce a bundle
// that installs and then crashes on launch.
func TestMatchesDoesNotConfuseArchitectures(t *testing.T) {
	amd := target{os: "linux", arch: "amd64"}
	arm := target{os: "linux", arch: "arm64"}
	for dir, want := range map[string]bool{
		"deja_linux_amd64": true,
		"deja_linux_arm64": false,
		"deja_darwin_all":  false,
	} {
		if got := matches(dir, amd); got != want {
			t.Fatalf("matches(%q, linux/amd64) = %v, want %v", dir, got, want)
		}
	}
	if !matches("deja_linux_arm64", arm) {
		t.Fatal("linux/arm64 did not match its own directory")
	}
	if matches("deja_windows_amd64", amd) {
		t.Fatal("a windows directory matched a linux target")
	}

	universal := target{os: "darwin", arch: "universal"}
	for dir, want := range map[string]bool{
		"deja_darwin_all":       true,
		"deja_darwin_universal": true,
		"deja_darwin_arm64":     false,
	} {
		if got := matches(dir, universal); got != want {
			t.Fatalf("matches(%q, darwin/universal) = %v, want %v", dir, got, want)
		}
	}
}

// Two candidate binaries mean the layout changed under us. Picking one at
// random would ship the wrong architecture to everybody who downloads it.
func TestFindBinaryRefusesToGuess(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"deja_linux_amd64", "other_linux_amd64"} {
		p := filepath.Join(root, dir)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "deja"), []byte("binary"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := findBinary(root, targets[1]); err == nil {
		t.Fatal("two candidates were resolved silently")
	}
	if _, err := findBinary(t.TempDir(), targets[1]); err == nil {
		t.Fatal("a missing binary was not reported")
	}
}

// The executable bit has to survive the zip. Without it the host installs a
// server it cannot start, and the failure appears as a connection error with
// nothing pointing at permissions.
func TestPackKeepsTheBinaryExecutable(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "deja")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "bundle.mcpb")
	if err := pack(out, templateJSON(t), binary, targets[1], "9.9.9"); err != nil {
		t.Fatal(err)
	}

	z, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = z.Close() }()
	seen := map[string]os.FileMode{}
	for _, f := range z.File {
		seen[f.Name] = f.Mode()
	}
	// manifest.json must sit at the root, or the host will not recognise the
	// file as a bundle at all.
	if _, ok := seen["manifest.json"]; !ok {
		t.Fatalf("no manifest.json at the bundle root: %v", seen)
	}
	mode, ok := seen["server/deja"]
	if !ok {
		t.Fatalf("binary missing from the bundle: %v", seen)
	}
	if mode&0o111 == 0 {
		t.Fatalf("binary mode = %v, not executable", mode)
	}
}

// The registry bundle is the one artifact MCP registries accept per server, so
// it must work on every platform and must not declare tools — Smithery rejects
// any bundle that does (smithery-ai/cli#787).
func TestRegistryBundleIsCrossPlatformAndToolless(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "registry.mcpb")
	if err := packRegistry(out, templateJSON(t), "1.2.3"); err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = z.Close() }()

	var manifest map[string]any
	found := false
	for _, f := range z.File {
		if f.Name != "manifest.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		if err := json.NewDecoder(rc).Decode(&manifest); err != nil {
			t.Fatal(err)
		}
		_ = rc.Close()
		found = true
	}
	if !found {
		t.Fatal("no manifest.json in the registry bundle")
	}
	if _, ok := manifest["tools"]; ok {
		t.Fatal("registry bundle declares tools; Smithery rejects those outright")
	}
	platforms := manifest["compatibility"].(map[string]any)["platforms"].([]any)
	if len(platforms) != 3 {
		t.Fatalf("platforms = %v, want all three", platforms)
	}
	cfg := manifest["server"].(map[string]any)["mcp_config"].(map[string]any)
	if cfg["command"] != "node" {
		t.Fatalf("command = %v, want node running the bundled entry point", cfg["command"])
	}
}

// A registry introspects a bundle by launching it, so the entry point has to be
// a working server rather than a note explaining where the real one lives. It
// also has to pin the version: an unpinned launcher drifts onto whatever npm
// publishes next, which is not what anyone reviewed.
func TestRegistryEntryPointLaunchesTheServer(t *testing.T) {
	js := entryJS("1.2.3")
	for _, want := range []string{"spawn", "@vshulcz/deja-vu@1.2.3", "\"mcp\"", "stdio"} {
		if !strings.Contains(js, want) {
			t.Fatalf("entry point is missing %q:\n%s", want, js)
		}
	}
	// stdio must pass straight through; anything that buffers or rewrites it
	// corrupts the JSON-RPC stream.
	if !strings.Contains(js, `stdio: "inherit"`) {
		t.Fatalf("entry point does not inherit stdio:\n%s", js)
	}
	if !strings.Contains(js, "npx.cmd") {
		t.Fatalf("entry point will not start on windows:\n%s", js)
	}
}
