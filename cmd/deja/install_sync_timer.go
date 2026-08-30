package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// A sync nobody runs is a memory that stops at whichever machine it was made
// on. `deja sync` is one command now, but it is still a command — and the
// machine that most needs to send is the one nobody is sitting at.
//
// So this installs a timer. Not a hook: hooks fire only while someone is
// working, which is exactly when the server is not. The unit runs `deja sync`,
// which exchanges with every machine deja already knows and does nothing at
// all when there are none.
const syncAutoLabel = "com.deja-vu.sync"

// syncAutoInterval is how often the timer fires. Half an hour is chosen
// against what a sync costs when there is nothing to send: watermarks make
// that case a manifest read and one ssh round trip.
const syncAutoInterval = 1800

func installSyncTimer(exe string, uninstall bool) (installResult, error) {
	return installSyncTimerFor(runtime.GOOS, exe, uninstall)
}

// installSyncTimerFor takes the platform as an argument so the decision itself
// can be tested from any machine. A refusal that only ever runs on the one
// platform deja cannot schedule is a refusal nobody checks.
func installSyncTimerFor(goos, exe string, uninstall bool) (installResult, error) {
	switch goos {
	case "darwin":
		return installSyncTimerLaunchd(exe, uninstall)
	case "linux":
		return installSyncTimerSystemd(exe, uninstall)
	default:
		// Saying "installed" on a platform where nothing runs it is the
		// failure this whole feature exists to prevent.
		return installResult{}, fmt.Errorf("sync-timer has no timer for %s yet — run `deja sync` from whatever this machine already uses to schedule work", goos)
	}
}

func syncAutoPlistPath() string {
	return filepath.Join(homeDir(), "Library", "LaunchAgents", syncAutoLabel+".plist")
}

func installSyncTimerLaunchd(exe string, uninstall bool) (installResult, error) {
	path := syncAutoPlistPath()
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	if uninstall {
		if len(old) > 0 {
			_, _ = runLaunchctl("unload", path)
		}
		return installResult{Path: path, Action: removeOurUnit(path)}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return installResult{}, err
	}
	next := []byte(syncAutoPlist(exe))
	action, err := writeIfChanged(path, old, next)
	if err != nil {
		return installResult{}, err
	}
	if action != "unchanged" {
		// Reloaded rather than loaded: launchd keeps the old copy running
		// until it is told otherwise, so an edited plist would sit on disk
		// looking installed while the previous one is what actually runs.
		if len(old) > 0 {
			_, _ = runLaunchctl("unload", path)
		}
		if out, err := runLaunchctl("load", path); err != nil {
			return installResult{}, fmt.Errorf("wrote %s but launchctl refused it: %v: %s", path, err, out)
		}
	}
	return installResult{Path: path, Action: action}, nil
}

