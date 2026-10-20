package sources

import "github.com/vshulcz/deja-vu/internal/model"

// The parse switches — DEJA_INDEX_TOOL_OUTPUT, DEJA_INDEX_COMMANDS,
// DEJA_INDEX_PATHS, DEJA_INDEX_EDITS and DEJA_INDEX_WRITES — each leave one
// record role out of the index. Readers tested them one by one, and six never
// tested the tool-output switch and two the commands switch, so =0 kept what
// it said it left out (#5413). The registry now drops the switched-off roles
// from whatever a reader returns; a reader's own test only saves the work.

// parseSwitches pairs each switch with the role it leaves out.
var parseSwitches = []struct {
	env  string
	on   func() bool
	role string
}{
	{"DEJA_INDEX_TOOL_OUTPUT", IndexToolOutput, RoleToolOutput},
	{"DEJA_INDEX_COMMANDS", IndexCommands, RoleCommand},
	{"DEJA_INDEX_PATHS", IndexToolPaths, RoleFiles},
	{"DEJA_INDEX_EDITS", IndexEdits, RoleEdit},
	{"DEJA_INDEX_WRITES", IndexWrites, RoleWrote},
}

// SwitchedOffRoles is the set of record roles the parse switches leave out
// this run, nil when every switch is on. A record that arrives by sync import
// is held to it as a parsed one is (#5425).
func SwitchedOffRoles() map[string]bool {
	var off map[string]bool
	for _, sw := range parseSwitches {
		if sw.on() {
			continue
		}
		if off == nil {
			off = map[string]bool{}
		}
		off[sw.role] = true
	}
	return off
}

// ParseSwitchesOff names the parse switches set to 0 this run.
func ParseSwitchesOff() []string {
	var names []string
	for _, sw := range parseSwitches {
		if !sw.on() {
			names = append(names, sw.env+"=0")
		}
	}
	return names
}

// ApplyParseSwitches drops the records a parse switch turned off from what a
// reader returned. The registry runs every parse through it.
func ApplyParseSwitches(ss []model.Session) []model.Session {
	off := SwitchedOffRoles()
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
	if off := SwitchedOffRoles(); off != nil {
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
