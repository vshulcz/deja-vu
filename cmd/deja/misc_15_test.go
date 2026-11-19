package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/termwidth"
	"github.com/vshulcz/deja-vu/internal/usage"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// A volume that was ejected takes its mount point with it, and creating that
// point back fails with EPERM on macOS — so the errno said permissions while
// the disk was simply gone, and doctor called the index missing and told the
// reader to rebuild it onto a path that is not there (#931).
func TestAnUnpluggedIndexDiskIsNotPermissionsOrAMissingIndex(t *testing.T) {
	tmp := hermeticEnv(t)
	// The parent stands in for the mount point: it is not there at all.
	dir := filepath.Join(tmp, "gone-volume", "index.db")

	err := ensureError(dir, os.ErrPermission)
	if err == nil || !strings.Contains(err.Error(), "is not there") || !strings.Contains(err.Error(), "unmounted") {
		t.Errorf("a vanished mount point was reported as: %v", err)
	}

	var out bytes.Buffer
	doctorIndex(&out, doctorIndexReport{State: "missing", Path: dir}, dir)
	if !strings.Contains(out.String(), "not reachable") {
		t.Errorf("doctor on an unreachable index:\n%s", out.String())
	}
	if strings.Contains(out.String(), "run `deja warmup`") {
		t.Errorf("doctor told the reader to rebuild onto a path that is not there:\n%s", out.String())
	}

	// A directory that is there and merely locked is still a permissions
	// problem, and an index that is simply absent is still "not built".
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	indexPath := filepath.Join(tmp, "here", "index.db")
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensureError(indexPath, os.ErrPermission); err == nil || !strings.Contains(err.Error(), "permissions") {
		t.Errorf("a locked directory stopped being a permissions problem: %v", err)
	}
	out.Reset()
	doctorIndex(&out, doctorIndexReport{State: "missing", Path: indexPath}, indexPath)
	if !strings.Contains(out.String(), "not built") {
		t.Errorf("an absent index stopped saying so:\n%s", out.String())
	}
}

// Removing a note promoted by mistake is `forget` on the note's own id, and
// nothing said so: the reader who types `unpromote` was sent back to the
// command that made the note, and the --state refusal explained marks (#1085).
func TestTheUndoOfAPromoteIsNamed(t *testing.T) {
	for _, word := range []string{"unpromote", "demote"} {
		got := commandHint(word)
		if !strings.Contains(got, "deja forget --session deja-note-") {
			t.Errorf("%q: the removal path is not named: %q", word, got)
		}
		if !strings.Contains(got, "--state rejected") {
			t.Errorf("%q: taking the decision back is not named: %q", word, got)
		}
	}
	// A word that means something else keeps its own redirect.
	if got := commandHint("unforget"); !strings.Contains(got, "--unforget <id>") {
		t.Errorf("unforget lost its own hint: %q", got)
	}
}

// A home whose cache directory refuses writes is a locked-down laptop, not an
// ejected disk and not a rebuild in flight — and deja told both of those
// stories, sending the reader to reconnect a disk that is present and to wait
// for a rebuild that will never finish (#2267).
func TestAnUnwritableCacheIsNotAnUnmountedDisk(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not deny creation the same way on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes through the permission")
	}
	tmp := t.TempDir()
	cache := filepath.Join(tmp, "cache")
	if err := os.MkdirAll(cache, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(cache, 0o700) })
	dir := filepath.Join(cache, "deja", "index.db")

	// The premise: the build really is refused here.
	err := index.Ensure(dir, "", false, nil)
	if err == nil {
		t.Fatal("the build succeeded under an unwritable cache")
	}

	said := ensureError(dir, err).Error()
	if strings.Contains(said, "unmounted") {
		t.Errorf("an unwritable directory was reported as an unmounted disk: %s", said)
	}
	if !strings.Contains(said, "permission") && !strings.Contains(said, "writable") {
		t.Errorf("the refusal does not name what is wrong: %s", said)
	}

	// And nothing is being rebuilt: the lock could not be created, which is not
	// the same as somebody holding it.
	if index.RebuildInProgress(dir) {
		t.Error("an unwritable index directory reads as a rebuild in progress")
	}
}

// One state, three stories: search worded it, stats handed back the syscall,
// and doctor called it ordinary staleness and advised the one command that
// cannot succeed here (#1004).
func TestAnUnwritableIndexReadsTheSameEverywhere(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny writes here")
	}
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"local work on the ticker window"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"loc","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "loc.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(tmp, "locked")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(parent, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// A store that changed after the build, and no way to write the result.
	// Rewritten rather than grown: since #3500 a file appearing or growing is
	// appended inside the index directory, which the parent's mode still
	// allows. A rewind is what needs the replacement path, and the replacement
	// path is what needs a temp directory beside the index.
	rewound := `{"type":"user","message":{"role":"user","content":"the ticker window, rewritten after the parent was locked"},"timestamp":"2026-08-04T12:00:00Z","sessionId":"loc","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "loc.jsonl"), []byte(rewound), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

	_, err := captureRun(t, "stats")
	if err == nil {
		t.Fatal("stats did not fail on an index it cannot write")
	}
	if strings.Contains(err.Error(), "mkdir") || strings.Contains(err.Error(), ".tmp") {
		t.Errorf("stats hands back the syscall: %v", err)
	}
	if !strings.Contains(err.Error(), "cannot write the index") {
		t.Errorf("stats does not word the failure like the other paths: %v", err)
	}

	var out bytes.Buffer
	doctorIndexSection(t, &out, dir)
	line := ""
	for l := range strings.SplitSeq(out.String(), "\n") {
		if strings.Contains(l, "freshness") {
			line = l
		}
	}
	if !strings.Contains(line, "cannot be written") {
		t.Errorf("doctor calls an unwritable index ordinarily stale: %q", line)
	}
	if strings.Contains(line, "run `deja index`") {
		t.Errorf("doctor advises the command that fails in this state: %q", line)
	}

	// With the directory writable again the ordinary advice comes back.
	if err := os.Chmod(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	doctorIndexSection(t, &out, dir)
	if !strings.Contains(out.String(), "run `deja index`") {
		t.Errorf("a writable index lost the advice that fits it:\n%s", out.String())
	}
}

func doctorIndexSection(t *testing.T, out *bytes.Buffer, dir string) {
	t.Helper()
	if err := runDoctor(out, nil, stubLookup("1.0.0", true), dir); err != nil {
		t.Fatal(err)
	}
}

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
	download := func(_ string, _ int64, label string) ([]byte, error) { //nolint:unparam // must match updateDownloader
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
		if err = os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(dir, "deja")
		if err = os.WriteFile(dest, []byte("old"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err = os.Chmod(dir, 0o555); err != nil {
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

// A binary built from source is by definition ahead of the newest tag — it is
// whatever the person just compiled — so replacing it in place is a downgrade
// that discards their build (#751).
func TestPerformUpdateLeavesADevBuildAlone(t *testing.T) {
	target := filepath.Join(t.TempDir(), "deja")
	if err := os.WriteFile(target, []byte("source build"), 0o755); err != nil {
		t.Fatal(err)
	}
	download, requests := newUpdateDownloader(t, "linux", []byte("release binary"), false)

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
	download, _ := newUpdateDownloader(t, "linux", []byte("release binary"), false)

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
	download := func(_ string, _ int64, label string) ([]byte, error) { //nolint:unparam // must match updateDownloader
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

// The upgrade line exists so the user runs it instead of --force, so a name
// that resolves to nothing sends them straight back to overwriting a managed
// binary: `npm update -g deja-vu` answered 404 while the published package is
// `@vshulcz/deja-vu` (#915). These names live in the shipped manifests, so the
// advice is checked against them rather than against a second copy of the
// string.
func TestManagerAdviceNamesThePackageTheManifestsShip(t *testing.T) {
	repo := filepath.Join("..", "..")

	scoop, err := filepath.Glob(filepath.Join(repo, "packaging", "scoop", "*.json"))
	if err != nil || len(scoop) != 1 {
		t.Fatalf("scoop manifests = %v (%v), want exactly one", scoop, err)
	}
	app := strings.TrimSuffix(filepath.Base(scoop[0]), ".json")
	if _, cmd := packageManagerOwning(`C:\Users\v\scoop\apps\deja\current\deja.exe`); cmd != "scoop update "+app {
		t.Errorf("scoop advice = %q, manifest ships %q", cmd, app)
	}

	winget, err := os.ReadFile(filepath.Join(repo, "packaging", "winget", "vshulcz.deja-vu.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	id := ""
	for line := range strings.SplitSeq(string(winget), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "PackageIdentifier:"); ok {
			id = strings.TrimSpace(rest)
			break
		}
	}
	if id == "" {
		t.Fatal("no PackageIdentifier in the winget manifest")
	}
	if _, cmd := packageManagerOwning(`C:\Users\v\AppData\Local\winget\packages\vshulcz.deja-vu\deja.exe`); cmd != "winget upgrade "+id {
		t.Errorf("winget advice = %q, manifest ships %q", cmd, id)
	}

	// npm's name is the one in the package it publishes, scope included.
	pkg, err := os.ReadFile(filepath.Join(repo, "npm", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	name := ""
	for line := range strings.SplitSeq(string(pkg), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), `"name":`); ok {
			name = strings.Trim(strings.TrimSpace(rest), `",`)
			break
		}
	}
	if name == "" {
		t.Fatal("no name in npm/package.json")
	}
	if _, cmd := packageManagerOwning("/usr/lib/node_modules/deja-vu/bin/deja"); cmd != "npm update -g "+name {
		t.Errorf("npm advice = %q, the package publishes %q", cmd, name)
	}
}

// Writing into a package manager's tree succeeds and then comes undone: the
// manager still records the old version and its next upgrade overwrites the
// file, so the user sees an update that did not stick (#775).
var errStopUpdateForTest = errors.New("stop")

func TestUpdateDefersToThePackageManagerThatOwnsTheBinary(t *testing.T) {
	cases := []struct {
		exe     string
		manager string
		command string
	}{
		{"/opt/homebrew/Cellar/deja-vu/0.16.5/bin/deja", "Homebrew", "brew upgrade deja-vu"},
		{"/home/linuxbrew/.linuxbrew/bin/deja", "Homebrew", "brew upgrade deja-vu"},
		{`C:\Users\v\scoop\apps\deja\current\deja.exe`, "scoop", "scoop update deja-vu"},
		{"/nix/store/abc123-deja-0.16.5/bin/deja", "Nix", "nix profile upgrade deja"},
		// README has promised this since the npm package shipped: "deja update
		// is for standalone installs. Homebrew and npm installs update through
		// the package manager." Nothing enforced it.
		// The name the registry knows, not the name of the binary: `npm update
		// -g deja-vu` answers 404, and the published package is scoped (#915).
		{"/usr/lib/node_modules/deja-vu/bin/deja", "npm", "npm update -g @vshulcz/deja-vu"},
		{`C:\Users\v\AppData\Local\winget\packages\vshulcz.deja-vu\deja.exe`, "winget", "winget upgrade vshulcz.deja-vu"},
	}
	for _, tc := range cases {
		var out bytes.Buffer
		err := performUpdate(updateConfig{
			currentVersion: "0.1.0",
			goos:           "linux",
			goarch:         "amd64",
			executable:     tc.exe,
			download: func(string, int64, string) ([]byte, error) {
				t.Errorf("%s: downloaded a release for a managed binary", tc.exe)
				return nil, nil
			},
		}, &out)
		if err != nil {
			t.Errorf("%s: %v", tc.exe, err)
		}
		if !strings.Contains(out.String(), tc.manager) || !strings.Contains(out.String(), tc.command) {
			t.Errorf("%s: want %q and %q, got %q", tc.exe, tc.manager, tc.command, out.String())
		}
	}

	// --force is the documented escape hatch, so it must still reach the
	// download; and an ordinary path must not be mistaken for a managed one.
	for _, tc := range []struct {
		name string
		cfg  updateConfig
	}{
		{"force", updateConfig{force: true, currentVersion: "0.1.0", goos: "linux", goarch: "amd64", executable: "/opt/homebrew/Cellar/deja-vu/0.16.5/bin/deja"}},
		{"plain path", updateConfig{currentVersion: "0.1.0", goos: "linux", goarch: "amd64", executable: filepath.FromSlash("/usr/local/bin/deja")}},
	} {
		reached := false
		var out bytes.Buffer
		_ = performUpdate(updateConfig{
			force: tc.cfg.force, currentVersion: tc.cfg.currentVersion,
			goos: tc.cfg.goos, goarch: tc.cfg.goarch, executable: tc.cfg.executable,
			download: func(string, int64, string) ([]byte, error) {
				reached = true
				return nil, errStopUpdateForTest
			},
		}, &out)
		if !reached {
			t.Errorf("%s: stopped before the download: %q", tc.name, out.String())
		}
	}
}

func stagingsIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".deja-update-") {
			out = append(out, e.Name())
		}
	}
	return out
}

func updateInto(t *testing.T, dir string) {
	t.Helper()
	destination := filepath.Join(dir, "deja")
	if err := os.WriteFile(destination, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installUpdateBinary(destination, []byte("new binary")); err != nil {
		t.Fatal(err)
	}
}

func strandStaging(t *testing.T, dir, name string, age time.Duration) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(p, when, when); err != nil {
		t.Fatal(err)
	}
}

// An update killed between staging and rename strands a binary's worth of bytes
// beside the real one, and every later update added another rather than
// clearing it (#1109).
func TestUpdateClearsStagingsLeftByAnInterruptedRun(t *testing.T) {
	dir := t.TempDir()
	strandStaging(t, dir, ".deja-update-123456", 48*time.Hour)
	updateInto(t, dir)
	if left := stagingsIn(t, dir); len(left) != 0 {
		t.Errorf("staging files survived a successful update: %v", left)
	}
}

// A staging file written moments ago may belong to an update running right now,
// and deleting it would break that run.
func TestUpdateLeavesAFreshStagingAlone(t *testing.T) {
	dir := t.TempDir()
	strandStaging(t, dir, ".deja-update-999999", time.Minute)
	updateInto(t, dir)
	if left := stagingsIn(t, dir); len(left) != 1 {
		t.Errorf("a staging file from a run in flight was touched: %v", left)
	}
}

// The sweep knows only its own litter: anything else beside the binary stays.
func TestUpdateSweepsOnlyItsOwnFiles(t *testing.T) {
	dir := t.TempDir()
	keep := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(keep, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(keep, old, old); err != nil {
		t.Fatal(err)
	}
	updateInto(t, dir)
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("a file that is not deja's was removed: %v", err)
	}
}

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
			download, requests := newUpdateDownloader(t, goos, []byte("new binary"), false)

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
	download, requests := newUpdateDownloader(t, "linux", []byte("unused"), false)

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

