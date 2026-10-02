package sources

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Claude Code names a project folder by writing every character of the
// working directory outside [A-Za-z0-9] as "-". That cannot be read back for a
// directory named in Cyrillic, CJK, with accents or spaces: "проект é" is nine
// dashes, and the walk over the folder name lands on the parent (#4175). Every
// transcript record carries the real directory as cwd, so it is read from
// there, and trusted only when it encodes to the folder it was found in.

// claudeEncodePath is the folder name Claude Code gives a working directory:
// `replace(/[^a-zA-Z0-9]/g, "-")`, which walks UTF-16 code units, so a
// character outside the BMP — an emoji — is two dashes, not one.
func claudeEncodePath(p string) string {
	var b strings.Builder
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9':
			b.WriteRune(r)
		case r > 0xFFFF:
			b.WriteString("--")
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// claudeFolderNameMax is where Claude Code cuts a folder name, adding "-" and
// a hash of the path after it.
const claudeFolderNameMax = 200

// claudeFolderIs reports whether base is the folder Claude Code names for cwd.
func claudeFolderIs(cwd, base string) bool {
	enc := claudeEncodePath(cwd)
	if len(enc) > claudeFolderNameMax {
		return strings.HasPrefix(base, enc[:claudeFolderNameMax]+"-")
	}
	return enc == base
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
	return transcriptCWD(path, func(cwd string) bool { return claudeFolderIs(cwd, base) })
}

// transcriptCWD is the first cwd among the head records of a JSONL transcript
// that fits, the check being whether its folder was named for it.
func transcriptCWD(path string, fits func(cwd string) bool) string {
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
			if json.Unmarshal(line, &v) == nil && v.CWD != "" && fits(v.CWD) {
				return v.CWD
			}
		}
		if err != nil {
			return ""
		}
	}
	return ""
}

// claudeProjectNameFor is the project of the transcript at path, which is the
// project of its folder: the directory recorded in the folder's transcripts
// where one is the directory the folder was named for, the folder name decoded
// otherwise.
func claudeProjectNameFor(path string) string {
	dir := claudeProjectDir(path)
	// Its own cache, keyed by the folder: the shared one is keyed by an
	// encoded name other harnesses' folders can share, and an entry decoded
	// for one of them would answer for this one.
	if v, ok := claudeCWDNameCache.Load(dir); ok {
		return v.(string)
	}
	name := cwdProjectName(claudeFolderCWD(dir))
	if name == "" {
		// Not cached: a new transcript's first line can be a snapshot with no
		// cwd, and the decoded name kept for it would outlive the cwd landing
		// in a long-lived process such as deja mcp (#4225).
		return claudeProjectName(dir)
	}
	claudeCWDNameCache.Store(dir, name)
	return name
}

var claudeCWDNameCache sync.Map // project folder -> display name

// cwdProjectName is the project named by a recorded working directory: its
// last two segments, as a decoded folder name would give them. A file:// URI
// is read as the path it names, escapes decoded (#4461), and a drive is not a
// parent: C:\proj is "proj".
func cwdProjectName(cwd string) string {
	if p, ok := fileURIPath(cwd); ok {
		cwd = p
	}
	segs := strings.FieldsFunc(cwd, func(r rune) bool { return r == '/' || r == '\\' })
	switch {
	case len(segs) >= 2 && !isDriveSegment(segs[len(segs)-2]):
		return projectSegments(segs[len(segs)-2], segs[len(segs)-1])
	case len(segs) >= 1:
		return segs[len(segs)-1]
	}
	return ""
}

func isDriveSegment(s string) bool {
	return len(s) == 2 && s[1] == ':'
}

// fileURIPath is the path a file:// URI names, percent-escapes decoded. A UNC
// share keeps its host, //server/share/proj, which on windows is
// \\server\share\proj (#4462). ok is false for anything that is not a file URI.
func fileURIPath(uri string) (string, bool) {
	if !strings.HasPrefix(strings.ToLower(uri), "file:") {
		return "", false
	}
	u, err := url.Parse(uri)
	if err != nil || !strings.EqualFold(u.Scheme, "file") || u.Path == "" {
		return "", false
	}
	if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
		return "//" + u.Host + u.Path, true
	}
	return u.Path, true
}

// claudeFolderScanFiles bounds how many transcripts are opened to name one
// folder.
const claudeFolderScanFiles = 32

// claudeFolderCWD is the directory a project folder was named for, read from
// the first of its transcripts, in name order, that records it. A folder's
// name is the folder's, not the file's: a long session whose head was written
// from somewhere else and the subagents beside it would otherwise be filed
// under two projects, and which one won would depend on which file a run read
// first.
func claudeFolderCWD(dir string) string {
	base := filepath.Base(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	for _, e := range entries {
		if e.IsDir() {
			subs, _ := filepath.Glob(filepath.Join(dir, e.Name(), "subagents", "*.jsonl"))
			sort.Strings(subs)
			files = append(files, subs...)
		}
		if len(files) >= claudeFolderScanFiles {
			break
		}
	}
	for i, f := range files {
		if i >= claudeFolderScanFiles {
			break
		}
		if cwd := claudeTranscriptCWD(f, base); cwd != "" {
			return cwd
		}
	}
	return ""
}

// ClaudeSessionDir is the directory a Claude Code session ran in, for the cd
// in front of `claude --resume`: the recorded one while it still exists, the
// folder name resolved on disk otherwise.
func ClaudeSessionDir(path string) string {
	base := ClaudeProjectDirBase(path)
	if base == "" {
		return ""
	}
	for _, cwd := range []string{claudeTranscriptCWD(path, base), claudeFolderCWD(claudeProjectDir(path))} {
		if cwd == "" {
			continue
		}
		if fi, err := os.Stat(cwd); err == nil && fi.IsDir() {
			return cwd
		}
	}
	return ResolveEncodedPath(base)
}
