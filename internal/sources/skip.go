package sources

// SkipReason says why a harness deja can see on disk produced nothing. Today
// that is only a missing sqlite3 CLI: five stores are read through it, and an
// index run that names every harness it read while staying silent about the
// one it could not made an empty deja look like an empty history (#794).
//
// It returns "" when there is nothing to explain — including on a machine that
// never used the harness, where a note about a tool would be noise.
func SkipReason(harness string) string {
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
