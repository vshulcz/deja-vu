package index

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/sources"
)

func hermeticDBEnv(t *testing.T) string {
	t.Helper()
	tmp := hermeticIndexEnv(t)
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "xdg-data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg-config"))
	t.Setenv("APPDATA", filepath.Join(tmp, "appdata"))
	t.Setenv("LOCALAPPDATA", filepath.Join(tmp, "localappdata"))
	for _, k := range []string{
		"DEJA_STORES",
		"DEJA_HERMES_HOME",
		"DEJA_HERMES_PROFILES_ROOT",
		"DEJA_CLAUDE_MEM_DB",
		"OPENCODE_DB",
		"DEJA_GOOSE_ROOT",
		"DEJA_GOOSE_DB",
		"GOOSE_PATH_ROOT",
		"DEJA_ZCODE_DB",
		"DEJA_KIRO_DB",
		"DEJA_KIRO_ROOT",
		"DEJA_KILO_DB",
		"DEJA_CRUSH_ROOT",
		"DEJA_OPENCLAW_ROOT",
		"DEJA_DEVIN_DB",
		"DEJA_MIMO_DB",
		"MIMOCODE_DB",
		"MIMOCODE_HOME",
		"DEJA_TELEAGENT_ROOT",
		"TELEAGENT_HOME",
		"DEJA_OMNIGENT_ROOT",
		"OMNIGENT_DATA_DIR",
		"DEJA_OMNIGENT_DB",
		"DEJA_ORCA_ROOT",
		"DEJA_ORCA_DB",
		"GROK_HOME",
		"DEJA_GROK_ROOT",
		"DEJA_GROK_DB",
	} {
		t.Setenv(k, "")
	}
	return tmp
}

