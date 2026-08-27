// Command pinmanifests rewrites the scoop, winget and Codex plugin manifests for a
// release.
//
// These files carry a version and the SHA-256 of the two Windows zips, and they
// used to be updated by hand after each release. That step is invisible when it
// is skipped: v0.16.0 shipped with the manifests still on 0.15.6, so scoop and
// winget users were two releases behind and nothing said so.
//
//	go run ./scripts/pinmanifests -version 0.16.1 -checksums dist/checksums.txt
//	go run ./scripts/pinmanifests -check          # fails if they lag the newest release
//
// -check is what CI runs. It fetches the newest published release, regenerates
// the manifests in memory and compares: a pin that was forgotten fails the build
// with the exact diff instead of going unnoticed until a user reports an old
// version.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	scoopPath   = "packaging/scoop/deja-vu.json"
	versionPath = "packaging/winget/vshulcz.deja-vu.yaml"
	localePath  = "packaging/winget/vshulcz.deja-vu.locale.en-US.yaml"
	installer   = "packaging/winget/vshulcz.deja-vu.installer.yaml"
	// The Codex plugin is installed from the default branch rather than from a
	// release asset, so the version committed here is the one a user sees. It
	// sat at 0.1.0 from July through 0.17.1 because nothing compared it to
	// anything.
	codexPlugin = "codex-plugin/.codex-plugin/plugin.json"
	releaseAPI  = "https://api.github.com/repos/vshulcz/deja-vu/releases/latest"
	assetBase   = "https://github.com/vshulcz/deja-vu/releases/download"
)

type pins struct {
	version string
	amd64   string
	arm64   string
}

func main() {
	version := flag.String("version", "", "release version without the leading v")
	checksums := flag.String("checksums", "", "path to the release checksums.txt")
	check := flag.Bool("check", false, "verify the committed manifests match the newest release")
	root := flag.String("root", ".", "repository root")
	flag.Parse()

	if *check {
		if err := runCheck(*root); err != nil {
			fmt.Fprintln(os.Stderr, "pinmanifests:", err)
			os.Exit(1)
		}
		fmt.Println("scoop, winget and the codex plugin match the newest release")
		return
	}

	if *version == "" || *checksums == "" {
		fmt.Fprintln(os.Stderr, "pinmanifests: -version and -checksums are required")
		os.Exit(1)
	}
	b, err := os.ReadFile(*checksums)
	if err != nil {
		fail(err)
	}
	p, err := parse(*version, string(b))
	if err != nil {
		fail(err)
	}
	if err := write(*root, p); err != nil {
		fail(err)
	}
	fmt.Printf("pinned scoop, winget and the codex plugin to %s\n", p.version)
}

// parse pulls the two Windows hashes out of a checksums file. Both must be
// present: a manifest with one stale hash installs a mismatched binary for half
// the users and passes every check we have.
func parse(version, checksums string) (pins, error) {
	p := pins{version: version}
	s := bufio.NewScanner(strings.NewReader(checksums))
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) != 2 {
			continue
		}
		sum, name := fields[0], filepath.Base(fields[1])
		switch name {
		case fmt.Sprintf("deja-vu_%s_windows_amd64.zip", version):
			p.amd64 = sum
		case fmt.Sprintf("deja-vu_%s_windows_arm64.zip", version):
			p.arm64 = sum
		}
	}
	if err := s.Err(); err != nil {
		return p, err
	}
	if p.amd64 == "" || p.arm64 == "" {
		return p, fmt.Errorf("checksums for %s are missing a windows zip (amd64=%q arm64=%q)", version, p.amd64, p.arm64)
	}
	return p, nil
}

