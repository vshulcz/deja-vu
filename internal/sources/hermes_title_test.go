package sources

import (
	"path/filepath"
	"testing"
)

// A session Hermes opens with its own greeting was titled by that greeting;
// the title is the person's first line, like every other parser (#3241).
func TestHermesTitleIsThePersonsFirstLine(t *testing.T) {
	root := t.TempDir()
	db := writeHermesDB(t, filepath.Join(root, "writer"), `
		INSERT INTO messages (session_id,role,content,timestamp) VALUES
		 ('greet','assistant','hello, what shall we do about the ptarmigan cache',1785000000.0),
		 ('greet','user','why does the ptarmigan cache miss on restart',1785000010.0),
		 ('greet','assistant','it is rebuilt from an empty map',1785000020.0),
		 ('bot','assistant','nobody typed here',1785100000.0);`)
	ss, err := ParseHermesDB(db)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	titles := map[string]string{}
	for _, s := range ss {
		titles[s.ID] = s.Title
	}
	if titles["greet"] != "why does the ptarmigan cache miss on restart" {
		t.Fatalf("greet title = %q", titles["greet"])
	}
	if titles["bot"] != "nobody typed here" {
		t.Fatalf("a session with no user line falls back to its first line; got %q", titles["bot"])
	}
}
