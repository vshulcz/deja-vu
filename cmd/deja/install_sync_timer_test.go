package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func syncTimerHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

// The machine that most needs to send is the one nobody is sitting at, so the
// timer has to exist as a file a service manager reads — not as a hook, which
// only fires while someone is working.
func TestInstallSyncTimerWritesAUnitTheSystemReads(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("no timer for this platform")
	}
	syncTimerHome(t)
	r, err := installSyncTimer("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Action == "unchanged" {
		t.Errorf("the first install wrote nothing")
	}
	if _, err := os.Stat(r.Path); err != nil {
		t.Fatalf("the unit is not where install said it is: %v", err)
	}
	// systemd splits the work in two: install reports the timer, because that
	// is what a person enables, and the command lives in the service beside
	// it. launchd keeps both in one plist.
	unit := r.Path
	if runtime.GOOS == "linux" {
		unit = filepath.Join(syncAutoUnitDir(), "deja-sync.service")
	}
	b, err := os.ReadFile(unit)
	if err != nil {
		t.Fatalf("cannot read %s: %v", unit, err)
	}
	body := string(b)
	// It has to run the bare form and nothing else: that is the one that
	// reaches every machine deja knows and does nothing when there are none.
	// Checked as the whole argument list, because "sync" appears in a unit
	// pinned to one host too.
	if got, want := syncTimerArgs(t, body), []string{"/usr/local/bin/deja", "sync"}; !sameArgs(got, want) {
		t.Errorf("the unit runs %v, want %v:\n%s", got, want, body)
	}
}

func TestInstallSyncTimerIsIdempotentAndReversible(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("no timer for this platform")
	}
	syncTimerHome(t)
	first, err := installSyncTimer("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := installSyncTimer("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if second.Action != "unchanged" {
		t.Errorf("a second install rewrote the unit: %s", second.Action)
	}
	if _, err := installSyncTimer("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first.Path); !os.IsNotExist(err) {
		t.Errorf("the unit survived the uninstall: %v", err)
	}
}

// Uninstalling on a machine that never installed must not create anything,
// the shape of #676.
func TestUninstallSyncTimerLeavesAnUntouchedMachineAlone(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("no timer for this platform")
	}
	home := syncTimerHome(t)
	// The directory a unit would go in already exists: a machine with other
	// launch agents or other systemd units is the ordinary case, and an
	// uninstall that writes an empty file is only invisible when the write
	// happens to fail for want of a directory.
	if err := os.MkdirAll(filepath.Dir(syncAutoPlistPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(syncAutoUnitDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := installSyncTimer("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	var wrote []string
	_ = filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			wrote = append(wrote, path)
		}
		return nil
	})
	if len(wrote) > 0 {
		t.Errorf("uninstall created files on a machine that never installed: %v", wrote)
	}
}

// A home directory is a user-chosen string, and both unit formats are picky in
// different ways: XML refuses an unescaped ampersand, systemd splits ExecStart
// on a space unless the word is quoted.
func TestSyncTimerUnitsSurviveAnAwkwardPath(t *testing.T) {
	exe := `/Users/John & "Jane" Smith/bin/deja`
	plist := syncAutoPlist(exe)
	for _, bad := range []string{`John & "Jane"`} {
		if strings.Contains(plist, bad) {
			t.Errorf("the plist carries the path unescaped, so launchd will refuse it:\n%s", plist)
		}
	}
	if !strings.Contains(plist, "&amp;") || !strings.Contains(plist, "&quot;") {
		t.Errorf("the plist did not escape the path:\n%s", plist)
	}

	service := syncAutoService(exe)
	for _, line := range strings.Split(service, "\n") {
		if !strings.HasPrefix(line, "ExecStart=") {
			continue
		}
		// systemd takes the first whitespace-delimited word as the binary
		// unless it is quoted, so an unquoted path with a space runs the
		// wrong thing — or nothing.
		if !strings.Contains(line, `"`+exe+`"`) {
			t.Errorf("ExecStart does not quote the path: %s", line)
		}
	}
}

// A platform with no timer must say so. Reporting success where nothing will
// ever run the sync is the failure this whole target exists to prevent — and
// it is checked from every platform, because a refusal that only runs on the
// one machine that cannot schedule is a refusal nobody checks.
func TestSyncTimerRefusesAPlatformItCannotSchedule(t *testing.T) {
	syncTimerHome(t)
	r, err := installSyncTimerFor("plan9", "/usr/local/bin/deja", false)
	if err == nil {
		t.Fatalf("install reported success on a platform with no timer: %+v", r)
	}
	if r.Action != "" {
		t.Errorf("a refusal still claimed to have done something: %q", r.Action)
	}
	if !strings.Contains(err.Error(), "plan9") {
		t.Errorf("the error does not name the platform: %v", err)
	}
}

// Every install target is driven for real by the suite's own invariants
// against a temp home. Without this guard one run of the suite loaded a live
// launch agent into the developer's launchd, pointing at a plist the test then
// deleted.
func TestSyncTimerDoesNotTouchTheRealServiceManager(t *testing.T) {
	if !underTestBinary() {
		t.Fatal("this binary is not recognised as a test binary, so install would call the real service manager")
	}
	out, err := runServiceManager("false")
	if err != nil || out != "" {
		t.Errorf("a service manager command ran under test: %q %v", out, err)
	}
}

// syncTimerArgs pulls the command a unit runs out of either format: the
// <string> entries of a plist's ProgramArguments, or the words of ExecStart.
func syncTimerArgs(t *testing.T, body string) []string {
	t.Helper()
	if strings.Contains(body, "ProgramArguments") {
		rest := body[strings.Index(body, "ProgramArguments"):]
		rest = rest[:strings.Index(rest, "</array>")]
		var out []string
		for _, part := range strings.Split(rest, "<string>")[1:] {
			out = append(out, strings.TrimSpace(part[:strings.Index(part, "</string>")]))
		}
		return out
	}
	for _, line := range strings.Split(body, "\n") {
		if v, ok := strings.CutPrefix(line, "ExecStart="); ok {
			return strings.Fields(strings.ReplaceAll(v, `"`, ""))
		}
	}
	t.Fatalf("no command in the unit:\n%s", body)
	return nil
}

func sameArgs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// The target is only offered where deja can actually schedule something.
// Offering one that always fails is worse than not offering it — and the
// suite's own invariants install every target for real, so a target that
// refuses its platform fails them.
func TestSyncTimerIsOfferedOnlyWhereItCanRun(t *testing.T) {
	for goos, want := range map[string]bool{
		"darwin": true, "linux": true,
		"windows": false, "plan9": false, "freebsd": false,
	} {
		if got := syncTimerSchedulable(goos); got != want {
			t.Errorf("syncTimerSchedulable(%q) = %v, want %v", goos, got, want)
		}
	}
	// And the list follows that answer on this machine.
	var listed bool
	for _, n := range installTargetNames() {
		if n == "sync-timer" {
			listed = true
		}
	}
	if listed != syncTimerSchedulable(runtime.GOOS) {
		t.Errorf("sync-timer listed=%v on %s, but schedulable=%v",
			listed, runtime.GOOS, syncTimerSchedulable(runtime.GOOS))
	}
}
