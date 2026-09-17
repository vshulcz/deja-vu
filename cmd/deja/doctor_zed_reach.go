package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Zed takes deja's server in two shapes under the same id, and only one of them
// carries a path:
//
//	"deja-context-server": {"command": "…/deja", "args": ["mcp"]}   what `deja install zed` writes
//	"deja-context-server": {"enabled": true, "settings": {}}        what Zed writes when an extension provides it
//
// The second defers entirely to `extensions/installed/<id>`, so a machine whose
// extension is gone — or, as found here, a symlink into a scratch directory
// that no longer exists — has an enabled server with nothing behind it. The
// wired check reads the id and the id is there, so the row said `wired` over a
// setup that cannot start anything (#3660).
func zedExtensionDirs() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{filepath.Join(homeDir(), "Library", "Application Support", "Zed", "extensions", "installed")}
	case "windows":
		if app := os.Getenv("APPDATA"); app != "" {
			return []string{filepath.Join(app, "Zed", "extensions", "installed")}
		}
		return nil
	default:
		base := os.Getenv("XDG_DATA_HOME")
		if base == "" {
			base = filepath.Join(homeDir(), ".local", "share")
		}
		return []string{filepath.Join(base, "zed", "extensions", "installed")}
	}
}

// zedEntryHasCommand reports whether Zed's settings entry names a binary
// itself, which is the shape deja's own install writes.
func zedEntryHasCommand(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	text := string(b)
	for _, id := range []string{zedServerID, zedLegacyServerID} {
		loc := zedLocate(text, id)
		if loc == nil || loc.entry == nil {
			continue
		}
		if strings.Contains(text[loc.entry.valueOpen:loc.entry.valueEnd], "\"command\"") {
			return true
		}
	}
	return false
}

// zedExtensionReachable reports whether the extension that would provide the
// server is installed and resolves. A dangling symlink is the case this exists
// for: os.Stat follows it and fails, which is exactly the answer wanted.
func zedExtensionReachable() bool {
	for _, dir := range zedExtensionDirs() {
		if _, err := os.Stat(filepath.Join(dir, zedServerID)); err == nil {
			return true
		}
	}
	return false
}

// zedUnreachableNote is the line for an entry that defers to an extension which
// is not there.
func zedUnreachableNote(path string) string {
	if zedEntryHasCommand(path) || zedExtensionReachable() {
		return ""
	}
	return "the entry names no command and expects the " + zedServerID +
		" extension, which is not installed — `deja install zed` writes an entry that does not need one"
}
