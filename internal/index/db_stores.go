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

// captureDatabaseStoreCursors snapshots rowid cursors for readable database
// stores after a full cold rebuild so the next incremental pass starts with a
// delta anchor instead of rescanning from rowid 0.
func captureDatabaseStoreCursors(files map[string]FileState) {
	if len(files) == 0 {
		return
	}
	failed := sources.DiagFailedPaths()
	reg := sources.Registry()
	for p, f := range files {
		if failed[p] != "" || f.DBCursor != "" {
			continue
		}
		k, ok := kindIn(reg, p)
		if !ok || k.CaptureDBCursor == nil {
			continue
		}
		if cur := k.CaptureDBCursor(p); cur != "" {
			f.DBCursor = cur
			files[p] = f
		}
	}
}

// carryDatabaseStoreCursors preserves prior rowid cursors for database stores
// that were not re-read on this pass. Stores in reread keep the cursor produced
// by ParseDBDelta (including empty string on fallback).
func carryDatabaseStoreCursors(files, oldFiles, reread map[string]FileState) {
	for p, f := range files {
		if _, ok := reread[p]; ok || f.DBCursor != "" {
			continue
		}
		if of, ok := oldFiles[p]; ok && !of.Kept && of.DBCursor != "" {
			f.DBCursor = of.DBCursor
			files[p] = f
		}
	}
}
