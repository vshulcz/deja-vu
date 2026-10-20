package sources

import "github.com/vshulcz/deja-vu/internal/model"

// The parse switches — DEJA_INDEX_TOOL_OUTPUT, DEJA_INDEX_COMMANDS,
// DEJA_INDEX_PATHS, DEJA_INDEX_EDITS and DEJA_INDEX_WRITES — each leave one
// record role out of the index. Readers tested them one by one, and six never
// tested the tool-output switch and two the commands switch, so =0 kept what
// it said it left out (#5413). The registry now drops the switched-off roles
// from whatever a reader returns; a reader's own test only saves the work.

// switchedOffRoles is the set of record roles the parse switches leave out
// this run, nil when every switch is on.
func switchedOffRoles() map[string]bool {
	var off map[string]bool
	drop := func(on bool, role string) {
		if on {
			return
		}
		if off == nil {
			off = map[string]bool{}
		}
		off[role] = true
	}
	drop(IndexToolOutput(), RoleToolOutput)
	drop(IndexCommands(), RoleCommand)
	drop(IndexToolPaths(), RoleFiles)
	drop(IndexEdits(), RoleEdit)
	drop(IndexWrites(), RoleWrote)
	return off
}

// ApplyParseSwitches drops the records a parse switch turned off from what a
// reader returned. The registry runs every parse through it.
func ApplyParseSwitches(ss []model.Session) []model.Session {
	off := switchedOffRoles()
	if off == nil {
		return ss
	}
	for i := range ss {
		ss[i].Messages = dropRoles(ss[i].Messages, off)
	}
	return ss
}

// ParseSwitched is ApplyParseSwitches for one session read outside the
// registry, as a compaction capture reads it.
func ParseSwitched(s model.Session) model.Session {
	if off := switchedOffRoles(); off != nil {
		s.Messages = dropRoles(s.Messages, off)
	}
	return s
}

func dropRoles(msgs []model.Message, off map[string]bool) []model.Message {
	kept := msgs[:0]
	for _, m := range msgs {
		if !off[m.Role] {
			kept = append(kept, m)
		}
	}
	clear(msgs[len(kept):])
	return kept
}
