package main

import (
	"fmt"
	"strings"
	"sync"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The commands built on the work records — `deja friction`, `deja files` —
// answer a person at a keyboard, and an agent will never run them. This block
// puts the same knowledge where an agent cannot miss it: the session-start
// injection every harness already receives.
//
// It is deliberately the narrowest true claim deja can make. Not "you tried
// this approach and reverted it", which is an accusation and has to be right
// (#541 died on exactly that); just what this machine is missing, counted from
// the transcripts themselves. `timeout` is absent on macOS and nine separate
// sessions rediscovered it one failed command at a time.
const (
	// environmentWalls is how many to name. Three is what fits in a glance and
	// what a stable set looks like: these clusters change over weeks, not
	// turns, so the block is not a nag that fires per action.
	environmentWalls = 3
	environmentMax   = 96
)

// environmentBlock names what the machine keeps failing on, or "" when nothing
// has failed in enough separate sessions to be worth an agent's attention.
func environmentBlock(dir string) string {
	walls := index.TopFriction(dir, environmentWalls)
	if len(walls) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("This machine, from deja's index of past sessions across every agent used here:\n")
	for _, w := range walls {
		text := w.Text
		if len(text) > environmentMax {
			text = text[:environmentMax] + "…"
		}
		fmt.Fprintf(&b, "- %d separate sessions hit `%s`\n", len(w.Sessions), text)
	}
	// Without this line the block reads as trivia. With it the model has
	// something to do differently on its next tool call, which is the only
	// reason the block is here.
	b.WriteString("These are environment facts, not history: the tool or module is still missing. " +
		"Check or use an alternative before running into them again.")
	return b.String()
}

// environmentServed is per process, which is what makes "once" countable on
// this side: an MCP server lives for the whole session.
var environmentServed sync.Once

// environmentOnce appends the block to the first recall an MCP session serves.
//
// The session-start injection reaches the thirteen harnesses deja can wire a
// hook into. The rest — and every agent whose user declined the hook — reach
// deja only through MCP. Appending the block to every recall would spend a
// tenth of the response budget repeating a fact the model already has.
func environmentOnce(dir string) string {
	out := ""
	environmentServed.Do(func() {
		if env := environmentBlock(dir); env != "" {
			out = "\n\n" + env
		}
	})
	return out
}