func writeDummyFile(t *testing.T, p string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteRegistryDBBacked(t *testing.T) {
	tmp := hermeticDBEnv(t)

	// Narrow DEJA_STORES on purpose to verify KindForPathAll inspects all harnesses.
	t.Setenv("DEJA_STORES", "claude")

	gooseDB := sources.GooseDB()
	writeDummyFile(t, gooseDB, []byte("sqlite"))

	crushProj := filepath.Join(tmp, "crush-proj")
	crushDB := filepath.Join(crushProj, ".crush", "crush.db")
	writeDummyFile(t, crushDB, []byte("sqlite"))
	crushRegistry := fmt.Sprintf(`{"projects":[{"path":%q,"data_dir":%q}]}`, crushProj, filepath.Dir(crushDB))
	writeDummyFile(t, filepath.Join(sources.CrushDataHome(), "projects.json"), []byte(crushRegistry))

	dbStores := []struct {
		harness string
		kind    string
		path    string
	}{
		{"claude", "claude-mem", sources.ClaudeMemDBPath()},
		{"opencode", "opencode", sources.OpencodeDB()},
		{"cursor", "cursor-db", filepath.Join(sources.CursorUserRoot(), "workspaceStorage", "ws1", "state.vscdb")},
		{"grok", "grok", sources.GrokDB()},
		{"hermes", "hermes", filepath.Join(sources.HermesHome(), "state.db")},
		{"goose", "goose-db", gooseDB},
		{"zcode", "zcode-db", sources.ZCodeDB()},
		{"kiro", "kiro-db", sources.KiroDB()},
		{"kilocode", "kilocode-db", sources.KiloDB()},
		{"crush", "crush", crushDB},
		{"openclaw", "openclaw-db", filepath.Join(sources.OpenClawRoot(), "agents", "main", "openclaw-agent.sqlite")},
		{"zed", "zed", sources.ZedDB()},
		{"devin", "devin", sources.DevinSessionsDB()},
		{"mimo", "mimo", sources.MimoDB()},
		{"teleagent", "teleagent", filepath.Join(sources.TeleAgentUsersDir(), "acct1", "teleagent.db")},
		{"omnigent", "omnigent", sources.OmnigentDB()},
		{"orca", "orca", sources.OrcaDB()},
	}

	if len(dbStores) != 17 {
		t.Fatalf("expected 17 SQLite stores, got %d", len(dbStores))
	}

	for _, tc := range dbStores {
		k, h, ok := sources.KindForPathAll(tc.path)
		if !ok {
			t.Fatalf("KindForPathAll(%q) did not match", tc.path)
		}
		if h != tc.harness || k.Name != tc.kind {
			t.Fatalf("KindForPathAll(%q) = (kind=%q harness=%q), want (kind=%q harness=%q)", tc.path, k.Name, h, tc.kind, tc.harness)
		}
		if !k.IsDBBacked() {
			t.Fatalf("KindForPathAll(%q).IsDBBacked() = false, want true", tc.path)
		}
	}

	nonDBPaths := []struct {
		kind string
		path string
	}{
		{"hermes-pg", "hermes-pg:0123456789ab"},
		{"grok", filepath.Join(sources.GrokRoot(), "sessions", "s1", "updates.jsonl")},
		{"goose-jsonl", filepath.Join(sources.GooseSessionsDirs()[0], "s1.jsonl")},
		{"opencode-diff", filepath.Join(sources.OpencodeDiffDir(), "s1.json")},
		{"kiro-cli", filepath.Join(sources.KiroRoot(), "cli", "s1.jsonl")},
		{"cursor", filepath.Join(sources.CursorCLIRoot(), "projects", "p1", "s1.jsonl")},
		{"openclaw", filepath.Join(sources.OpenClawRoot(), "main", "sessions", "s1.jsonl")},
	}
	for _, tc := range nonDBPaths {
		k, _, ok := sources.KindForPathAll(tc.path)
		if !ok {
			t.Fatalf("KindForPathAll(%q) did not match non-DB kind %q", tc.path, tc.kind)
		}
		if k.Name != tc.kind {
			t.Fatalf("KindForPathAll(%q).Name = %q, want %q", tc.path, k.Name, tc.kind)
		}
		if k.IsDBBacked() {
			t.Fatalf("KindForPathAll(%q).IsDBBacked() = true, want false", tc.path)
		}
	}

	t.Setenv("DEJA_STORES", "")
	dbCount := 0
	for _, h := range sources.Registry() {
		for _, k := range h.Kinds {
			if k.IsDBBacked() {
				dbCount++
			}
		}
	}
	if dbCount != 17 {
		t.Fatalf("total IsDBBacked() kinds in Registry() = %d, want 17", dbCount)
	}
}

func TestReadSQLiteWatermarkAndSameFile(t *testing.T) {
	tmp := hermeticDBEnv(t)
	db := filepath.Join(tmp, "custom.sqlite")
	writeDummyFile(t, db, []byte("sqlite-main"))
	writeDummyFile(t, db+"-wal", []byte("wal-1"))
	writeDummyFile(t, db+"-shm", []byte("shm-1"))

	wm := ReadSQLiteWatermark(db)
	if wm.WALSize == 0 || wm.WALMTime == 0 || wm.SHMSize == 0 || wm.SHMMTime == 0 {
		t.Fatalf("ReadSQLiteWatermark missed sidecar stats: %+v", wm)
	}
	if zero := ReadSQLiteWatermark(filepath.Join(tmp, "session.jsonl")); zero != (SQLiteWatermark{}) {
		t.Fatalf("non-SQLite path returned non-zero watermark: %+v", zero)
	}

	fiBefore, err := os.Lstat(db)
	if err != nil {
		t.Fatal(err)
	}
	fsBefore := walkFileStateIn(nil, db, fiBefore, nil)
	if fsBefore.WALSize != wm.WALSize || fsBefore.SHMSize != wm.SHMSize || fsBefore.SHMMTime != wm.SHMMTime {
		t.Fatalf("walkFileStateIn mismatch: fs=%+v wm=%+v", fsBefore, wm)
	}

	// Changing ONLY -shm must invalidate sameFile.
	writeDummyFile(t, db+"-shm", []byte("shm-2222"))
	later := time.Now().Add(2 * time.Second)
	if chErr := os.Chtimes(db+"-shm", later, later); chErr != nil {
		t.Fatal(chErr)
	}
	fiAfter, err := os.Lstat(db)
	if err != nil {
		t.Fatal(err)
	}
	fsAfter := walkFileStateIn(nil, db, fiAfter, nil)
	if sameFile(fsBefore, fsAfter) {
		t.Fatal("sameFile returned true after -shm change")
	}
}

func TestStampDatabaseStoresAndWholeSessionAttribution(t *testing.T) {
	tmp := hermeticDBEnv(t)
	t.Setenv("HERMES_HOME", filepath.Join(tmp, "hermes"))

	// Configure multi-DB Goose via separate root with primary + XDG fallback dirs.
	gooseRoot := filepath.Join(tmp, "goose-root")
	t.Setenv("DEJA_GOOSE_ROOT", "")
	t.Setenv("GOOSE_PATH_ROOT", gooseRoot)
	gooseDB := sources.GooseDB()
	writeDummyFile(t, gooseDB, []byte("goose"))

	kiroDB := sources.KiroDB()
	writeDummyFile(t, kiroDB, []byte("kiro"))
	devinDB := sources.DevinSessionsDB()
	writeDummyFile(t, devinDB, []byte("devin"))
	devinLegacy := sources.DevinLegacySessionsDB()
	writeDummyFile(t, devinLegacy, []byte("devin-legacy"))

	cursor1 := filepath.Join(sources.CursorUserRoot(), "workspaceStorage", "ws-busy", "state.vscdb")
	cursor2 := filepath.Join(sources.CursorUserRoot(), "workspaceStorage", "ws-quiet", "state.vscdb")
	writeDummyFile(t, cursor1, []byte("c1"))
	writeDummyFile(t, cursor2, []byte("c2"))

	opencodeDB := sources.OpencodeDB()
	kiloDB := sources.KiloDB()
	mimoDB := sources.MimoDB()
	writeDummyFile(t, opencodeDB, []byte("oc"))
	writeDummyFile(t, kiloDB, []byte("kilo"))
	writeDummyFile(t, mimoDB, []byte("mimo"))

	t1 := time.Now().Add(-30 * time.Minute).UTC()
	t2 := time.Now().Add(-10 * time.Minute).UTC()
	t3 := time.Now().Add(-5 * time.Minute).UTC()

	files := map[string]FileState{
		gooseDB:     {Path: gooseDB},
		kiroDB:      {Path: kiroDB},
		devinDB:     {Path: devinDB},
		devinLegacy: {Path: devinLegacy},
		cursor1:     {Path: cursor1},
		cursor2:     {Path: cursor2},
		opencodeDB:  {Path: opencodeDB},
		kiloDB:      {Path: kiloDB},
		mimoDB:      {Path: mimoDB},
	}

	sessions := map[string]SessionMeta{
		"goose:g1":    {ID: "g1", Harness: "goose", Path: gooseDB, Updated: t1},
		"kiro:k1":     {ID: "k1", Harness: "kiro", Path: kiroDB, Updated: t2},
		"devin:d1":    {ID: "d1", Harness: "devin", Path: devinDB, Updated: t2},
		"devin:d2":    {ID: "d2", Harness: "devin", Path: devinLegacy, Updated: t1},
		"cursor:c1":   {ID: "c1", Harness: "cursor", Path: cursor1, Updated: t3},
		"cursor:c2":   {ID: "c2", Harness: "cursor", Path: cursor2, Updated: t1},
		"opencode:o1": {ID: "o1", Harness: "opencode", Path: "/Users/alex/work/proj-a", Updated: t2},
		"kilocode:kt": {ID: "kt", Harness: "kilocode", Path: "/Users/alex/kilo/api_conversation_history.json", Updated: t3},
		"kilocode:kd": {ID: "kd", Harness: "kilocode", Path: "/Users/alex/work/proj-b", Updated: t1},
		"mimo:m1":     {ID: "m1", Harness: "mimo", Path: "/Users/alex/work/proj-c", Updated: t2},
	}

	stampDatabaseStores(files, sessions)

	want := map[string]int64{
		gooseDB:     t1.UnixNano(),
		kiroDB:      t2.UnixNano(),
		devinDB:     t2.UnixNano(),
		devinLegacy: t1.UnixNano(),
		cursor1:     t3.UnixNano(),
		cursor2:     t1.UnixNano(),
		opencodeDB:  t2.UnixNano(),
		kiloDB:      t1.UnixNano(), // must ignore newer kilocode task file
		mimoDB:      t2.UnixNano(),
	}
	for db, w := range want {
		if got := files[db].LastUpdated; got != w {
			t.Fatalf("LastUpdated(%s) = %d, want %d", db, got, w)
		}
	}

	passStoreHarness = new(sync.Map)
	passStores = nil
	t.Cleanup(func() {
		passStoreHarness = new(sync.Map)
		passStores = nil
	})

	for _, tc := range []struct {
		path    string
		harness string
	}{
		{kiroDB, "kiro"},
		{devinDB, "devin"},
		{devinLegacy, "devin"},
	} {
		if h := storeHarness(tc.path); h != tc.harness {
			t.Fatalf("storeHarness(%q) = %q, want %q", tc.path, h, tc.harness)
		}
		if !rereadsWholeSessions(tc.path) {
			t.Fatalf("rereadsWholeSessions(%q) = false, want true", tc.path)
		}
	}
}

func TestSQLiteStorePathAndSidecarEdges(t *testing.T) {
	tmp := hermeticDBEnv(t)

	for _, nonDB := range []string{
		"",
		"/Users/alex/s.jsonl",
		"/Users/alex/s.json",
		"/Users/alex/s.gob",
		"/Users/alex/s.bin",
		"/Users/alex/unknown-store",
		"hermes-pg:0123456789ab",
	} {
		if isSQLiteStorePath(nonDB) {
			t.Fatalf("isSQLiteStorePath(%q) = true, want false", nonDB)
		}
		if got := ReadSQLiteWatermark(nonDB); got != (SQLiteWatermark{}) {
			t.Fatalf("ReadSQLiteWatermark(%q) = %+v, want zero", nonDB, got)
		}
	}

	customOpenCode := filepath.Join(tmp, "custom-opencode-store")
	t.Setenv("DEJA_OPENCODE_DB", customOpenCode)
	t.Setenv("OPENCODE_DB", customOpenCode)
	writeDummyFile(t, customOpenCode, []byte("sqlite"))
	writeDummyFile(t, customOpenCode+"-wal", make([]byte, 7))
	writeDummyFile(t, customOpenCode+"-shm", make([]byte, 11))
	if !isSQLiteStorePath(customOpenCode) {
		t.Fatalf("isSQLiteStorePath(%q) = false, want true for registered DB", customOpenCode)
	}
	wm := ReadSQLiteWatermark(customOpenCode)
	if wm.WALSize != 7 || wm.SHMSize != 11 {
		t.Fatalf("ReadSQLiteWatermark(customOpenCode) = %+v, want wal=7 shm=11", wm)
	}

	dirDB := filepath.Join(tmp, "dirside.db")
	writeDummyFile(t, dirDB, []byte("sqlite"))
	if mkErr := os.MkdirAll(dirDB+"-wal", 0o750); mkErr != nil {
		t.Fatal(mkErr)
	}
	if mkErr := os.MkdirAll(dirDB+"-shm", 0o750); mkErr != nil {
		t.Fatal(mkErr)
	}
	if got := ReadSQLiteWatermark(dirDB); got != (SQLiteWatermark{}) {
		t.Fatalf("ReadSQLiteWatermark(dir sidecars) = %+v, want zero", got)
	}
}
