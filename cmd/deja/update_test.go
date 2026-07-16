package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

const testLatestReleaseURL = "test://latest"

func TestPerformUpdate(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "deja")
			if goos == "windows" {
				target += ".exe"
			}
			if err := os.WriteFile(target, []byte("old binary"), 0o750); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(target)
			if err != nil {
				t.Fatal(err)
			}
			download, requests := newUpdateDownloader(t, "1.2.0", goos, "amd64", []byte("new binary"), false)

			var out bytes.Buffer
			err = performUpdate(updateConfig{
				currentVersion: "1.1.0",
				goos:           goos,
				goarch:         "amd64",
				executable:     target,
				latestURL:      testLatestReleaseURL,
				download:       download,
			}, &out)
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != "new binary" {
				t.Fatalf("installed binary = %q", got)
			}
			if requests.Load() != 3 {
				t.Fatalf("requests = %d, want release + checksum + archive", requests.Load())
			}
			if !strings.Contains(out.String(), "v1.1.0 -> v1.2.0") || !strings.Contains(out.String(), target) {
				t.Fatalf("output = %q", out.String())
			}
			if runtime.GOOS != "windows" {
				info, err := os.Stat(target)
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != before.Mode().Perm() {
					t.Fatalf("installed mode = %o, want %o", info.Mode().Perm(), before.Mode().Perm())
				}
			}
		})
	}
}

