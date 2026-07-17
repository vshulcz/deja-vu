package sources

import (
	"bufio"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

const (
	packageID      = "vshulcz.deja-vu"
	manifestSchema = "1.12.0"
)

var (
	semverRE = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	hashRE   = regexp.MustCompile(`^[A-Fa-f0-9]{64}$`)
)

type scoopDownload struct {
	URL  string `json:"url"`
	Hash string `json:"hash"`
}

type scoopManifest struct {
	Version      string                   `json:"version"`
	Description  string                   `json:"description"`
	Homepage     string                   `json:"homepage"`
	License      string                   `json:"license"`
	Architecture map[string]scoopDownload `json:"architecture"`
	Bin          string                   `json:"bin"`
	Checkver     string                   `json:"checkver"`
	Autoupdate   struct {
		Architecture map[string]scoopDownload `json:"architecture"`
	} `json:"autoupdate"`
}

func TestScoopManifest(t *testing.T) {
	b, err := os.ReadFile("../../packaging/scoop/deja-vu.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest scoopManifest
	if err := json.Unmarshal(b, &manifest); err != nil {
		t.Fatal(err)
	}
	if !semverRE.MatchString(manifest.Version) {
		t.Errorf("version = %q, want stable semantic version", manifest.Version)
	}
	if manifest.Description == "" || manifest.Homepage != "https://github.com/vshulcz/deja-vu" || manifest.License != "MIT" {
		t.Error("description, homepage, or license is missing")
	}
	if manifest.Bin != "deja.exe" || manifest.Checkver != "github" {
		t.Errorf("bin/checkver = %q/%q", manifest.Bin, manifest.Checkver)
	}
	for architecture, assetArchitecture := range map[string]string{"64bit": "amd64", "arm64": "arm64"} {
		download, ok := manifest.Architecture[architecture]
		if !ok {
			t.Errorf("missing %s download", architecture)
			continue
		}
		wantURL := releaseURL(manifest.Version, assetArchitecture)
		if download.URL != wantURL || !hashRE.MatchString(download.Hash) {
			t.Errorf("%s download = %#v, want URL %q and SHA-256", architecture, download, wantURL)
		}
		update, ok := manifest.Autoupdate.Architecture[architecture]
		wantUpdate := releaseURL("$version", assetArchitecture)
		if !ok || update.URL != wantUpdate {
			t.Errorf("%s autoupdate URL = %q, want %q", architecture, update.URL, wantUpdate)
		}
	}
}

func TestWinGetManifestSet(t *testing.T) {
	files := []struct {
		path         string
		manifestType string
	}{
		{"../../packaging/winget/vshulcz.deja-vu.yaml", "version"},
		{"../../packaging/winget/vshulcz.deja-vu.locale.en-US.yaml", "defaultLocale"},
		{"../../packaging/winget/vshulcz.deja-vu.installer.yaml", "installer"},
	}
	var version string
	for _, file := range files {
		values := topLevelYAML(t, file.path)
		if values["PackageIdentifier"] != packageID || values["ManifestType"] != file.manifestType || values["ManifestVersion"] != manifestSchema {
			t.Errorf("%s has inconsistent identity or manifest metadata: %#v", file.path, values)
		}
		if version == "" {
			version = values["PackageVersion"]
		}
		if values["PackageVersion"] != version {
			t.Errorf("%s version = %q, want %q", file.path, values["PackageVersion"], version)
		}
	}
	if !semverRE.MatchString(version) {
		t.Fatalf("PackageVersion = %q, want stable semantic version", version)
	}

	locale := topLevelYAML(t, files[1].path)
	for _, key := range []string{"PackageLocale", "Publisher", "PackageName", "License", "ShortDescription"} {
		if locale[key] == "" {
			t.Errorf("defaultLocale is missing %s", key)
		}
	}

	b, err := os.ReadFile(files[2].path)
	if err != nil {
		t.Fatal(err)
	}
	installer := string(b)
	for architecture, assetArchitecture := range map[string]string{"x64": "amd64", "arm64": "arm64"} {
		if !strings.Contains(installer, "- Architecture: "+architecture+"\n") || !strings.Contains(installer, "  InstallerUrl: "+releaseURL(version, assetArchitecture)+"\n") {
			t.Errorf("installer is missing %s release URL", architecture)
		}
	}
	if strings.Count(installer, "  InstallerSha256: ") != 2 || strings.Count(installer, "- RelativeFilePath: deja.exe\n") != 1 {
		t.Error("installer must have two hashes and one portable deja.exe entry")
	}
	for _, line := range strings.Split(installer, "\n") {
		if hash, ok := strings.CutPrefix(line, "  InstallerSha256: "); ok && !hashRE.MatchString(hash) {
			t.Errorf("invalid InstallerSha256 %q", hash)
		}
	}
}

func topLevelYAML(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	values := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "-") {
			continue
		}
		if key, value, ok := strings.Cut(line, ":"); ok {
			values[key] = strings.TrimSpace(value)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return values
}

func releaseURL(version, architecture string) string {
	return "https://github.com/vshulcz/deja-vu/releases/download/v" + version + "/deja-vu_" + version + "_windows_" + architecture + ".zip"
}
