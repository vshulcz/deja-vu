package main

import (
	"fmt"
	"strings"
)

// dejaHookCount is how many of deja's hooks a config runs for one event. The
// entry is the unit a harness fires, and several of them can name the same
// subcommand: a merge that kept both sides, a hand-edited copy, or — until
// #3421 — an install from a build under another path, which stacked one more
// every time instead of taking the existing one over.
func dejaHookCount(hooks map[string]any, event, sub string) int {
	entries, _ := hooks[event].([]any)
	n := 0
	for _, entryAny := range entries {
		entry, _ := entryAny.(map[string]any)
		if entry == nil {
			continue
		}
		hs, _ := entry["hooks"].([]any)
		for _, hAny := range hs {
			h, _ := hAny.(map[string]any)
			if h == nil || h["type"] != "command" {
				continue
			}
			if hookCommandKindOf(h["command"], "deja "+sub) != hookNotDejas {
				n++
			}
		}
	}
	return n
}

// doctorHookRepeats is the line a hook row prints when an event runs deja more
// than once. Nothing else in doctor could say it: every check answered "is the
// hook there", which a file holding eight copies of it answers yes to. What
// the reader saw instead was the same memory injected eight times on every
// prompt, eight processes started for it, and no report naming the cause
// (#3421, #3422).
func doctorHookRepeats(hooks map[string]any, wiring []struct{ Event, Sub, Matcher string }, target string) string {
	var parts []string
	for _, h := range wiring {
		if n := dejaHookCount(hooks, h.Event, h.Sub); n > 1 {
			parts = append(parts, fmt.Sprintf("%s ×%d", h.Event, n))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "runs deja more than once per event (" + strings.Join(parts, ", ") +
		") — every copy fires; `deja install " + target + "` leaves one"
}