func TestPerformUpdateFromCommitDevVersion(t *testing.T) {
	target := filepath.Join(t.TempDir(), "deja")
	if err := os.WriteFile(target, []byte("dev binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	download, _ := newUpdateDownloader(t, "linux", []byte("release binary"), false)
	var out bytes.Buffer
	err := performUpdate(updateConfig{
		currentVersion: "dev-795f6e6",
		goos:           "linux",
		goarch:         "amd64",
		executable:     target,
		latestURL:      testLatestReleaseURL,
		download:       download,
	}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "updated deja dev-795f6e6 -> v1.2.0") || strings.Contains(out.String(), "vdev-") {
		t.Fatalf("output = %q, want 'updated deja dev-795f6e6 -> v1.2.0'", out.String())
	}
}

func TestPerformUpdateDoesNotDowngrade(t *testing.T) {
	target := filepath.Join(t.TempDir(), "deja")
	if err := os.WriteFile(target, []byte("newer binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	download, requests := newUpdateDownloader(t, "linux", []byte("older binary"), false)

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
	download, _ := newUpdateDownloader(t, "linux", []byte("new binary"), true)

	err := performUpdate(updateConfig{
		// The subject here is the checksum guard, not the dev-build policy
		// added in #751.
		force:          true,
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
			return nil, errors.New("service unavailable")
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
	out, err := captureRun(t, "help")
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

func TestHTTPUpdateDownloader(t *testing.T) {
	insecure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "non-HTTPS redirect was followed", http.StatusInternalServerError)
	}))
	defer insecure.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/vnd.github+json" || !strings.HasPrefix(r.Header.Get("User-Agent"), "deja/") {
			t.Errorf("headers = %#v", r.Header)
		}
		switch r.URL.Path {
		case "/ok":
			_, _ = io.WriteString(w, "release")
		case "/error":
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		case "/redirect":
			http.Redirect(w, r, insecure.URL, http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	download := newHTTPUpdateDownloader(server.Client())
	body, err := download(server.URL+"/ok", 32, "test asset")
	if err != nil || string(body) != "release" {
		t.Fatalf("download body=%q err=%v", body, err)
	}
	if _, err := download(server.URL+"/ok", 3, "test asset"); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("size limit error = %v", err)
	}
	if _, err := download(server.URL+"/error", 32, "test asset"); err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("HTTP status error = %v", err)
	}
	if _, err := download("http://example.invalid", 32, "test asset"); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("insecure URL error = %v", err)
	}
	if _, err := download(server.URL+"/redirect", 32, "test asset"); err == nil || !strings.Contains(err.Error(), "non-HTTPS") {
		t.Fatalf("insecure redirect error = %v", err)
	}
}

func newUpdateDownloader(t *testing.T, goos string, binary []byte, badChecksum bool) (updateDownloader, *atomic.Int32) {
	t.Helper()
	const version, goarch = "1.2.0", "amd64"
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

func TestPerformUpdateRefreshesOutdatedPluginScripts(t *testing.T) {
	tmp := hermeticEnv(t)
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(tmp, "deja")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	piPath := filepath.Join(sources.PiConfigDir(), "extensions", "deja.ts")
	if err := os.MkdirAll(filepath.Dir(piPath), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := "// generated by deja install — safe to delete\n// hook-context only, pre-hook-prompt\n"
	if err := os.WriteFile(piPath, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	download, _ := newUpdateDownloader(t, "linux", []byte("new binary"), false)
	var out bytes.Buffer
	err := performUpdate(updateConfig{
		refreshPlugins: true,
		currentVersion: "1.1.0",
		goos:           "linux",
		goarch:         "amd64",
		executable:     target,
		latestURL:      testLatestReleaseURL,
		download:       download,
	}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "refreshed pi plugin") {
		t.Fatalf("expected performUpdate to report refreshed pi plugin, got:\n%s", out.String())
	}
	updated, err := os.ReadFile(piPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "hook-prompt") {
		t.Fatalf("expected refreshed pi plugin to contain hook-prompt:\n%s", string(updated))
	}
}

// `usage.EventRoom` is how large one event may be for the log's own rotation to
// leave it under its threshold — the relationship `internal/usage` states. This
// is the half that holds the writers to it: a real recall over a store with
// many sessions, whose ids are what makes an event grow.
func TestARealEventFitsItsRoom(t *testing.T) {
	tmp := t.TempDir()
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	// Shaped for the worst case rather than around it: an event grows with the
	// number of ids, an answer holds more sessions when each says less, and an
	// id is as long as a filename may be. Long turns would cut the answer to a
	// handful of sessions and measure nothing.
	for i := range 60 {
		id := strings.Repeat("a", 246) + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26))
		seedClaude(t, claude, "app", id, "pgbouncer timed out", "we retried")
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	if _, err := callMCPTool(index.DefaultDir(), "recall", json.RawMessage(`{"query":"pgbouncer","limit":100}`)); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(usage.Path(index.DefaultDir()))
	if err != nil {
		t.Fatal(err)
	}
	var widest int
	var carried int
	for line := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
		var e usage.Event
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		if len(line) > widest {
			widest, carried = len(line), len(e.SessionIDs)
		}
	}
	if widest == 0 {
		t.Fatal("nothing was recorded, so this proves nothing")
	}
	// The ids are what grows, so the count is what the failure has to name: the
	// number of sessions seeded says nothing about how many the answer held.
	if carried < 2 {
		t.Fatalf("the widest event carried %d ids, so it measures nothing", carried)
	}
	if widest > usage.EventRoom {
		t.Errorf("a recall wrote an event of %d bytes against %d of room, carrying %d ids of 248 characters",
			widest, usage.EventRoom, carried)
	}
	t.Logf("widest event: %d bytes of %d, carrying %d ids", widest, usage.EventRoom, carried)
}

// The usage log is appended to by every recall and read by `deja log`, the
// status bar, the first screen and stats. `statusline` was hardened against ten
// shapes of broken stdin; nothing held the log itself to the same standard,
// though it is the file most likely to be caught mid-write.
//
// Each shape keeps one good line, so this asks two things at once: that no
// reader fails, and that the corruption does not take the record beside it.
func TestEveryReaderSurvivesABrokenUsageLog(t *testing.T) {
	// A size no other row carries, so "the good line survived" cannot be
	// satisfied by something else on the screen.
	good := fmt.Sprintf(`{"t":%q,"kind":"hook","bytes":8181,"sessions":2}`,
		time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano))
	future := fmt.Sprintf(`{"t":%q,"kind":"hook","bytes":100,"sessions":1}`,
		time.Now().AddDate(1, 0, 0).UTC().Format(time.RFC3339Nano))

	for _, c := range []struct{ name, body string }{
		{"a line torn mid-write", good + "\n" + `{"t":"2026-08-2`},
		{"a run of NULs", good + "\n" + strings.Repeat("\x00", 64) + "\n"},
		{"a stamp in the future", good + "\n" + future + "\n"},
		{"no newline at the end", good},
		{"a line that is not JSON", good + "\nnot json at all\n"},
		{"a line of a megabyte", good + "\n" +
			`{"t":"2026-08-26T00:00:00Z","kind":"hook","digest":"` + strings.Repeat("x", 1<<20) + `"}` + "\n"},
		// Valid JSON and half a record: docs/json-output.md says a line needs
		// both `t` and `kind` to appear at all, "rather than shown with a
		// missing half".
		{"a line with no kind", good + "\n" + `{"t":"2026-08-26T00:00:00Z","bytes":42}` + "\n"},
		{"a line with no stamp", good + "\n" + `{"kind":"hook","bytes":42}` + "\n"},
		// A line from a newer deja, carrying keys this one does not know: the
		// same document says the log keeps what it was given.
		{"a line with unknown keys", good + "\n" +
			`{"t":"2026-08-26T00:00:00Z","kind":"hook","bytes":7,"weather":"rain"}` + "\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			hermeticEnv(t)
			dir := index.DefaultDir()
			if err := index.Ensure(dir, "", true, nil); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(usage.Path(dir), []byte(c.body), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"log"}, {"stats"}, {"statusline"}, {"brief"}} {
				out, err := captureRun(t, args...)
				if err != nil {
					t.Errorf("%v failed: %v", args, err)
				}
				if strings.TrimSpace(out) == "" {
					t.Errorf("%v printed nothing", args)
				}
			}
			// The good line is still there: a broken neighbour must not take
			// the record beside it.
			out, err := captureRun(t, "log")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "8.0 KB") && !strings.Contains(out, "8181") {
				t.Errorf("the good line did not survive:\n%s", out)
			}
		})
	}
}

// An empty log is a state, not a corruption, and every reader says so rather
// than failing.
func TestAnEmptyUsageLogReadsAsEmpty(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(usage.Path(dir), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "log")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no usage recorded yet") {
		t.Errorf("an empty log does not read as empty:\n%s", out)
	}
}

