package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Without --force deja explains that a manager owns the binary and that the
// next manager run puts the old one back. With --force it wrote in silence —
// the consequence was told only to the reader who did not take that path
// (#866).
func TestForcedUpdateOverAManagedBinaryWarnsBeforeWriting(t *testing.T) {
	archiveName, binaryName, err := updateAssetNames("2.0.0", "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	archive := makeUpdateArchive(t, "linux", binaryName, []byte("new binary"))
	sum := fmt.Sprintf("%x", sha256.Sum256(archive))
	release := []byte(fmt.Sprintf(`{"tag_name":"v2.0.0","assets":[{"name":%q,"browser_download_url":"archive"},{"name":"checksums.txt","browser_download_url":"checksums"}]}`, archiveName))
	download := func(url string, limit int64, label string) ([]byte, error) {
		switch label {
		case "checksums.txt":
			return []byte(sum + "  " + archiveName + "\n"), nil
		case archiveName:
			return archive, nil
		default:
			return release, nil
		}
	}
	run := func(t *testing.T, dir string) string {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(dir, "deja")
		if err := os.WriteFile(dest, []byte("old"), 0o755); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := performUpdate(updateConfig{force: true, currentVersion: "0.16.5", goos: "linux", goarch: "amd64",
			executable: dest, latestURL: testLatestReleaseURL, download: download}, &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	got := run(t, filepath.Join(t.TempDir(), "Cellar", "deja-vu", "0.16.5", "bin"))
	if !strings.Contains(got, "Homebrew manages this binary") || !strings.Contains(got, "brew upgrade deja-vu") {
		t.Errorf("forced update over a Homebrew binary said nothing about it:\n%s", got)
	}
	// The warning comes before the write is reported, not after it.
	if i, j := strings.Index(got, "Homebrew manages"), strings.Index(got, "updated deja"); i < 0 || j < 0 || i > j {
		t.Errorf("warning does not precede the result:\n%s", got)
	}

	// Nothing owns this one: no warning to give.
	got = run(t, filepath.Join(t.TempDir(), "bin"))
	if strings.Contains(got, "manages this binary") {
		t.Errorf("unmanaged binary warned about a manager:\n%s", got)
	}
}