func TestPerformUpdateAlreadyCurrent(t *testing.T) {
	target := filepath.Join(t.TempDir(), "deja")
	if err := os.WriteFile(target, []byte("current binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	download, requests := newUpdateDownloader(t, "1.2.0", "linux", "amd64", []byte("unused"), false)

	var out bytes.Buffer
	err := performUpdate(updateConfig{
		currentVersion: "v1.2.0",
		goos:           "linux",
		goarch:         "amd64",
		executable:     target,
		latestURL:      testLatestReleaseURL,
		download:       download,
	}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, downloaded an unnecessary asset", requests.Load())
	}
	got, _ := os.ReadFile(target)
	if string(got) != "current binary" || !strings.Contains(out.String(), "already up to date") {
		t.Fatalf("binary=%q output=%q", got, out.String())
	}
}

func TestPerformUpdateDoesNotDowngrade(t *testing.T) {
	target := filepath.Join(t.TempDir(), "deja")
	if err := os.WriteFile(target, []byte("newer binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	download, requests := newUpdateDownloader(t, "1.2.0", "linux", "amd64", []byte("older binary"), false)

	var out bytes.Buffer
	err := performUpdate(updateConfig{
		currentVersion: "1.3.0-alpha.1",
		goos:           "linux",
		goarch:         "amd64",
		executable:     target,
		latestURL:      testLatestReleaseURL,
		download:       download,
	}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 || !strings.Contains(out.String(), "newer than") {
		t.Fatalf("requests=%d output=%q", requests.Load(), out.String())
	}
	got, _ := os.ReadFile(target)
	if string(got) != "newer binary" {
		t.Fatalf("downgrade installed %q", got)
	}
}

func TestPerformUpdateChecksumFailurePreservesBinary(t *testing.T) {
	target := filepath.Join(t.TempDir(), "deja")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	download, _ := newUpdateDownloader(t, "1.2.0", "linux", "amd64", []byte("new binary"), true)

	err := performUpdate(updateConfig{
		currentVersion: "dev",
		goos:           "linux",
		goarch:         "amd64",
		executable:     target,
		latestURL:      testLatestReleaseURL,
		download:       download,
	}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("error = %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "old binary" {
		t.Fatalf("failed update replaced binary with %q", got)
	}
}

func TestPerformUpdateDownloadFailurePreservesBinary(t *testing.T) {
	target := filepath.Join(t.TempDir(), "deja")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := performUpdate(updateConfig{
		currentVersion: "1.1.0",
		goos:           "linux",
		goarch:         "amd64",
		executable:     target,
		latestURL:      testLatestReleaseURL,
		download: func(_ string, _ int64, _ string) ([]byte, error) {
			return nil, fmt.Errorf("service unavailable")
		},
	}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "service unavailable") {
		t.Fatalf("error = %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "old binary" {
		t.Fatalf("failed update replaced binary with %q", got)
	}
}

func TestUpdateCommandShapeAndPlatforms(t *testing.T) {
	if err := runUpdate([]string{"unexpected"}, io.Discard); err == nil || !strings.Contains(err.Error(), "takes no arguments") {
		t.Fatalf("runUpdate error = %v", err)
	}
	if err := run([]string{"update", "unexpected"}); err == nil || !strings.Contains(err.Error(), "takes no arguments") {
		t.Fatalf("dispatcher error = %v", err)
	}
	out, err := captureRun(t)
	if err != nil || !strings.Contains(out, "deja update") {
		t.Fatalf("usage output = %q, error = %v", out, err)
	}

	for _, tc := range []struct {
		goos, goarch string
		archive      string
		binary       string
		wantErr      bool
	}{
		{"linux", "amd64", "deja-vu_2.0.0_linux_amd64.tar.gz", "deja", false},
		{"darwin", "arm64", "deja-vu_2.0.0_darwin_arm64.tar.gz", "deja", false},
		{"windows", "amd64", "deja-vu_2.0.0_windows_amd64.zip", "deja.exe", false},
		{"freebsd", "amd64", "", "", true},
		{"linux", "386", "", "", true},
	} {
		archive, binary, err := updateAssetNames("2.0.0", tc.goos, tc.goarch)
		if (err != nil) != tc.wantErr || archive != tc.archive || binary != tc.binary {
			t.Fatalf("updateAssetNames(%s/%s) = %q, %q, %v", tc.goos, tc.goarch, archive, binary, err)
		}
	}

	for _, tc := range []struct {
		left, right string
		want        int
	}{
		{"0.9.2", "0.9.2", 0},
		{"0.10.0", "0.9.9", 1},
		{"1.0.0-alpha.2", "1.0.0-alpha.10", -1},
		{"1.0.0-alpha", "1.0.0", -1},
		{"2.0.0", "1.99.99", 1},
	} {
		got, ok := compareUpdateVersions(tc.left, tc.right)
		if !ok || got != tc.want {
			t.Fatalf("compareUpdateVersions(%q, %q) = %d, %v", tc.left, tc.right, got, ok)
		}
	}
}

func TestUpdateDownloaderReportsMissingCurl(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := defaultUpdateDownloader()("https://example.invalid", 1024, "test asset")
	if err == nil || !strings.Contains(err.Error(), "curl") {
		t.Fatalf("missing curl error = %v", err)
	}
}

func newUpdateDownloader(t *testing.T, version, goos, goarch string, binary []byte, badChecksum bool) (updateDownloader, *atomic.Int32) {
	t.Helper()
	archiveName, binaryName, err := updateAssetNames(version, goos, goarch)
	if err != nil {
		t.Fatal(err)
	}
	archive := makeUpdateArchive(t, goos, binaryName, binary)
	sum := sha256.Sum256(archive)
	checksum := fmt.Sprintf("%x  %s\n", sum, archiveName)
	if badChecksum {
		checksum = strings.Repeat("0", sha256.Size*2) + "  " + archiveName + "\n"
	}

	release, err := json.Marshal(updateRelease{
		TagName: "v" + version,
		Assets: []updateAsset{
			{Name: archiveName, URL: "test://archive"},
			{Name: "checksums.txt", URL: "test://checksums"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	responses := map[string][]byte{
		testLatestReleaseURL: release,
		"test://archive":     archive,
		"test://checksums":   []byte(checksum),
	}
	requests := &atomic.Int32{}
	download := func(url string, limit int64, label string) ([]byte, error) {
		requests.Add(1)
		body, ok := responses[url]
		if !ok {
			return nil, fmt.Errorf("download %s: unexpected URL %s", label, url)
		}
		if int64(len(body)) > limit {
			return nil, fmt.Errorf("download %s: response exceeds %d bytes", label, limit)
		}
		return append([]byte(nil), body...), nil
	}
	return download, requests
}

func makeUpdateArchive(t *testing.T, goos, binaryName string, binary []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if goos == "windows" {
		zw := zip.NewWriter(&buf)
		file, err := zw.Create(binaryName)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(binary); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}

	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: binaryName, Mode: 0o755, Size: int64(len(binary))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(binary); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
