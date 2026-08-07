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
// 2x2 quadrant cells and mapping to the xterm-256 cube).
var loopArt = []string{
	"\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;104m▗\x1b[38;5;104m▄\x1b[38;5;68m▄\x1b[38;5;68m▟\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;104m▄\x1b[38;5;104m▟\x1b[38;5;104m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m▛\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;104m▄\x1b[38;5;104m█\x1b[38;5;104m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m▛\x1b[38;5;68m▀\x1b[38;5;68m▀\x1b[38;5;68m▘\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;104m▗\x1b[38;5;104m█\x1b[38;5;104m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m▀\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[0m \x1b[38;5;104m▗\x1b[38;5;104m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m▛\x1b[38;5;68m▘\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[0m \x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m▛\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;74m▗\x1b[38;5;74m▄\x1b[38;5;74m▄\x1b[38;5;74m▟\x1b[0m \x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[38;5;68m▐\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m▘\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;68m▄\x1b[38;5;68m▄\x1b[38;5;68m▄\x1b[38;5;68m▄\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;74m▗\x1b[38;5;74m▟\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m▌\x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[38;5;68m▐\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;68m▐\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m▌\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;74m▗\x1b[38;5;74m▟\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m▌\x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[38;5;68m▐\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m▖\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;74m▀\x1b[38;5;74m▀\x1b[38;5;74m▀\x1b[38;5;74m▀\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;74m▜\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m▌\x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[0m \x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m▙\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;74m▟\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[0m \x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[0m \x1b[38;5;68m▝\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m▙\x1b[38;5;68m▖\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;74m▗\x1b[38;5;74m▟\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m▘\x1b[0m \x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;68m▝\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;68m█\x1b[38;5;74m▄\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;74m▄\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m▘\x1b[0m \x1b[0m \x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;68m▀\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m▙\x1b[38;5;74m▄\x1b[38;5;74m▄\x1b[38;5;74m▖\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;74m▗\x1b[38;5;74m▄\x1b[38;5;74m▄\x1b[38;5;74m▟\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m▀\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;74m▀\x1b[38;5;74m▜\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m▛\x1b[38;5;74m▀\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m\x1b[0m",
	"\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[38;5;74m▝\x1b[38;5;74m▀\x1b[38;5;74m▀\x1b[38;5;74m▜\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m█\x1b[38;5;74m▛\x1b[38;5;74m▀\x1b[38;5;74m▀\x1b[38;5;74m▘\x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m \x1b[0m\x1b[0m",
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

var logoWanted = defaultLogoWanted

func defaultLogoWanted(f *os.File) bool {
	// TERM=dumb is a terminal that cannot do any of this: emacs shell-mode, a
	// CI shell, an editor's built-in console. NO_COLOR was honoured and this
	// was not, so those readers got escape sequences as literal text (#903).
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	if fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	// The null device is a character device too, so it passed this test and
	// `deja index >/dev/null` took the interactive branch: the live display
	// painted into the sink that discards it, and the plain progress lines —
	// which go to stderr and were the point of redirecting stdout away — were
	// never printed at all. #862 hit this from the detached warmup and patched
	// that one caller by sentinel; the redirect a person types in a script or
	// a cron entry reaches the same place.
	return !isNullDevice(fi)
}

// logoInfoCol is where the info column starts when the terminal has room for
// it. The mark is a fixed 40-column block, so this is a two-space margin plus
// the mark.
const logoInfoCol = 42

// logoArtWidth is the widest line of the mark itself, without the margin.
func logoArtWidth() int {
	w := 0
	for _, a := range loopArt {
		if n := visibleLen(a); n > w {
			w = n
		}
	}
	return w
}

// logoInfoStart picks the column the info lines start at. Everything was laid
// out against a hard 42 and nothing was ever added up, so `try: deja "something
// you fixed weeks ago"` landed at column 83 on a terminal 80 wide and wrapped —
// the same shape as #1073 and #1076. Pull the column left until the longest
// info line fits; when even a two-space gutter cannot hold both, say so and let
// the caller stack the column under the mark.
func logoInfoStart(info []string) (col int, stacked bool) {
	widest := 0
	for _, s := range info {
		if n := visibleLen(s); n > widest {
			widest = n
		}
	}
	col = logoInfoCol
	if room := briefWidth() - widest; room < col {
		col = room
	}
	if col < logoArtWidth()+4 {
		return 2, true
	}
	return col, false
}

// logoLines lays the info column beside the mark, vertically centred, and
// returns the composed lines. The live build display repaints these, so the
// layout has to be shared with printLogo rather than reimplemented.
func logoLines(info []string) []string {
	col, stacked := logoInfoStart(info)
	out := make([]string, 0, len(loopArt)+len(info)+2)
	out = append(out, "")
	if stacked {
		// Too narrow for two columns. The mark still fits on its own, so keep
		// it and put the numbers underneath rather than shredding both.
		for _, a := range loopArt {
			out = append(out, "  "+a)
		}
		out = append(out, "")
		for _, s := range info {
			if s == "" {
				out = append(out, "")
				continue
			}
			out = append(out, spaces(col)+s)
		}
		return append(out, "")
	}
	top := (len(loopArt) - len(info)) / 2
	if top < 0 {
		top = 0
	}
	for i, a := range loopArt {
		line := "  " + a
		if j := i - top; j >= 0 && j < len(info) && info[j] != "" {
			line += spaces(col-2-visibleLen(a)) + info[j]
		}
		out = append(out, line)
	}
	// An info column taller than the mark keeps going underneath it rather
	// than being cut off: sixteen harnesses plus a header already overflow,
	// so the last stores were silently dropped from the greeting.
	for j := len(loopArt) - top; j < len(info); j++ {
		if j < 0 {
			continue
		}
		out = append(out, spaces(col)+info[j])
	}
	return append(out, "")
}

// printLogo lays the info column beside the mark, vertically centred.
func printLogo(w io.Writer, info []string) {
	for _, l := range logoLines(info) {
		fmt.Fprintln(w, l)
	}
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

// prepareFirstIndexGreeting silences the per-harness narration when the
// greeting is about to show the same numbers in its info column.
func prepareFirstIndexGreeting(dir string) {
	if !index.HasManifest(dir) && logoWanted(os.Stdout) {
		index.SuppressHarnessNarration = true
	}
}

func maybeFirstIndexGreeting(dir string) {
	index.SuppressHarnessNarration = false
	b := index.LastBuild
	if !b.Initial || b.Messages == 0 || !logoWanted(os.Stdout) {
		return
	}
	tryLine := logoDim + `try: deja "something you fixed weeks ago"` + logoReset
	if q := suggestFirstQuery(dir); q != "" {
		tryLine = "try it on your own history:  " + logoBold + `deja "` + q + `"` + logoReset
	}
	info := firstIndexInfo(b, tryLine)
	if warning := doctorParsedZeroWarning(); warning != "" {
		info = append(info, warning)
	}
	printLogo(os.Stdout, info)
}

// firstIndexInfo is the column of numbers beside the mark on the first build.
func firstIndexInfo(b index.BuildSummary, tryLine string) []string {
	info := brandInfo()
	info = append(info, "")
	nameW := 0
	for _, h := range b.PerHarness {
		if h.Messages > 0 && len(h.Name) > nameW {
			nameW = len(h.Name)
		}
	}
	for _, h := range b.PerHarness {
		if h.Messages == 0 {
			continue
		}
		info = append(info, fmt.Sprintf("%-*s  %s%6d%s messages · %d session%s",
			nameW, h.Name, logoBold, h.Messages, logoReset, h.Sessions, pluralS(h.Sessions)))
	}
	return append(info,
		"",
		fmt.Sprintf("indexed %s%d%s messages across %s%d%s agents", logoBold, b.Messages, logoReset, logoBold, b.Harnesses, logoReset),
		tryLine,
	)
}
