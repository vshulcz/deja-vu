package sources

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// A store at rest is one macOS cannot read.
//
// opencode, zed, goose, hermes and grok all keep SQLite databases in WAL mode,
// and SQLite removes the `-wal` and `-shm` sidecars when the last connection
// closes cleanly. That is the normal resting state of a store nobody is using —
// which is exactly the state deja reads it in.
//
// Apple's bundled sqlite3 cannot open a WAL database read-only in that state:
//
//	$ /usr/bin/sqlite3 -readonly t.db "select * from t"
//	Error: in prepare, unable to open database file (14)
//
// Reproduced on macOS 26.5.1 with /usr/bin/sqlite3 3.51.0, which is what a
// stock Mac puts on PATH. Homebrew's 3.53.4 reads the same file in the same
// state, and so does the Apple binary itself through a URI with `immutable=1`.
//
// It self-heals, which is why it is so hard to catch: any read-write open
// recreates the sidecars, so whether a store reads tracks whether its agent is
// running rather than anything about the data. #1642 was reported against a
// 1 GB opencode store, and the reporter caught it live only on the first call
// of the day.
//
// With no `-wal` beside the file there are no uncheckpointed frames, so nothing
// is being hidden by declaring the file immutable. That is the whole of the
// condition: immutable unconditionally would lie to SQLite about writers.
func sqliteTarget(db string) string {
	if !walAtRest(db) {
		return db
	}
	return "file:" + uriPath(db) + "?immutable=1"
}

// walAtRest reports whether the file is a WAL-mode database with no sidecar.
//
// Read from the header rather than by asking sqlite3: the answer decides how
// the query is run, and a round trip that has to fail first cannot be used by
// the callers that stream their results.
func walAtRest(db string) bool {
	if db == "" {
		return false
	}
	if _, err := os.Stat(db + "-wal"); err == nil {
		return false
	}
	f, err := os.Open(db)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	var head [20]byte
	if n, err := f.Read(head[:]); err != nil || n < len(head) {
		return false
	}
	if string(head[:15]) != "SQLite format 3" {
		return false
	}
	// Bytes 18 and 19 are the file format read and write versions: 2 means WAL,
	// 1 means the rollback journal, which opens read-only without a sidecar.
	return head[18] == 2 && head[19] == 2
}

// uriPath renders a path for a SQLite file: URI. Windows separators become
// slashes, and every segment is escaped, so a store under a directory with a
// space, a question mark or a hash reaches sqlite3 whole.
func uriPath(p string) string {
	p = filepath.ToSlash(p)
	lead := ""
	if strings.HasPrefix(p, "/") {
		lead, p = "/", strings.TrimPrefix(p, "/")
	}
	parts := strings.Split(p, "/")
	for i, seg := range parts {
		parts[i] = url.PathEscape(seg)
	}
	return lead + strings.Join(parts, "/")
}
