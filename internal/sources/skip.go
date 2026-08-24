package sources

import "strings"

// SkipReason says why a harness deja can see on disk produced nothing. That is
// a missing external tool: six stores are read through the sqlite3 CLI, and an
// index run that names every harness it read while staying silent about the
// one it could not made an empty deja look like an empty history (#794).
//
// Zed needs a second tool. Its store is SQLite like the others, but every
// thread body inside it is a zstd frame, so sqlite3 alone opens the store and
// reads nothing out of it — the failure this exists to stop, one layer down.
//
// It returns "" when there is nothing to explain — including on a machine that
// never used the harness, where a note about a tool would be noise.
func SkipReason(harness string) string {
	if harness == "zed" {
		return zedSkipReason()
	}
	// DeepSeek Harness writes its log as zstd frames by default, so without the
	// tool the files are there and unreadable — the same failure as Zed's, one
	// layer up: whole sessions rather than thread bodies inside a store.
	if harness == "deepseek" {
		// Only the framed ones need the tool. A store of plain session.jsonl
		// reads without it, and saying part of it could not be read there
		// names a problem the reader does not have (#1758).
		if ZstdAvailable() || !anyZstdFramed(DeepSeekSessionFiles()) {
			return ""
		}
		return "zstd CLI not found"
	}
	if SQLite3Available() {
		return ""
	}
	var present bool
	switch harness {
	case "opencode":
		present = fileExists(OpencodeDB())
	case "cursor":
		present = len(CursorDBs()) > 0
	case "grok":
		present = fileExists(GrokDB())
	case "hermes":
		present = len(HermesDBs()) > 0
	case "goose":
		present = fileExists(GooseDB())
	}
	if !present {
		return ""
	}
	return "sqlite3 CLI not found"
}

// anyZstdFramed reports whether any of these files is a zstd frame rather than
// plain text — the DeepSeek Harness writes either, depending on its settings.
func anyZstdFramed(files []string) bool {
	for _, f := range files {
		if strings.HasSuffix(f, ".zstd") || strings.HasSuffix(f, ".zst") {
			return true
		}
	}
	return false
}

func zedSkipReason() string {
	if !fileExists(ZedDB()) {
		return ""
	}
	sqlite, zstd := SQLite3Available(), ZstdAvailable()
	switch {
	case !sqlite && !zstd:
		return "sqlite3 and zstd CLIs not found"
	case !sqlite:
		return "sqlite3 CLI not found"
	case !zstd:
		return "zstd CLI not found"
	}
	return ""
}
