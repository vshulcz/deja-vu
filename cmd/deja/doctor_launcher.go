package main

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// launcherCandidatePaths returns candidate deja binary paths in the same
// resolution order the launcher tries at runtime.
func launcherCandidatePaths() []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	add(os.Getenv("DEJA_BIN"))
	st := readWiringState()
	for _, p := range st.Exes {
		add(p)
	}
	add(st.Exe)
	for _, p := range launcherCandidates("") {
		add(p)
	}
	if p, err := exec.LookPath("deja"); err == nil {
		add(p)
	}
	return out
}

// launcherResolves reports whether the launcher would find a deja to run, and
// the path it would run.
//
// The check beside this one asks whether the file a hook names is on disk, and
// since #3422 that file is the launcher, which is always there — so the
// question it answers moved. What can be gone now is everything the launcher
// resolves to: the binary the install ran from, the PATH, the usual install
// locations. A machine in that state has hooks that start, find nothing and
// exit 0, which is silence that looks exactly like having no history.
func launcherResolves() (string, bool) {
	for _, p := range launcherCandidatePaths() {
		if runnableFile(p) {
			return p, true
		}
	}
	return "", false
}

func executableFile(p string) bool {
	if p == "" {
		return false
	}
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

func runnableFile(p string) bool {
	return executableFile(p) && dejaBinaryRunErr(p) == ""
}

func launcherFirstBroken(legacy bool) (string, string) {
	for _, p := range launcherCandidatePaths() {
		if !executableFile(p) {
			continue
		}
		if errStr := dejaBinaryRunErr(p); errStr != "" {
			return p, errStr
		}
		if legacy {
			return "", ""
		}
	}
	return "", ""
}

// doctorLauncherNote is the line a hook row prints when the entries name the
// launcher and the launcher has nothing runnable (or a legacy exec launcher
// stops on a broken first candidate).
func doctorLauncherNote(path, target string) string {
	launcher := dejaLauncherPath()
	if launcher == "" || strings.TrimSpace(path) == "" {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), launcher) {
		return ""
	}
	lb, err := os.ReadFile(launcher)
	if err != nil {
		return "runs " + reportPath(launcher) + ", which is not there — `deja install " + target + "` writes it again"
	}
	if !strings.Contains(string(lb), "-lt 126") {
		if broken, why := launcherFirstBroken(true); broken != "" {
			return "runs " + reportPath(launcher) + ", which picks " + reportPath(broken) + " and fails to start (" + why + ") — `deja install " + target + "` updates the launcher to skip broken binaries"
		}
	}
	if _, ok := launcherResolves(); ok {
		return ""
	}
	if broken, why := launcherFirstBroken(false); broken != "" {
		return "runs " + reportPath(launcher) + ", whose binary " + reportPath(broken) + " fails to start (" + why + ") — reinstall deja"
	}
	return "runs " + reportPath(launcher) + ", which finds no deja to run — put the binary back on the PATH, or set DEJA_BIN to it"
}

// hookExeIsLauncher reports whether the binary a hook file runs and cannot
// find is the launcher itself, which doctorLauncherNote already names.
func hookExeIsLauncher(path string) bool {
	missing := dejaHookCommandMissing(path)
	launcher := dejaLauncherPath()
	return missing != "" && launcher != "" && samePathSpelling(missing, launcher, runtime.GOOS == "windows")
}
