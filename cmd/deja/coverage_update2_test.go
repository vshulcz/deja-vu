package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCompareUpdateVersionsAllBranches(t *testing.T) {
	cases := []struct {
		left, right string
		want        int
		ok          bool
	}{
		{"1.0.0", "bad", 0, false},
		{"1.0.0-3", "1.0.0-2", 1, true},
		{"1.0.0-2", "1.0.0-alpha", -1, true},
		{"1.0.0-alpha", "1.0.0-2", 1, true},
		{"1.0.0-alpha", "1.0.0-beta", -1, true},
		{"1.0.0-beta", "1.0.0-alpha", 1, true},
		{"1.0.0-alpha", "1.0.0-alpha.1", -1, true},
		{"1.0.0-alpha.1", "1.0.0-alpha", 1, true},
		{"1.0.0", "1.0.0-alpha", 1, true},
		{"1.0.0-alpha.1", "1.0.0-alpha.1", 0, true},
	}
	for _, tc := range cases {
		got, ok := compareUpdateVersions(tc.left, tc.right)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Fatalf("compareUpdateVersions(%q, %q) = %d, %v; want %d, %v", tc.left, tc.right, got, ok, tc.want, tc.ok)
		}
	}
}

func TestParseUpdateVersionInvalidBranches(t *testing.T) {
	for _, v := range []string{"1.a.0", "-1.0.0", "1.0.0-", "1.0.0-alpha..1"} {
		if _, ok := parseUpdateVersion(v); ok {
			t.Fatalf("parseUpdateVersion(%q) unexpectedly ok", v)
		}
	}
}

// --- findUpdateAsset / checksumForArchive ---

func TestFindUpdateAssetNotFound(t *testing.T) {
	if _, ok := findUpdateAsset([]updateAsset{{Name: "other", URL: "u"}}, "wanted"); ok {
		t.Fatal("expected not found")
	}
}

func TestChecksumForArchiveBranches(t *testing.T) {
	name := "deja-vu_1.0.0_linux_amd64.tar.gz"
	valid := strings.Repeat("a", 64)

	// A decoy line for a different asset is skipped via "continue" before the
	// real line matches.
	multi := "deadbeef  other-file\n" + valid + "  " + name + "\n"
	if got, err := checksumForArchive([]byte(multi), name); err != nil || got != valid {
		t.Fatalf("checksumForArchive multi-line = %q, %v", got, err)
	}

	wrongLen := "abcd  " + name + "\n"
	if _, err := checksumForArchive([]byte(wrongLen), name); err == nil || !strings.Contains(err.Error(), "invalid checksum") {
		t.Fatalf("wrong length err = %v", err)
	}

	invalidHex := strings.Repeat("z", 64) + "  " + name + "\n"
	if _, err := checksumForArchive([]byte(invalidHex), name); err == nil || !strings.Contains(err.Error(), "invalid checksum") {
		t.Fatalf("invalid hex err = %v", err)
	}

	if _, err := checksumForArchive([]byte("nothing here\n"), name); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("not found err = %v", err)
	}
}

// --- extractUpdateBinary corrupt/edge archives ---

func TestExtractUpdateBinaryZipBranches(t *testing.T) {
	if _, err := extractUpdateBinary([]byte("not a zip"), "deja.exe", "windows"); err == nil {
		t.Fatal("expected corrupt zip error")
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create("other.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := extractUpdateBinary(buf.Bytes(), "deja.exe", "windows"); err == nil || !strings.Contains(err.Error(), "did not contain") {
		t.Fatalf("zip missing binary err = %v", err)
	}

	// A corrupted local file header makes file.Open() fail even though the
	// central directory (read by zip.NewReader) is intact.
	var okBuf bytes.Buffer
	zwOK := zip.NewWriter(&okBuf)
	entry, err := zwOK.Create("deja.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("hello world")); err != nil {
		t.Fatal(err)
	}
	if err := zwOK.Close(); err != nil {
		t.Fatal(err)
	}
	corrupted := append([]byte(nil), okBuf.Bytes()...)
	corrupted[0] = 0xff
	corrupted[1] = 0xff
	if _, err := extractUpdateBinary(corrupted, "deja.exe", "windows"); err == nil {
		t.Fatal("expected corrupt local file header error")
	}

	// A zero-length entry surfaces readUpdateBinary's "is empty" error.
	var emptyBuf bytes.Buffer
	zw2 := zip.NewWriter(&emptyBuf)
	if _, err := zw2.Create("deja.exe"); err != nil {
		t.Fatal(err)
	}
	if err := zw2.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := extractUpdateBinary(emptyBuf.Bytes(), "deja.exe", "windows"); err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("zip empty entry err = %v", err)
	}
}

