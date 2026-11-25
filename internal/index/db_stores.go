package index

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// SQLiteWatermark captures the sizes and nanosecond mtimes of a SQLite store's
// -wal and -shm sidecars.
type SQLiteWatermark struct {
	WALSize  int64
	WALMTime int64
	SHMSize  int64
	SHMMTime int64
}

func hasSQLiteExt(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".db", ".sqlite", ".sqlite3", ".vscdb":
		return true
	default:
		return false
	}
}

func isSQLiteStorePath(p string) bool {
	if hasSQLiteExt(p) {
		return true
	}
	switch strings.ToLower(filepath.Ext(p)) {
	case ".jsonl", ".json", ".md", ".txt", ".log", ".pb", ".zst", ".gob", ".bin":
		return false
	}
	k, _, ok := sources.KindForPathAll(p)
	return ok && k.IsDBBacked()
}

func statRegularSidecar(p string) (size, stamp int64) {
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return 0, 0
	}
	return fi.Size(), fi.ModTime().UnixNano()
}

func readSQLiteSidecars(p string) SQLiteWatermark {
	walSize, walMTime := statRegularSidecar(p + "-wal")
	shmSize, shmMTime := statRegularSidecar(p + "-shm")
	return SQLiteWatermark{
		WALSize:  walSize,
		WALMTime: walMTime,
		SHMSize:  shmSize,
		SHMMTime: shmMTime,
	}
}

// ReadSQLiteWatermark returns WAL and SHM sidecar watermarks when p refers to a
// SQLite store path, or the zero watermark otherwise.
func ReadSQLiteWatermark(p string) SQLiteWatermark {
	if !isSQLiteStorePath(p) {
		return SQLiteWatermark{}
	}
	return readSQLiteSidecars(p)
}

func stampDatabaseStores(files map[string]FileState, sessions map[string]SessionMeta) {
	for h, db := range opencodeSchemaDBs {
		setStoreLastUpdated(files, sessions, h, db())
	}
	stampAll := func(harness string, dbs []string) {
		for _, db := range dbs {
			setStoreLastUpdated(files, sessions, harness, db)
		}
	}
	stampAll("goose", sources.GooseDBs())
	setStoreLastUpdated(files, sessions, "grok", sources.GrokDB())
	stampAll("hermes", sources.HermesDBs())
	setStoreLastUpdated(files, sessions, "zed", sources.ZedDB())
	stampAll("cursor", sources.CursorDBs())
	stampAll("openclaw", sources.OpenClawAgentDBs())
	stampAll("crush", sources.CrushDBs())
	stampAll("teleagent", sources.TeleAgentSessionFiles())
	if cm := sources.ClaudeMemDBPath(); cm != "" {
		setStoreLastUpdated(files, sessions, "claude", cm)
	}
	setStoreLastUpdated(files, sessions, "kiro", sources.KiroDB())
	setStoreLastUpdated(files, sessions, "devin", sources.DevinSessionsDB())
	setStoreLastUpdated(files, sessions, "devin", sources.DevinLegacySessionsDB())
	setStoreLastUpdated(files, sessions, "omnigent", sources.OmnigentDB())
	setStoreLastUpdated(files, sessions, "orca", sources.OrcaDB())
}
