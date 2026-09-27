package main

import (
	"errors"
	"os"
	"path/filepath"

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
	if hasRoot && !reasonixPackageIsOurs(manifest) {
		return installResult{Path: manifest, Action: "unchanged",
			Note: "a plugin named deja that deja did not write is installed there — left as it was"}, nil
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
// created, while nothing has been signed with it: a receipt beside it means
// Reasonix has used it since, and then it stays.
func removeReasonixReceiptKey() {
	key := reasonixReceiptKey()
	if !wiringCreated(key) {
		return
	}
	entries, err := os.ReadDir(filepath.Dir(key))
	if err != nil || len(entries) != 1 {
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
