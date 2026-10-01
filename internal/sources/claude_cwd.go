package sources

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Claude Code names a project folder by writing every character of the
// working directory outside [A-Za-z0-9] as "-". That cannot be read back for a
// directory named in Cyrillic, CJK, with accents or spaces: "проект é" is nine
// dashes, and the walk over the folder name lands on the parent (#4175). Every
// transcript record carries the real directory as cwd, so it is read from
// there, and trusted only when it encodes to the folder it was found in.

// claudeEncodePath is the folder name Claude Code gives a working directory.
func claudeEncodePath(p string) string {
	b := []rune(p)
	for i, r := range b {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			b[i] = '-'
		}
	}
	return string(b)
}

// claudeCWDScanLines bounds the read: the first records of a transcript carry
// cwd, and a project name is not worth reading a long session for.
const claudeCWDScanLines = 64

// claudeTranscriptCWD is the working directory a transcript records, when it
// is the one the project folder base was named for; "" otherwise.
func claudeTranscriptCWD(path, base string) string {
	if base == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for i := 0; i < claudeCWDScanLines; i++ {
		line, err := r.ReadBytes('\n')
		if bytes.Contains(line, []byte(`"cwd"`)) {
			var v struct {
				CWD string `json:"cwd"`
			}
			if json.Unmarshal(line, &v) == nil && v.CWD != "" && claudeEncodePath(v.CWD) == base {
				return v.CWD
			}
		}
		if err != nil {
			return ""
		}
	}
	return ""
}

// claudeProjectNameFor is the project of the transcript at path: its recorded
// directory where that is the one the folder was named for, the folder name
// decoded otherwise.
func claudeProjectNameFor(path string) string {
	dir := claudeProjectDir(path)
	base := filepath.Base(dir)
	// Its own cache, keyed by the folder: the shared one is keyed by an
	// encoded name other harnesses' folders can share, and an entry decoded
	// for one of them would answer for this one.
	if v, ok := claudeCWDNameCache.Load(dir); ok {
		return v.(string)
	}
	if cwd := claudeTranscriptCWD(path, base); cwd != "" {
		segs := strings.FieldsFunc(cwd, func(r rune) bool { return r == '/' || r == '\\' })
		name := ""
		switch {
		case len(segs) >= 2:
			name = projectSegments(segs[len(segs)-2], segs[len(segs)-1])
		case len(segs) == 1:
			name = segs[0]
		}
		if name != "" {
			claudeCWDNameCache.Store(dir, name)
			return name
		}
	}
	name := claudeProjectName(dir)
	claudeCWDNameCache.Store(dir, name)
	return name
}

var claudeCWDNameCache sync.Map // project folder -> display name

// ClaudeSessionDir is the directory a Claude Code session ran in, for the cd
// in front of `claude --resume`: the recorded one while it still exists, the
// folder name resolved on disk otherwise.
func ClaudeSessionDir(path string) string {
	base := ClaudeProjectDirBase(path)
	if base == "" {
		return ""
	}
	if cwd := claudeTranscriptCWD(path, base); cwd != "" {
		if fi, err := os.Stat(cwd); err == nil && fi.IsDir() {
			return cwd
		}
	}
	return ResolveEncodedPath(base)
}
