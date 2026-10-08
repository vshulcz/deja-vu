package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// deja keeps a session searchable after the agent deletes its transcript
// (#2970, #3529), so resume is offered sessions the agent no longer has, and
// the command it printed failed in the agent: codex said "No saved session
// found" (#4185). A session whose own transcript file is gone is refused with
// where it can still be read.
func resumeGoneError(s model.Session) error {
	if !transcriptGone(s) {
		return nil
	}
	short := digest.Short(s.ID)
	if sources.CanWriteBack(s.Harness) {
		return errors.New("session " + short + " is no longer in " + s.Harness +
			"'s own store (deleted or expired) — `deja resume " + short + " --write-back` writes it back from the index, and `deja show " + short + "` still has it")
	}
	return errors.New("session " + short + " is no longer in " + s.Harness +
		"'s own store (deleted or expired), so it cannot be reopened there — `deja show " + short + "` still has it")
}

// transcriptGone reports whether the file a session was read from has left
// the disk. Only per-session transcript files count: a database or a working
// directory in Path says nothing about one session, so the database stores
// are asked by id.
func transcriptGone(s model.Session) bool {
	// opencode and Kilo keep every session in one database, and Path is the
	// directory it ran in, so the database is asked instead: `opencode -s`
	// on a deleted id says "Session not found", and 2.x starts an empty
	// session under it (#4205).
	if s.Harness == "opencode" || s.Harness == "kilocode" {
		return sources.OpencodeStoreLacks(s.Harness, s.ID)
	}
	// goose >= 1.10 the same, with Path naming its sessions.db; a legacy
	// .jsonl session is a file of its own and goes through the check below
	// (#4271).
	if s.Harness == "goose" && !isTranscriptFile(s.Path) {
		return sources.GooseStoreLacks(s.Path, s.ID)
	}
	// Hermes the same, with Path naming the store: `hermes sessions delete`
	// leaves `hermes --resume` answering "Session not found" (#4250).
	if s.Harness == "hermes" {
		return sources.HermesStoreLacks(s.Path, s.ID)
	}
	if s.Path == "" || !isTranscriptFile(s.Path) {
		return false
	}
	if _, err := os.Stat(s.Path); !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	// Codex moves its own rollouts: `codex archive` into archived_sessions,
	// and a background pass compresses one a week old to .jsonl.zst. Either
	// way `codex resume` still finds it.
	if s.Harness == "codex" && codexRolloutExists(s.ID) {
		return false
	}
	return true
}

func isTranscriptFile(p string) bool {
	for _, ext := range []string{".jsonl", ".jsonl.zst", ".json"} {
		if strings.HasSuffix(strings.ToLower(p), ext) {
			return true
		}
	}
	return false
}

func codexRolloutExists(id string) bool {
	if id == "" || strings.ContainsAny(id, `*?[\/`) {
		return false
	}
	for _, root := range sources.CodexRoots() {
		for _, pattern := range []string{
			filepath.Join(root, "sessions", "*", "*", "*", "rollout-*"+id+".jsonl*"),
			filepath.Join(root, "archived_sessions", "rollout-*"+id+".jsonl*"),
		} {
			if m, _ := filepath.Glob(pattern); len(m) > 0 {
				return true
			}
		}
	}
	return false
}
