package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// releaseNoticeOff is the variable that silences the once-a-day line (#4622).
const releaseNoticeOff = "DEJA_NO_UPDATE_NOTICE"

// releaseLookEnv carries the stamp path to the detached child that does the
// look. The child is `deja version` with this set, so an older binary that
// gets it after an upgrade prints its version into /dev/null and exits.
const releaseLookEnv = "DEJA_RELEASE_LOOK"

// releaseNoticeInterval is how long one look at the latest release stands,
// and how long a shown line stays quiet.
const releaseNoticeInterval = 24 * time.Hour

// releaseNoticeSkips are the commands that already speak about versions, or
// whose whole job is the upgrade: a second line there would only repeat them.
// `-v` is not here: it is a search for "-v", not a version flag.
var releaseNoticeSkips = map[string]bool{
	"update": true, "doctor": true, "version": true, "--version": true, "-version": true,
}

// releaseStamp sits next to the index as `<index dir>.release`. Looked is the
// last time a look started, Latest what the last finished look found, Shown
// the last time the line was printed.
type releaseStamp struct {
	Looked int64  `json:"looked"`
	Latest string `json:"latest,omitempty"`
	Shown  int64  `json:"shown,omitempty"`
}

func readReleaseStamp(path string) releaseStamp {
	var s releaseStamp
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

func writeReleaseStamp(path string, s releaseStamp) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// releaseNotice prints what an earlier look found. The look itself runs in a
// detached process, so the command never waits on the network and an answer
// that comes back after the command ends is kept for the next one.
type releaseNotice struct {
	dir     string
	stamp   string
	current string
	now     time.Time
	offline bool // no look and no line, but what changed still shows
}

// startReleaseNotice decides whether this run may show the line and, once a
// day, starts a look in the background. Only an interactive run qualifies:
// both stdout and stderr a terminal, which a hook or the MCP server never has.
// DEJA_NO_UPDATE_NOTICE=1 turns the whole thing off. DEJA_OFFLINE=1 turns off
// the look, so no request is made; what a new version brings comes from the
// binary and still shows. The look time is written before the look, so a
// failed or offline look waits a day instead of retrying on every command.
func startReleaseNotice(args []string, interactive bool, dir string, now time.Time, spawn func(stamp string) error) *releaseNotice {
	if !interactive || os.Getenv(releaseNoticeOff) != "" {
		return nil
	}
	if len(args) > 0 && releaseNoticeSkips[args[0]] {
		return nil
	}
	// Without a home the index dir is relative, and the stamp would land in
	// whatever directory deja was run from (#1692).
	if !filepath.IsAbs(dir) {
		return nil
	}
	current := normalizeUpdateVersion(version)
	if _, ok := parseUpdateVersion(current); !ok {
		return nil // a dev build has nothing to be behind
	}
	stamp := dir + ".release"
	if os.Getenv("DEJA_OFFLINE") == "1" {
		return &releaseNotice{dir: dir, stamp: stamp, current: current, now: now, offline: true}
	}
	s := readReleaseStamp(stamp)
	if now.Sub(time.Unix(s.Looked, 0)) >= releaseNoticeInterval {
		s.Looked = now.Unix()
		if err := writeReleaseStamp(stamp, s); err != nil {
			return nil // a look nobody can record would repeat on every run
		}
		_ = spawn(stamp)
	}
	return &releaseNotice{dir: dir, stamp: stamp, current: current, now: now}
}

// finish prints the line when the last look found a newer release and the
// line has not been shown in the last day. Before that it prints, once per
// version, what a new version brings on its first run after an upgrade.
func (n *releaseNotice) finish(w io.Writer, exe string) {
	if n == nil {
		return
	}
	// The first run of a new version says once what it brings (#4619).
	if msg := whatChangedTerminal(n.dir); msg != "" {
		fmt.Fprint(w, msg)
	}
	if n.offline {
		return
	}
	s := readReleaseStamp(n.stamp)
	if n.now.Sub(time.Unix(s.Shown, 0)) < releaseNoticeInterval {
		return
	}
	line := releaseNoticeLine(n.current, s.Latest, exe)
	if line == "" {
		return
	}
	s.Shown = n.now.Unix()
	if err := writeReleaseStamp(n.stamp, s); err != nil {
		return // shown without a record, it would be shown on every run
	}
	fmt.Fprintln(w, line)
}

// spawnReleaseLook starts the detached look: `deja version` with the stamp
// path in releaseLookEnv, its output thrown away.
func spawnReleaseLook(stamp string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer func() { _ = devNull.Close() }()
	cmd := exec.Command(exe, "version")
	cmd.Env = append(os.Environ(), releaseLookEnv+"="+stamp)
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	return startDetached(cmd)
}

// releaseLookRequested reports whether this process is the detached look. The
// stamp must be the one next to this index, so the variable cannot point the
// write anywhere else.
func releaseLookRequested(args []string, dir string) (string, bool) {
	stamp := os.Getenv(releaseLookEnv)
	if stamp == "" || len(args) != 1 || args[0] != "version" {
		return "", false
	}
	if !filepath.IsAbs(dir) || stamp != dir+".release" {
		return "", false
	}
	return stamp, true
}

// runReleaseLook is the detached look: one request for the latest release,
// its answer kept in the stamp. DEJA_OFFLINE=1 is checked again here, so not
// even a child started before it was set makes a request.
func runReleaseLook(stamp string, lookup doctorVersionLookup) {
	if os.Getenv("DEJA_OFFLINE") == "1" || os.Getenv(releaseNoticeOff) != "" {
		return
	}
	latest, ok := lookup()
	if !ok {
		return
	}
	s := readReleaseStamp(stamp)
	s.Latest = latest
	_ = writeReleaseStamp(stamp, s)
}

// releaseNoticeLine is the one line, with the upgrade command for whatever
// installed this binary, or "" when there is nothing newer to say.
func releaseNoticeLine(current, latest, exe string) string {
	if order, ok := compareUpdateVersions(current, latest); !ok || order >= 0 {
		return ""
	}
	command := "deja update"
	if _, managed := packageManagerOwning(exe); managed != "" {
		command = managed
	}
	return fmt.Sprintf("deja v%s is out (this is v%s) — `%s` upgrades it; %s=1 hides this line", latest, current, command, releaseNoticeOff)
}
