package sources

import (
	"os"

	"github.com/vshulcz/deja-vu/internal/model"
)

// DEJA_INCLUDE_SUBAGENTS means one thing in every reader (#5301): unset, a
// sub-agent run comes in as the task it was handed, the edits it made and its
// last turns; =1 takes the whole run; =0 leaves it out. Readers used to test
// the variable themselves, and twelve of them never did, so =0 kept their
// children and the default read some of them whole.

// SubagentsOff reports DEJA_INCLUDE_SUBAGENTS=0.
func SubagentsOff() bool { return os.Getenv("DEJA_INCLUDE_SUBAGENTS") == "0" }

// SubagentsWhole reports DEJA_INCLUDE_SUBAGENTS=1.
func SubagentsWhole() bool { return os.Getenv("DEJA_INCLUDE_SUBAGENTS") == "1" }

// SpawnedKind reports whether a session's kind is a reader's word for an
// agent another session spawned.
func SpawnedKind(kind string) bool { return model.SpawnedKind(kind) }

// ApplySubagentSwitch is the switch applied to what a reader returned. The
// registry runs every parse through it, so a reader only has to mark its
// children with a spawned Kind. Cutting twice is the same as cutting once, so
// a reader that already cut keeps its own cut.
func ApplySubagentSwitch(ss []model.Session) []model.Session {
	if SubagentsWhole() {
		return ss
	}
	off := SubagentsOff()
	out := ss[:0:0]
	for _, s := range ss {
		if SpawnedKind(s.Kind) {
			if off {
				continue
			}
			s.Messages = KeepSubagentTail(s.Messages)
		}
		out = append(out, s)
	}
	return out
}

func switchedParse(f func(string, int64) ([]model.Session, error)) func(string, int64) ([]model.Session, error) {
	if f == nil {
		return nil
	}
	return func(p string, nano int64) ([]model.Session, error) {
		ss, err := f(p, nano)
		return applyReadSwitches(ss), err
	}
}

func switchedParseFrom(f func(string, int64, int64) ([]model.Session, error)) func(string, int64, int64) ([]model.Session, error) {
	if f == nil {
		return nil
	}
	return func(p string, off, nano int64) ([]model.Session, error) {
		ss, err := f(p, off, nano)
		return applyReadSwitches(ss), err
	}
}

func switchedLoad(f func() []model.Session) func() []model.Session {
	if f == nil {
		return nil
	}
	return func() []model.Session { return applyReadSwitches(f()) }
}

// applyReadSwitches is every switch on what a reader returned: the parse
// switches, then the sub-agent cut, so the turns a cut keeps are counted
// among the records the index keeps.
func applyReadSwitches(ss []model.Session) []model.Session {
	return ApplySubagentSwitch(ApplyParseSwitches(ss))
}

// withSubagentSwitch wraps every parse a harness makes in the sub-agent and
// parse switches.
func withSubagentSwitch(hs []Harness) []Harness {
	for i := range hs {
		hs[i].Load, hs[i].switched = switchedLoad(hs[i].Load), true
		kinds := make([]FileKind, len(hs[i].Kinds))
		for j, k := range hs[i].Kinds {
			k.Parse = switchedParse(k.Parse)
			k.ParseFrom = switchedParseFrom(k.ParseFrom)
			k.switched = true
			kinds[j] = k
		}
		hs[i].Kinds = kinds
	}
	return hs
}
