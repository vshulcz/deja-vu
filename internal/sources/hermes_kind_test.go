package sources

import (
	"path/filepath"
	"testing"
)

// Hermes records who started a session in sessions.source and the spawning
// session in parent_session_id. A subagent's run folds under its parent and a
// cron job's run is marked as one (#4948); a store from before those columns
// still reads its cwd.
func TestHermesSessionKindsComeFromTheSessionsTable(t *testing.T) {
	root := t.TempDir()
	db := writeHermesDB(t, filepath.Join(root, "p"), `
		CREATE TABLE sessions (id TEXT PRIMARY KEY, source TEXT, parent_session_id TEXT, cwd TEXT);
		INSERT INTO sessions VALUES ('s-cli','cli',NULL,'/w/app'),('s-sub','subagent','s-cli','/w/app'),('s-cron','cron',NULL,'/w/app'),('s-orphan','subagent',NULL,NULL);
		INSERT INTO messages (session_id,role,content,timestamp) VALUES
		 ('s-cli','user','plan the migration',1785000000),
		 ('s-sub','user','read the schema',1785000010),
		 ('s-cron','user','nightly digest',1785000020),
		 ('s-orphan','user','a child whose parent is not recorded',1785000030);`)
	ss, err := ParseHermesDB(db)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range ss {
		got[s.ID] = s.Kind + "/" + s.Parent + "/" + s.Project
	}
	want := map[string]string{
		"s-cli":    "//w/app",
		"s-sub":    "subagent/s-cli/w/app",
		"s-cron":   "cron//w/app",
		"s-orphan": "//p",
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s = %q, want %q", id, got[id], w)
		}
	}

	old := writeHermesDB(t, filepath.Join(root, "old"), `
		CREATE TABLE sessions (id TEXT PRIMARY KEY, cwd TEXT);
		INSERT INTO sessions VALUES ('s-1','/w/app');
		INSERT INTO messages (session_id,role,content,timestamp) VALUES ('s-1','user','plan the migration',1785000000);`)
	ss, err = ParseHermesDB(old)
	if err != nil || len(ss) != 1 || ss[0].Project != "w/app" || ss[0].Kind != "" {
		t.Fatalf("a store without source: %+v %v", ss, err)
	}
}
