package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- newHTTPUpdateDownloader / download plumbing ---

func TestDownloaderRequestAndReadErrors(t *testing.T) {
	download := newHTTPUpdateDownloader(&http.Client{})

	// Malformed URL fails at http.NewRequest, before any dial.
	if _, err := download("http://[::1]:namedport/x", 100, "bad url"); err == nil || !strings.Contains(err.Error(), "request bad url") {
		t.Fatalf("bad url err = %v", err)
	}

	// A closed local listener makes client.Do fail without touching the network.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	if _, err := download("http://"+addr+"/x", 100, "closed conn"); err == nil || !strings.Contains(err.Error(), "closed conn") {
		t.Fatalf("closed conn err = %v", err)
	}
}

func TestDownloaderReadAllErrorAndOverLimitAfterRead(t *testing.T) {
	// A handler that declares a Content-Length larger than what it actually
	// sends, then drops the connection, makes io.ReadAll surface an error.
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("ResponseWriter does not support hijacking")
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\nshort")
		_ = buf.Flush()
	}))
	defer server.Close()
	download := newHTTPUpdateDownloader(server.Client())
	if _, err := download(server.URL, 1000, "truncated"); err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("truncated body err = %v", err)
	}

	// A chunked (unknown Content-Length) response bigger than the limit is
	// only caught by the post-read size check.
	chunked := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("ResponseWriter does not support flushing")
		}
		for i := 0; i < 10; i++ {
			_, _ = w.Write([]byte("x"))
			flusher.Flush()
		}
	}))
	defer chunked.Close()
	download2 := newHTTPUpdateDownloader(chunked.Client())
	if _, err := download2(chunked.URL, 3, "chunked"); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("chunked over-limit err = %v", err)
	}
}

func TestDownloaderRedirectChainBranches(t *testing.T) {
	var hops int
	var mux http.ServeMux
	var srv *httptest.Server
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+"/mid", http.StatusFound)
	})
	mux.HandleFunc("/mid", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+"/final", http.StatusFound)
	})
	mux.HandleFunc("/final", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "done")
	})
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) {
		hops++
		http.Redirect(w, r, srv.URL+"/loop", http.StatusFound)
	})
	srv = httptest.NewTLSServer(&mux)
	defer srv.Close()

	// Default client (no preexisting CheckRedirect): a short https->https
	// chain succeeds, exercising the "return nil" fallthrough.
	plain := newHTTPUpdateDownloader(srv.Client())
	body, err := plain(srv.URL+"/start", 100, "chain")
	if err != nil || string(body) != "done" {
		t.Fatalf("redirect chain body=%q err=%v", body, err)
	}

	// A never-ending redirect trips the >=10 hop guard.
	if _, err := plain(srv.URL+"/loop", 100, "loop"); err == nil || !strings.Contains(err.Error(), "stopped after 10 redirects") {
		t.Fatalf("redirect loop err = %v", err)
	}

	// A client with a preexisting CheckRedirect delegates to it.
	var called bool
	withPrevious := srv.Client()
	withPrevious.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		called = true
		return nil
	}
	wrapped := newHTTPUpdateDownloader(withPrevious)
	if _, err := wrapped(srv.URL+"/start", 100, "delegate"); err != nil {
		t.Fatalf("delegated redirect err = %v", err)
	}
	if !called {
		t.Fatal("expected previousRedirect to be invoked")
	}
}

// --- performUpdate additional branches ---

func TestPerformUpdateArgumentValidation(t *testing.T) {
	if err := performUpdate(updateConfig{}, io.Discard); err == nil || !strings.Contains(err.Error(), "downloader is required") {
		t.Fatalf("nil downloader err = %v", err)
	}
	noopDownload := func(string, int64, string) ([]byte, error) { return nil, nil }
	if err := performUpdate(updateConfig{download: noopDownload}, io.Discard); err == nil || !strings.Contains(err.Error(), "executable path is required") {
		t.Fatalf("empty executable err = %v", err)
	}
	if err := performUpdate(updateConfig{download: noopDownload, executable: "x", goos: "plan9", goarch: "amd64"}, io.Discard); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("unsupported platform err = %v", err)
	}
}

func TestPerformUpdateReleaseDecodeAndTagBranches(t *testing.T) {
	target := filepath.Join(t.TempDir(), "deja")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	base := updateConfig{currentVersion: "1.0.0", goos: "linux", goarch: "amd64", executable: target, latestURL: testLatestReleaseURL}

	badJSON := base
	badJSON.download = func(url string, limit int64, label string) ([]byte, error) { return []byte("not json"), nil }
	if err := performUpdate(badJSON, io.Discard); err == nil || !strings.Contains(err.Error(), "decode latest release") {
		t.Fatalf("bad json err = %v", err)
	}

	noTag := base
	noTag.download = func(url string, limit int64, label string) ([]byte, error) { return []byte(`{"tag_name":""}`), nil }
	if err := performUpdate(noTag, io.Discard); err == nil || !strings.Contains(err.Error(), "did not include a tag") {
		t.Fatalf("empty tag err = %v", err)
	}

	// current and latest are both unparseable-but-equal strings: skips the
	// SemVer comparison and takes the direct string-equality shortcut.
	weird := base
	weird.currentVersion = "weird"
	weird.download = func(url string, limit int64, label string) ([]byte, error) {
		return []byte(`{"tag_name":"vweird"}`), nil
	}
	var out bytes.Buffer
	if err := performUpdate(weird, &out); err != nil || !strings.Contains(out.String(), "already up to date") {
		t.Fatalf("weird-equal err=%v out=%q", err, out.String())
	}
}

