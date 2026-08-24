package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A read-only config directory was reported as a permission error on
// ~/.codex/.deja-tmp-4168817699 — the scratch file writeIfChanged creates to
// write atomically. The reader cannot look at it, chmod it, or find it (#1686).
func TestInstallNamesTheConfigNotTheTempFile(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("the directory mode is not enforced here")
	}
	hermeticEnv(t)
	home := os.Getenv("HOME")
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// No config to back up: with one present the backup step fails first and
	// names a real path, and the temp name never surfaces.
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	_, err := captureRun(t, "install", "codex")
	if err == nil {
		t.Fatal("install wrote into a read-only directory")
	}
	msg := err.Error()
	if strings.Contains(msg, "deja-tmp") {
		t.Errorf("the refusal names deja's scratch file: %s", msg)
	}
	if !strings.Contains(msg, filepath.Join(".codex", "config.toml")) {
		t.Errorf("the refusal does not name the config it could not write: %s", msg)
	}
	// The remedy still has to read as a permissions problem, which means the
	// error must stay an fs.ErrPermission after the path is rewritten.
	if !strings.Contains(msg, "permissions") {
		t.Errorf("a permission denial lost its remedy: %s", msg)
	}
}

// writeIfChanged's own error, checked directly: the path is the config, and
// errors.Is still sees the permission underneath.
func TestWriteIfChangedReportsTheDestination(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("the directory mode is not enforced here")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	path := filepath.Join(dir, "config.toml")
	_, err := writeIfChanged(path, nil, []byte("x\n"))
	if err == nil {
		t.Fatal("writeIfChanged wrote into a read-only directory")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error names %q, not the destination %q", err, path)
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("error is no longer a permission error: %v", err)
	}
}
