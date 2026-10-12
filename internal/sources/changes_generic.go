package sources

import (
	"encoding/json"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// genericChangesMaxBytes bounds the transcript the generic reader walks: it
// decodes every JSON value in it, and the nested JSON strings inside those.
const genericChangesMaxBytes = 64 << 20

// Field names the agents put on an edit call. Most coding agents took their
// edit tool's shape from one another — a path, the text replaced and the text
// it became — and differ only in spelling (Kimi and Cursor write old_string,
// Copilot CLI old_str, opencode oldString).
var (
	editPathKeys    = []string{"file_path", "filePath", "path", "absolute_path", "target_file", "filename"}
	editOldKeys     = []string{"old_string", "oldString", "old_str", "old_text", "oldText"}
	editNewKeys     = []string{"new_string", "newString", "new_str", "new_text", "newText"}
	editContentKeys = []string{"content", "file_text", "contents"}
	editCwdKeys     = []string{"cwd", "workdir", "working_directory", "workingDirectory", "directory", "projectRoot", "project_root", "workspace_root"}
	toolNameKeys    = []string{"name", "toolName", "tool_name", "tool", "function"}
)

// writeToolName is a tool that writes a whole file. A path with content is
// also what a read returns, so a whole-file write is only taken from a call
// named as one.
var writeToolName = regexp.MustCompile(`(?i)write|create|save|new_?file`).MatchString

// genericChanges reads the edits out of a JSON or JSONL transcript whose
// harness has no reader of its own here: every object shaped like an edit
// call, every apply_patch text, in the order the file holds them. Only for a
// file that holds this one session — read back from a store that keeps many
// sessions in one file, one session would show another's code — so the file
// is parsed by its own harness first and must come back as one session.
func genericChanges(s model.Session) []Change {
	fi, err := os.Stat(s.Path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > genericChangesMaxBytes {
		return nil
	}
	k, ok := KindForPathKind(s.Path)
	if !ok || k.Parse == nil {
		return nil
	}
	if ss, err := k.Parse(s.Path, 0); err != nil || len(ss) != 1 {
		return nil
	}
	f, err := os.Open(s.Path)
	if err != nil {
		return nil
	}
	defer f.Close()
	w := &editWalker{seen: map[Change]bool{}}
	dec := json.NewDecoder(f)
	for {
		var v any
		if err := dec.Decode(&v); err != nil {
			if err == io.EOF {
				break
			}
			// A line that is not JSON ends the walk where it stands; what
			// was read before it still counts.
			break
		}
		w.walk(v, "", 0)
	}
	cwd := w.cwd
	if cwd == "" {
		cwd = commonDir(w.out)
	}
	for i := range w.out {
		w.out[i].Path = relTo(cwd, w.out[i].Path)
	}
	return w.out
}

type editWalker struct {
	out  []Change
	seen map[Change]bool
	cwd  string
}

func (w *editWalker) add(c Change) {
	if c.Path == "" || strings.ContainsAny(c.Path, "\n\r") || w.seen[c] {
		return
	}
	w.seen[c] = true
	w.out = append(w.out, c)
}

func (w *editWalker) walk(v any, tool string, depth int) {
	if depth > 16 {
		return
	}
	switch t := v.(type) {
	case string:
		if s := strings.TrimSpace(t); len(s) < 4<<20 && (strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[")) {
			var inner any
			if json.Unmarshal([]byte(s), &inner) == nil {
				w.walk(inner, tool, depth+1)
				return
			}
		}
		for _, patch := range patchTexts(t, 0) {
			for _, c := range parseApplyPatch(patch) {
				w.add(c)
			}
		}
	case []any:
		for _, x := range t {
			w.walk(x, tool, depth+1)
		}
	case map[string]any:
		if name := firstStringField(t, toolNameKeys); name != "" {
			tool = name
		}
		if w.cwd == "" {
			if c := firstStringField(t, editCwdKeys); absPath(strings.ReplaceAll(c, `\`, "/")) {
				w.cwd = c
			}
		}
		if p := firstStringField(t, editPathKeys); p != "" {
			old, oldOK := firstStringFieldOK(t, editOldKeys)
			nw, newOK := firstStringFieldOK(t, editNewKeys)
			switch {
			case oldOK && newOK:
				w.add(Change{Path: p, Old: old, New: nw})
			case writeToolName(tool):
				if body, ok := firstStringFieldOK(t, editContentKeys); ok {
					w.add(Change{Path: p, New: body, Created: true})
				}
			}
			if edits, ok := t["edits"].([]any); ok {
				for _, e := range edits {
					if m, ok := e.(map[string]any); ok {
						old, oldOK := firstStringFieldOK(m, editOldKeys)
						nw, newOK := firstStringFieldOK(m, editNewKeys)
						if oldOK && newOK {
							w.add(Change{Path: p, Old: old, New: nw})
						}
					}
				}
			}
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			w.walk(t[k], tool, depth+1)
		}
	}
}

func firstStringField(m map[string]any, keys []string) string {
	s, _ := firstStringFieldOK(m, keys)
	return s
}

func firstStringFieldOK(m map[string]any, keys []string) (string, bool) {
	for _, k := range keys {
		if s, ok := m[k].(string); ok {
			return s, true
		}
	}
	return "", false
}

// commonDir is the directory every absolute edit path sits under, for a
// transcript that never names the directory it ran in.
func commonDir(cs []Change) string {
	dir := ""
	for _, c := range cs {
		p := strings.ReplaceAll(c.Path, `\`, "/")
		if !absPath(p) {
			continue
		}
		d := path.Dir(p)
		if dir == "" {
			dir = d
			continue
		}
		for dir != "/" && dir != "." && d != dir && !strings.HasPrefix(d, dir+"/") {
			dir = path.Dir(dir)
		}
	}
	if dir == "/" || dir == "." {
		return ""
	}
	return dir
}
