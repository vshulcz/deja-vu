// Command mcpb packs deja into MCP Bundles — the .mcpb format desktop apps
// install by double-clicking.
//
// The manifest format has no architecture selector: platform_overrides
// distinguishes darwin, linux and win32 and nothing finer. A single universal
// bundle would therefore have to guess an architecture and be wrong for half of
// Linux, so this builds one bundle per platform-architecture pair instead. macOS
// is the exception: goreleaser joins the two Mach-O binaries into one universal
// file, so a Mac user still has a single bundle to choose.
//
// Usage:
//
//	go run ./scripts/mcpb -version 0.16.0 -in dist -out dist
//
// -in holds the built binaries laid out the way goreleaser leaves them. Targets
// are located by the binary inside each directory, not by the directory name,
// which goreleaser does not treat as a public contract.
package main

import (
	"archive/zip"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type target struct {
	os       string // GOOS, as it appears in goreleaser's directory names
	arch     string // GOARCH, or "universal" for the joined macOS binary
	platform string // manifest platform id: darwin, linux or win32
	suffix   string // artifact name suffix
	exe      string
}

var targets = []target{
	{os: "darwin", arch: "universal", platform: "darwin", suffix: "darwin", exe: "deja"},
	{os: "linux", arch: "amd64", platform: "linux", suffix: "linux_amd64", exe: "deja"},
	{os: "linux", arch: "arm64", platform: "linux", suffix: "linux_arm64", exe: "deja"},
	{os: "windows", arch: "amd64", platform: "win32", suffix: "windows_amd64", exe: "deja.exe"},
	{os: "windows", arch: "arm64", platform: "win32", suffix: "windows_arm64", exe: "deja.exe"},
}

func main() {
	version := flag.String("version", "", "release version, without the leading v")
	in := flag.String("in", "dist", "directory holding the built binaries")
	out := flag.String("out", "dist", "directory to write the bundles into")
	manifestPath := flag.String("manifest", filepath.Join("packaging", "mcpb", "manifest.json"), "manifest template")
	flag.Parse()

	if *version == "" {
		fail(fmt.Errorf("-version is required"))
	}
	template, err := os.ReadFile(*manifestPath)
	if err != nil {
		fail(err)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fail(err)
	}

	built := 0
	for _, t := range targets {
		binary, err := findBinary(*in, t)
		if err != nil {
			// A missing target is not fatal: a local run may have built one
			// platform. Say so, rather than packing a bundle whose manifest
			// promises a binary it does not carry.
			fmt.Fprintf(os.Stderr, "skipping %s: %v\n", t.suffix, err)
			continue
		}
		path := filepath.Join(*out, fmt.Sprintf("deja-vu_%s_%s.mcpb", *version, t.suffix))
		if err := pack(path, template, binary, t, *version); err != nil {
			fail(fmt.Errorf("%s: %w", t.suffix, err))
		}
		info, err := os.Stat(path)
		if err != nil {
			fail(err)
		}
		fmt.Printf("%s  %d KB\n", filepath.Base(path), info.Size()/1024)
		built++
	}
	if built == 0 {
		fail(fmt.Errorf("no binaries found under %s — nothing to pack", *in))
	}
}

func findBinary(root string, t target) (string, error) {
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == t.exe && matches(filepath.Dir(path), t) {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("no %s/%s binary under %s", t.os, t.arch, root)
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("%d candidates for %s/%s: %s", len(found), t.os, t.arch, strings.Join(found, ", "))
	}
}

func matches(dir string, t target) bool {
	name := strings.ToLower(filepath.Base(dir))
	if !strings.Contains(name, t.os) {
		return false
	}
	if t.arch == "universal" {
		// goreleaser names the joined build "…_darwin_all".
		return strings.Contains(name, "universal") || strings.Contains(name, "all")
	}
	// "amd64" must not match a directory built for arm64, and vice versa.
	return strings.Contains(name, t.arch)
}

// pack writes the bundle: a zip with manifest.json at the root and the binary
// under server/, which is the layout the format expects.
func pack(path string, template []byte, binary string, t target, version string) error {
	manifest, err := render(template, t, version)
	if err != nil {
		return err
	}
	body, err := os.ReadFile(binary)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	z := zip.NewWriter(f)
	if err := add(z, "manifest.json", manifest, 0o644); err != nil {
		return err
	}
	// The executable bit has to survive the zip, or the host installs a server
	// it cannot start.
	if err := add(z, "server/"+t.exe, body, 0o755); err != nil {
		return err
	}
	if err := z.Close(); err != nil {
		return err
	}
	return f.Close()
}

func add(z *zip.Writer, name string, body []byte, mode fs.FileMode) error {
	h := &zip.FileHeader{Name: name, Method: zip.Deflate}
	h.SetMode(mode)
	w, err := z.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

// render fills the template in for one target. Each bundle carries exactly one
// binary, so it declares exactly one platform: a manifest listing three while
// shipping one would install cleanly and then fail to start.
func render(template []byte, t target, version string) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(template, &m); err != nil {
		return nil, fmt.Errorf("manifest template: %w", err)
	}
	server, ok := m["server"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("manifest template has no server object")
	}

	entry := "server/" + t.exe
	m["version"] = version
	server["type"] = "binary"
	server["entry_point"] = entry
	server["mcp_config"] = map[string]any{
		"command": "${__dirname}/" + entry,
		"args":    []any{"mcp"},
		"env":     map[string]any{},
	}
	m["compatibility"] = map[string]any{"platforms": []any{t.platform}}

	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "mcpb:", err)
	os.Exit(1)
}
