// Package harnesscolor is the one palette for harness names. The terminal
// screens, the stats page and the view page each kept their own: claude was
// orange in the CLI and on no page, codex green in the CLI and every harness
// green on the stats page and orange on the view page. One table, read by all
// three, keeps the same agent the same colour wherever it is printed.
package harnesscolor

import (
	"fmt"
	"hash/fnv"
)

// named holds the agents seen most often, as xterm-256 indexes so the terminal
// and the pages can print exactly the same colour.
var named = map[string]int{
	"claude":      208, // orange
	"codex":       77,  // green
	"cursor":      80,  // cyan
	"opencode":    75,  // blue
	"gemini":      170, // magenta
	"aider":       178, // yellow
	"antigravity": 111, // light blue
	"copilot":     147, // lavender
	"pi":          149, // lime
}

// fallback colours every other harness, picked by a hash of its name so the
// pick never changes between runs. None of them repeats a named colour.
var fallback = []int{174, 216, 186, 152, 182, 109, 144, 138}

// Index is the xterm-256 colour for harness h.
func Index(h string) int {
	if n, ok := named[h]; ok {
		return n
	}
	f := fnv.New32a()
	_, _ = f.Write([]byte(h))
	return fallback[f.Sum32()%uint32(len(fallback))]
}

// ANSI is the escape that starts h's colour in a terminal.
func ANSI(h string) string {
	return fmt.Sprintf("\x1b[38;5;%dm", Index(h))
}

// Tag is "[h]", in h's colour when color is set. The colour is closed after the
// bracket so nothing printed after it inherits it.
func Tag(h string, color bool) string {
	tag := "[" + h + "]"
	if !color {
		return tag
	}
	return ANSI(h) + tag + "\x1b[0m"
}

// Paint wraps s in h's colour when color is set.
func Paint(h, s string, color bool) string {
	if !color {
		return s
	}
	return ANSI(h) + s + "\x1b[0m"
}

// Hex is h's colour as a CSS/SVG hex string, the same colour the terminal
// shows for it.
func Hex(h string) string {
	return hex256(Index(h))
}

// Map is Hex for every name given, for a page that colours rows in script.
func Map(names []string) map[string]string {
	out := make(map[string]string, len(names))
	for _, n := range names {
		out[n] = Hex(n)
	}
	return out
}

func hex256(n int) string {
	if n < 16 || n > 231 {
		return "#bcbcbc"
	}
	levels := [6]int{0, 95, 135, 175, 215, 255}
	i := n - 16
	return fmt.Sprintf("#%02x%02x%02x", levels[i/36], levels[(i/6)%6], levels[i%6])
}
