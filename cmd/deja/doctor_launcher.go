package main

import (
	"os"
	"os/exec"
	"strings"
)

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
	if p := os.Getenv("DEJA_BIN"); executableFile(p) {
		return p, true
	}
	st := readWiringState()
	for _, p := range append(append([]string(nil), st.Exes...), st.Exe) {
		if executableFile(p) {
			return p, true
		}
	}
	for _, p := range launcherCandidates("") {
		if executableFile(p) {
			return p, true
		}
	}
	if p, err := exec.LookPath("deja"); err == nil && executableFile(p) {
		return p, true
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

// doctorLauncherNote is the line a hook row prints when the entries name the
// launcher and the launcher has nothing to run.
func doctorLauncherNote(path, target string) string {
	launcher := dejaLauncherPath()
	if launcher == "" || strings.TrimSpace(path) == "" {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), launcher) {
		return ""
	}
	if _, err := os.Stat(launcher); err != nil {
		return "runs " + reportPath(launcher) + ", which is not there — `deja install " + target + "` writes it again"
	}
	if _, ok := launcherResolves(); ok {
		return ""
	}
	return "runs " + reportPath(launcher) + ", which finds no deja to run — put the binary back on the PATH, or set DEJA_BIN to it"
}
