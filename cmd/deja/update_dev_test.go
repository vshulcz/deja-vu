package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A binary built from source is by definition ahead of the newest tag — it is
// whatever the person just compiled — so replacing it in place is a downgrade
// that discards their build (#751).
func TestPerformUpdateLeavesADevBuildAlone(t *testing.T) {
	target := filepath.Join(t.TempDir(), "deja")
	if err := os.WriteFile(target, []byte("source build"), 0o755); err != nil {
		t.Fatal(err)
	}
	download, requests := newUpdateDownloader(t, "1.2.0", "linux", "amd64", []byte("release binary"), false)

	var out bytes.Buffer
	if err := performUpdate(updateConfig{
		currentVersion: "dev",
		goos:           "linux",
		goarch:         "amd64",
		executable:     target,
		latestURL:      testLatestReleaseURL,
		download:       download,
	}, &out); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != "source build" {
		t.Errorf("the source build was replaced by %q", got)
	}
	if !strings.Contains(out.String(), "built from source") || !strings.Contains(out.String(), "--force") {
		t.Errorf("output = %q", out.String())
	}
	// Only the release listing was fetched, not the asset.
	if requests.Load() != 1 {
		t.Errorf("requests = %d — it downloaded the release", requests.Load())
	}
}

func TestPerformUpdateForcedOnADevBuild(t *testing.T) {
	target := filepath.Join(t.TempDir(), "deja")
	if err := os.WriteFile(target, []byte("source build"), 0o755); err != nil {
		t.Fatal(err)
	}
	download, _ := newUpdateDownloader(t, "1.2.0", "linux", "amd64", []byte("release binary"), false)

	var out bytes.Buffer
	if err := performUpdate(updateConfig{
		force:          true,
		currentVersion: "dev",
		goos:           "linux",
		goarch:         "amd64",
		executable:     target,
		latestURL:      testLatestReleaseURL,
		download:       download,
	}, &out); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != "release binary" {
		t.Errorf("--force did not replace the binary: %q", got)
	}
}

func TestRunUpdateRejectsUnknownFlags(t *testing.T) {
	var out bytes.Buffer
	err := runUpdate([]string{"--nosuchflag"}, &out)
	if err == nil || !strings.Contains(err.Error(), "except --force") {
		t.Errorf("err = %v", err)
	}
}
