package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// A Claude Code fork made with --resume <id> --fork-session writes the turns it
// copies only after its first tool result, and SessionStart says
// "source":"fork" without naming the source. Until then the fork's transcript
// is missing or holds no forkedFrom, and its first prompt's recall and first
// tool's note led with the source (#5128). The claude process's argv names
// the source, so SessionStart reads it there once and keeps it for the prompt
// and tool hooks. A TUI /branch writes the copy, forkedFrom on every record,
// before any hook runs, and needs none of this.

// ancestorArgs is the argv of this process's parent and the ones above it,
// nearest first, at most depth of them. Empty where it cannot be read.
var ancestorArgs = processAncestorArgs

// claudeForkAncestors bounds the walk: claude, or the shell it runs the hook
// command in, then claude.
const claudeForkAncestors = 4

// forkMarkTTL is how long a mark is kept: past the fork's first tool result
// the transcript names its source, and past a build the index does.
const forkMarkTTL = 48 * time.Hour

var claudeSessionUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// claudeArgvFlags are claude's own flags, read off its argv; deja takes none
// of them.
var claudeArgvFlags = map[string]string{
	"--fork-session": "fork",
	"-c":             "continue",
	"--continue":     "continue",
	"-r":             "resume",
	"--resume":       "resume",
}

func forkMarksPath(dir string) string { return dir + ".forks" }

// rememberClaudeForkSource keeps the source of the fork starting now, when its
// transcript does not name one yet and the claude process's argv does.
func rememberClaudeForkSource(dir, sid, transcript string) {
	sid = strings.TrimSpace(sid)
	if sid == "" || !hookseenField(sid) {
		return
	}
	if transcript != "" {
		if h, err := sources.TranscriptHead(transcript, sid); err == nil && h.Parent != "" {
			return
		}
	}
	src := claudeForkSourceIn(ancestorArgs(claudeForkAncestors), sid, transcript)
	if src == "" {
		return
	}
	line := sid + " " + src + " " + strconv.FormatInt(time.Now().Unix(), 10) + "\n"
	p := forkMarksPath(dir)
	// Appended, so two forks starting together both keep theirs; pruned once
	// the file holds more than a few days of them.
	if fi, err := os.Stat(p); err == nil && fi.Size() > forkMarksPruneAt {
		pruneForkMarks(p)
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	_, _ = f.WriteString(line)
	_ = f.Close()
}

// forkMarksPruneAt is the size past which the next mark drops expired ones.
const forkMarksPruneAt = 8 << 10

func pruneForkMarks(p string) {
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	now := time.Now()
	var keep []string
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			continue
		}
		if at, err := strconv.ParseInt(f[2], 10, 64); err != nil || now.Sub(time.Unix(at, 0)) > forkMarkTTL {
			continue
		}
		keep = append(keep, line+"\n")
	}
	_ = atomicfile.Write(p, []byte(strings.Join(keep, "")), 0o600)
}

// claudeForkMark is the source SessionStart kept for sid, or "". A missing
// file, the usual state, costs one failed open.
func claudeForkMark(dir, sid string) string {
	b, err := os.ReadFile(forkMarksPath(dir))
	if err != nil {
		return ""
	}
	src := ""
	for _, line := range strings.Split(string(b), "\n") {
		if f := strings.Fields(line); len(f) == 3 && f[0] == sid {
			src = f[1]
		}
	}
	return src
}

// claudeForkSourceIn reads the source off the nearest claude argv that forks:
// the id after -r/--resume, or with -c/--continue the session claude continues,
// the newest other transcript in the fork's project folder. A resume by name or
// through the picker names no id, and nothing is guessed.
func claudeForkSourceIn(ancestors [][]string, sid, transcript string) string {
	for _, args := range ancestors {
		if len(args) == 0 {
			continue
		}
		fork, cont, id := false, false, ""
		for i, a := range args[1:] {
			switch claudeArgvFlags[a] {
			case "fork":
				fork = true
			case "continue":
				cont = true
			case "resume":
				if i+2 < len(args) && id == "" {
					id = args[i+2]
				}
			default:
				if v, ok := strings.CutPrefix(a, "--resume="); ok && id == "" {
					id = v
				}
			}
		}
		if !fork {
			if isClaudeArgv(args) {
				// The claude this hook runs under does not fork; one further up
				// is some other session's.
				return ""
			}
			continue
		}
		if claudeSessionUUID.MatchString(id) && id != sid {
			return id
		}
		if cont && id == "" {
			return newestOtherTranscript(transcript, sid)
		}
		return ""
	}
	return ""
}

// isClaudeArgv reports a claude process: the native binary under its own name
// or a version path, or node/bun running the npm package.
func isClaudeArgv(args []string) bool {
	base := strings.ToLower(filepath.Base(args[0]))
	if base == "claude" || base == "claude.exe" || strings.Contains(filepath.ToSlash(args[0]), "/claude/versions/") {
		return true
	}
	return len(args) > 1 && strings.Contains(filepath.ToSlash(args[1]), "claude-code")
}

// newestOtherTranscript is the session --continue picks: the most recently
// written session file in the fork's project folder other than the fork's own.
func newestOtherTranscript(transcript, sid string) string {
	if transcript == "" {
		return ""
	}
	entries, err := os.ReadDir(filepath.Dir(transcript))
	if err != nil {
		return ""
	}
	best, bestAt := "", time.Time{}
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".jsonl")
		if !ok || e.IsDir() || id == sid || !claudeSessionUUID.MatchString(id) {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if fi.ModTime().After(bestAt) {
			best, bestAt = id, fi.ModTime()
		}
	}
	return best
}
