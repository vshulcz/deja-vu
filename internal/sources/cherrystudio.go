package sources

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Cherry Studio (github.com/CherryHQ/cherry-studio) runs Claude Code sessions
// from a desktop app and writes them as ordinary Claude Code transcripts under
// its own app data:
//
//	<app data>/CherryStudio/Data/Agents/.claude/<projects>/<workspace>/<session>.jsonl
//
// and, before the upgrade that added `Data/Agents`, directly under
// `<app data>/CherryStudio/.claude/<projects>/…`. Both are read so a store that
// predates it is not lost.
//
// One difference from a stock store, and it matters for text rather than for
// tokens: Cherry Studio appends the same API call three or four times as the
// stream progresses — a new uuid each time, the same requestId and message id,
// the text growing. Read plainly that makes one reply into three messages, each
// a prefix of the next, so a recall can quote half a sentence and `deja show`
// prints the answer twice before finishing it. The reader collapses a run by
// its request id and keeps the longest (#3644).
//
// Sessions are their own harness rather than extra Claude roots: a reader
// should see which app the work happened in, and `deja sources` should say
// cherrystudio when Cherry Studio is what is on the machine.

// CherryStudioRoots are the transcript roots of a Cherry Studio install.
// DEJA_CHERRYSTUDIO_ROOTS replaces the list.
func CherryStudioRoots() []string {
	if list := os.Getenv("DEJA_CHERRYSTUDIO_ROOTS"); list != "" {
		var out []string
		for _, p := range filepath.SplitList(list) {
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	projects := claudeProjectsDirName()
	var out []string
	for _, base := range cherryStudioAppDirs() {
		out = append(out,
			filepath.Join(base, "Data", "Agents", ".claude", projects),
			filepath.Join(base, ".claude", projects),
		)
	}
	var live []string
	for _, p := range out {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			live = append(live, p)
		}
	}
	return live
}

// cherryStudioAppDirs is where the app keeps its data on this platform.
func cherryStudioAppDirs() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{filepath.Join(Home(), "Library", "Application Support", "CherryStudio")}
	case "windows":
		app := os.Getenv("APPDATA")
		if app == "" {
			app = filepath.Join(Home(), "AppData", "Roaming")
		}
		return []string{filepath.Join(app, "CherryStudio")}
	default:
		cfg := os.Getenv("XDG_CONFIG_HOME")
		if cfg == "" {
			cfg = filepath.Join(Home(), ".config")
		}
		return []string{filepath.Join(cfg, "CherryStudio")}
	}
}

// CherryStudioSessionFiles lists the transcripts on disk.
func CherryStudioSessionFiles() []string {
	var out []string
	for _, root := range CherryStudioRoots() {
		out = append(out, walkFiles(root, func(p string) bool {
			return strings.HasSuffix(p, ".jsonl")
		})...)
	}
	return out
}

// ParseCherryStudioFile reads one transcript: Claude Code's format, with the
// snapshot run collapsed.
func ParseCherryStudioFile(path string) ([]model.Session, error) {
	return ParseCherryStudioFileFromOffset(path, 0)
}

// ParseCherryStudioFileFromOffset is the incremental read. A collapse spanning
// the watermark cannot see the earlier snapshots, so the longest text in the
// tail wins and the ingest de-duplicator drops what repeats — the same
// behaviour the appended-transcript path already relies on.
func ParseCherryStudioFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parseClaudeTypedWithOptions(path, func(fn func([]byte)) error {
		return scanJSONLBytes(path, offset, fn)
	}, claudeParseOptions{Harness: "cherrystudio", CollapseSnapshots: true})
}

// CherryStudioUnderRoot reports whether a path belongs to this store, so the
// registry can claim it without stealing a stock Claude transcript.
func CherryStudioUnderRoot(p string) bool {
	for _, root := range CherryStudioRoots() {
		if strings.HasPrefix(p, root) {
			return true
		}
	}
	return false
}

func LoadCherryStudio() []model.Session {
	return parseFiles(CherryStudioSessionFiles(), ParseCherryStudioFile)
}
