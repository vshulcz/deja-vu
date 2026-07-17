package main

import (
	"fmt"
	"io"
	"os"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The mark prints on exactly two occasions: the end of a successful install
// and the first index build. Everywhere else deja lives in pipes, hooks and
// status bars, where a banner is noise.
//
// It is the rewind-loop from the project logo: an arrow circling back on
// itself around a dot, purple into teal like the gradient in logo.svg.
const (
	logoPurple = "\x1b[38;5;141m"
	logoTeal   = "\x1b[38;5;80m"
	logoBold   = "\x1b[1m"
	logoDim    = "\x1b[2m"
	logoReset  = "\x1b[0m"
)

func logoLines(tagline string) []string {
	p, t, b, d, r := logoPurple, logoTeal, logoBold, logoDim, logoReset
	return []string{
		p + "  ╭──────╴" + r,
		p + " ╭╯" + r,
		p + " │    ●    " + t + "▲" + r + "    " + b + "deja-vu" + r,
		p + " ╰╮        " + t + "│" + r + "    " + d + tagline + r,
		p + "  ╰────────" + t + "╯" + r,
	}
}

func logoWanted(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func printLogo(w io.Writer, tagline string) {
	fmt.Fprintln(w)
	for _, l := range logoLines(tagline) {
		fmt.Fprintf(w, " %s\n", l)
	}
	fmt.Fprintln(w)
}

func maybeFirstIndexGreeting() {
	b := index.LastBuild
	if !b.Initial || b.Messages == 0 || !logoWanted(os.Stdout) {
		return
	}
	printLogo(os.Stdout, "memory for coding agents")
	fmt.Printf("  indexed %d messages from %d sessions across %d agents\n", b.Messages, b.Sessions, b.Harnesses)
	fmt.Printf("  try: deja \"something you fixed weeks ago\"\n\n")
}