// Half a record is not a record. The log keeps what it was given, but a line
// needs both a stamp and a kind to be shown at all — docs/json-output.md says
// so, "rather than shown with a missing half" — and a line from a newer deja
// carrying keys this one does not know is still a line.
func TestTheLogShowsWholeRecordsAndKeepsUnknownKeys(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	body := strings.Join([]string{
		`{"t":"2026-08-26T01:00:00Z","kind":"hook","bytes":4242}`,
		`{"t":"2026-08-26T02:00:00Z","bytes":1717}`,
		`{"kind":"hook","bytes":1818}`,
		`{"t":"2026-08-26T03:00:00Z","kind":"hook","bytes":9393,"weather":"rain"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(usage.Path(dir), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "log")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "4.1 KB") {
		t.Errorf("a whole record is missing:\n%s", out)
	}
	if !strings.Contains(out, "9.2 KB") {
		t.Errorf("a record with an unknown key was dropped:\n%s", out)
	}
	for _, half := range []string{"1.7 KB", "1.8 KB"} {
		if strings.Contains(out, half) {
			t.Errorf("a line missing its stamp or its kind was shown (%s):\n%s", half, out)
		}
	}
}

// A usage event stamped later than this machine's clock: `deja log` shows it,
// and the counters leave it out. Pinned rather than argued — it is the same
// shape #2104 named for sessions, and the decision about it is open.
func TestAFutureUsageEventIsShownButNotCounted(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	future := time.Now().AddDate(1, 0, 0).UTC().Format(time.RFC3339Nano)
	body := fmt.Sprintf(`{"t":%q,"kind":"hook","bytes":5151,"sessions":1}`, future) + "\n"
	if err := os.WriteFile(usage.Path(dir), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "log")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "5.0 KB") {
		t.Errorf("the log hides an event stamped ahead:\n%s", out)
	}
	bar, err := captureRun(t, "statusline")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(bar, "5.0 KB") {
		t.Errorf("the bar counted an event that has not happened: %q", bar)
	}
	// The premise: the same event, stamped an hour ago, is counted — otherwise
	// the absence above says nothing about the future stamp.
	// An hour ago, unless an hour ago was yesterday: the bar counts from local
	// midnight, so between midnight and one in the morning this fixture landed
	// outside the window it is meant to be inside.
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	at := now.Add(-time.Hour)
	if at.Before(midnight) {
		at = midnight.Add(time.Second)
	}
	past := at.UTC().Format(time.RFC3339Nano)
	if err = os.WriteFile(usage.Path(dir), []byte(fmt.Sprintf(`{"t":%q,"kind":"hook","bytes":5151,"sessions":1}`, past)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bar, err = captureRun(t, "statusline")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bar, "5.0 KB") {
		t.Fatalf("the bar does not report an ordinary event either, so this measures nothing: %q", bar)
	}
}

// A volume that was unmounted fails with EACCES on macOS, because deja is then
// trying to create a directory under /Volumes — and "check that file's
// permissions" sends the reader after a problem they do not have (#907).
func TestAVanishedDirectoryIsNotAPermissionProblem(t *testing.T) {
	tmp := hermeticEnv(t)
	gone := filepath.Join(tmp, "not-mounted", "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", gone)

	err := notesWriteError(os.ErrPermission)
	if err == nil {
		t.Fatal("no error")
	}
	got := err.Error()
	if !strings.Contains(got, "is not there") || !strings.Contains(got, "unmounted") {
		t.Errorf("a missing directory was described as: %q", got)
	}
	if strings.Contains(got, "check that file and its directory's permissions") {
		t.Errorf("still sends the reader after permissions: %q", got)
	}

	// A directory that is there keeps the permission wording.
	live := filepath.Join(tmp, "live")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(live, "notes.jsonl"))
	if got := notesWriteError(os.ErrPermission).Error(); !strings.Contains(got, "permissions") {
		t.Errorf("a live directory lost the permission wording: %q", got)
	}
	if notesWriteError(nil) != nil {
		t.Error("a nil error was turned into one")
	}
	if !dirExists(live) || dirExists(filepath.Join(tmp, "nope")) {
		t.Error("dirExists disagrees with the filesystem")
	}
}

// `deja stats --json` reports four week figures and the document never said
// what a week is; the rules lived only in Go, and until #1921 deja itself did
// not agree with them (#1939). This holds the sentence against the code it
// describes rather than against itself.
func TestTheStatsSectionSaysWhatAWeekIs(t *testing.T) {
	doc, err := os.ReadFile("../../docs/json-output.md")
	if err != nil {
		t.Fatal(err)
	}
	section := docSection(t, string(doc), "## `deja stats --json`")
	prose := strings.Join(strings.Fields(section), " ")

	for _, want := range []struct{ what, phrase string }{
		{"a week is seven calendar days", "seven calendar days"},
		{"a day starts at local midnight", "local midnight"},
		{"the wall clock is what is kept across a change", "wall clock"},
	} {
		if !strings.Contains(prose, want.phrase) {
			t.Errorf("the stats section does not say %s (looked for %q)", want.what, want.phrase)
		}
	}

	// And the sentence has to keep describing what WeekCut does: seven calendar
	// days at the same wall time, not a fixed number of hours.
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata: ", err)
	}
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, ny)
	if cut := usage.WeekCut(now); cut.Format("15:04:05") != now.Format("15:04:05") {
		t.Errorf("WeekCut no longer keeps the wall clock (%s from %s), so the documented sentence is wrong",
			cut.Format("15:04:05 MST"), now.Format("15:04:05 MST"))
	}
	if cut, fixed := usage.WeekCut(now), now.Add(-7*24*time.Hour); cut.Equal(fixed) {
		t.Error("WeekCut and a fixed 168 hours agree here, so this test proves nothing about the rule")
	}
}

// A bare `deja` into a pipe or a script used to print the full usage block,
// which starts with commands only harnesses call. Issue #4621 wants a short
// welcome instead: what is indexed and one thing to try, with the full list
// kept behind `deja help`. This test pins the welcome shape.
func TestBareDejaPrintsWelcomeNotFullUsage(t *testing.T) {
	hermeticEnv(t)
	out, err := captureRun(t)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Usage:") {
		t.Fatalf("welcome fell back to the full usage block:\n%s", out)
	}
	for _, want := range []string{"nothing indexed yet", "deja <query>", "deja help"} {
		if !strings.Contains(out, want) {
			t.Fatalf("welcome is missing %q:\n%s", want, out)
		}
	}

	withTempStores(t)
	if _, err = captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	out, err = captureRun(t)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "indexed across") {
		t.Fatalf("welcome does not say what is indexed:\n%s", out)
	}
}

// The first run of a new version says once what it brings: a short line plus
// the top three changes from the CHANGELOG since the last version this machine
// ran (#4619).
func TestWhatChangedItemsSaysWhatIsNew(t *testing.T) {
	changelog := `# Changelog

## [0.2.0] - 2026-10-09

### Added

- first new thing
- second new thing
- third new thing
- fourth new thing

### Fixed

- a fix

## [0.1.0] - 2026-10-01

### Added

- old thing
`
	items := whatChangedItems(changelog, "0.1.0", "0.2.0")
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3: %v", len(items), items)
	}
	for i, want := range []string{"first new thing", "second new thing", "third new thing"} {
		if items[i] != want {
			t.Errorf("item %d = %q, want %q", i, items[i], want)
		}
	}
}

// A two-version jump takes the top three items across every section newer than
// the last version this machine ran, not only the current version's section.
func TestWhatChangedItemsAcrossTwoVersions(t *testing.T) {
	changelog := `# Changelog

## [0.3.0] - 2026-10-09

### Added

- newest thing

## [0.2.0] - 2026-10-04

### Added

- middle thing one
- middle thing two

## [0.1.0] - 2026-10-01

### Added

- old thing
`
	// Last ran 0.1.0, now on 0.3.0: the top three come from 0.3.0 and 0.2.0.
	items := whatChangedItems(changelog, "0.1.0", "0.3.0")
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3: %v", len(items), items)
	}
	for i, want := range []string{"newest thing", "middle thing one", "middle thing two"} {
		if items[i] != want {
			t.Errorf("item %d = %q, want %q", i, items[i], want)
		}
	}
}

// The message shows once per version: the same version again says nothing.
func TestWhatChangedShowsOncePerVersion(t *testing.T) {
	changelog := `## [0.2.0] - 2026-10-09

### Added

- a thing
`
	if got := whatChangedItems(changelog, "0.2.0", "0.2.0"); len(got) != 0 {
		t.Errorf("same version said %v, want nothing", got)
	}
	if got := whatChangedItems(changelog, "0.2.0", "0.1.0"); len(got) != 0 {
		t.Errorf("an older version said %v, want nothing", got)
	}
	if got := whatChangedItems(changelog, "", "0.2.0"); len(got) != 0 {
		t.Errorf("a first run ever said %v, want nothing", got)
	}
	if got := whatChangedItems(changelog, "0.1.0", "dev"); len(got) != 0 {
		t.Errorf("a dev build said %v, want nothing", got)
	}
}

// whatChangedTerminal and whatChangedNote share the once-per-version record:
// whichever runs first marks the version shown, so the other says nothing.
func TestWhatChangedTerminalAndNoteShareRecord(t *testing.T) {
	withVersion(t, "0.2.0")
	dir := releaseNoticeEnv(t)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	// The machine last ran 0.1.0.
	if err := os.WriteFile(dir+".lastversion", []byte("0.1.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The terminal runs first and says what is new.
	term := whatChangedTerminal(dir)
	if !strings.Contains(term, "deja v0.2.0 is new") {
		t.Fatalf("terminal said %q, want the new-version line", term)
	}
	if b, _ := os.ReadFile(dir + ".lastversion"); strings.TrimSpace(string(b)) != "0.2.0" {
		t.Errorf("record kept %q, want 0.2.0", b)
	}

	// The hook then says nothing: the version was already shown.
	if got := whatChangedNote(dir); got != "" {
		t.Errorf("the hook said %q after the terminal, want nothing", got)
	}
}

// whatChangedNote is the one-line hook note, and it stays quiet when the
// terminal already showed the version.
func TestWhatChangedNoteOneLine(t *testing.T) {
	withVersion(t, "0.2.0")
	dir := releaseNoticeEnv(t)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+".lastversion", []byte("0.1.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	note := whatChangedNote(dir)
	if !strings.Contains(note, "deja v0.2.0 is new") {
		t.Fatalf("note said %q, want the new-version line", note)
	}
	if strings.Contains(note, "\n") {
		t.Errorf("note is not one line: %q", note)
	}
	// The embedded changelog's bullets run past 140 characters; the note
	// cuts the first to what fits.
	if w := termwidth.Columns(note); w > noteWidth {
		t.Errorf("note is %d columns, over %d: %q", w, noteWidth, note)
	}
	// The terminal then says nothing.
	if got := whatChangedTerminal(dir); got != "" {
		t.Errorf("the terminal said %q after the hook, want nothing", got)
	}
}

// whatChangedFor stays quiet when the index dir is relative, so the stamp
// would land in whatever directory deja was run from.
func TestWhatChangedForRelativeDir(t *testing.T) {
	withVersion(t, "0.2.0")
	if items, _ := whatChangedFor(filepath.Join(".cache", "deja", "index")); len(items) != 0 {
		t.Errorf("a relative index dir said %v, want nothing", items)
	}
}

// The first version this feature sees has nothing to count from, so it stays
// quiet but records itself; the next upgrade is the first to say what changed.
// Without that record the news would never show for anyone upgrading.
func TestWhatChangedFirstRunRecordsQuietly(t *testing.T) {
	withVersion(t, "0.1.0")
	dir := releaseNoticeEnv(t)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if items, _ := whatChangedFor(dir); len(items) != 0 {
		t.Fatalf("a first run said %v, want nothing", items)
	}
	if b, _ := os.ReadFile(dir + ".lastversion"); strings.TrimSpace(string(b)) != "0.1.0" {
		t.Fatalf("a first run recorded %q, want 0.1.0", b)
	}
	withVersion(t, "9.9.9")
	if items, current := whatChangedFor(dir); current != "9.9.9" {
		t.Fatalf("the upgrade after a first run said %v (%q), want the 9.9.9 news", items, current)
	}
}

// A changelog bullet is often a paragraph; the list shows its first sentence.
func TestWhatChangedItemsAreShort(t *testing.T) {
	long := "Recall got faster. It now skips sessions it already read, which on one machine " +
		"cut a cold search from four seconds to under one, and the index stays the same size."
	if got := firstSentence(long); got != "Recall got faster." {
		t.Errorf("firstSentence = %q, want the first sentence", got)
	}
	run := strings.Repeat("word ", 60)
	if got := firstSentence(run); len([]rune(got)) > 140 {
		t.Errorf("firstSentence left %d characters, want at most 140", len([]rune(got)))
	}
}

// A cut never ends inside a code span or an issue reference.
func TestWhatChangedCutKeepsCodeAndReferencesWhole(t *testing.T) {
	item := "Session start shows one note at a time, and `deja hook-context --notes` prints the rest later (#4993) for hosts that render it"
	for width := 20; width < len(item); width++ {
		got := cutChangelogItem(item, width)
		if termwidth.Columns(got) > width {
			t.Fatalf("width %d: %q is %d columns", width, got, termwidth.Columns(got))
		}
		if strings.Count(got, "`")%2 == 1 {
			t.Fatalf("width %d: cut inside the code span: %q", width, got)
		}
		if i := strings.LastIndex(got, "(#"); i >= 0 && !strings.Contains(got[i:], ")") {
			t.Fatalf("width %d: cut inside the reference: %q", width, got)
		}
	}
}

// Most installs arrive through a marketplace, so the README is never opened and
// the install tail is the only thing deja ever says to that person. It said
// nothing about where to read more or where to report something (#3030).
func TestTheInstallProofSaysWhereToLook(t *testing.T) {
	tmp := hermeticEnv(t)
	seedProofSessions(t)
	dir := os.Getenv("DEJA_INDEX_DIR")
	out := captureStderr(t, func() { printInstallProof(dir) })
	if !strings.Contains(out, "deja already knows this machine:") {
		t.Fatalf("the proof did not print, so nothing was measured:\n%s", out)
	}
	if !strings.Contains(out, "card: `deja stats --card`") ||
		!strings.Contains(out, "docs: vshulcz.github.io/deja-vu") ||
		!strings.Contains(out, "issues: github.com/vshulcz/deja-vu/issues") {
		t.Fatalf("the proof does not say where the card, docs and tracker are:\n%s", out)
	}

	// Once per machine: a pointer that repeats is a pointer nobody reads.
	again := captureStderr(t, func() { printInstallProof(dir) })
	if strings.Contains(again, "docs: vshulcz.github.io") {
		t.Fatalf("the line came back on the second run:\n%s", again)
	}
	if !strings.Contains(again, "deja already knows this machine:") {
		t.Fatalf("the proof itself stopped printing with it:\n%s", again)
	}
	_ = tmp
}

// The first-build notice is the only line a marketplace install sees at all, so
// it carries the same pointer — and the two share the marker, so between them
// it is said once.
func TestTheFirstBuildNoticeCarriesItOnce(t *testing.T) {
	hermeticEnv(t)
	seedProofSessions(t)
	dir := os.Getenv("DEJA_INDEX_DIR")
	note := builtNote(dir)
	if note == "" {
		t.Fatal("no built note, so nothing was measured")
	}
	if !strings.Contains(note, "docs: vshulcz.github.io/deja-vu") {
		t.Fatalf("the first-build notice does not say where to look:\n%s", note)
	}
	if out := captureStderr(t, func() { printInstallProof(dir) }); strings.Contains(out, "docs: vshulcz.github.io") {
		t.Fatalf("the install said it again after the notice had:\n%s", out)
	}
}

// seedProofSessions puts enough history in the store for the proof block to
// have something real to print.
func seedProofSessions(t *testing.T) {
	t.Helper()
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	proj := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","sessionId":"p1","cwd":"/work/app","timestamp":"2026-08-02T10:00:00Z","message":{"role":"user","content":"the retry queue keeps double-acking"}}` + "\n" +
		`{"type":"assistant","sessionId":"p1","cwd":"/work/app","timestamp":"2026-08-02T10:01:00Z","message":{"role":"assistant","content":"one shard fixed it"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "p1.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
}

// widthStore indexes one session that names several files inside a repository
// on this disk — `files` only reports paths it can still find, so the fixture
// has to build the tree as well as the transcript.
func widthStore(t *testing.T) {
	t.Helper()
	hermeticEnv(t)
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, 2)
	for _, rel := range []string{
		"internal/application/partner/requests.go",
		"internal/domain/partner/webhook_delivery.go",
	} {
		full := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("package partner\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, full)
	}
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString(`{"type":"user","message":{"role":"user","content":"why does the retry queue stall on staging when the workers back off with a fixed delay instead of full jitter, and every one of them wakes at the same moment"},"timestamp":"2026-08-02T10:00:00Z","sessionId":"aaa","cwd":"/app"}` + "\n")
	for _, full := range paths {
		esc := strings.ReplaceAll(full, `\`, `\\`)
		b.WriteString(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Read","input":{"file_path":"` + esc + `"}}]},"timestamp":"2026-08-02T10:01:00Z","sessionId":"aaa","cwd":"/app"}` + "\n")
	}
	if err := os.WriteFile(filepath.Join(proj, "a.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", false, nil); err != nil {
		t.Fatal(err)
	}
}

// The path column was a fixed 56, so on a 60-column pane every row wrapped
// (#604). COLUMNS is the override a reader sets on purpose, and it is what a
// test can drive without a pty.
func TestFilesFitsTheTerminal(t *testing.T) {
	widthStore(t)
	t.Setenv("COLUMNS", "50")
	out, err := captureRun(t, "files", "retry")
	if err != nil {
		t.Fatal(err)
	}
	rows := 0
	for line := range strings.SplitSeq(out, "\n") {
		if !strings.HasPrefix(line, "  ") {
			continue
		}
		rows++
		if len([]rune(line)) > 50 {
			t.Errorf("a row ran to %d runes at COLUMNS=50: %q", len([]rune(line)), line)
		}
		if !strings.HasSuffix(strings.TrimSpace(line), "1 session") {
			t.Errorf("the count fell off the row: %q", line)
		}
	}
	if rows == 0 {
		t.Fatalf("no file rows to judge:\n%s", out)
	}
}

// The tail of a path is what identifies it, so the cut takes the head.
func TestFilesKeepsTheFileName(t *testing.T) {
	widthStore(t)
	t.Setenv("COLUMNS", "44")
	out, err := captureRun(t, "files", "retry")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "requests.go") || !strings.Contains(out, "webhook_delivery.go") {
		t.Errorf("a narrow window lost the file names:\n%s", out)
	}
}

// A pipe is not a terminal: the whole path goes out, because a script reading
// this wants the text rather than the layout.
func TestFilesIsNotCutWhenPiped(t *testing.T) {
	widthStore(t)
	// First with a narrow window, to show the fixture's paths are long enough
	// that cutting would be visible at all.
	t.Setenv("COLUMNS", "44")
	out, err := captureRun(t, "files", "retry")
	if err != nil {
		t.Fatal(err)
	}
	// The width cut takes more than trimPath's head removal does, which is what
	// gives the pipe assertion below something to be different from.
	if strings.Contains(out, filepath.ToSlash(filepath.Join("internal", "application", "partner", "requests.go"))) {
		t.Fatalf("a narrow window did not cut these paths, so this test means nothing:\n%s", out)
	}
	// Narrow enough that a terminal would have cut it, and the pipe must not.
	t.Setenv("COLUMNS", "")
	out, err = captureRun(t, "files", "retry")
	if err != nil {
		t.Fatal(err)
	}
	// trimPath still drops the head of a deep path (#727); what must not happen
	// is a second, width-driven cut on top of it.
	if !strings.Contains(out, filepath.ToSlash(filepath.Join("internal", "application", "partner", "requests.go"))) {
		t.Errorf("the path a pipe gets was cut further than trimPath cuts:\n%s", out)
	}
}

// And the search itself: the printer can budget all it likes, but the width has
// to reach it from the command that owns the terminal.
func TestSearchOutputFitsTheTerminal(t *testing.T) {
	widthStore(t)
	t.Setenv("COLUMNS", "50")
	out, err := captureRun(t, "retry")
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for line := range strings.SplitSeq(out, "\n") {
		if !strings.HasPrefix(line, "  ") {
			continue
		}
		seen = true
		if len([]rune(line)) > 50 {
			t.Errorf("a snippet ran to %d runes at COLUMNS=50: %q", len([]rune(line)), line)
		}
	}
	if !seen {
		t.Fatalf("no snippet lines to judge:\n%s", out)
	}
}

// Reading the terminal is best-effort: under `go test` stdout is a pipe, so the
// answer must be "no" rather than a made-up number. Every caller falls back when
// it hears no.
func TestTerminalWidthSaysNoWhenThereIsNoTerminal(t *testing.T) {
	if n, ok := terminalWidth(); ok {
		t.Errorf("a pipe reported a width of %d columns", n)
	}
}

