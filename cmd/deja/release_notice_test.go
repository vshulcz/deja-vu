package main

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func withVersion(t *testing.T, v string) {
	t.Helper()
	saved := version
	version = v
	t.Cleanup(func() { version = saved })
}

// releaseNoticeEnv clears the switches a developer's shell may carry, and
// points the index at a temp dir so nothing lands next to the real one.
func releaseNoticeEnv(t *testing.T) string {
	t.Helper()
	t.Setenv(releaseNoticeOff, "")
	t.Setenv("DEJA_OFFLINE", "")
	t.Setenv(releaseLookEnv, "")
	dir := filepath.Join(t.TempDir(), "index")
	t.Setenv("DEJA_INDEX_DIR", dir)
	return dir
}

// countingTransport answers every request with a release and counts them, so
// a test sees each request deja tried to make.
type countingTransport struct{ n atomic.Int32 }

func (c *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	c.n.Add(1)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"tag_name":"v1.2.0"}`)),
	}, nil
}

func withCountingTransport(t *testing.T) *countingTransport {
	t.Helper()
	ct := &countingTransport{}
	saved := http.DefaultTransport
	http.DefaultTransport = ct
	t.Cleanup(func() { http.DefaultTransport = saved })
	return ct
}

// The line names both versions, the upgrade command for whatever installed the
// binary, and the variable that hides it; it says nothing when there is
// nothing newer (#4622).
func TestReleaseNoticeLine(t *testing.T) {
	line := releaseNoticeLine("1.0.0", "1.2.0", "/usr/local/bin/deja")
	for _, want := range []string{"v1.2.0", "v1.0.0", "`deja update`", releaseNoticeOff + "=1"} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q does not contain %q", line, want)
		}
	}
	if got := releaseNoticeLine("1.0.0", "1.2.0", "/opt/homebrew/Cellar/deja-vu/1.0.0/bin/deja"); !strings.Contains(got, "`brew upgrade deja-vu`") {
		t.Errorf("a Homebrew binary is told %q", got)
	}
	for _, latest := range []string{"1.0.0", "0.9.0", ""} {
		if got := releaseNoticeLine("1.0.0", latest, "/usr/local/bin/deja"); got != "" {
			t.Errorf("latest %q: got %q, want nothing", latest, got)
		}
	}
}

func TestReleaseNoticeNeverStartsALookWhereItMayNotSpeak(t *testing.T) {
	withVersion(t, "1.0.0")
	dir := releaseNoticeEnv(t)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	var spawns atomic.Int32
	spawn := func(string) error { spawns.Add(1); return nil }
	refuse := func(name string, n *releaseNotice) {
		t.Helper()
		if n != nil || spawns.Load() != 0 {
			t.Errorf("%s: got a notice (spawns %d)", name, spawns.Load())
		}
		if _, err := os.Stat(dir + ".release"); err == nil {
			t.Errorf("%s: wrote a stamp", name)
		}
	}

	refuse("not interactive", startReleaseNotice([]string{"search", "x"}, false, dir, now, spawn))
	for _, cmd := range []string{"update", "doctor", "version", "--version", "-version"} {
		refuse("deja "+cmd, startReleaseNotice([]string{cmd}, true, dir, now, spawn))
	}
	refuse("relative index dir", startReleaseNotice(nil, true, filepath.Join(".cache", "deja", "index"), now, spawn))

	t.Setenv(releaseNoticeOff, "1")
	refuse(releaseNoticeOff+"=1", startReleaseNotice(nil, true, dir, now, spawn))
	t.Setenv(releaseNoticeOff, "")

	t.Setenv("DEJA_OFFLINE", "1")
	// Offline there is no look and no line, only what a new version brings.
	if n := startReleaseNotice(nil, true, dir, now, spawn); n == nil || !n.offline || spawns.Load() != 0 {
		t.Errorf("DEJA_OFFLINE=1: notice %+v, spawns %d", n, spawns.Load())
	}
	if _, err := os.Stat(dir + ".release"); err == nil {
		t.Error("DEJA_OFFLINE=1 wrote a stamp")
	}
	t.Setenv("DEJA_OFFLINE", "")

	withVersion(t, "dev")
	refuse("dev build", startReleaseNotice(nil, true, dir, now, spawn))
	withVersion(t, "1.0.0")

	// `-v` is a search for "-v", so it qualifies like any other search.
	if startReleaseNotice([]string{"-v"}, true, dir, now, spawn) == nil || spawns.Load() != 1 {
		t.Errorf("`deja -v` got no look (spawns %d)", spawns.Load())
	}
}

// One look a day, in another process; what it found is printed by the next
// run with no wait, once a day, and never for a release that is not newer.
func TestReleaseNoticeLooksOnceADayAndPrintsWhatTheLookFound(t *testing.T) {
	withVersion(t, "1.0.0")
	dir := releaseNoticeEnv(t)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	stamp := dir + ".release"
	now := time.Unix(1_800_000_000, 0)
	var spawned []string
	spawn := func(s string) error { spawned = append(spawned, s); return nil }
	exe := "/usr/local/bin/deja"
	runAt := func(at time.Time) string {
		var out bytes.Buffer
		startReleaseNotice([]string{"search", "x"}, true, dir, at, spawn).finish(&out, exe)
		return out.String()
	}

	if out := runAt(now); out != "" || len(spawned) != 1 || spawned[0] != stamp {
		t.Fatalf("first run: printed %q, spawned %v", out, spawned)
	}
	// The child answers after the command has ended.
	runReleaseLook(stamp, func() (string, bool) { return "1.2.0", true })

	if out := runAt(now.Add(time.Hour)); !strings.Contains(out, "deja v1.2.0 is out") {
		t.Fatalf("the run after the look said %q", out)
	}
	if out := runAt(now.Add(2 * time.Hour)); out != "" {
		t.Errorf("the line came back the same day: %q", out)
	}
	if len(spawned) != 1 {
		t.Errorf("a second look inside the day: %v", spawned)
	}
	if out := runAt(now.Add(25 * time.Hour)); !strings.Contains(out, "deja v1.2.0 is out") || len(spawned) != 2 {
		t.Errorf("next day: printed %q, spawned %v", out, spawned)
	}

	withVersion(t, "1.2.0")
	if out := runAt(now.Add(50 * time.Hour)); out != "" {
		t.Errorf("an upgraded binary was told %q", out)
	}
}

// DEJA_OFFLINE=1 is the opt-out SECURITY-MODEL.md names: with it set the look
// makes no request at all, even in a child started before it was set.
func TestReleaseLookMakesNoRequestOffline(t *testing.T) {
	withVersion(t, "1.0.0")
	dir := releaseNoticeEnv(t)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	stamp := dir + ".release"
	ct := withCountingTransport(t)

	t.Setenv("DEJA_OFFLINE", "1")
	runReleaseLook(stamp, defaultDoctorVersionLookup())
	if got := ct.n.Load(); got != 0 {
		t.Fatalf("DEJA_OFFLINE=1 made %d requests", got)
	}
	if _, err := os.Stat(stamp); err == nil {
		t.Error("DEJA_OFFLINE=1 wrote a stamp")
	}
	started := false
	if n := startReleaseNotice([]string{"search", "x"}, true, dir, time.Now(), func(string) error { started = true; return nil }); n == nil || started {
		t.Error("DEJA_OFFLINE=1 started a look or dropped what changed")
	}

	// The same look without it reaches the transport, so the zero above is
	// the switch working and not a stub that sees nothing.
	t.Setenv("DEJA_OFFLINE", "")
	runReleaseLook(stamp, defaultDoctorVersionLookup())
	if got := ct.n.Load(); got != 1 {
		t.Fatalf("online look made %d requests, want 1", got)
	}
	if got := readReleaseStamp(stamp).Latest; got != "1.2.0" {
		t.Errorf("stamp kept %q, want 1.2.0", got)
	}
}

// The child is only `deja version` with the stamp of this very index: any
// other command, or a path elsewhere, is not taken as a look.
func TestReleaseLookRequestedOnlyForThisIndex(t *testing.T) {
	dir := releaseNoticeEnv(t)
	stamp := dir + ".release"
	t.Setenv(releaseLookEnv, stamp)
	if got, ok := releaseLookRequested([]string{"version"}, dir); !ok || got != stamp {
		t.Errorf("the child was not recognised: %q %v", got, ok)
	}
	if _, ok := releaseLookRequested([]string{"search", "version"}, dir); ok {
		t.Error("a search was taken for the look")
	}
	t.Setenv(releaseLookEnv, filepath.Join(t.TempDir(), "elsewhere.release"))
	if _, ok := releaseLookRequested([]string{"version"}, dir); ok {
		t.Error("a stamp outside the index was accepted")
	}
}

// What a new version brings is in the binary, so offline still says it.
func TestWhatChangedShowsOffline(t *testing.T) {
	withVersion(t, "0.21.7")
	dir := releaseNoticeEnv(t)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+".lastversion", []byte("0.21.5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_OFFLINE", "1")
	var out bytes.Buffer
	startReleaseNotice([]string{"last"}, true, dir, time.Now(), func(string) error { return nil }).finish(&out, "/usr/local/bin/deja")
	if !strings.Contains(out.String(), "what changed") {
		t.Errorf("offline first run printed %q", out.String())
	}
}
