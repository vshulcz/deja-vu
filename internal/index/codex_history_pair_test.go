package index

import (
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Codex writes an interactive session twice: the rollout, which holds the
// whole conversation and the directory it ran in, and a line of history.jsonl
// with the same session id and only the prompt. history.jsonl sorts before
// sessions/, so it owned the row: every TUI session was filed under the
// project "history", counted as a clash, and refused by resume as an exec
// entry (#4180). One conversation in two stores is the goose case.
func TestACodexRolloutOwnsTheSessionItsHistoryLineShares(t *testing.T) {
	hist := filepath.Join("/home", "u", ".codex", "history.jsonl")
	roll := filepath.Join("/home", "u", ".codex", "sessions", "2026", "10", "01", "rollout-2026-10-01T12-31-51-01a0.jsonl")

	held := SessionMeta{Harness: "codex", ID: "01a0", Path: hist, Project: "history"}
	owns, collided := attributeSession(held, model.Session{Harness: "codex", ID: "01a0", Path: roll, Project: "work"})
	if collided {
		t.Error("the rollout was reported as clashing with its own history line")
	}
	if !owns {
		t.Error("the history line kept the row; the session is filed under \"history\"")
	}

	heldRoll := SessionMeta{Harness: "codex", ID: "01a0", Path: roll, Project: "work"}
	owns, collided = attributeSession(heldRoll, model.Session{Harness: "codex", ID: "01a0", Path: hist, Project: "history"})
	if collided || owns {
		t.Errorf("the history line arriving second: owns=%v collided=%v, want neither", owns, collided)
	}

	// Two rollouts under one id are still two transcripts.
	other := SessionMeta{Harness: "codex", ID: "01a0", Path: filepath.Join(filepath.Dir(roll), "rollout-other-01a0.jsonl")}
	if _, collided = attributeSession(other, model.Session{Harness: "codex", ID: "01a0", Path: roll}); !collided {
		t.Error("two rollouts under one id stopped being reported")
	}
}