// COLUMNS is an override, not a hint: a reader who exports it, or a script that
// pins the layout, decides.
func TestBriefWidthPrefersColumns(t *testing.T) {
	t.Setenv("COLUMNS", "60")
	if got := briefWidth(); got != 60 {
		t.Errorf("briefWidth = %d with COLUMNS=60", got)
	}
}

// Nonsense in COLUMNS is ignored rather than obeyed, and with no terminal to ask
// the answer is the old default.
func TestBriefWidthFallsBackTo80(t *testing.T) {
	for _, v := range []string{"", "wide", "0", "5", "100000"} {
		if v == "" {
			if err := os.Unsetenv("COLUMNS"); err != nil {
				t.Fatal(err)
			}
		} else {
			t.Setenv("COLUMNS", v)
		}
		if got := briefWidth(); got != 80 {
			t.Errorf("COLUMNS=%q: briefWidth = %d, want the 80-column default", v, got)
		}
	}
}

// deja.exe is a console program. Started from a host that has no console of
// its own, such as the DSH desktop app, it gets a fresh console window unless
// the spawn asks for windowsHide, and that window flashes on every hook
// (#5635). Every JavaScript or Python file we ship or write into a client's
// config has to ask for it: the plugins under extensions/, the npm launcher,
// and the plugins install generates from this package. In Python the ask is
// creationflags, CREATE_NO_WINDOW on Windows.
func TestShippedSpawnsHideTheirWindow(t *testing.T) {
	root := filepath.Join("..", "..")
	var files []string
	_ = filepath.WalkDir(filepath.Join(root, "extensions"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "test" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		switch filepath.Ext(path) {
		case ".js", ".mjs", ".cjs", ".ts", ".py":
			files = append(files, path)
		}
		return nil
	})
	files = append(files, filepath.Join(root, "npm", "bin", "deja"))
	generated, _ := filepath.Glob("*.go")
	for _, path := range generated {
		if !strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
	}

	spawners := regexp.MustCompile(`(^|[^.\w])(spawn|spawnSync|execFile|execFileSync|execSync|exec|fork)\(`)
	promisified := regexp.MustCompile(`(\w+)\s*=\s*promisify\((?:execFile|exec)\)`)
	pyCalls := regexp.MustCompile(`(^|[^.\w])(subprocess\.(?:run|Popen|call|check_call|check_output))\(`)
	checked, pyChecked := 0, 0
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		src := string(raw)
		if strings.Contains(src, "import subprocess") {
			for _, loc := range pyCalls.FindAllStringSubmatchIndex(src, -1) {
				pyChecked++
				if !strings.Contains(callArgs(src, loc[1]), "creationflags=") {
					line := strings.Count(src[:loc[4]], "\n") + 1
					t.Errorf("%s:%d: %s( runs without creationflags", path, line, src[loc[4]:loc[5]])
				}
			}
		}
		if !strings.Contains(src, "child_process") {
			continue
		}
		calls := spawners
		if m := promisified.FindStringSubmatch(src); m != nil {
			calls = regexp.MustCompile(`(^|[^.\w])(spawn|spawnSync|execFile|execFileSync|execSync|exec|fork|` + regexp.QuoteMeta(m[1]) + `)\(`)
		}
		for _, loc := range calls.FindAllStringSubmatchIndex(src, -1) {
			args := callArgs(src, loc[1])
			// The options come before the callback, and a callback body
			// mentioning windowsHide hides nothing.
			for _, cb := range []string{"=>", "function"} {
				if i := strings.Index(args, cb); i >= 0 {
					args = args[:i]
				}
			}
			line := strings.Count(src[:loc[4]], "\n") + 1
			checked++
			if !strings.Contains(args, "windowsHide: true") {
				t.Errorf("%s:%d: %s( spawns without windowsHide: true", path, line, src[loc[4]:loc[5]])
			}
		}
	}
	if checked < 20 {
		t.Fatalf("checked %d spawn sites, want at least 20: the scan stopped finding them", checked)
	}
	if pyChecked < 3 {
		t.Fatalf("checked %d Python subprocess calls, want at least 3: the scan stopped finding them", pyChecked)
	}
}

// callArgs returns the text from just after a call's opening paren to its
// matching close, or to the end of src when the parens never balance.
func callArgs(src string, open int) string {
	depth := 1
	for i := open; i < len(src); i++ {
		switch src[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return src[open:i]
			}
		}
	}
	return src[open:]
}

// On a fresh machine wip opened a manifest that was never written and printed
// `open …/manifest.gob: no such file or directory`, exit 1, where every other
// reader builds the index and says there is no history.
func TestWIPOnAFreshMachineSaysThereIsNoHistory(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "home", ".cache", "deja")
	t.Setenv("DEJA_INDEX_DIR", dir)
	var out bytes.Buffer
	if err := runWIP(dir, nil, &out); err != nil {
		t.Fatalf("wip on a fresh machine failed: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "manifest.gob") || !strings.Contains(got, "no agent history was found") {
		t.Fatalf("fresh-machine wip said:\n%s", got)
	}
}

// What an uninstall leaves in deja's own state file, and what the next session
// does with it.
//
// The file survives — it is deja's, not a config in someone else's tree, so the
// rule #840 states does not apply to it — and the field anything acts on is
// emptied. The regression this guards is unpleasant: a refresh that still knew
// the old targets would rewire harnesses the reader had just removed, on the
// next session start, without being asked (#2674).
func TestUninstallLeavesNoTargetsToRewire(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	for _, d := range []string{".claude", ".codex", ".cursor", ".qwen"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := captureRun(t, "install", "--all", "--no-index"); err != nil {
		t.Fatalf("install: %v", err)
	}
	if wiringTargets(t) == 0 {
		t.Fatal("install recorded no targets, so this pins nothing")
	}
	if _, err := captureRun(t, "uninstall", "--all"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if n := wiringTargets(t); n != 0 {
		t.Fatalf("uninstall left %d target%s recorded for a refresh to rewire", n, pluralS(n))
	}
}

// And the whole point of that: a binary that moves afterwards rewires nothing
// and says nothing, because there is nothing recorded to rewire.
func TestAMovedBinaryRewiresNothingAfterAnUninstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	for _, d := range []string{".claude", ".codex", ".cursor"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "--all", "--no-index"); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "--all"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	// The upgrade: the recorded binary is not where it was.
	writeWiringExe(t, filepath.Join(home, "moved", "deja"))

	rewired := refreshWiringAfterUpgrade()

	if note := rewireNote(rewired); note != "" {
		t.Fatalf("a machine with nothing installed was told deja rewired it: %q", note)
	}
	for _, gone := range []string{".claude.json.deja", ".codex/config.toml", ".cursor/mcp.json"} {
		if b, err := os.ReadFile(filepath.Join(home, gone)); err == nil && strings.Contains(string(b), "deja") {
			t.Fatalf("%s was wired again after the reader removed it:\n%s", gone, b)
		}
	}
}

func wiringTargets(t *testing.T) int {
	t.Helper()
	b, err := os.ReadFile(wiringStatePath())
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatal(err)
	}
	var st struct {
		Targets []string `json:"targets"`
	}
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatalf("the state file is not readable: %v\n%s", err, b)
	}
	return len(st.Targets)
}

func writeWiringExe(t *testing.T, exe string) {
	t.Helper()
	b, err := os.ReadFile(wiringStatePath())
	if err != nil {
		t.Fatal(err)
	}
	var st map[string]any
	if err = json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	st["exe"] = exe
	out, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wiringStatePath(), out, 0o644); err != nil {
		t.Fatal(err)
	}
}

// The repair rewrites the wiring. The /deja command holds the same absolute
// path and was written by the install command rather than by installTarget, so
// a move left it running a binary that is gone — and `deja install` for the
// same target, which the repair exists to save the reader, fixed it (#2693).
func TestTheRepairRewritesTheCommandFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("CURSOR_CONFIG_DIR", filepath.Join(home, ".cursor"))

	old := filepath.Join(home, "old", "deja")
	if _, err := installTarget("cursor-auto", old, false); err != nil {
		t.Fatal(err)
	}
	if _, err := installCommandFile(guidanceHarness("cursor-auto"), old, false); err != nil {
		t.Fatal(err)
	}
	command := commandFilePath(guidanceHarness("cursor-auto"))
	before, err := os.ReadFile(command)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(before), old) {
		t.Fatalf("the command file does not name the binary it was written for:\n%s", before)
	}

	// The state by hand, the way the other wiring tests seed it: what install
	// records depends on which harnesses the machine running the test has.
	if err = os.MkdirAll(filepath.Dir(wiringStatePath()), 0o755); err != nil {
		t.Fatal(err)
	}
	state := `{"version":"0.0.1","targets":["cursor-auto"],"exe":` + strconv.Quote(old) +
		`,"home":` + strconv.Quote(homeDir()) + `}`
	if err = os.WriteFile(wiringStatePath(), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := exeIsTemporary
	t.Cleanup(func() { exeIsTemporary = restore })
	exeIsTemporary = func(string) bool { return false }

	if rewired := refreshWiringAfterUpgrade(); len(rewired) == 0 {
		t.Fatalf("nothing was rewired (stuck: %v)", stuckWiring)
	}
	after, err := os.ReadFile(command)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), old) {
		t.Errorf("the /deja command still runs the binary that is gone:\n%s", after)
	}

	// And a machine that asked for the plumbing without the text keeps it that
	// way: the repair rewrites a command file, it does not hand out one.
	if err := os.Remove(command); err != nil {
		t.Fatal(err)
	}
	state = `{"version":"0.0.2","targets":["cursor-auto"],"exe":` + strconv.Quote(old) +
		`,"home":` + strconv.Quote(homeDir()) + `}`
	if err := os.WriteFile(wiringStatePath(), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	// The repair still runs — otherwise this proves only that it bailed. It
	// reports nothing changed, which is the point: the wiring already names
	// this binary and the command file is not there to rewrite.
	refreshWiringAfterUpgrade()
	if len(stuckWiring) != 0 {
		t.Fatalf("the second repair could not write: %v", stuckWiring)
	}
	if _, err := os.Stat(command); err == nil {
		t.Errorf("the repair wrote a command file the machine had declined: %s", command)
	}
}

// Goose keeps the command in config.yaml and the workflow in a recipe beside
// it. Going by the recipe alone would put back a slash command someone took
// out of the config by hand (#2693).
func TestTheRepairLeavesAGooseCommandTakenOutByHand(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	old := filepath.Join(home, "old", "deja")
	if _, err := installTarget("goose-auto", old, false); err != nil {
		t.Fatal(err)
	}
	if _, err := installCommandFile("goose", old, false); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(gooseConfigDir(), "config.yaml")
	b, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if !gooseSlashCommandPresent() {
		t.Fatalf("the command was never written:\n%s", b)
	}
	// Taken out by hand, recipe left behind.
	if err := os.WriteFile(config, []byte(removeGooseSlashCommand(string(b))), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Dir(wiringStatePath()), 0o755); err != nil {
		t.Fatal(err)
	}
	state := `{"version":"0.0.1","targets":["goose-auto"],"exe":` + strconv.Quote(old) +
		`,"home":` + strconv.Quote(homeDir()) + `}`
	if err := os.WriteFile(wiringStatePath(), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := exeIsTemporary
	t.Cleanup(func() { exeIsTemporary = restore })
	exeIsTemporary = func(string) bool { return false }

	if rewired := refreshWiringAfterUpgrade(); len(rewired) == 0 {
		t.Fatalf("nothing was rewired (stuck: %v)", stuckWiring)
	}
	if gooseSlashCommandPresent() {
		after, _ := os.ReadFile(config)
		t.Errorf("the repair put the slash command back:\n%s", after)
	}
}

// The repair derives every path from the environment it runs in. A process
// whose HOME points elsewhere while the record is still visible — sudo with a
// preserved XDG_CONFIG_HOME, a container, an su session — wrote a fresh set of
// configs into a home nobody installed into, left the real ones pointing at
// the old binary, and marked the record repaired (#885).
func TestWiringRepairStaysInTheHomeItWasWrittenFor(t *testing.T) {
	tmp := hermeticEnv(t)
	cfg := filepath.Join(tmp, "config")
	t.Setenv("XDG_CONFIG_HOME", cfg)
	if err := os.MkdirAll(filepath.Join(cfg, "deja"), 0o700); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(tmp, "other-home")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(home string) {
		b, err := json.Marshal(wiringState{Version: "older", Targets: []string{"claude-auto"}, Exe: filepath.Join(tmp, "old-deja"), Home: home})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(wiringStatePath(), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// The record belongs to another home: nothing is repaired, nothing spreads.
	write(elsewhere)
	if changed := refreshWiringAfterUpgrade(); len(changed) != 0 {
		t.Errorf("repair ran in a home it was not written for: %v", changed)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Errorf("configs were written into a home nobody installed into: %v", err)
	}

	// The record belongs to this home: the repair runs as before.
	write(os.Getenv("HOME"))
	if changed := refreshWiringAfterUpgrade(); len(changed) == 0 {
		t.Error("the repair stopped working in its own home")
	}

	// A record from before this field exists: repaired, as it always was. The
	// config is removed first so the repair has something to write — after the
	// case above it already points at this binary.
	if err := os.RemoveAll(filepath.Join(os.Getenv("HOME"), ".claude")); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(wiringState{Version: "older", Targets: []string{"claude-auto"}, Exe: filepath.Join(tmp, "old-deja")})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wiringStatePath(), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if changed := refreshWiringAfterUpgrade(); len(changed) == 0 {
		t.Error("a record written before the home field stopped being repaired")
	}
}

// Moving a binary without changing its version is ordinary — a relink, a
// reinstall of the same release, a `go install` over a manual download — and
// left every config naming a path that no longer exists (#773).
func TestWiringRepairFollowsAMovedBinary(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude-code", "--no-index"); err != nil {
		t.Fatal(err)
	}
	wired := readWiringState()
	if wired.Exe == "" {
		t.Fatal("install recorded no binary path")
	}
	if len(wired.Targets) == 0 {
		t.Fatal("install recorded no targets")
	}

	// Pretend the binary was somewhere else when the configs were written.
	moved := wired.Exe
	wired.Exe = filepath.Join(home, "old-location", "deja")
	b, err := json.Marshal(wired)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(wiringStatePath(), b, 0o644); err != nil {
		t.Fatal(err)
	}
	claudeJSON := filepath.Join(home, ".claude.json")
	before, err := os.ReadFile(claudeJSON)
	if err != nil {
		t.Fatal(err)
	}
	// The entry names the binary as it was started, which on macOS is the
	// /var spelling of the /private/var path the record holds.
	invoked, _ := os.Executable()
	invoked, _ = filepath.Abs(invoked)
	// As JSON holds it, too: on Windows the separators are escaped.
	old := string(before)
	for _, p := range []string{moved, invoked} {
		esc := func(s string) string { return strings.Trim(jsonString(s), `"`) }
		old = strings.ReplaceAll(old, esc(p), esc(wired.Exe))
		old = strings.ReplaceAll(old, p, wired.Exe)
	}
	if err = os.WriteFile(claudeJSON, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}

	if changed := refreshWiringAfterUpgrade(); len(changed) == 0 {
		t.Error("a moved binary did not trigger the repair")
	}
	after, err := os.ReadFile(claudeJSON)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), "old-location") {
		t.Errorf("config still names the old path:\n%s", after)
	}

	// Nothing moved and nothing upgraded: the repair must stay quiet, or every
	// session start rewrites the user's configs.
	if changed := refreshWiringAfterUpgrade(); len(changed) != 0 {
		t.Errorf("repair ran with nothing to repair: %v", changed)
	}
}