func syncAutoPlist(exe string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>sync</string>
  </array>
  <key>StartInterval</key><integer>%d</integer>
  <key>RunAtLoad</key><false/>
  <key>ProcessType</key><string>Background</string>
</dict>
</plist>
`, syncAutoLabel, xmlEscape(exe), syncAutoInterval)
}

func syncAutoUnitDir() string {
	// deja's own unit file, so the spec's rule applies: a relative
	// XDG_CONFIG_HOME is ignored rather than followed into the working
	// directory, where systemd would never look for it (#1693).
	return filepath.Join(xdgConfigHome(), "systemd", "user")
}

func installSyncTimerSystemd(exe string, uninstall bool) (installResult, error) {
	dir := syncAutoUnitDir()
	service := filepath.Join(dir, "deja-sync.service")
	timer := filepath.Join(dir, "deja-sync.timer")
	oldService, err := readConfig(service)
	if err != nil {
		return installResult{}, err
	}
	oldTimer, err := readConfig(timer)
	if err != nil {
		return installResult{}, err
	}
	if uninstall {
		if len(oldTimer) > 0 {
			_, _ = runSystemctl("--user", "disable", "--now", "deja-sync.timer")
		}
		removeOurUnit(service)
		return installResult{Path: timer, Action: removeOurUnit(timer)}, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return installResult{}, err
	}
	if _, err := writeIfChanged(service, oldService, []byte(syncAutoService(exe))); err != nil {
		return installResult{}, err
	}
	action, err := writeIfChanged(timer, oldTimer, []byte(syncAutoTimer()))
	if err != nil {
		return installResult{}, err
	}
	if action != "unchanged" {
		_, _ = runSystemctl("--user", "daemon-reload")
		if out, err := runSystemctl("--user", "enable", "--now", "deja-sync.timer"); err != nil {
			return installResult{}, fmt.Errorf("wrote %s but systemctl refused it: %v: %s", timer, err, out)
		}
	}
	return installResult{Path: timer, Action: action}, nil
}

func syncAutoService(exe string) string {
	return fmt.Sprintf(`[Unit]
Description=deja: exchange memory with the machines this one syncs with

[Service]
Type=oneshot
ExecStart="%s" sync
`, systemdEscape(exe))
}

func syncAutoTimer() string {
	return fmt.Sprintf(`[Unit]
Description=deja sync every %d minutes

[Timer]
OnBootSec=5min
OnUnitActiveSec=%dmin
Persistent=true

[Install]
WantedBy=timers.target
`, syncAutoInterval/60, syncAutoInterval/60)
}

// runLaunchctl and runSystemctl are variables so tests can drive install
// without a service manager. A missing one is not an error on its own: the
// file is written either way, and the report says where it went.
var runLaunchctl = func(args ...string) (string, error) {
	return runServiceManager("launchctl", args...)
}

var runSystemctl = func(args ...string) (string, error) {
	return runServiceManager("systemctl", args...)
}

// runServiceManager registers or drops the unit — unless this is a test.
//
// Every install target is driven by the suite's own invariants (every target
// writes readable files, every target quotes a path with a space in it), and
// those run installTarget for real against a temp home. Without this guard
// that loaded a live launch agent into the developer's own launchd, pointing
// at a plist in a directory the test then deleted. Measured, not imagined:
// `launchctl list` showed com.deja-vu.sync after one run of the suite.
func runServiceManager(name string, args ...string) (string, error) {
	if underTestBinary() {
		return "", nil
	}
	out, err := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// underTestBinary reports whether this process is a `go test` binary, the same
// check the hook refresher uses before spawning itself.
func underTestBinary() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	return strings.HasSuffix(strings.TrimSuffix(exe, ".exe"), ".test")
}

// removeOurUnit deletes a unit file. Unlike every other install target, these
// files are deja's alone — nothing else writes to them and there is no block
// inside someone else's config to cut out — so uninstall removes them rather
// than rewriting them empty. A machine that never installed keeps its answer:
// nothing was there, nothing changed.
func removeOurUnit(path string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "unchanged"
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return "unchanged"
	}
	return "removed"
}

// syncTimerBinary returns the binary the scheduled timer names, and whether a
// timer is scheduled at all.
//
// Read back out of the file rather than remembered: the file is what the
// service manager runs, and the failure worth catching is that it disagrees
// with where deja is now — an upgrade or a move leaves the old path behind and
// the hourly sync stops without a word (#2636). The same staleness
// refreshWiringAfterUpgrade handles on the harness side.
func syncTimerBinary(goos string) (exe string, scheduled bool) {
	switch goos {
	case "darwin":
		b, err := os.ReadFile(syncAutoPlistPath())
		if err != nil {
			return "", false
		}
		// Anchored on the key, not on the first <array>: a hand-edited plist
		// can carry another array above this one, and reading the wrong
		// element would have doctor call a healthy timer broken. install
		// writes the path XML-escaped, so it comes back unescaped here.
		body := string(b)
		k := strings.Index(body, "<key>ProgramArguments</key>")
		if k < 0 {
			return "", true
		}
		i := strings.Index(body[k:], "<array>")
		if i < 0 {
			return "", true
		}
		i += k
		rest := body[i:]
		start := strings.Index(rest, "<string>")
		end := strings.Index(rest, "</string>")
		if start < 0 || end < start {
			return "", true
		}
		return xmlUnescape(rest[start+len("<string>") : end]), true
	case "linux":
		b, err := os.ReadFile(filepath.Join(syncAutoUnitDir(), "deja-sync.service"))
		if err != nil {
			return "", false
		}
		for _, line := range strings.Split(string(b), "\n") {
			if !strings.HasPrefix(line, "ExecStart=") {
				continue
			}
			v := strings.TrimPrefix(line, "ExecStart=")
			if !strings.HasPrefix(v, `"`) {
				return "", true
			}
			v = v[1:]
			if j := strings.LastIndex(v, `" `); j >= 0 {
				v = v[:j]
			}
			return systemdUnescape(v), true
		}
		return "", true
	}
	return "", false
}

// xmlUnescape and systemdUnescape undo what the two writers escape, so the path
// read back out of a file is the path that was put in.
func xmlUnescape(s string) string {
	return strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", `"`, "&apos;", "'", "&amp;", "&").Replace(s)
}

func systemdUnescape(s string) string {
	return strings.NewReplacer(`\"`, `"`, "$$", "$", "%%", "%", `\\`, `\`).Replace(s)
}

// systemdEscape keeps a path from being read as anything but a path. The same
// reason as xmlEscape below, for the other format: a unit file resolves %-
// specifiers, substitutes $variables and reads C escapes in its values, so
// /Users/50%off/bin/deja ran /Users/50<os-id>ff/bin/deja and a path holding a
// quote did not parse at all (#2621).
func systemdEscape(s string) string {
	r := strings.NewReplacer("\\", "\\\\", "%", "%%", "$", "$$", `"`, `\"`)
	return r.Replace(s)
}

// xmlEscape keeps a path with an ampersand or a quote in it from producing a
// plist launchd refuses to parse. A home directory is a user-chosen string.
func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}
