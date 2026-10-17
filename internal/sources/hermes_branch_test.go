package sources

import (
	"path/filepath"
	"testing"
)

// /branch copies a conversation into a new session and marks it with the one
// it came from in model_config._branched_from. Read as an unrelated session,
// the branch's recall answered with its source (#5141). A compression's
// continuation also names a parent, without the marker, and stays as it is.
func TestAHermesBranchIsAForkOfItsSource(t *testing.T) {
	db := writeHermesDB(t, filepath.Join(t.TempDir(), "p"), `
		CREATE TABLE sessions (id TEXT PRIMARY KEY, source TEXT, parent_session_id TEXT, cwd TEXT, model_config TEXT);
		INSERT INTO sessions VALUES
		 ('20261018_012943_14524c','cli',NULL,'/w/app','{"max_iterations":90}'),
		 ('20261018_013350_8f1cbe','cli','20261018_012943_14524c',NULL,'{"_branched_from":"20261018_012943_14524c"}'),
		 ('20261018_014000_aaaaaa','cli','20261018_012943_14524c','/w/app','{"max_iterations":90}'),
		 ('20261018_015000_bbbbbb','cli',NULL,'/w/app','not json');
		INSERT INTO messages (session_id,role,content,timestamp) VALUES
		 ('20261018_012943_14524c','user','password hashing uses bcrypt cost 12',1792200000),
		 ('20261018_013350_8f1cbe','user','password hashing uses bcrypt cost 12',1792200100),
		 ('20261018_014000_aaaaaa','user','summary of the earlier turns',1792200200),
		 ('20261018_015000_bbbbbb','user','a row whose model_config is not json',1792200300);`)
	ss, err := ParseHermesDB(db)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range ss {
		got[s.ID] = s.Kind + "/" + s.Parent + "/" + s.Project
	}
	want := map[string]string{
		"20261018_012943_14524c": "//w/app",
		"20261018_013350_8f1cbe": "fork/20261018_012943_14524c/w/app",
		"20261018_014000_aaaaaa": "//w/app",
		"20261018_015000_bbbbbb": "//w/app",
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s = %q, want %q", id, got[id], w)
		}
	}

	// Read off the store by id, for a branch no build has seen.
	t.Setenv("DEJA_HERMES_DB", db)
	h, ok := HermesSessionHead("20261018_013350_8f1cbe")
	if !ok || h.Kind != "fork" || h.Parent != "20261018_012943_14524c" || h.Harness != "hermes" {
		t.Errorf("head of the branch = %+v %v", h, ok)
	}
	if _, ok := HermesSessionHead("20261018_999999_cccccc"); ok {
		t.Error("an id no store holds was found")
	}
	if _, ok := HermesSessionHead("x' or '1'='1"); ok {
		t.Error("an id that is not Hermes' shape reached the query")
	}
}