func write(root string, p pins) error {
	for path, render := range map[string]func(pins) ([]byte, error){
		scoopPath:   renderScoop,
		versionPath: renderVersion,
		localePath:  renderLocale,
		installer:   renderInstaller,
		codexPlugin: renderCodexPlugin,
	} {
		body, err := render(p)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(root, path), body, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func renderScoop(p pins) ([]byte, error) {
	m := map[string]any{
		"version":     p.version,
		"description": "Persistent memory for coding agents",
		"homepage":    "https://github.com/vshulcz/deja-vu",
		"license":     "MIT",
		"architecture": map[string]any{
			"64bit": map[string]any{
				"url":  fmt.Sprintf("%s/v%s/deja-vu_%s_windows_amd64.zip", assetBase, p.version, p.version),
				"hash": p.amd64,
			},
			"arm64": map[string]any{
				"url":  fmt.Sprintf("%s/v%s/deja-vu_%s_windows_arm64.zip", assetBase, p.version, p.version),
				"hash": p.arm64,
			},
		},
		"bin":      "deja.exe",
		"checkver": "github",
		"autoupdate": map[string]any{
			"architecture": map[string]any{
				"64bit": map[string]any{"url": assetBase + "/v$version/deja-vu_$version_windows_amd64.zip"},
				"arm64": map[string]any{"url": assetBase + "/v$version/deja-vu_$version_windows_arm64.zip"},
			},
		},
	}
	b, err := json.MarshalIndent(m, "", "    ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// The winget files are edited rather than regenerated: they carry tags and
// descriptions that are not derived from a release, and rewriting them from a
// template here would quietly drop whatever someone adds later.
// renderVersion covers the winget version manifest, which carries its own
// PackageVersion. Leaving it out pinned two of the three winget files and the
// set failed its own consistency test — caught after 0.16.2 shipped, having
// been fixed by hand before that.
func renderVersion(p pins) ([]byte, error) { return edit(versionPath, p) }

func renderLocale(p pins) ([]byte, error) { return edit(localePath, p) }

func renderInstaller(p pins) ([]byte, error) { return edit(installer, p) }

// renderCodexPlugin rewrites only the version field. The manifest carries
// descriptions, prompts and pointers that no release derives, so regenerating it
// from a template here would drop whatever someone adds later — the same reason
// the winget files are edited rather than rendered.
func renderCodexPlugin(p pins) ([]byte, error) {
	b, err := os.ReadFile(codexPlugin)
	if err != nil {
		return nil, err
	}
	s := jsonVersion.ReplaceAllString(string(b), `${1}"`+p.version+`"`)
	if !jsonVersion.MatchString(string(b)) {
		return nil, fmt.Errorf("%s has no version field to pin", codexPlugin)
	}
	return []byte(s), nil
}

var (
	versionLine = regexp.MustCompile(`(?m)^PackageVersion: .*$`)
	// (?m) because a tag reference is often the last thing on its line: the
	// ReleaseNotesUrl in the winget locale sat at v0.16.1 through three
	// releases while every reference followed by a slash moved (#2088).
	tagRefs     = regexp.MustCompile(`(?m)/v\d+\.\d+\.\d+(/|$)`)
	fileRefs    = regexp.MustCompile(`deja-vu_\d+\.\d+\.\d+_windows`)
	jsonVersion = regexp.MustCompile(`(?m)^(\s*"version":\s*)"[^"]*"`)
	amdSha      = regexp.MustCompile(`(?m)^(\s*InstallerSha256: )[0-9A-Fa-f]{64}(\s*)$`)
)

func edit(path string, p pins) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := string(b)
	s = versionLine.ReplaceAllString(s, "PackageVersion: "+p.version)
	s = tagRefs.ReplaceAllString(s, "/v"+p.version+"$1")
	s = fileRefs.ReplaceAllString(s, "deja-vu_"+p.version+"_windows")

	// Two hashes in file order: amd64 first, then arm64, matching the
	// Installers list. Anything else means the file was reordered and the
	// rewrite would put the wrong hash against an architecture.
	want := []string{strings.ToUpper(p.amd64), strings.ToUpper(p.arm64)}
	i := 0
	var bad error
	s = amdSha.ReplaceAllStringFunc(s, func(m string) string {
		if i >= len(want) {
			bad = fmt.Errorf("%s has more InstallerSha256 lines than architectures", path)
			return m
		}
		sub := amdSha.FindStringSubmatch(m)
		out := sub[1] + want[i] + sub[2]
		i++
		return out
	})
	if bad != nil {
		return nil, bad
	}
	if i != 0 && i != len(want) {
		return nil, fmt.Errorf("%s has %d InstallerSha256 lines, want %d", path, i, len(want))
	}
	return []byte(s), nil
}

func runCheck(root string) error {
	version, err := latestRelease()
	if err != nil {
		return err
	}
	sums, err := fetch(fmt.Sprintf("%s/v%s/checksums.txt", assetBase, version))
	if err != nil {
		return err
	}
	p, err := parse(version, string(sums))
	if err != nil {
		return err
	}
	for path, render := range map[string]func(pins) ([]byte, error){
		scoopPath:   renderScoop,
		versionPath: renderVersion,
		localePath:  renderLocale,
		installer:   renderInstaller,
		codexPlugin: renderCodexPlugin,
	} {
		want, err := render(p)
		if err != nil {
			return err
		}
		got, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			return err
		}
		if string(got) != string(want) {
			return fmt.Errorf("%s is not pinned to %s — run: go run ./scripts/pinmanifests -version %s -checksums <checksums.txt>", path, version, version)
		}
	}
	return nil
}

func latestRelease() (string, error) {
	b, err := fetch(releaseAPI)
	if err != nil {
		return "", err
	}
	var r struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return "", err
	}
	if r.TagName == "" {
		return "", fmt.Errorf("no tag_name in the latest release")
	}
	return strings.TrimPrefix(r.TagName, "v"), nil
}

func fetch(url string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if token := os.Getenv("GITHUB_TOKEN"); token != "" && strings.HasPrefix(url, "https://api.github.com") {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "pinmanifests:", err)
	os.Exit(1)
}