// The record is written from the arguments, before any of them has been
// through installTarget, so a refused name was kept beside the ones that
// worked — and every repair afterwards retried it, failed, and counted it in
// the line a person sees at session start (#3695).
func TestAMistypedTargetIsNotRecorded(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude-code", "--no-index"); err != nil {
		t.Fatal(err)
	}
	// Refused, and the run reports it — what matters is what it leaves behind.
	_, _ = captureRun(t, "install", "nosuchtarget", "--no-index")
	if got := readWiringState().Targets; slices.Contains(got, "nosuchtarget") {
		t.Errorf("a target deja does not know was recorded: %v", got)
	}
	if got := readWiringState().Targets; !slices.Contains(got, "claude-code") {
		t.Errorf("the target that worked was dropped: %v", got)
	}
}

// And one already in a record written by an older build goes on the next
// install, rather than being retried until somebody edits the file by hand.
func TestARecordedTargetDejaDoesNotKnowIsDropped(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude-code", "--no-index"); err != nil {
		t.Fatal(err)
	}
	st := readWiringState()
	st.Targets = append(st.Targets, "nosuchtarget")
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wiringStatePath(), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude-code", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if got := readWiringState().Targets; slices.Contains(got, "nosuchtarget") {
		t.Errorf("an unknown target survived an install: %v", got)
	}
}

// Homebrew keeps each release in its own Cellar directory and moves the
// `bin/deja` link to the new one. The repair after an upgrade wrote the
// resolved Cellar path into every entry, including entries that named the link
// and still worked, so the next upgrade broke them again (#4189).
func TestWiringRepairKeepsTheLinkTheBinaryIsReachedBy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ = filepath.EvalSymlinks(resolved)
	link := filepath.Join(home, ".local", "bin", "deja")
	if err = os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(resolved, link); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runningExe = os.Executable })
	runningExe = func() (string, error) { return link, nil }

	if _, err = captureRun(t, "install", "cursor", "--no-index"); err != nil {
		t.Fatal(err)
	}
	// The upgrade: the binary the record names is gone.
	writeWiringExe(t, filepath.Join(home, "Cellar", "deja-vu", "1.0", "bin", "deja"))

	refreshWiringAfterUpgrade()

	b, err := os.ReadFile(filepath.Join(home, ".cursor", "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), jsonString(link)) {
		t.Fatalf("the entry no longer names the link %s:\n%s", link, b)
	}
	if strings.Contains(string(b), jsonString(resolved)) {
		t.Fatalf("the entry was pinned to the resolved binary %s:\n%s", resolved, b)
	}
}

// Linux reports the resolved binary as the executable, so the link has to be
// found where the installers leave one.
func TestWiringRepairFindsTheLinkWhenTheExecutableIsResolved(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	hermeticEnv(t)
	home := os.Getenv("HOME")
	resolved, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ = filepath.EvalSymlinks(resolved)
	link := filepath.Join(home, ".local", "bin", "deja")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(resolved, link); err != nil {
		t.Fatal(err)
	}
	if got := stableExe(resolved, resolved); got != link {
		t.Fatalf("stableExe = %s, want the link %s", got, link)
	}
	// A link to some other binary is not this one.
	other := filepath.Join(home, "bin", "deja")
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	if got := stableExe(resolved, resolved); got != resolved {
		t.Fatalf("stableExe = %s, want the binary itself %s", got, resolved)
	}
}

// A link in a temp directory is not a stable path: the repair would trade a
// binary that stays for a link that goes with the directory.
func TestWiringRepairSkipsALinkInATempDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	hermeticEnv(t)
	home := os.Getenv("HOME")
	resolved, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ = filepath.EvalSymlinks(resolved)
	link := filepath.Join(home, "scratch", "tmp", "deja")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(resolved, link); err != nil {
		t.Fatal(err)
	}
	was := exeIsTemporary
	t.Cleanup(func() { exeIsTemporary = was })
	exeIsTemporary = func(p string) bool {
		return strings.Contains(p, string(filepath.Separator)+"tmp"+string(filepath.Separator))
	}
	if got := stableExe(link, resolved); got != resolved {
		t.Fatalf("stableExe = %s, want the binary %s rather than a temp link", got, resolved)
	}
}

