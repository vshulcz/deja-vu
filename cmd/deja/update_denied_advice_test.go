package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A failed replace named the temporary file deja was about to write — a path
// that does not exist and cannot be acted on — and finished with "or use your
// package manager" on a binary whose manager deja can name (#865).
func TestUpdateDeniedNamesTheDirectoryAndTheManager(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits do not block writes the same way on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory write permissions")
	}
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

	run := func(t *testing.T, dir string) error {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(dir, "deja")
		if err := os.WriteFile(dest, []byte("old"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
		return performUpdate(updateConfig{force: true, currentVersion: "dev", goos: "linux", goarch: "amd64",
			executable: dest, latestURL: testLatestReleaseURL, download: download}, io.Discard)
	}

	brewDir := filepath.Join(t.TempDir(), "Cellar", "deja-vu", "0.16.5", "bin")
	err = run(t, brewDir)
	if err == nil {
		t.Fatal("replace into an unwritable directory succeeded")
	}
	got := err.Error()
	if !strings.Contains(got, brewDir) {
		t.Errorf("message does not name the directory: %q", got)
	}
	if !strings.Contains(got, "brew upgrade deja-vu") {
		t.Errorf("message does not name the manager's command: %q", got)
	}
	if strings.Contains(got, ".deja-update-") {
		t.Errorf("message names the internal temporary file: %q", got)
	}

	// No manager owns it: the advice stops at the directory rather than
	// inventing a package manager the reader does not have.
	plain := filepath.Join(t.TempDir(), "bin")
	err = run(t, plain)
	if err == nil {
		t.Fatal("replace into an unwritable directory succeeded")
	}
	got = err.Error()
	if !strings.Contains(got, plain) {
		t.Errorf("message does not name the directory: %q", got)
	}
	if strings.Contains(got, "package manager") {
		t.Errorf("unmanaged binary still told to use a package manager: %q", got)
	}
}
