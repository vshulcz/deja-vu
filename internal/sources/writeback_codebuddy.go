package sources

import (
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// CodeBuddy Code: <config>/projects/<folder>/<id>.jsonl, one Responses-style
// message record per turn, each naming the session and the directory it ran
// in. `codebuddy -r <id>` looks the id up only under the folder of the
// directory it is run from, so the file needs that directory: the folder's
// other transcripts record it, or the folder name decodes to one still on
// disk. WorkBuddy's store is left out: deja knows no command that reopens it.
func init() {
	registerWriteBack("codebuddy", codeBuddyWriteBackRoots, renderCodeBuddy)
}

func codeBuddyWriteBackRoots() []string { return []string{CodeBuddyRoot()} }

func renderCodeBuddy(s model.Session, turns []model.Message) (string, []byte, error) {
	if IsWorkBuddyTranscript(s.Path) {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "session is WorkBuddy's, and deja knows no command that reopens one"}
	}
	if s.Kind == "subagent" || !strings.HasSuffix(s.Path, ".jsonl") || strings.TrimSuffix(filepath.Base(s.Path), ".jsonl") != s.ID {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "the index has no <id>.jsonl path for this session, which is where `codebuddy -r` looks"}
	}
	cwd := codeBuddyFolderCWD(filepath.Dir(s.Path))
	if cwd == "" {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "the index does not keep the directory the session ran in, and its folder name no longer decodes to one on this machine; `codebuddy -r` finds a session only from there"}
	}
	var lines []any
	parent := ""
	for i, m := range turns {
		id := writeBackUUID(s.ID, i)
		kind := "input_text"
		if m.Role == "assistant" {
			kind = "output_text"
		}
		line := map[string]any{
			"id":        id,
			"timestamp": m.Time.UnixMilli(),
			"type":      "message",
			"role":      m.Role,
			"content":   []any{map[string]any{"type": kind, "text": m.Text}},
			"sessionId": s.ID,
			"cwd":       cwd,
		}
		if parent != "" {
			line["parentId"] = parent
		}
		if m.Role == "assistant" {
			line["status"] = "completed"
		}
		lines = append(lines, line)
		parent = id
	}
	data, err := jsonLines(lines)
	return s.Path, data, err
}

// codeBuddyFolderCWD is the directory a CodeBuddy project folder was named
// for: Claude Code's encoding without the leading dash. A sibling transcript
// that records it answers first; otherwise the name is decoded against the
// disk.
func codeBuddyFolderCWD(dir string) string {
	base := filepath.Base(dir)
	fits := func(cwd string) bool { return strings.TrimPrefix(claudeEncodePath(cwd), "-") == base }
	matches, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	for i, f := range matches {
		if i >= claudeFolderScanFiles {
			break
		}
		if cwd := transcriptCWD(f, fits); cwd != "" {
			return cwd
		}
	}
	for _, enc := range []string{"-" + base, base} {
		if cwd := ResolveEncodedPath(enc); cwd != "" && fits(cwd) {
			return cwd
		}
	}
	return ""
}