// The configs name the link; remove it and they are dead while the binary is
// still where the record says, so the repair has to look at the link too.
func TestWiringRepairNoticesTheLinkItWroteIsGone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ = filepath.EvalSymlinks(resolved)
	link := filepath.Join(home, ".local", "bin", "deja")
	if err = os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(resolved, link); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runningExe = os.Executable })
	runningExe = func() (string, error) { return link, nil }
	if _, err = captureRun(t, "install", "cursor", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if st := readWiringState(); st.Written != link || st.Exe != resolved {
		t.Fatalf("record exe=%s written=%s, want %s through %s", st.Exe, st.Written, resolved, link)
	}

	if err = os.Remove(link); err != nil {
		t.Fatal(err)
	}
	runningExe = func() (string, error) { return resolved, nil }
	refreshWiringAfterUpgrade()

	b, err := os.ReadFile(filepath.Join(home, ".cursor", "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), jsonString(resolved)) {
		t.Fatalf("the entry still names the removed link:\n%s", b)
	}
	if st := readWiringState(); st.Written != "" {
		t.Fatalf("record still names a link: %q", st.Written)
	}
}

// The scenario this exists for: a user installed months ago, the generator
// has been fixed since, and their config still holds the broken version.
// Nobody re-runs an installer that already succeeded, so the repair has to
// happen without being asked.
func TestWiringRefreshesAfterUpgrade(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("DEJA_QWEN_ROOT", "")
	if err := os.MkdirAll(filepath.Join(home, ".qwen"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := installQwenAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	recordWiring([]string{"qwen-auto"}, false)

	// Rewind: the wiring an older deja wrote — the old binary path, and a
	// timeout qwen reads as ten milliseconds.
	settings := filepath.Join(home, ".qwen", "settings.json")
	stale := map[string]any{"hooks": map[string]any{
		"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{
			"type": "command", "command": "/old/deja hook-context", "timeout": 10,
		}}}},
	}}
	b, err := json.MarshalIndent(stale, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(settings, b, 0o644); err != nil {
		t.Fatal(err)
	}
	statePath := wiringStatePath()
	if err = os.WriteFile(statePath, []byte(`{"version":"0.14.0","targets":["qwen-auto"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	changed := refreshWiringAfterUpgrade()
	if len(changed) == 0 {
		t.Fatal("upgrade left the stale wiring in place")
	}
	var got map[string]any
	raw, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	hooks, _ := got["hooks"].(map[string]any)
	if _, live := hooks["UserPromptSubmit"]; !live {
		t.Fatalf("the working hook was not restored: %s", raw)
	}
	// The stale entry is taken over rather than left as it was: same event,
	// this binary, and a timeout in the units qwen actually reads.
	start, _ := hooks["SessionStart"].([]any)
	if len(start) != 1 {
		t.Fatalf("SessionStart entries = %d, want the stale one adopted: %s", len(start), raw)
	}
	entry, _ := start[0].(map[string]any)
	inner, _ := entry["hooks"].([]any)
	h, _ := inner[0].(map[string]any)
	if cmd, _ := h["command"].(string); strings.HasPrefix(cmd, "/old/deja") {
		t.Fatalf("the old binary path survived the upgrade: %s", raw)
	}
	if to, _ := h["timeout"].(float64); to < 1000 {
		t.Fatalf("timeout = %v, still the value qwen reads as ten milliseconds: %s", to, raw)
	}
	// And the record now says this binary owns it, so the repair does not
	// repeat on every session start.
	st := readWiringState()
	if st.Version != version {
		t.Fatalf("state still says %q", st.Version)
	}
	if second := refreshWiringAfterUpgrade(); second != nil {
		t.Fatalf("repair ran twice: %v", second)
	}
}

// Repair, not spread: a harness the user never wired must stay untouched.
func TestWiringRefreshTouchesOnlyRecordedTargets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	if err := os.MkdirAll(filepath.Join(home, ".qwen"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(wiringStatePath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wiringStatePath(), []byte(`{"version":"0.1.0","targets":["qwen-auto"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	refreshWiringAfterUpgrade()
	if _, err := os.Stat(filepath.Join(home, ".kimi-code", "config.toml")); !os.IsNotExist(err) {
		t.Fatal("refresh wired a harness the user never asked for")
	}
}

// The repair exists because deja moves — a release replaces it, a package
// manager relocates it. It cannot tell that from one run of a build somewhere
// else, and the only guard was a temp directory (#2684), so a build under a
// home directory, a checkout or an agent's scratch adopted every config on its
// first run: one machine ended with eight hook entries per event, each naming a
// different build, all firing on every prompt (#3421).
func TestAStrangerDoesNotAdoptTheWiring(t *testing.T) {
	tmp := hermeticEnv(t)
	installed := filepath.Join(tmp, "bin", "deja")
	if err := os.MkdirAll(filepath.Dir(installed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeWiringFixture(t, wiringState{
		Version: "older", Targets: []string{"claude-code"}, Exe: installed, Home: homeDir(),
	})

	// This test binary stands in for the stranger: a different path, and the
	// recorded one is still on disk.
	if got := refreshWiringAfterUpgrade(); len(got) != 0 {
		t.Errorf("a build that is not the installed deja rewrote %v", got)
	}

	// The honest case: the recorded binary is gone, so this one is the move.
	if err := os.Remove(installed); err != nil {
		t.Fatal(err)
	}
	if got := refreshWiringAfterUpgrade(); len(got) == 0 {
		t.Error("the recorded binary is gone and the repair still stood down")
	}
}

// An entry deja wrote from a build under another name is still deja's own: the
// record remembers the paths it installed from, so a later install replaces
// that entry instead of stacking a second one beside it (#3421).
func TestAnEntryFromAPreviousPathIsRecognisedAsOurs(t *testing.T) {
	hermeticEnv(t)
	const old = "/somewhere/scratch/deja-arm"
	writeWiringFixture(t, wiringState{
		Version: "dev", Targets: []string{"claude-code"}, Exe: "/usr/local/bin/deja",
		Exes: []string{old}, Home: homeDir(),
	})
	forgetWrittenExes()
	t.Cleanup(forgetWrittenExes)

	if !isDejaHookCommand(old+" hook-prompt", "/usr/local/bin/deja hook-prompt") {
		t.Errorf("an entry deja wrote from %s is not recognised as ours", old)
	}
	if isDejaHookCommand("/opt/somebody/else hook-prompt", "/usr/local/bin/deja hook-prompt") {
		t.Error("a command deja never wrote was claimed as ours")
	}
}

func writeWiringFixture(t *testing.T, st wiringState) {
	t.Helper()
	p := wiringStatePath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// underTempDir is what decides it, so it is pinned on its own: the paths a real
// deja lives at, and the ones a scratch build does.
func TestUnderTempDirKnowsAScratchBuild(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	for _, tc := range []struct {
		path string
		temp bool
	}{
		{filepath.Join(tmp, "deja"), true},
		{filepath.Join(tmp, "go-build123", "b001", "exe", "deja"), true},
		{"/tmp/deja", true},
		{"/private/tmp/scratch/deja", true},
		{"/usr/local/bin/deja", false},
		{"/opt/homebrew/bin/deja", false},
		// Under nobody's TMPDIR and thrown away all the same. An agent's
		// scratch build there had adopted 28 targets on one machine, and the
		// only screen that ever said so was Gemini's own start banner quoting
		// deja's note back (#3656).
		{"/home/someone/.claude/jobs/1a2b3c4d/tmp/deja-sl", true},
		{"/home/someone/projects/deja-vu/temp/deja", true},
		// A literal home, not this process's: the suite runs with HOME under a
		// temp directory, where ~/.local/bin really is temporary.
		{"/home/someone/.local/bin/deja", false},
		{"", false},
	} {
		if got := underTempDir(tc.path); got != tc.temp {
			t.Errorf("underTempDir(%q) = %v, want %v", tc.path, got, tc.temp)
		}
	}
}

// The repair exists because deja moves — a release replaces the binary, a
// package manager relocates it — and every config still names the old path. It
// cannot tell that from a one-off run of a build somewhere else, so a `go run`,
// a colleague's checkout or a CI job rewrote every config to a path that will
// not exist tomorrow (#2684).
func TestTheRepairRefusesToAdoptATempBinary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	codex := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(codex), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codex, []byte("[tools]\nweb = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The state written by hand, the way TestWiringRefreshTouchesOnlyRecorded-
	// Targets does: what install records depends on which harnesses a machine
	// happens to have, and this test is about the guard, not about detection.
	if err := os.MkdirAll(filepath.Dir(wiringStatePath()), 0o755); err != nil {
		t.Fatal(err)
	}
	state := `{"version":"0.0.1","targets":["codex"],"exe":"/opt/deja/bin/deja","home":` +
		strconv.Quote(homeDir()) + `}`
	if err := os.WriteFile(wiringStatePath(), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(codex)
	if err != nil {
		t.Fatal(err)
	}

	restore := exeIsTemporary
	t.Cleanup(func() { exeIsTemporary = restore })

	// A scratch build runs the session hook once.
	exeIsTemporary = func(string) bool { return true }
	if rewired := refreshWiringAfterUpgrade(); len(rewired) != 0 {
		t.Fatalf("a build in a temp directory was adopted: %v", rewired)
	}
	after, err := os.ReadFile(codex)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("the config was rewritten for a build in a temp directory:\nwas:\n%s\nnow:\n%s", before, after)
	}

	// And a real upgrade still repairs itself.
	exeIsTemporary = func(string) bool { return false }
	rewired := refreshWiringAfterUpgrade()
	if len(rewired) == 0 {
		t.Fatalf("a binary that moved to a real location was not adopted (stuck: %v)", stuckWiring)
	}
	repaired, err := os.ReadFile(codex)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(before, repaired) {
		t.Fatalf("the repair reported %v and changed nothing:\n%s", rewired, repaired)
	}
}

// `deja retry` answers from the exact tier and stops there, so the sessions
// that wrote "retries" are neither returned nor mentioned — and to whoever
// reads the output, one form's sessions look like the whole store. deja
// narrates every other rung of the ladder; this one was silent.
func TestSearchNamesTheWordFormsItLeftOut(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	ts := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	for id, text := range map[string]string{
		"wfa": "we decided the uploader will retry once on 5xx",
		"wfb": "we decided to cap uploader retries at 3",
	} {
		writeClaudeFixture(t, filepath.Join(claudeRoot, "wf", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"` + ts + `","message":{"role":"user","content":"` + text + `"}}`,
		})
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	result, err := indexSearch(dir, query.Options{Query: "retry", All: true})
	if err != nil {
		t.Fatal(err)
	}
	o := query.Options{Query: "retry", All: true, Tier: search.TierExact}
	detailed, err := search.RunDetailed(result, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(detailed.Hits) != 1 || detailed.Hits[0].Session.ID != "wfa" {
		t.Fatalf("exact tier hits = %+v, want only wfa", detailed.Hits)
	}
	note := otherWordFormsNote(dir, o, detailed.Hits)
	if !strings.Contains(note, `"retries" in 1 more`) {
		t.Fatalf("note = %q, want it to name the retries session the answer leaves out", note)
	}

	// A form already on the page is not left out, and a query with no other
	// form in the corpus gets no line at all.
	uploader := query.Options{Query: "uploader", All: true, Tier: search.TierExact}
	ss, err := indexSearch(dir, uploader)
	if err != nil {
		t.Fatal(err)
	}
	both, err := search.RunDetailed(ss, uploader)
	if err != nil {
		t.Fatal(err)
	}
	if got := otherWordFormsNote(dir, uploader, both.Hits); got != "" {
		t.Errorf("uploader note = %q, want silence", got)
	}
	// Nothing to leave out when nothing came back, and the close/relevance
	// tiers already narrate their own variants.
	if got := otherWordFormsNote(dir, o, nil); got != "" {
		t.Errorf("empty result note = %q, want silence", got)
	}
	closeTier := o
	closeTier.Tier = search.TierClose
	if got := otherWordFormsNote(dir, closeTier, detailed.Hits); got != "" {
		t.Errorf("close tier note = %q, want silence", got)
	}
}

// deja prints this note into a terminal, the session that ran deja keeps the
// output, and the next index reads it as history — so a form deja generated to
// search with comes back as a form the store holds. In real-world stores, // every such form sat in zero sessions where anything was said (#3820).
func TestTheWordFormsNoteIgnoresWhatOnlyAToolPrinted(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	ts := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "wf", "said.jsonl"), "said", []string{
		`{"type":"user","sessionId":"said","timestamp":"` + ts + `","message":{"role":"user","content":"we decided the uploader will retry once on 5xx"}}`,
	})
	// The only place this session holds the other form is deja's own answer,
	// pasted into the transcript by the shell that ran it.
	writeClaudeFixture(t, filepath.Join(claudeRoot, "wf", "printed.jsonl"), "printed", []string{
		`{"type":"user","sessionId":"printed","timestamp":"` + ts + `","message":{"role":"user","content":"check the index"}}`,
		`{"type":"user","sessionId":"printed","timestamp":"` + ts + `","message":{"role":"user","content":[{"type":"tool_result","content":"deja: word forms this answer leaves out: \"retries\" in 2 more — search the form to see them"}]}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	o := query.Options{Query: "retry", All: true, Tier: search.TierExact}
	result, err := indexSearch(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	detailed, err := search.RunDetailed(result, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(detailed.Hits) == 0 {
		t.Fatal("the exact tier found nothing, so there is no note to check")
	}
	if note := otherWordFormsNote(dir, o, detailed.Hits); strings.Contains(note, "retries") {
		t.Fatalf("the note names a form only deja printed: %q", note)
	}
}

// "retry" inside "retrying" is a different form; counting it as present would
// hide the one the note exists to name.
func TestContainsWordIsNotSubstring(t *testing.T) {
	for _, tc := range []struct {
		text, word string
		want       bool
	}{
		{"the uploader is retrying the chunk", "retry", false},
		{"the uploader will retry once", "retry", true},
		{"retry.", "retry", true},
		{"cap uploader retries at 3", "retries", true},
	} {
		if got := containsWord(tc.text, tc.word); got != tc.want {
			t.Errorf("containsWord(%q, %q) = %v", tc.text, tc.word, got)
		}
	}
}

// The key WorkBuddy AI 5.6.2 wrote into mcp-approvals.json when Trust was
// clicked for a server running "/tmp/hhsbx/workbuddy-gui/deja mcp" (#4740).
func TestWorkBuddyApprovalKeyMatchesTheApp(t *testing.T) {
	got := workBuddyApprovalKey("deja", "/tmp/hhsbx/workbuddy-gui/deja", []string{"mcp"}, nil, "")
	want := "2e57a628dff522acd0dd45f93882c0ae52b7e7a3d20e52cea3d1240ede48420c::deja"
	if got != want {
		t.Fatalf("key = %s, want %s", got, want)
	}
	entry := map[string]any{"command": "/tmp/hhsbx/workbuddy-gui/deja", "args": []any{"mcp"}}
	if got := workBuddyEntryApprovalKey("deja", entry); got != want {
		t.Fatalf("entry key = %s, want %s", got, want)
	}
	// Env keys and cwd are part of what the app hashes.
	entry["env"] = map[string]any{"X": "1"}
	if workBuddyEntryApprovalKey("deja", entry) == want {
		t.Fatal("env keys did not change the key")
	}
}

// workBuddyAIHome makes a WorkBuddy AI home the way the app leaves one.
func workBuddyAIHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(hermeticEnv(t), "home")
	wb := filepath.Join(home, ".workbuddy-ai")
	if err := os.MkdirAll(filepath.Join(wb, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wb, "workbuddy.db"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return wb
}

// approveDeja writes the approval the app writes for deja's entry as mcp.json
// holds it.
func approveDeja(t *testing.T, wb string) {
	t.Helper()
	servers, _ := readCodeBuddyJSON(t, filepath.Join(wb, "mcp.json"))["mcpServers"].(map[string]any)
	entry, _ := servers["deja"].(map[string]any)
	if entry == nil {
		t.Fatalf("no deja entry in %v", servers)
	}
	command, _ := entry["command"].(string)
	raw, ok := entry["args"].([]any)
	if !ok {
		t.Fatalf("deja entry args are not a list: %v", entry)
	}
	args := make([]string, 0, len(raw))
	for _, a := range raw {
		arg, ok := a.(string)
		if !ok {
			t.Fatalf("deja entry arg is not a string: %v", a)
		}
		args = append(args, arg)
	}
	b, err := json.Marshal(map[string]int64{workBuddyApprovalKey("deja", command, args, nil, ""): 1759600000000})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(wb, "mcp-approvals.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Install wires the server and says the one click it still needs; once the
// app holds an approval for that exact command, it says nothing more.
func TestInstallWorkBuddySaysToTrust(t *testing.T) {
	wb := workBuddyAIHome(t)
	dir := index.DefaultDir()
	for _, target := range []string{"workbuddy", "workbuddy-auto"} {
		out := captureStdout(t, func() {
			if err := runInstall(dir, []string{target, "--no-index", "--no-guidance"}, false); err != nil {
				t.Fatal(err)
			}
		})
		if !strings.Contains(out, workBuddyTrustNote) {
			t.Fatalf("%s: no trust line in:\n%s", target, out)
		}
	}
	approveDeja(t, wb)
	out := captureStdout(t, func() {
		if err := runInstall(dir, []string{"workbuddy", "--no-index", "--no-guidance"}, false); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, workBuddyTrustNote) {
		t.Fatalf("approved entry still told to trust:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(wb, "mcp-approvals.json")); err != nil {
		t.Fatal(err)
	}
}

// Install never writes the approval itself, and the workbuddy.cn home, read
// from another edition, gets no line.
func TestInstallWorkBuddyLeavesApprovalsAlone(t *testing.T) {
	wb := workBuddyAIHome(t)
	if _, err := installTarget("workbuddy", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wb, "mcp-approvals.json")); !os.IsNotExist(err) {
		t.Fatalf("install wrote mcp-approvals.json: %v", err)
	}

	home := filepath.Join(hermeticEnv(t), "home")
	if err := os.MkdirAll(filepath.Join(home, ".workbuddy", "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("workbuddy", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if n := workBuddyUntrustedNote(); n != "" {
		t.Fatalf("~/.workbuddy got %q", n)
	}
}

// Doctor reads the approval: untrusted with the fix until the app has one for
// the entry as written, wired after.
func TestDoctorWorkBuddyTrust(t *testing.T) {
	wb := workBuddyAIHome(t)
	if _, err := installTarget("workbuddy", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	row := func() doctorMCPStatus {
		for _, r := range collectDoctorMCP() {
			if r.Name == "workbuddy" {
				return r
			}
		}
		t.Fatal("no workbuddy row")
		return doctorMCPStatus{}
	}
	text := func() string {
		var buf bytes.Buffer
		doctorMCP(&buf)
		for line := range strings.SplitSeq(buf.String(), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "workbuddy ") {
				return line
			}
		}
		return ""
	}

	var buf bytes.Buffer
	doctorMCP(&buf)
	if !strings.Contains(text(), "untrusted") || !strings.Contains(buf.String(), workBuddyTrustNote) {
		t.Fatalf("untrusted entry reported as:\n%s", buf.String())
	}
	if r := row(); r.State != "untrusted" || r.Note != workBuddyTrustNote {
		t.Fatalf("json row = %+v", r)
	}

	approveDeja(t, wb)
	buf.Reset()
	doctorMCP(&buf)
	if !strings.Contains(text(), " wired ") || strings.Contains(buf.String(), workBuddyTrustNote) {
		t.Fatalf("approved entry reported as:\n%s", buf.String())
	}
	if r := row(); r.State != "wired" || r.Note != "" {
		t.Fatalf("json row = %+v", r)
	}

	// An approval for another command is not one for this entry.
	b, err := json.Marshal(map[string]int64{workBuddyApprovalKey("deja", "/old/deja", []string{"mcp"}, nil, ""): 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(wb, "mcp-approvals.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if r := row(); r.State != "untrusted" {
		t.Fatalf("stale approval read as trusted: %+v", r)
	}
}

// Two surfaces now say the same thing about a missing wrapper: the line before
// the command (#3487) and the session-start block (#3488). They share the
// predicate on purpose — the lesson #3473 recorded is that one idea spelled
// twice drifts, and the narrower copy then wins by accident. This is that
// sharing, asserted: one store, both surfaces, the same fact.
func TestBothSurfacesSayTheWrapperRemedy(t *testing.T) {
	// timeout is on PATH on Linux and Windows; the line is for a machine without it.
	t.Setenv("PATH", t.TempDir())
	dir := seedWrapperWall(t, func(int) string { return longWorkerCommand })

	// The wording differs because the sentences differ — the pre-tool line has
	// already named the program ("timeout is not on this machine — …"), the block
	// has not — so each is asserted as it reads, and what is shared is the fact
	// and the predicate behind it.
	before := missingProgramLine(dir, "timeout 30 "+longWorkerCommand)
	if !strings.Contains(before, "same command ran without it") {
		t.Errorf("the line before the command lost the remedy: %q", before)
	}
	block, _ := environmentBlockFrom(dir, "auto")
	if !strings.Contains(block, "the same command without `timeout`") {
		t.Errorf("the session-start block lost the remedy:\n%s", block)
	}

	// And neither surface invents one: the same store with a different command
	// after the refusal leaves both silent about a remedy.
	other := seedWrapperWall(t, func(int) string { return "launchctl print system/app.worker | grep state" })
	if line := missingProgramLine(other, "timeout 30 "+longWorkerCommand); strings.Contains(line, "same command ran without") {
		t.Errorf("the line before the command claimed a remedy no pair supports: %q", line)
	}
	if block, _ := environmentBlockFrom(other, "auto"); strings.Contains(block, "without `timeout`") {
		t.Errorf("the session-start block claimed a remedy no pair supports:\n%s", block)
	}
}

// `deja goose` and `deja aider` refresh recall and then become the harness.
// Both were shipped without tests, and one of them started an agent from a
// one-word search before that was caught.
func TestGooseHookWritesTheRecallFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "idx"))
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", "")

	if err := cmdGooseHook(filepath.Join(home, "idx"), nil); err != nil {
		t.Fatalf("hook: %v", err)
	}
	hints := gooseHintsPath()
	b, err := os.ReadFile(hints)
	if err != nil {
		t.Fatalf("hook wrote no hints: %v", err)
	}
	// An empty index still has to leave a file: Goose refuses to start when a
	// configured hints file is missing.
	if len(b) == 0 {
		t.Fatal("hints file is empty")
	}

	// With MOIM set the digest goes there instead, so the wrapper does not
	// inject the same text twice.
	moim := filepath.Join(home, "recall.md")
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", moim)
	if err := cmdGooseHook(filepath.Join(home, "idx"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(moim); err != nil {
		t.Fatalf("MOIM file not written: %v", err)
	}
}

func TestRecallCountsReadTheRightFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", "")

	if n := gooseRecallCount(); n != 0 {
		t.Fatalf("count on a missing file = %d", n)
	}
	if n := aiderRecallCount(); n != 0 {
		t.Fatalf("aider count on a missing file = %d", n)
	}
	digest := "<deja-recall>\n  - Session: **a** `1`\n  - Session: **b** `2`\n</deja-recall>\n"
	for _, path := range []string{gooseHintsPath(), aiderContextPath()} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(digest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if n := gooseRecallCount(); n != 2 {
		t.Fatalf("goose count = %d, want 2", n)
	}
	if n := aiderRecallCount(); n != 2 {
		t.Fatalf("aider count = %d, want 2", n)
	}
}

// The wrappers must not start a harness that is not installed, and must say
// which one is missing rather than failing obscurely.
func TestWrappersReportAMissingHarness(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "idx"))
	t.Setenv("PATH", filepath.Join(home, "empty"))
	// cmdGoose sets GOOSE_MOIM_MESSAGE_FILE with a raw os.Setenv so the goose
	// child inherits it. Scope it here, or the value — a path under this test's
	// temp home — leaks to whatever test runs next and sends its goose hints to
	// a directory that no longer exists (a shuffled-order failure of
	// TestInstallGooseAutoWritesHints). t.Setenv's cleanup restores it either way.
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", "")
	for name, run := range map[string]func(string, []string, string) error{"aider": cmdAider, "goose": cmdGoose} {
		err := run(filepath.Join(home, "idx"), []string{"--version"}, "")
		if err == nil {
			t.Fatalf("%s: no error with the harness absent", name)
		}
		if !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), "PATH") {
			t.Fatalf("%s: unhelpful error %v", name, err)
		}
	}
}

func TestAiderContextRefreshLeavesAFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "idx"))
	if err := refreshAiderContext(filepath.Join(home, "idx")); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	b, err := os.ReadFile(aiderContextPath())
	if err != nil {
		t.Fatalf("no context file: %v", err)
	}
	// aider fails to start when a file in read: does not exist, so an empty
	// index must still produce something.
	if strings.TrimSpace(string(b)) == "" {
		t.Fatal("context file is empty; aider would refuse to start")
	}
}

// The index has said "no space left where the index is built" since #888,
// while every other write path handed back the syscall: the notes file, the
// sync export and the stats page all reported ENOSPC as `open /…: no space
// left on device` (#906).
func TestWriteFailureReasonSpeaksOneVocabulary(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"permission", fs.ErrPermission, "permission denied"},
		{"full disk", syscall.ENOSPC, "no space left on that disk"},
		{"disk gone", syscall.EIO, "that disk is not reachable"},
		{"detached", syscall.ENXIO, "that disk is not reachable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := writeFailureReason(fmt.Errorf("open /tmp/x: %w", tc.err))
			if got != tc.want {
				t.Errorf("reason = %q, want %q", got, tc.want)
			}
			if strings.Contains(got, "open /tmp/x") {
				t.Errorf("the syscall came through: %q", got)
			}
		})
	}
	// Anything deja does not recognise comes through whole rather than being
	// guessed at.
	other := errors.New("some other failure")
	if got := writeFailureReason(other); got != other.Error() {
		t.Errorf("unknown cause was reworded: %q", got)
	}
	// exportPromoted's own reasons still win where they are more specific.
	if got := exportFailureReason(fmt.Errorf("open x: %w", fs.ErrNotExist)); got != "its directory does not exist" {
		t.Errorf("export reason = %q", got)
	}
}

// The XDG spec says a relative path in one of these variables is invalid and
// must be ignored. deja honoured it, so `XDG_CONFIG_HOME=relcfg deja install`
// wrote its wiring record into the directory the command was run from — where
// no later run would look for it (#1693).
func TestRelativeXDGConfigHomeIsIgnored(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	wd := t.TempDir()
	t.Chdir(wd)
	t.Setenv("XDG_CONFIG_HOME", "relcfg")

	if _, err := captureRun(t, "install", "claude-code"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wd, "relcfg")); err == nil {
		t.Error("install created relcfg/ in the working directory")
	}
	want := filepath.Join(home, ".config", "deja", "wiring.json")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("the wiring record is not at the spec's fallback %s: %v", want, err)
	}
	if got := wiringStatePath(); !strings.HasPrefix(got, home) {
		t.Errorf("wiringStatePath() is %q, outside the home directory", got)
	}
}

// An absolute value is still honoured, spaces and all.
func TestAbsoluteXDGConfigHomeIsHonoured(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(t.TempDir(), "my cfg")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", base)

	if _, err := captureRun(t, "install", "claude-code"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(base, "deja", "wiring.json")); err != nil {
		t.Errorf("an absolute XDG_CONFIG_HOME was not used: %v", err)
	}
}

// The same rule held for the wiring record but not for the rest of deja's own
// directory: forget put the tombstone list, and install aider and rules sync
// aider's context file, under relcfg/ in the working directory (#5052).
func TestRelativeXDGConfigHomeIsIgnoredByEveryConfigFile(t *testing.T) {
	tmp := hermeticEnv(t)
	writeClaudeTurn(t, filepath.Join(tmp, "claude", "w", "sess-1.jsonl"), "sess-1", "a turn to forget")
	home := os.Getenv("HOME")
	wd := t.TempDir()
	t.Chdir(wd)
	t.Setenv("XDG_CONFIG_HOME", "relcfg")

	if _, err := captureRun(t, "install", "aider", "--no-index"); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(home, ".config", "deja")
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "rules.md"), []byte("Use tabs.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "rules", "sync"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "forget", "--session", "sess-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wd, "relcfg")); err == nil {
		t.Fatal("a command created relcfg/ in the working directory")
	}
	ctx, err := os.ReadFile(filepath.Join(cfg, "aider-context.md"))
	if err != nil || !strings.Contains(string(ctx), "Use tabs.") {
		t.Fatalf("aider's context file under the home directory: %q, %v", ctx, err)
	}
	if conf, _ := os.ReadFile(filepath.Join(home, ".aider.conf.yml")); strings.Contains(string(conf), "relcfg") {
		t.Fatalf("aider was pointed at the relative directory:\n%s", conf)
	}
	if _, err := os.Stat(filepath.Join(cfg, "tombstones")); err != nil {
		t.Fatalf("the tombstone list is not under the home directory: %v", err)
	}
}

// #2604 gave the JSON writers the rule: a container deja added goes with deja,
// one the reader wrote stays. Sweeping the rest of the family, the two YAML
// writers still fail their own version of it — hermes leaves the `mcp_servers:`
// block it added in a config the reader owned, and goose returns the file
// without the trailing newline it came with (#2606).
func TestAYamlConfigComesBackAsItWas(t *testing.T) {
	// The path each writer actually uses, asked rather than restated: goose is
	// one of the few that does not keep its config under ~/.config on Windows,
	// so a hard-coded relative path was a config deja never reads (#2808).
	for _, tc := range []struct {
		name    string
		target  string
		path    func() string
		content string
	}{
		{"hermes", "hermes", func() string {
			return filepath.Join(os.Getenv("HOME"), ".hermes", "config.yaml")
		}, "profile: default\n"},
		{"goose", "goose", func() string {
			return filepath.Join(gooseConfigDir(), "config.yaml")
		}, "GOOSE_MODEL: gpt-5\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			path := tc.path()
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := captureRun(t, "install", tc.target, "--no-index", "--no-guidance"); err != nil {
				t.Fatal(err)
			}
			// The premise: deja really wired itself into that file.
			if b, err := os.ReadFile(path); err != nil || !strings.Contains(string(b), "deja") {
				t.Fatalf("install wired nothing here: %v", err)
			}
			if _, err := captureRun(t, "uninstall", tc.target); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("the reader's config is gone: %v", err)
			}
			if string(b) != tc.content {
				t.Errorf("the config did not come back as it was:\nwant %q\ngot  %q", tc.content, b)
			}
		})
	}
}

// A container the reader wrote survives, entries and all.
func TestAYamlConfigKeepsTheBlockTheReaderWrote(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	path := filepath.Join(home, ".hermes", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := "profile: default\n\nmcp_servers:\n  theirs:\n    command: \"x\"\n"
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "hermes", "--no-index", "--no-guidance"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "hermes"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "theirs") || !strings.Contains(string(b), "mcp_servers:") {
		t.Errorf("the reader's own block did not survive:\n%s", b)
	}
}

// The writers ask what indent a block uses; these two readers assumed two
// spaces. Uninstall then left hermes wired, and doctor called a goose install
// it had just done unwired (#2727).
func TestHermesUninstallTakesItsBlockOutAtAnyIndent(t *testing.T) {
	for _, c := range []struct{ name, indent string }{
		{"one space", " "},
		{"two spaces", "  "},
		{"four spaces", "    "},
	} {
		t.Run(c.name, func(t *testing.T) {
			in := "mcp_servers:\n" +
				c.indent + "deja:\n" +
				c.indent + c.indent + "cmd: /old/deja\n" +
				c.indent + "mine:\n" +
				c.indent + c.indent + "cmd: /usr/bin/mine\n"
			got := removeHermesMCPBlock(in)
			if strings.Contains(got, "deja:") {
				t.Errorf("deja's own block survived the uninstall:\n%s", got)
			}
			if !strings.Contains(got, "mine:") || !strings.Contains(got, "/usr/bin/mine") {
				t.Errorf("the reader's own server did not survive:\n%s", got)
			}
			if !strings.Contains(got, "mcp_servers:") {
				t.Errorf("the key went with it:\n%s", got)
			}
		})
	}
}

// And what doctor says about a config deja itself wrote.
func TestDoctorReadsAWiredBlockAtAnyIndent(t *testing.T) {
	for _, c := range []struct{ name, indent string }{
		{"one space", " "},
		{"two spaces", "  "},
		{"four spaces", "    "},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			goose := filepath.Join(dir, "config.yaml")
			if err := os.WriteFile(goose, []byte("extensions:\n"+
				c.indent+"deja:\n"+c.indent+c.indent+"cmd: /opt/deja\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if !doctorGooseWired(goose) {
				t.Errorf("doctor calls a wired goose unwired at this indent")
			}
			hermes := filepath.Join(dir, "hermes.yaml")
			if err := os.WriteFile(hermes, []byte("mcp_servers:\n"+
				c.indent+"deja:\n"+c.indent+c.indent+"cmd: /opt/deja\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if !doctorHermesWired(hermes) {
				t.Errorf("doctor calls a wired hermes unwired at this indent")
			}
		})
	}
}

// A key at the top level is not an entry under anything, and a server someone
// else named is not deja.
func TestDoctorDoesNotReadATopLevelKeyAsAWiredServer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("deja:\n  note: not a server block\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if doctorGooseWired(path) {
		t.Error("a top-level key was read as a wired extension")
	}
	if err := os.WriteFile(path, []byte("extensions:\n  dejavu:\n    cmd: /opt/other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if doctorGooseWired(path) {
		t.Error("another name that starts the same was read as deja")
	}
}

// The blank line after deja's block belongs to the file, not to the block:
// swallowing it made a second install differ from the first, which is what
// TestInstallIsIdempotent is for (#2727).
func TestHermesUninstallLeavesTheFileAroundItAlone(t *testing.T) {
	in := "mcp_servers:\n" +
		"  mine:\n    cmd: /usr/bin/mine\n" +
		"\n" +
		"  deja:\n    cmd: /old/deja\n" +
		"\n" +
		"plugins:\n  enabled:\n    - other\n"
	want := "mcp_servers:\n" +
		"  mine:\n    cmd: /usr/bin/mine\n" +
		"\n" +
		"\n" +
		"plugins:\n  enabled:\n    - other\n"
	if got := removeHermesMCPBlock(in); got != want {
		t.Errorf("the file around deja's block changed:\nwant:\n%q\ngot:\n%q", want, got)
	}
}

// A comment inside deja's own block goes with it; one belonging to what
// follows does not.
func TestHermesUninstallTakesTheCommentsInsideItsBlock(t *testing.T) {
	in := "mcp_servers:\n" +
		"  deja:\n" +
		"    # written by deja install\n" +
		"    cmd: /old/deja\n" +
		"  # the reader's note about their own server\n" +
		"  mine:\n    cmd: /usr/bin/mine\n"
	got := removeHermesMCPBlock(in)
	if strings.Contains(got, "written by deja install") {
		t.Errorf("a comment inside deja's block stayed:\n%s", got)
	}
	if !strings.Contains(got, "the reader's note") {
		t.Errorf("the reader's own comment went with it:\n%s", got)
	}
}

// deja's entry is a direct child of the block it belongs to. Reading `deja:`
// at any depth emptied a server of somebody else's whose env holds a key by
// that name — deja breaking a config while claiming to remove itself (#2730).
func TestHermesUninstallLeavesAForeignKeyByThatNameAlone(t *testing.T) {
	in := "mcp_servers:\n" +
		"  other:\n    cmd: /o\n    env:\n      deja:\n        HOME: /x\n    enabled: true\n" +
		"  third:\n    cmd: /t\n"
	if got := removeHermesMCPBlock(in); got != in {
		t.Errorf("a config with no deja server was rewritten:\nwant:\n%s\ngot:\n%s", in, got)
	}
}

// And what doctor calls wired: the same key, in the same places.
func TestDoctorDoesNotReadAMentionAsAWiredServer(t *testing.T) {
	for _, c := range []struct{ name, body string }{
		{"under another server's env", "mcp_servers:\n  other:\n    env:\n      deja:\n        x: 1\n"},
		{"in an example under notes", "mcp_servers:\n  a:\n    cmd: /a\nnotes:\n  examples:\n    deja:\n      how: to wire it\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(c.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if doctorHermesWired(path) {
				t.Errorf("hermes was called wired by a mention:\n%s", c.body)
			}
			if doctorGooseWired(path) {
				t.Errorf("goose was called wired by a mention:\n%s", c.body)
			}
		})
	}
}

