package sources

import (
	"path/filepath"
	"testing"
)

// A session Hermes opens with its own greeting was titled by that greeting
// (#3241); titling in the parser then skipped the index's rule for a person's
// greeting (#3251). The parser leaves the title empty, and the index derives it
// the way it does for every other harness.
func TestHermesLeavesTheTitleToTheIndex(t *testing.T) {
	root := t.TempDir()
	db := writeHermesDB(t, filepath.Join(root, "writer"), `
		INSERT INTO messages (session_id,role,content,timestamp) VALUES
		 ('greet','assistant','hello, what shall we do about the ptarmigan cache',1785000000.0),
		 ('greet','user','why does the ptarmigan cache miss on restart',1785000010.0),
		 ('bot','assistant','nobody typed here',1785100000.0);`)
	ss, err := ParseHermesDB(db)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, s := range ss {
		if s.Title != "" {
			t.Errorf("session %s titled %q in the parser", s.ID, s.Title)
		}
	}
}
