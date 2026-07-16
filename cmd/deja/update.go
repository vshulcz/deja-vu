package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	latestReleaseURL = "https://api.github.com/repos/vshulcz/deja-vu/releases/latest"
	maxReleaseJSON   = 2 << 20
	maxChecksums     = 2 << 20
	maxArchive       = 128 << 20
	maxExecutable    = 128 << 20
)

type updateAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type updateRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []updateAsset `json:"assets"`
}

type updateDownloader func(url string, limit int64, label string) ([]byte, error)

func defaultUpdateDownloader() updateDownloader {
	return newHTTPUpdateDownloader(&http.Client{Timeout: 2 * time.Minute})
}

func newHTTPUpdateDownloader(client *http.Client) updateDownloader {
	configured := *client
	previousRedirect := configured.CheckRedirect
	configured.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" {
			return fmt.Errorf("refusing non-HTTPS redirect")
		}
		if previousRedirect != nil {
			return previousRedirect(req, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		return nil
	}
	client = &configured
	return func(url string, limit int64, label string) ([]byte, error) {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("request %s: %w", label, err)
		}
		if req.URL.Scheme != "https" {
			return nil, fmt.Errorf("download %s: URL must use HTTPS", label)
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "deja/"+normalizeUpdateVersion(version))
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("download %s: %w", label, err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("download %s: HTTP %s", label, resp.Status)
		}
		if resp.ContentLength > limit {
			return nil, fmt.Errorf("download %s: response exceeds %d bytes", label, limit)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		if err != nil {
			return nil, fmt.Errorf("download %s: %w", label, err)
		}
		if int64(len(body)) > limit {
			return nil, fmt.Errorf("download %s: response exceeds %d bytes", label, limit)
		}
		return body, nil
	}
}

type updateConfig struct {
	currentVersion string
	goos           string
	goarch         string
	executable     string
	latestURL      string
	download       updateDownloader
}

func runUpdate(args []string, out io.Writer) error {
	if len(args) != 0 {
		return fmt.Errorf("update takes no arguments")
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	return performUpdate(updateConfig{
		currentVersion: version,
		goos:           runtime.GOOS,
		goarch:         runtime.GOARCH,
		executable:     executable,
		latestURL:      latestReleaseURL,
		download:       defaultUpdateDownloader(),
	}, out)
}

func performUpdate(cfg updateConfig, out io.Writer) error {
	if cfg.download == nil {
		return fmt.Errorf("update downloader is required")
	}
	if cfg.executable == "" {
		return fmt.Errorf("update executable path is required")
	}
	if _, _, err := updateAssetNames("version", cfg.goos, cfg.goarch); err != nil {
		return err
	}

	body, err := cfg.download(cfg.latestURL, maxReleaseJSON, "latest release")
	if err != nil {
		return err
	}
	var release updateRelease
	if err := json.Unmarshal(body, &release); err != nil {
		return fmt.Errorf("decode latest release: %w", err)
	}
	latest := normalizeUpdateVersion(release.TagName)
	if latest == "" {
		return fmt.Errorf("latest release did not include a tag")
	}
	current := normalizeUpdateVersion(cfg.currentVersion)
	if current != "" && current != "dev" {
		if order, ok := compareUpdateVersions(current, latest); ok {
			switch {
			case order == 0:
				fmt.Fprintf(out, "deja is already up to date (v%s)\n", latest)
				return nil
			case order > 0:
				fmt.Fprintf(out, "deja v%s is newer than the latest stable release (v%s)\n", current, latest)
				return nil
			}
		} else if current == latest {
			fmt.Fprintf(out, "deja is already up to date (v%s)\n", latest)
			return nil
		}
	}

	archiveName, binaryName, err := updateAssetNames(latest, cfg.goos, cfg.goarch)
	if err != nil {
		return err
	}
	archiveAsset, ok := findUpdateAsset(release.Assets, archiveName)
	if !ok {
		return fmt.Errorf("release v%s has no asset for %s/%s", latest, cfg.goos, cfg.goarch)
	}
	checksumsAsset, ok := findUpdateAsset(release.Assets, "checksums.txt")
	if !ok {
		return fmt.Errorf("release v%s has no checksums.txt", latest)
	}

	checksums, err := cfg.download(checksumsAsset.URL, maxChecksums, "checksums.txt")
	if err != nil {
		return err
	}
	want, err := checksumForArchive(checksums, archiveName)
	if err != nil {
		return err
	}
	archive, err := cfg.download(archiveAsset.URL, maxArchive, archiveName)
	if err != nil {
		return err
	}
	actual := sha256.Sum256(archive)
	if !strings.EqualFold(hex.EncodeToString(actual[:]), want) {
		return fmt.Errorf("checksum mismatch for %s", archiveName)
	}

	binary, err := extractUpdateBinary(archive, binaryName, cfg.goos)
	if err != nil {
		return fmt.Errorf("extract %s: %w", archiveName, err)
	}
	if err := installUpdateBinary(cfg.executable, binary); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("replace %s: %w; rerun with permission to write its install directory or use your package manager", cfg.executable, err)
		}
		return fmt.Errorf("replace %s: %w", cfg.executable, err)
	}
	from := current
	if from == "" {
		from = "unknown"
	}
	if from != "dev" && from != "unknown" {
		from = "v" + from
	}
	fmt.Fprintf(out, "updated deja %s -> v%s at %s\n", from, latest, cfg.executable)
	return nil
}

func normalizeUpdateVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

type parsedUpdateVersion struct {
	core       [3]int
	prerelease []string
}

// compareUpdateVersions returns -1, 0, or 1 when a is older, equal to, or
// newer than b. Release tags use SemVer, so refusing a downgrade is safe.
func compareUpdateVersions(a, b string) (int, bool) {
	left, ok := parseUpdateVersion(a)
	if !ok {
		return 0, false
	}
	right, ok := parseUpdateVersion(b)
	if !ok {
		return 0, false
	}
	for i := range left.core {
		if left.core[i] < right.core[i] {
			return -1, true
		}
		if left.core[i] > right.core[i] {
			return 1, true
		}
	}
	if len(left.prerelease) == 0 && len(right.prerelease) == 0 {
		return 0, true
	}
	if len(left.prerelease) == 0 {
		return 1, true
	}
	if len(right.prerelease) == 0 {
		return -1, true
	}
	for i := 0; i < len(left.prerelease) && i < len(right.prerelease); i++ {
		if left.prerelease[i] == right.prerelease[i] {
			continue
		}
		ln, lerr := strconv.Atoi(left.prerelease[i])
		rn, rerr := strconv.Atoi(right.prerelease[i])
		switch {
		case lerr == nil && rerr == nil:
			if ln < rn {
				return -1, true
			}
			return 1, true
		case lerr == nil:
			return -1, true
		case rerr == nil:
			return 1, true
		case left.prerelease[i] < right.prerelease[i]:
			return -1, true
		default:
			return 1, true
		}
	}
	if len(left.prerelease) < len(right.prerelease) {
		return -1, true
	}
	if len(left.prerelease) > len(right.prerelease) {
		return 1, true
	}
	return 0, true
}

func parseUpdateVersion(v string) (parsedUpdateVersion, bool) {
	var parsed parsedUpdateVersion
	v = normalizeUpdateVersion(v)
	v = strings.SplitN(v, "+", 2)[0]
	parts := strings.SplitN(v, "-", 2)
	core := strings.Split(parts[0], ".")
	if len(core) != len(parsed.core) {
		return parsed, false
	}
	for i, part := range core {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return parsed, false
		}
		parsed.core[i] = n
	}
	if len(parts) == 2 {
		if parts[1] == "" {
			return parsed, false
		}
		parsed.prerelease = strings.Split(parts[1], ".")
		for _, identifier := range parsed.prerelease {
			if identifier == "" {
				return parsedUpdateVersion{}, false
			}
		}
	}
	return parsed, true
}

