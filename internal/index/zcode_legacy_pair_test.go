package index

import (
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A ZCode snapshot indexed before the user restored it stays on disk, unchanged,
// so the incremental pass never re-reads it, and the restored session arrives
// from the CLI database under the same id. That is one conversation in two
// stores: the database row owns it, and the pair is not a clash. Sort order
// handed the row to the snapshot (~/.zcode sorts before most project paths),
// so resume kept refusing the restored session (#4432).
func TestAZCodeSnapshotYieldsToItsRestoredSession(t *testing.T) {
	snap := filepath.Join("/Users", "x", ".zcode", "v2", "sessions", "5f0c", "task-1.json")
	dir := filepath.Join("/Users", "x", "proj")

	held := SessionMeta{Harness: "zcode", ID: "acp-9", Path: snap}
	owns, collided := attributeSession(held, model.Session{Harness: "zcode", ID: "acp-9", Path: dir})
	if collided || !owns {
		t.Errorf("restored session: owns=%v collided=%v, want it to take the row without a clash", owns, collided)
	}

	heldDB := SessionMeta{Harness: "zcode", ID: "acp-9", Path: dir}
	owns, collided = attributeSession(heldDB, model.Session{Harness: "zcode", ID: "acp-9", Path: snap})
	if collided || owns {
		t.Errorf("snapshot after the database: owns=%v collided=%v, want the database to keep the row", owns, collided)
	}
}
