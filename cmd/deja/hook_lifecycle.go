package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// orderForInjection puts the sessions a person marked rejected last and
// returns the warning that has to travel with them.
//
// recall attaches a correction to the session it belongs to (#684, #694). The
// two paths that inject unprompted did not: the session-start block listed the
// note as a separate item and left the session unmarked, and hook-prompt
// served a rejected session as an equal answer (#761). The hooks are what the
// agent reads before it does anything.
func orderForInjection(ss []model.Session) ([]model.Session, string) {
	states := sources.PromotedLifecycles()
	if len(states) == 0 {
		return ss, ""
	}
	rejected := func(s model.Session) (string, bool) {
		lc, ok := states[s.Harness+":"+s.ID]
		if !ok || lc.State != lifecycleRejected {
			return "", false
		}
		return lc.Note, true
	}
	out := append([]model.Session(nil), ss...)
	sort.SliceStable(out, func(i, j int) bool {
		_, ri := rejected(out[i])
		_, rj := rejected(out[j])
		return !ri && rj
	})
	var warn []string
	for _, s := range out {
		note, ok := rejected(s)
		if !ok {
			continue
		}
		line := fmt.Sprintf("session %s was tried and rejected", s.ID)
		if note != "" {
			line += ": " + note
		}
		warn = append(warn, line)
	}
	if len(warn) == 0 {
		return out, ""
	}
	return out, "Some of this was rejected — " + strings.Join(warn, "; ") + ".\n"
}