func TestPerformUpdateAssetAndChecksumLookupErrors(t *testing.T) {
	target := filepath.Join(t.TempDir(), "deja")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := updateConfig{currentVersion: "dev", goos: "linux", goarch: "amd64", executable: target, latestURL: testLatestReleaseURL}

	noAsset := cfg
	noAsset.download = func(url string, limit int64, label string) ([]byte, error) {
		return []byte(`{"tag_name":"v2.0.0","assets":[]}`), nil
	}
	if err := performUpdate(noAsset, io.Discard); err == nil || !strings.Contains(err.Error(), "no asset for") {
		t.Fatalf("no archive asset err = %v", err)
	}

	archiveName, _, _ := updateAssetNames("2.0.0", "linux", "amd64")
	noChecksums := cfg
	noChecksums.download = func(url string, limit int64, label string) ([]byte, error) {
		return []byte(fmt.Sprintf(`{"tag_name":"v2.0.0","assets":[{"name":%q,"browser_download_url":"u"}]}`, archiveName)), nil
	}
	if err := performUpdate(noChecksums, io.Discard); err == nil || !strings.Contains(err.Error(), "no checksums.txt") {
		t.Fatalf("no checksums asset err = %v", err)
	}

	release := []byte(fmt.Sprintf(`{"tag_name":"v2.0.0","assets":[{"name":%q,"browser_download_url":"archive"},{"name":"checksums.txt","browser_download_url":"checksums"}]}`, archiveName))

	checksumDownloadFails := cfg
	checksumDownloadFails.download = func(url string, limit int64, label string) ([]byte, error) {
		if label == "checksums.txt" {
			return nil, fmt.Errorf("checksum fetch boom")
		}
		return release, nil
	}
	if err := performUpdate(checksumDownloadFails, io.Discard); err == nil || !strings.Contains(err.Error(), "checksum fetch boom") {
		t.Fatalf("checksum download err = %v", err)
	}

	badChecksumFormat := cfg
	badChecksumFormat.download = func(url string, limit int64, label string) ([]byte, error) {
		if label == "checksums.txt" {
			return []byte("not-a-checksum-line " + archiveName + "\n"), nil
		}
		return release, nil
	}
	if err := performUpdate(badChecksumFormat, io.Discard); err == nil || !strings.Contains(err.Error(), "invalid checksum") {
		t.Fatalf("invalid checksum format err = %v", err)
	}

	archiveDownloadFails := cfg
	archiveDownloadFails.download = func(url string, limit int64, label string) ([]byte, error) {
		if label == "checksums.txt" {
			return []byte(strings.Repeat("a", 64) + "  " + archiveName + "\n"), nil
		}
		if label == archiveName {
			return nil, fmt.Errorf("archive fetch boom")
		}
		return release, nil
	}
	if err := performUpdate(archiveDownloadFails, io.Discard); err == nil || !strings.Contains(err.Error(), "archive fetch boom") {
		t.Fatalf("archive download err = %v", err)
	}
}

func TestPerformUpdateExtractAndInstallErrors(t *testing.T) {
	archiveName, binaryName, err := updateAssetNames("2.0.0", "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	release := []byte(fmt.Sprintf(`{"tag_name":"v2.0.0","assets":[{"name":%q,"browser_download_url":"archive"},{"name":"checksums.txt","browser_download_url":"checksums"}]}`, archiveName))

	// A tar.gz whose entry name never matches the expected binary makes
	// extractUpdateBinary fail, which performUpdate wraps as "extract ...".
	emptyArchive := makeUpdateArchive(t, "linux", "not-"+binaryName, []byte("x"))
	sum := fmt.Sprintf("%x", sha256.Sum256(emptyArchive))
	cfg := updateConfig{
		currentVersion: "dev",
		goos:           "linux",
		goarch:         "amd64",
		latestURL:      testLatestReleaseURL,
		download: func(url string, limit int64, label string) ([]byte, error) {
			switch label {
			case "checksums.txt":
				return []byte(sum + "  " + archiveName + "\n"), nil
			case archiveName:
				return emptyArchive, nil
			default:
				return release, nil
			}
		},
	}
	target := filepath.Join(t.TempDir(), "deja")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg.executable = target
	if err := performUpdate(cfg, io.Discard); err == nil || !strings.Contains(err.Error(), "extract") {
		t.Fatalf("extract error = %v", err)
	}

	// installUpdateBinary failing (destination is a directory) surfaces as
	// "replace ...".
	goodArchive := makeUpdateArchive(t, "linux", binaryName, []byte("new binary"))
	goodSum := fmt.Sprintf("%x", sha256.Sum256(goodArchive))
	dirTarget := t.TempDir()
	cfg2 := cfg
	cfg2.executable = dirTarget
	cfg2.download = func(url string, limit int64, label string) ([]byte, error) {
		switch label {
		case "checksums.txt":
			return []byte(goodSum + "  " + archiveName + "\n"), nil
		case archiveName:
			return goodArchive, nil
		default:
			return release, nil
		}
	}
	if err := performUpdate(cfg2, io.Discard); err == nil || !strings.Contains(err.Error(), "replace") || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("replace error = %v", err)
	}
}