func updateAssetNames(version, goos, goarch string) (archive, binary string, err error) {
	if goos != "darwin" && goos != "linux" && goos != "windows" {
		return "", "", fmt.Errorf("updates are not available for %s/%s", goos, goarch)
	}
	if goarch != "amd64" && goarch != "arm64" {
		return "", "", fmt.Errorf("updates are not available for %s/%s", goos, goarch)
	}
	ext := ".tar.gz"
	binary = "deja"
	if goos == "windows" {
		ext = ".zip"
		binary = "deja.exe"
	}
	return fmt.Sprintf("deja-vu_%s_%s_%s%s", version, goos, goarch, ext), binary, nil
}

func findUpdateAsset(assets []updateAsset, name string) (updateAsset, bool) {
	for _, asset := range assets {
		if asset.Name == name && asset.URL != "" {
			return asset, true
		}
	}
	return updateAsset{}, false
}

func checksumForArchive(checksums []byte, archiveName string) (string, error) {
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != archiveName {
			continue
		}
		if len(fields[0]) != sha256.Size*2 {
			return "", fmt.Errorf("invalid checksum for %s", archiveName)
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			return "", fmt.Errorf("invalid checksum for %s", archiveName)
		}
		return fields[0], nil
	}
	return "", fmt.Errorf("checksum entry not found for %s", archiveName)
}

func extractUpdateBinary(archive []byte, binaryName, goos string) ([]byte, error) {
	if goos == "windows" {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, file := range zr.File {
			if path.Base(file.Name) != binaryName || file.FileInfo().IsDir() {
				continue
			}
			if file.UncompressedSize64 > maxExecutable {
				return nil, fmt.Errorf("%s exceeds %d bytes", binaryName, maxExecutable)
			}
			r, err := file.Open()
			if err != nil {
				return nil, err
			}
			binary, readErr := readUpdateBinary(r, binaryName)
			closeErr := r.Close()
			if readErr != nil {
				return nil, readErr
			}
			if closeErr != nil {
				return nil, closeErr
			}
			return binary, nil
		}
		return nil, fmt.Errorf("archive did not contain %s", binaryName)
	}

	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if path.Base(header.Name) != binaryName || !header.FileInfo().Mode().IsRegular() {
			continue
		}
		if header.Size > maxExecutable {
			return nil, fmt.Errorf("%s exceeds %d bytes", binaryName, maxExecutable)
		}
		return readUpdateBinary(tr, binaryName)
	}
	return nil, fmt.Errorf("archive did not contain %s", binaryName)
}

func readUpdateBinary(r io.Reader, name string) ([]byte, error) {
	binary, err := io.ReadAll(io.LimitReader(r, maxExecutable+1))
	if err != nil {
		return nil, err
	}
	if len(binary) == 0 {
		return nil, fmt.Errorf("%s is empty", name)
	}
	if len(binary) > maxExecutable {
		return nil, fmt.Errorf("%s exceeds %d bytes", name, maxExecutable)
	}
	return binary, nil
}

func installUpdateBinary(destination string, binary []byte) (err error) {
	info, err := os.Stat(destination)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("destination is not a regular file")
	}
	staged, err := os.CreateTemp(filepath.Dir(destination), ".deja-update-*")
	if err != nil {
		return err
	}
	stagedPath := staged.Name()
	defer func() {
		_ = staged.Close()
		_ = os.Remove(stagedPath)
	}()
	if _, err := staged.Write(binary); err != nil {
		return err
	}
	if err := staged.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	if err := staged.Sync(); err != nil {
		return err
	}
	if err := staged.Close(); err != nil {
		return err
	}
	return replaceExecutable(stagedPath, destination)
}
