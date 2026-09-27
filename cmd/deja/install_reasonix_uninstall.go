package main

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Taking deja's Reasonix package back out, and what an install left beside it.

func uninstallReasonix() (installResult, error) {
	root := reasonixInstalledRoot()
	manifest := reasonixInstalledManifest()
	statePath := reasonixStatePath()
	st, old, err := readReasonixState(statePath)
	if err != nil {
		return installResult{}, err
	}
	i, _ := st.ours()
	hasRoot := isRealDir(root)
	if reasonixForeignPackage() {
		return installResult{Path: manifest, Action: "unchanged",
			Note: "a plugin named deja that deja did not write is installed there — left as it was"}, nil
	}
	if i >= 0 && !reasonixRecordIsOurs(st.Plugins[i], hasRoot) {
		// A record by the same name that points somewhere else is another
		// package's, whatever it is called.
		i = -1
	}
	if !hasRoot && i < 0 {
		removeReasonixSource()
		return installResult{Path: manifest, Action: "unchanged"}, nil
	}
	removeReasonixReceiptKey()
	pruneCreatedDir(reasonixCrashDir())
	if hasRoot {
		if err := os.RemoveAll(root); err != nil {
			return installResult{}, err
		}
		pruneCreatedDir(filepath.Dir(root))
	}
	if i >= 0 {
		st.Plugins = append(st.Plugins[:i], st.Plugins[i+1:]...)
		if len(st.Plugins) == 0 && wiringCreated(statePath) {
			if err := os.Remove(statePath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return installResult{}, err
			}
			if snapshotTaken(statePath) {
				dropOwnBackup(statePath)
			}
			pruneCreatedDir(filepath.Dir(statePath))
		} else if _, err := writeIfChanged(statePath, old, st.marshal()); err != nil {
			return installResult{}, err
		}
	}
	removeReasonixSource()
	return installResult{Path: manifest, Action: "removed"}, nil
}

// reasonixReceiptKey is the HMAC key Reasonix creates on first use
// (internal/config/model_credential_commit.go) and recreates when it is gone.
func reasonixReceiptKey() string {
	return filepath.Join(sources.ReasonixHome(), "transactions", "model-settings-receipts", "request-digest.key")
}

// reasonixCrashDir is where Reasonix's CLI keeps fatal crash reports; it makes
// the directory on every start.
func reasonixCrashDir() string {
	return filepath.Join(sources.ReasonixHome(), "cli-crash-fatal")
}

// removeReasonixReceiptKey takes back the key the install's reasonix run
// created, while nothing has been signed with it: a receipt beside it, or a
// model-credential journal, means Reasonix has used it since, and then it
// stays.
func removeReasonixReceiptKey() {
	key := reasonixReceiptKey()
	if !wiringCreated(key) {
		return
	}
	entries, err := os.ReadDir(filepath.Dir(key))
	if err != nil || len(entries) != 1 {
		return
	}
	// The same key signs the model-credential journals beside the receipts.
	if journals, err := os.ReadDir(filepath.Join(sources.ReasonixHome(), "transactions", "model-credentials")); err == nil && len(journals) > 0 {
		return
	}
	if os.Remove(key) == nil {
		pruneCreatedDir(filepath.Dir(key))
	}
}

// removeReasonixSource takes deja's copy of the package with it.
func removeReasonixSource() {
	src := reasonixPluginSourceDir()
	if !isRealDir(src) {
		return
	}
	_ = os.RemoveAll(src)
	pruneCreatedDir(filepath.Dir(src))
}

// rxSamePath reports whether two spellings name the same path: cleaned,
// either separator, resolved where the path exists (on Windows that also
// expands a short name like RUNNER~1), and without case on Windows.
func rxSamePath(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	resolve := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return p
	}
	return samePathSpelling(a, b, runtime.GOOS == "windows") ||
		samePathSpelling(resolve(a), resolve(b), runtime.GOOS == "windows")
}

// samePathSpelling compares two path strings as text: cleaned, with either
// separator, and without case when fold is set.
func samePathSpelling(a, b string, fold bool) bool {
	norm := func(p string) string {
		p = strings.ReplaceAll(p, `\`, "/")
		p = strings.TrimRight(path.Clean(p), "/")
		return p
	}
	if fold {
		return strings.EqualFold(norm(a), norm(b))
	}
	return norm(a) == norm(b)
}
