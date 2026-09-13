package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// A hook entry names the binary the install ran from. A package upgrade moves
// that binary — Homebrew keeps each version in its own Cellar directory — and
// every entry written before the launcher (#3426) then points at a path that no
// longer exists. The hooks exit 127, so session-start recall, the per-prompt
// block, the line before a tool runs and the compaction capture all stop, and
// nothing says why: the surface that would report it is a hook.
//
// `deja doctor` names it, and nobody runs doctor until they already suspect
// something. This is the line on the screen someone does look at — the one a
// bare `deja` prints — reported from the binary they just ran rather than from
// the hooks that cannot run (#3502).
//
// Once a day, because the answer changes when an install does and the check is
// a read of every wiring file. The stamp lives beside the index like the week
// note's.
const deadHookNoticeEvery = 24 * time.Hour

// deadHookNotice is that line without its label, or "". `now` is a parameter
// so a test can move the clock instead of sleeping a day.
func deadHookNotice(dir string) string {
	return deadHookNoticeAt(dir, time.Now())
}

func deadHookNoticeAt(dir string, now time.Time) string {
	stamp := dir + ".deadhooks"
	if b, err := os.ReadFile(stamp); err == nil {
		if ts, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil &&
			now.Sub(time.Unix(ts, 0)) < deadHookNoticeEvery {
			return ""
		}
	}
	_ = os.WriteFile(stamp, []byte(strconv.FormatInt(now.Unix(), 10)), 0o600)

	gone := map[string]bool{}
	for _, path := range hookWiringFiles() {
		if missing := dejaHookCommandMissing(path); missing != "" {
			gone[missing] = true
		}
	}
	if len(gone) == 0 {
		return ""
	}
	// The path, not the count of files: one binary is gone and several entries
	// name it, and the reader recognises the path as the version they upgraded
	// from. More than one is possible on a machine that installed twice, and
	// then the count is what there is to say.
	if len(gone) == 1 {
		for p := range gone {
			return fmt.Sprintf("run %s, which is not there — `deja install --auto` rewrites them for this binary", reportPath(p))
		}
	}
	return fmt.Sprintf("run %d deja binaries that are not there — `deja install --auto` rewrites them for this binary", len(gone))
}

// hookWiringFiles is every file deja writes a hook into, which is where a dead
// binary can be named. The MCP entries are deliberately not here: doctor covers
// them, and a stale MCP server fails loudly in the agent that tried to start it
// rather than silently like a hook.
func hookWiringFiles() []string {
	files := []string{
		filepath.Join(sources.ClaudeConfigDir(), "settings.json"),
		filepath.Join(sources.CodexHome(), "hooks.json"),
	}
	for _, a := range autoWirings() {
		if a.marker == "" {
			// aider's file is a digest and roo's is guidance: neither runs
			// anything, and both can quote a path for reasons of their own.
			continue
		}
		files = append(files, a.path())
	}
	return files
}