// The writer wrote its entry at two spaces whatever the file used, so on a
// config indented at three or four deja's entry landed shallower than its
// siblings — and the uninstall then took those siblings with it (#2730).
func TestHermesRoundTripsAtEveryIndent(t *testing.T) {
	for _, c := range []struct{ name, indent string }{
		{"one space", " "},
		{"two spaces", "  "},
		{"three spaces", "   "},
		{"four spaces", "    "},
	} {
		t.Run(c.name, func(t *testing.T) {
			tmp := hermeticEnv(t)
			t.Setenv("DEJA_HERMES_ROOT", filepath.Join(tmp, "hermes"))
			path := filepath.Join(sources.HermesHome(), "config.yaml")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			before := "# hermes config\nmcp_servers:\n" +
				c.indent + "before:\n" + c.indent + c.indent + "cmd: /b\n" +
				c.indent + "after:\n" + c.indent + c.indent + "cmd: /a\n"
			if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
				t.Fatal(err)
			}

			if _, err := installHermesMCP("/opt/deja", false); err != nil {
				t.Fatal(err)
			}
			wired, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"before:", "after:", "deja:", hookExeInConfigs("/opt/deja")} {
				if !strings.Contains(string(wired), want) {
					t.Fatalf("%q is missing after the install:\n%s", want, wired)
				}
			}
			if !strings.Contains(string(wired), c.indent+"deja:") {
				t.Errorf("deja's entry was not written at the block's indent:\n%s", wired)
			}

			if _, err = installHermesMCP("/opt/deja", true); err != nil {
				t.Fatal(err)
			}
			back, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(back) != before {
				t.Errorf("the config did not come back:\nwas:\n%q\nnow:\n%q", before, back)
			}
		})
	}
}

