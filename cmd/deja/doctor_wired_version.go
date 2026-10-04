package main

import (
	"bufio"
	"context"
	"debug/buildinfo"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// The checks beside this one ask whether the binary a wiring runs is there,
// and whether it sits somewhere that gets cleaned. Neither asks how old it is.
// A machine with two installs — Homebrew and a `go install` in ~/.local/bin —
// runs whichever the launcher finds first, and on the machine this was written
// on that was a build a week behind the one `deja doctor` came from: every hook
// and the MCP server were missing the fixes since, and every row said `wired`.

// wiredDejaBinary is the deja binary a wiring file ends up running: the path
// it names, the launcher's pick when it names the launcher, the PATH's when it
// names a bare `deja`. "" when the file names none or the answer depends on
// something doctor cannot see.
func wiredDejaBinary(path string) string {
	cmd := dejaHookCommandIn(path)
	if cmd == "" {
		cmd = dejaCommandIn(path)
	}
	if cmd == "" {
		return ""
	}
	if launcher := dejaLauncherPath(); launcher != "" && samePathSpelling(cmd, launcher, false) {
		return launcherPick(launcher)
	}
	if !filepath.IsAbs(cmd) {
		base := strings.TrimSuffix(strings.ToLower(filepath.Base(cmd)), ".exe")
		if base != "deja" {
			return ""
		}
		p, err := exec.LookPath(cmd)
		if err != nil {
			return ""
		}
		cmd = p
	}
	if !executableFile(cmd) {
		return ""
	}
	return cmd
}

// launcherPick runs the launcher's own resolution without running it: DEJA_BIN,
// then the paths written into the script in its order, then the PATH. Read
// from the script on disk rather than from launcherCandidates, because the
// order that matters is the one the installed launcher has, which an older
// install wrote.
func launcherPick(launcher string) string {
	if p := os.Getenv("DEJA_BIN"); executableFile(p) {
		return p
	}
	f, err := os.Open(launcher)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	in := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "for c in"):
			in = true
			continue
		case in && strings.HasPrefix(line, "; do"):
			in = false
			continue
		}
		if !in {
			continue
		}
		c := strings.TrimSpace(strings.TrimSuffix(line, `\`))
		c = strings.ReplaceAll(strings.Trim(c, "'"), `'\''`, "'")
		if executableFile(c) {
			return c
		}
	}
	if p, err := exec.LookPath("deja"); err == nil && executableFile(p) {
		return p
	}
	return ""
}

// cellarVersion reads the version out of a Homebrew keg path, which names it.
var cellarVersion = regexp.MustCompile(`/Cellar/deja-vu/([^/]+)/`)

// versionLine is what `deja version` prints.
var versionLine = regexp.MustCompile(`^deja v?(\S+)`)

type binaryStamp struct {
	path  string
	size  int64
	mtime time.Time
}

var (
	binaryVersionMu    sync.Mutex
	binaryVersionCache = map[binaryStamp]string{}
)

// dejaBinaryVersion is the version a deja binary reports, "" when it cannot be
// told. Cheapest source first: a `go install` build carries its module
// version, a Homebrew keg's path names it. Only a binary that says it is
// deja-vu's is ever run, and once per file per run.
var dejaBinaryVersion = func(p string) string {
	fi, err := os.Stat(p)
	if err != nil {
		return ""
	}
	key := binaryStamp{p, fi.Size(), fi.ModTime()}
	binaryVersionMu.Lock()
	v, ok := binaryVersionCache[key]
	binaryVersionMu.Unlock()
	if ok {
		return v
	}
	v = readDejaBinaryVersion(p)
	binaryVersionMu.Lock()
	binaryVersionCache[key] = v
	binaryVersionMu.Unlock()
	return v
}

func readDejaBinaryVersion(p string) string {
	bi, err := buildinfo.ReadFile(p)
	if err != nil || bi.Main.Path != "github.com/vshulcz/deja-vu" {
		return ""
	}
	if _, ok := parseUpdateVersion(bi.Main.Version); ok {
		return normalizeUpdateVersion(bi.Main.Version)
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		if m := cellarVersion.FindStringSubmatch(filepath.ToSlash(real)); m != nil {
			if _, ok := parseUpdateVersion(m[1]); ok {
				return m[1]
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, p, "version").Output()
	if err != nil {
		return ""
	}
	if m := versionLine.FindStringSubmatch(strings.TrimSpace(string(out))); m != nil {
		return m[1]
	}
	return ""
}

// wiredFile is one config that can run deja, under the name doctor gives it.
type wiredFile struct{ name, path string }

// doctorWiredFiles is every file doctor reports as wiring: the MCP entries,
// Claude Code's and Codex's hooks, and the auto-recall rows that run the
// binary (aider's digest and roo's guidance run nothing).
func doctorWiredFiles() []wiredFile {
	var out []wiredFile
	for _, c := range doctorMCPConfigs() {
		out = append(out, wiredFile{c.name, c.path})
	}
	out = append(out, wiredFile{"claude-code", claudeHookWiringState().path})
	out = append(out, wiredFile{"codex-hook", codexHookWiringState().path})
	for _, a := range autoWirings() {
		if a.marker != "" {
			out = append(out, wiredFile{a.name, a.path()})
		}
	}
	return out
}

// olderBinary is one deja binary the wiring runs that is behind this one, and
// the harnesses that run it.
type olderBinary struct {
	path, version string
	names         []string
}

// olderWiredBinaries groups the wiring by the binary each file ends up running
// and keeps the ones older than the deja running doctor. Silent for a source
// build on either side: "dev" has no order.
func olderWiredBinaries(files []wiredFile) []olderBinary {
	running := normalizeUpdateVersion(version)
	if _, ok := parseUpdateVersion(running); !ok {
		return nil
	}
	var out []olderBinary
	at := map[string]int{}
	for _, f := range files {
		if f.path == "" {
			continue
		}
		bin := wiredDejaBinary(f.path)
		if bin == "" {
			continue
		}
		if i, ok := at[bin]; ok {
			if !slices.Contains(out[i].names, f.name) {
				out[i].names = append(out[i].names, f.name)
			}
			continue
		}
		wired := dejaBinaryVersion(bin)
		if wired == "" {
			continue
		}
		if order, ok := compareUpdateVersions(wired, running); !ok || order >= 0 {
			continue
		}
		at[bin] = len(out)
		out = append(out, olderBinary{bin, wired, []string{f.name}})
	}
	return out
}

// doctorOlderBinaries prints one row per older binary rather than one under
// every row that runs it: on the machine this was written on that was the same
// line 37 times.
func doctorOlderBinaries(w io.Writer) {
	running := normalizeUpdateVersion(version)
	for _, o := range olderWiredBinaries(doctorWiredFiles()) {
		names := o.names
		more := ""
		if len(names) > 6 {
			more = fmt.Sprintf(" and %d more", len(names)-6)
			names = names[:6]
		}
		fix := "`" + shellQuoteIfNeeded(o.path) + " update`"
		if _, command := packageManagerOwning(o.path); command != "" {
			fix = "`" + command + "`"
		}
		fmt.Fprintf(w, "  %-12s %-11s %s is v%s, older than this deja (v%s), and runs for %s — %s brings it up\n",
			"wiring", "older", reportPath(o.path), o.version, running, strings.Join(names, ", ")+more, fix)
	}
}
