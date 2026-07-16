package main

import (
	"fmt"
	"io"
	"os"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The wordmark prints on exactly two occasions: the end of a successful
// install and the first index build. Everywhere else deja lives in pipes,
// hooks and status bars, where a banner is noise.
const logoAccent = "\x1b[38;5;141m"
const logoDim = "\x1b[2m"
const logoReset = "\x1b[0m"

var logoLines = []string{
	`┌┬┐┌─┐ ┬┌─┐   ┬  ┬┬ ┬`,
	` ││├┤  │├─┤───└┐┌┘│ │`,
	`─┴┘└─┘└┘┴ ┴    └┘ └─┘`,
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
	for _, l := range logoLines {
		fmt.Fprintf(w, "  %s%s%s\n", logoAccent, l, logoReset)
	}
	if tagline != "" {
		fmt.Fprintf(w, "  %s%s%s\n", logoDim, tagline, logoReset)
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