func TestExtractUpdateBinaryZipOversizedEntry(t *testing.T) {
	if testing.Short() {
		t.Skip("large-payload test skipped in -short mode")
	}
	big := make([]byte, maxExecutable+1)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create("deja.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(big); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := extractUpdateBinary(buf.Bytes(), "deja.exe", "windows"); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("zip oversized entry err = %v", err)
	}
}

func TestExtractUpdateBinaryTarBranches(t *testing.T) {
	if _, err := extractUpdateBinary([]byte("not gzip"), "deja", "linux"); err == nil {
		t.Fatal("expected corrupt gzip error")
	}

	corruptTar := func() []byte {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		if _, err := gz.Write([]byte("this is not a valid tar payload at all, just plain text bytes padded out")); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}()
	if _, err := extractUpdateBinary(corruptTar, "deja", "linux"); err == nil {
		t.Fatal("expected corrupt tar error")
	}

	// A directory entry with the matching name must be skipped via "continue".
	var dirBuf bytes.Buffer
	gz := gzip.NewWriter(&dirBuf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "deja", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: "other", Mode: 0o755, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := extractUpdateBinary(dirBuf.Bytes(), "deja", "linux"); err == nil || !strings.Contains(err.Error(), "did not contain") {
		t.Fatalf("tar dir+mismatch err = %v", err)
	}
}

func TestExtractUpdateBinaryTarOversizedHeader(t *testing.T) {
	if testing.Short() {
		t.Skip("large-payload test skipped in -short mode")
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	big := make([]byte, maxExecutable+1)
	if err := tw.WriteHeader(&tar.Header{Name: "deja", Mode: 0o755, Size: int64(len(big))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(big); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := extractUpdateBinary(buf.Bytes(), "deja", "linux"); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("tar oversized header err = %v", err)
	}
}

// --- readUpdateBinary direct ---

type errAfterReader struct{}

func (errAfterReader) Read(p []byte) (int, error) {
	return 0, errors.New("boom read")
}

func TestReadUpdateBinaryDirectBranches(t *testing.T) {
	if _, err := readUpdateBinary(errAfterReader{}, "name"); err == nil || !strings.Contains(err.Error(), "boom read") {
		t.Fatalf("read error branch = %v", err)
	}
	if _, err := readUpdateBinary(bytes.NewReader(nil), "name"); err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("empty branch = %v", err)
	}
	if testing.Short() {
		t.Skip("large-payload test skipped in -short mode")
	}
	big := make([]byte, maxExecutable+1)
	if _, err := readUpdateBinary(bytes.NewReader(big), "name"); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized branch = %v", err)
	}
}

// --- installUpdateBinary edge branches ---

func TestInstallUpdateBinaryStatAndTypeBranches(t *testing.T) {
	if err := installUpdateBinary(filepath.Join(t.TempDir(), "absent"), []byte("x")); err == nil {
		t.Fatal("expected stat error for missing destination")
	}
	if err := installUpdateBinary(t.TempDir(), []byte("x")); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("directory destination err = %v", err)
	}
}

func TestPerformUpdateSuccessUnknownVersionAndPermissionBranches(t *testing.T) {
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

	// Empty current version takes the "from unknown" branch on success.
	target := filepath.Join(t.TempDir(), "deja")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := performUpdate(updateConfig{goos: "linux", goarch: "amd64", executable: target, latestURL: testLatestReleaseURL, download: download}, &out); err != nil {
		t.Fatalf("performUpdate unknown-version err = %v", err)
	}
	if !strings.Contains(out.String(), "updated deja unknown -> v2.0.0") {
		t.Fatalf("unknown-version output = %q", out.String())
	}

	if runtime.GOOS == "windows" {
		t.Skip("permission bits do not block writes the same way on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory write permissions")
	}
	dir := t.TempDir()
	dest := filepath.Join(dir, "deja")
	if err := os.WriteFile(dest, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(dir, 0o755) }()
	err = performUpdate(updateConfig{currentVersion: "dev", goos: "linux", goarch: "amd64", executable: dest, latestURL: testLatestReleaseURL, download: download}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "replace") || !strings.Contains(err.Error(), "rerun with permission") {
		t.Fatalf("permission-denied replace err = %v", err)
	}
}

func TestInstallUpdateBinaryCreateTempPermissionDenied(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits do not block writes the same way on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory write permissions")
	}
	dir := t.TempDir()
	dest := filepath.Join(dir, "deja")
	if err := os.WriteFile(dest, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(dir, 0o755) }()
	if err := installUpdateBinary(dest, []byte("new")); err == nil {
		t.Fatal("expected CreateTemp permission error")
	}
}
