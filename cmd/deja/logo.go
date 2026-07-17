package main

import (
	"fmt"
	"io"
	"os"
	"regexp"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The mark prints on exactly two occasions: the end of a successful install
// and the first index build. Everywhere else deja lives in pipes, hooks and
// status bars, where a banner is noise.
//
// loopArt is the rewind-loop from logo.svg rendered to half-block cells with
// the same purple-to-teal gradient, neofetch style: art on the left, an info
// column on the right. It is generated from the vector mark, not hand-drawn
// (scripts live outside the repo; regenerate by rasterizing logo.svg at
// 34x34 cells and mapping to the xterm-256 cube).
var loopArt = []string{
	"\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;104m▄\x1b[38;5;104m▄\x1b[38;5;68m▄\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;104m▄\x1b[38;5;104m█\x1b[38;5;104m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;104m▄\x1b[38;5;104m█\x1b[38;5;104m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m▀\x1b[38;5;68m▀\x1b[38;5;68m▀\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;104m█\x1b[38;5;104m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m▀\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[0m \x1b[38;5;104m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[38;5;68m▄\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;74m▄\x1b[38;5;74m▄\x1b[38;5;74m█\x1b[38;5;74m█\x1b[0m \x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m▀\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;68m▄\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m▄\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;74m▄\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;74m▄\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m▄\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;74m▀\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m▀\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;74m▀\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[38;5;68m▀\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;74m▄\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[0m \x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[0m \x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m▄\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;74m▄\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m▀\x1b[0m \x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;68m▀\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;74m█\x1b[38;5;74m▄\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;74m▄\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m▀\x1b[0m \x1b[0m \x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;68m▀\x1b[38;5;68m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m▄\x1b[38;5;74m▄\x1b[38;5;74m▄\x1b[38;5;74m▄\x1b[38;5;74m▄\x1b[38;5;74m▄\x1b[38;5;74m▄\x1b[38;5;74m▄\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m▀\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;74m▀\x1b[38;5;74m▀\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m▀\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;74m▀\x1b[38;5;74m▀\x1b[38;5;74m▀\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m▀\x1b[38;5;74m▀\x1b[38;5;74m▀\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m\x1b[0m",
}

const (
	logoAccent = "\x1b[38;5;141m"
	logoBold   = "\x1b[1m"
	logoDim    = "\x1b[2m"
	logoReset  = "\x1b[0m"
)

var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

func visibleLen(s string) int { return len([]rune(ansiRE.ReplaceAllString(s, ""))) }

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

// printLogo lays the info column beside the mark, vertically centred.
func printLogo(w io.Writer, info []string) {
	top := (len(loopArt) - len(info)) / 2
	if top < 0 {
		top = 0
	}
	fmt.Fprintln(w)
	for i, a := range loopArt {
		line := "  " + a
		if j := i - top; j >= 0 && j < len(info) && info[j] != "" {
			line += spaces(40-visibleLen(a)) + info[j]
		}
		fmt.Fprintln(w, line)
	}
	fmt.Fprintln(w)
}

func spaces(n int) string {
	if n < 1 {
		n = 1
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}

func brandInfo() []string {
	return []string{
		logoBold + "deja-vu" + logoReset,
		logoAccent + "───────────────" + logoReset,
		logoDim + "memory for coding agents" + logoReset,
	}
}

func maybeFirstIndexGreeting() {
	b := index.LastBuild
	if !b.Initial || b.Messages == 0 || !logoWanted(os.Stdout) {
		return
	}
	info := append(brandInfo(),
		"",
		fmt.Sprintf("messages   %s%d%s", logoBold, b.Messages, logoReset),
		fmt.Sprintf("sessions   %s%d%s", logoBold, b.Sessions, logoReset),
		fmt.Sprintf("agents     %s%d%s", logoBold, b.Harnesses, logoReset),
		"",
		logoDim+`try: deja "something you fixed weeks ago"`+logoReset,
	)
	printLogo(os.Stdout, info)
}
