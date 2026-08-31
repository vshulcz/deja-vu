package index

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// hermesEnv points every other store somewhere empty and returns the hermes
// home, so a pass sees this store and nothing else.
func hermesEnv(t *testing.T) (tmp, home string) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp = t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_GOOSE_DB", filepath.Join(tmp, "none-goose.db"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none-opencode.db"))
	t.Setenv("DEJA_GROK_DB", filepath.Join(tmp, "none-grok.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	home = filepath.Join(tmp, "hermes")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_HERMES_HOME", home)
	return tmp, home
}

func seedHermes(t *testing.T, db string, rows string) {
	t.Helper()
	stmts := `create table if not exists messages (id integer primary key, session_id text, role text, content text, timestamp real);` + rows
	if out, err := exec.Command("sqlite3", db, stmts).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
}

func hermesHits(t *testing.T, dir, marker string) int {
	t.Helper()
	got, err := Search(dir, search.Options{Query: marker, All: true})
	if err != nil {
		t.Fatal(err)
	}
	return len(got)
}

// hermes had a since-the-watermark parser wired into the registry and nothing
// stamped its store, so every pass read the whole history to index one line
// (#2075). Stamping it needs the second-resolution backoff first: without it
// the turns sharing the watermark's second are skipped for good.
func TestTheHermesStoreIsAskedOnlyForWhatIsNew(t *testing.T) {
	tmp, home := hermesEnv(t)
	db := filepath.Join(home, "state.db")
	seedHermes(t, db, `insert into messages values (1,'s1','user','marker-hermes-one',1767268800);`)

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if hermesHits(t, dir, "marker-hermes-one") != 1 {
		t.Fatalf("the store was not indexed at all, so this measures nothing")
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Files[db].LastUpdated == 0 {
		t.Errorf("the store carries no watermark, so the next pass reads it whole")
	}

	// One turn after the watermark, and one in the same whole second as it.
	seedHermes(t, db, `insert into messages values (2,'s1','user','marker-hermes-same',1767268800);`+
		`insert into messages values (3,'s1','user','marker-hermes-two',1767272400);`)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := hermesHits(t, dir, "marker-hermes-two"); n != 1 {
		t.Errorf("the turn added after the watermark is not indexed: %d hits", n)
	}
	if n := hermesHits(t, dir, "marker-hermes-same"); n != 1 {
		t.Errorf("a turn stamped in the watermark's own second was skipped: %d hits", n)
	}
	if n := hermesHits(t, dir, "marker-hermes-one"); n != 1 {
		t.Errorf("the first turn is held %d times", n)
	}
}

// The other half: the store changes whenever any session in it does, so a pass
// that reads only the new messages must not drop the sessions it did not ask
// about. Without fromDatabase knowing the store, the changed-file rule takes
// them — which is what stamping turns on.
func TestAQuietHermesSessionSurvivesAPassThatDidNotAskForIt(t *testing.T) {
	tmp, home := hermesEnv(t)
	db := filepath.Join(home, "state.db")
	seedHermes(t, db, `insert into messages values (1,'quiet','user','marker-hermes-quiet',1767268800);`+
		`insert into messages values (2,'busy','user','marker-hermes-busy',1767272400);`)

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if hermesHits(t, dir, "marker-hermes-quiet") != 1 || hermesHits(t, dir, "marker-hermes-busy") != 1 {
		t.Fatalf("both sessions were not indexed, so this measures nothing")
	}

	seedHermes(t, db, `insert into messages values (3,'busy','user','marker-hermes-added',1767276000);`)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := hermesHits(t, dir, "marker-hermes-added"); n != 1 {
		t.Errorf("the added turn is not indexed: %d hits", n)
	}
	if n := hermesHits(t, dir, "marker-hermes-quiet"); n != 1 {
		t.Errorf("the session the pass did not ask about was dropped: %d hits", n)
	}
}

// Older builds shard the store per profile, so a machine can hold several. Each
// carries its own watermark: stamping them alike would give a quiet profile the
// busy one's time and skip everything it gained below that line (#2071).
func TestEachHermesProfileKeepsItsOwnWatermark(t *testing.T) {
	tmp, home := hermesEnv(t)
	profiles := filepath.Join(home, "profiles")
	var dbs []string
	for i, name := range []string{"busy", "quiet"} {
		dir := filepath.Join(profiles, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		db := filepath.Join(dir, "state.db")
		seedHermes(t, db, fmt.Sprintf(`insert into messages values (1,'s%d','user','marker-hermes-%s',%d);`,
			i, name, 1767268800+int64(i)*-3600))
		dbs = append(dbs, db)
	}

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if hermesHits(t, dir, "marker-hermes-busy") != 1 || hermesHits(t, dir, "marker-hermes-quiet") != 1 {
		t.Fatalf("the two profiles were not both indexed, so this measures nothing")
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Files[dbs[1]].LastUpdated == m.Files[dbs[0]].LastUpdated {
		t.Errorf("both profiles carry one watermark (%d), so the quiet one is stamped with the busy one's newest",
			m.Files[dbs[1]].LastUpdated)
	}

	// The quiet profile gains a turn newer than anything it holds and older
	// than the busy profile's newest.
	seedHermes(t, dbs[1], `insert into messages values (2,'s1','user','marker-hermes-quiet-two',1767267000);`)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := hermesHits(t, dir, "marker-hermes-quiet-two"); n != 1 {
		t.Errorf("the turn added to the quiet profile is not indexed: %d hits", n)
	}
}