// A 3.17+ desktop build reads config.json, so hooks have to reach it even
// when config.json has no hooks block at all yet (a fresh install on a machine
// whose runtime has not written one). The version is the primary signal; the
// file shape is only the fallback when the version cannot be read.
func TestZCodeDesktopVersionSelectsTheLiveConfig(t *testing.T) {
	hermeticEnv(t)
	dir := filepath.Join(os.Getenv("HOME"), ".zcode", "cli")
	legacy := filepath.Join(dir, "config.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// No hooks block: only the version can identify this as a live config.
	bare := `{"mcp":{"servers":{"docs":{"command":"docs-server"}}}}`
	if err := os.WriteFile(legacy, []byte(bare), 0o600); err != nil {
		t.Fatal(err)
	}

	orig := zcodeDesktopVersion
	defer func() { zcodeDesktopVersion = orig }()

	zcodeDesktopVersion = func() ([3]int, bool) { return [3]int{3, 17, 2}, true }
	if _, err := installZCodeHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(legacy)
	if !strings.Contains(string(b), "hook-context") {
		t.Errorf("3.17+ did not get hooks in config.json: %s", b)
	}
	if strings.Contains(string(b), `"deja"`) {
		t.Errorf("config.json gained a server, which the migration would carry back: %s", b)
	}

	// A fresh 3.17+ install before config.json even exists must also recognise
	// config.json as the live runtime config.
	homeFresh := t.TempDir()
	t.Setenv("HOME", homeFresh)
	t.Setenv("USERPROFILE", homeFresh)
	if !zcodeConfigIsLiveRuntimeConfig() {
		t.Error("3.17+ without config.json was not recognised as live runtime config")
	}

	// An older runtime keeps the setting.json behaviour: config.json is a
	// first-launch migration source and must stay byte for byte.
	home2 := t.TempDir()
	t.Setenv("HOME", home2)
	t.Setenv("USERPROFILE", home2)
	dir2 := filepath.Join(home2, ".zcode", "cli")
	legacy2 := filepath.Join(dir2, "config.json")
	if err := os.MkdirAll(dir2, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy2, []byte(bare), 0o600); err != nil {
		t.Fatal(err)
	}
	zcodeDesktopVersion = func() ([3]int, bool) { return [3]int{3, 14, 4}, true }
	if _, err := installZCodeHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(legacy2); string(got) != bare {
		t.Errorf("3.14.4 config.json was changed:\n%s", got)
	}

	// Version unreadable: fall back to the file's shape, so a live config with
	// a hooks block is still recognised on another platform.
	home3 := t.TempDir()
	t.Setenv("HOME", home3)
	t.Setenv("USERPROFILE", home3)
	dir3 := filepath.Join(home3, ".zcode", "cli")
	legacy3 := filepath.Join(dir3, "config.json")
	if err := os.MkdirAll(dir3, 0o755); err != nil {
		t.Fatal(err)
	}
	live := `{"hooks":{"enabled":true,"events":{"PreToolUse":[]}}}`
	if err := os.WriteFile(legacy3, []byte(live), 0o600); err != nil {
		t.Fatal(err)
	}
	zcodeDesktopVersion = func() ([3]int, bool) { return [3]int{}, false }
	if !zcodeConfigIsLiveRuntimeConfig() {
		t.Error("a live config with hooks.events was not recognised without a version")
	}
}

// doctor reports against whichever file exists. A 3.17+ machine has only
// config.json, so asking for setting.json read a wired install as missing.
func TestDoctorZCodeNamesTheFileThatIsThere(t *testing.T) {
	hermeticEnv(t)
	dir := filepath.Join(os.Getenv("HOME"), ".zcode", "cli")
	legacy := filepath.Join(dir, "config.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(`{"mcp":{"servers":{"deja":{"command":"/usr/local/bin/deja","args":["mcp"]}}},"hooks":{"enabled":true,"events":{"SessionStart":[{"hooks":[{"type":"command","command":"/usr/local/bin/deja hook-context --strict","timeout":30}]}]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := zcodeDoctorPath(); got != legacy {
		t.Errorf("doctor path = %q, want %q", got, legacy)
	}
	out, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out, "zcode") {
		t.Errorf("doctor has no zcode row:\n%s", out)
	}
	if strings.Contains(out, "setting.json") {
		t.Errorf("doctor still names setting.json on a config.json machine:\n%s", out)
	}
}

// `omitempty` does nothing to a struct, and time.Time is one: a field tagged
// that way is always written, and a zero time reads as January of year 1. The
// document promises `recall.since` is absent on a store with no recall history
// (#1874).
func TestStatsOmitsTheRecallWindowItNeverOpened(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"s1","cwd":"/proj","timestamp":"2026-08-20T10:00:00Z","message":{"role":"user","content":"fix the parser"}}`
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "stats", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Recall map[string]any `json:"recall"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if v, ok := report.Recall["since"]; ok {
		t.Errorf("a store that has served no recall reports a window opening at %v", v)
	}
	// The control: the block itself is still there, so the absence above is an
	// omitted field and not a missing section.
	if _, ok := report.Recall["recalls_served"]; !ok {
		t.Errorf("the recall block is gone entirely: %v", report.Recall)
	}
}

// The same tag on the same type is elsewhere in the emitted JSON. This walks
// for it rather than waiting to meet the next one: a new field written this way
// is a documented omission that never happens.
func TestNoEmittedTimestampReliesOnOmitemptyAloneToVanish(t *testing.T) {
	// internal/peers is deja's own file rather than a reported contract, and a
	// year-1 stamp there parses back to a zero time.Time, so nothing reads it
	// wrong. Recorded here so the exception is a decision rather than a gap.
	allowed := map[string]bool{
		"../../internal/peers/peers.go": true,
	}
	// A pointer is the fix, not the flaw: *time.Time with omitempty does
	// vanish. Only the bare struct is the bug.
	tagged := regexp.MustCompile(`[^*\w.]time\.Time\s+` + "`" + `json:"[^"]*,omitempty"`)
	var found []string
	roots := []string{".", "../../internal"}
	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			if allowed[filepath.ToSlash(path)] {
				return nil
			}
			src, err := os.ReadFile(path) //nolint:gosec // test walks the repo's own source
			if err != nil {
				return err
			}
			if m := tagged.FindString(string(src)); m != "" {
				found = append(found, path+": "+m)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(found) > 0 {
		t.Errorf("these fields are written even when the time is zero, which reads as year 1:\n%s", strings.Join(found, "\n"))
	}
}

// The Chinese numerals the count can be written with. Separate from countWords
// because none of the checks around this one can read them: the word check
// scans for "twenty-five", the digit check wants digits, and both skip
// README.zh.md and docs/zh entirely. That README shipped 二十五 — the count from
// eight harnesses ago — while every English document said thirty-three, and its
// lede named only the first twenty-five agents.
var zhCountWords = map[int]string{
	15: "十五", 16: "十六", 17: "十七", 18: "十八", 19: "十九",
	20: "二十", 21: "二十一", 22: "二十二", 23: "二十三", 24: "二十四",
	25: "二十五", 26: "二十六", 27: "二十七", 28: "二十八", 29: "二十九",
	30: "三十", 31: "三十一", 32: "三十二", 33: "三十三", 34: "三十四",
	35: "三十五", 36: "三十六", 37: "三十七", 38: "三十八", 39: "三十九",
	40: "四十", 41: "四十一", 42: "四十二", 43: "四十三",
	44: "四十四", 45: "四十五", 46: "四十六", 47: "四十七", 48: "四十八", 49: "四十九", 50: "五十",
}

// A bare numeral scan does not work here: 三十条任务链 and 十三个项目 count other
// things and are meant to stay. The counted noun is what turns a numeral into a
// claim about harnesses, so these are the shapes to pin.
var zhHarnessClaims = []*regexp.Regexp{
	regexp.MustCompile(`([一二三四五六七八九十]+)个(?:编程)?(?:智能体|工具)`),
	regexp.MustCompile(`今天是([一二三四五六七八九十]+)个`),
}

func TestChineseDocsCountTheHarnessesTheRegistryHas(t *testing.T) {
	root := filepath.Join("..", "..")
	n := registryHarnessCount(t, root)
	want, ok := zhCountWords[n]
	if !ok {
		t.Fatalf("registry has %d harnesses and this test has no Chinese numeral for it; add one", n)
	}

	// docs/llms.txt carries the Chinese list too, and it is neither under
	// docs/zh nor named for the language, so the walk below never reached it:
	// its line said 三十三个智能体 while the English lines in the same file said
	// thirty-four.
	files := []string{"docs/readme/README.zh.md", filepath.ToSlash(filepath.Join("docs", "llms.txt"))}
	// docs/zh was the whole list, so the count went stale outside it:
	// `extensions/dsh/docs/zh.md` said twenty-one agents, twelve releases out
	// of date. A Chinese page under extensions/ is a Chinese page, and the
	// walk covers both trees.
	for _, dir := range []string{filepath.Join(root, "docs", "zh"), filepath.Join(root, "extensions")} {
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			switch filepath.Ext(path) {
			case ".html":
			case ".md":
				// Only the translated ones: an English README states the count
				// in words and is checked by the test beside this one.
				if !strings.Contains(filepath.Base(path), "zh") {
					return nil
				}
			default:
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
	}

	claims := 0
	for _, name := range files {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		text := string(b)
		for _, re := range zhHarnessClaims {
			for _, loc := range re.FindAllStringSubmatchIndex(text, -1) {
				num := text[loc[2]:loc[3]]
				if !knownZhNumeral(num) {
					continue
				}
				claims++
				// 其他 is the Chinese "other": a document shipping inside one
				// harness counts the rest, the same relative shape the English
				// test allows. It moves with the total without equalling it.
				expect := want
				if before := text[max(0, loc[2]-len("其他")):loc[2]]; before == "其他" {
					expect = zhCountWords[n-1]
				}
				if num != expect {
					t.Errorf("%s says %q; the registry has %d, so it is %s", name, text[loc[0]:loc[1]], n, expect)
				}
			}
		}
		// The lede names the agents one by one, and adding a harness never
		// touches that sentence. A correct count over a short list is worse
		// than no count.
		if name == "docs/readme/README.zh.md" {
			list := zhNamedList(text)
			if list == "" {
				t.Error("README.zh.md no longer names the harnesses; if that is on purpose, this check goes with it")
			} else if got := strings.Count(list, "·") + 1; got != n {
				t.Errorf("README.zh.md names %d harnesses; the registry has %d", got, n)
			}
		}
	}
	if claims == 0 {
		t.Fatal("no Chinese document says how many harnesses there are; if that is on purpose, this test goes with it")
	}
}

// knownZhNumeral reports whether a numeral is one of the counts this test can
// be about, so 十三个项目 and other unrelated counts are left alone.
func knownZhNumeral(s string) bool {
	for _, w := range zhCountWords {
		if w == s {
			return true
		}
	}
	return false
}

// zhNamedList returns the paragraph that lists the harnesses by name — the one
// closed by the full stop Chinese uses, right after Zed.
func zhNamedList(text string) string {
	const end = "Zed。"
	i := strings.LastIndex(text, end)
	if i < 0 {
		return ""
	}
	start := strings.LastIndex(text[:i], "\n\n")
	if start < 0 {
		return ""
	}
	return text[start+2 : i+len(end)]
}

// The Chinese half of the resume claim, which the English check cannot see: it
// scans for "twenty-six of the thirty-three", and the translated page states
// the same thing as 三十三个智能体里有二十六个. That sentence went stale once
// already — it was faithfully translated from the English page while the
// English page was wrong — and the numeral check beside this one pins only the
// total, because 二十六个 has no counted noun after it.
//
// The shape of this check comes from @theluckystrike's PR #3783.
func TestChineseResumeClaimMatchesTheRegistry(t *testing.T) {
	root := filepath.Join("..", "..")
	reg := readHarnessResume(t, root)

	wantTotal, wantResumes := zhNumeral(reg.total), zhNumeral(reg.resumes)
	wantWithout := zhNumeral(len(reg.without))
	if wantTotal == "" || wantResumes == "" || wantWithout == "" {
		t.Fatalf("no Chinese numeral for %d/%d/%d", reg.total, reg.resumes, len(reg.without))
	}

	// 三十三个智能体里有二十六个 — "of the thirty-three agents, twenty-six".
	pair := regexp.MustCompile(`([一二三四五六七八九十]+)个智能体里有([一二三四五六七八九十]+)个`)
	// 剩下七个——aider、…、Zed——没有自己的恢复入口 — the ones that cannot.
	rest := regexp.MustCompile(`剩下([一二三四五六七八九十]+)个——([^—]*)——`)

	pairs, lists := 0, 0
	err := filepath.WalkDir(filepath.Join(root, "docs", "zh"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".html" {
			return err
		}
		b, err := os.ReadFile(path) //nolint:gosec // test walks the repo's own site pages
		if err != nil {
			return err
		}
		text := string(b)
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)

		for _, m := range pair.FindAllStringSubmatch(text, -1) {
			if !isZhNumeral(m[1]) || !isZhNumeral(m[2]) {
				continue
			}
			pairs++
			if m[1] != wantTotal {
				t.Errorf("%s says %q; the registry has %d harnesses (%s)", name, m[0], reg.total, wantTotal)
			}
			if m[2] != wantResumes {
				t.Errorf("%s says %q; %d harnesses resume (%s)", name, m[0], reg.resumes, wantResumes)
			}
		}

		for _, m := range rest.FindAllStringSubmatch(text, -1) {
			if !isZhNumeral(m[1]) {
				continue
			}
			lists++
			if m[1] != wantWithout {
				t.Errorf("%s says 剩下%s个; %d harnesses have no resume path (%s)",
					name, m[1], len(reg.without), wantWithout)
			}
			// The names stay in Latin script in the translated sentence, so they
			// are comparable to the registry's display names — but only as whole
			// list items: "pi" is inside "VS Code Copilot Chat", and a substring
			// match reads the sentence as naming a harness that resumes. The
			// list is separated by 、 and closed with 和.
			named := map[string]bool{}
			for item := range strings.SplitSeq(strings.ReplaceAll(m[2], "和", "、"), "、") {
				if item = strings.TrimSpace(item); item != "" {
					named[item] = true
				}
			}
			// This is the half that was wrong rather than merely old: Continue
			// was listed here as unable to resume while `cn --fork` reopens it.
			for _, missing := range reg.without {
				if !named[missing] {
					t.Errorf("%s does not name %s among the harnesses without a resume path", name, missing)
				}
			}
			for display, resumes := range reg.names {
				if !resumes || display == "Roo Code" || display == "Kilo Code" {
					continue
				}
				if named[display] {
					t.Errorf("%s lists %s among the harnesses without a resume path; the registry says it has one", name, display)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if pairs == 0 {
		t.Fatal("no Chinese page states how many harnesses resume; if that is on purpose, this test goes with it")
	}
	if lists == 0 {
		t.Fatal("no Chinese page lists the harnesses that cannot resume; if that is on purpose, this check goes with it")
	}
}

// zhNumeral writes a count the way these pages do: 七, 十, 十一, 二十六, 三十三.
// zhCountWords next door starts at fifteen, because the harness count will
// never be seven again — but the number of harnesses that refuse to resume is
// small, and it is the other half of the same sentence.
func zhNumeral(n int) string {
	digits := []rune("一二三四五六七八九")
	switch {
	case n < 1 || n > 99:
		return ""
	case n < 10:
		return string(digits[n-1])
	case n < 20:
		if n == 10 {
			return "十"
		}
		return "十" + string(digits[n-10-1])
	}
	out := string(digits[n/10-1]) + "十"
	if n%10 != 0 {
		out += string(digits[n%10-1])
	}
	return out
}

// isZhNumeral keeps the scans off numerals that count something else, the way
// the English checks skip words that are not counts.
func isZhNumeral(s string) bool {
	for n := 1; n <= 99; n++ {
		if zhNumeral(n) == s {
			return true
		}
	}
	return false
}
