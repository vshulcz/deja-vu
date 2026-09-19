package index

import (
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// opencode's database and its per-session diff file carry the same session id,
// so on the per-file path they arrive as two sessions with one identity. The
// messages merge either way — checked on a real store, all 60 sampled sessions
// kept both their conversation and their edits — but the pass reported the pair
// and a machine with 84 diffs was told that 84 of its sessions were clashing.
// One conversation in two stores is the goose case, and it is not reported
// either (#3791).
func TestADiffFileIsNotACollisionWithItsOwnSession(t *testing.T) {
	db := filepath.Join("/data", "opencode", "opencode.db")
	diff := filepath.Join("/data", "opencode", "storage", "session_diff", "ses_a.json")

	// The diff arriving second: the database row keeps the session.
	held := SessionMeta{Harness: "opencode", ID: "ses_a", Path: db, Project: "pool"}
	owns, collided := attributeSession(held, model.Session{Harness: "opencode", ID: "ses_a", Path: diff})
	if collided {
		t.Error("the diff beside the database was reported as a clashing transcript")
	}
	if owns {
		t.Error("the diff took the session's row from the database, which holds its project and title")
	}

	// And the other order, because a pass can read either first.
	heldDiff := SessionMeta{Harness: "opencode", ID: "ses_a", Path: diff}
	owns, collided = attributeSession(heldDiff, model.Session{Harness: "opencode", ID: "ses_a", Path: db, Project: "pool"})
	if collided {
		t.Error("the database row was reported as clashing with the diff it belongs to")
	}
	if !owns {
		t.Error("the database row did not take the session from the diff-only row")
	}

	// Two genuinely different transcripts under one id still are a collision:
	// that is what the report exists for.
	other := SessionMeta{Harness: "opencode", ID: "ses_a", Path: filepath.Join("/data", "opencode", "other.db")}
	if _, collided = attributeSession(other, model.Session{Harness: "opencode", ID: "ses_a", Path: db}); !collided {
		t.Error("two databases under one id stopped being reported")
	}
}

func TestOpencodeDiffPathIsRecognisedByItsDirectory(t *testing.T) {
	for path, want := range map[string]bool{
		"/data/opencode/storage/session_diff/ses_a.json": true,
		"/elsewhere/session_diff/ses_b.json":             true,
		"/data/opencode/opencode.db":                     false,
		"/data/opencode/storage/session_diff":            false,
		"/data/opencode/storage/other/ses_a.json":        false,
		"/data/opencode/storage/session_diff/notes.txt":  false,
	} {
		if got := isOpencodeDiff(path); got != want {
			t.Errorf("isOpencodeDiff(%q) = %v, want %v", path, got, want)
		}
	}
}
