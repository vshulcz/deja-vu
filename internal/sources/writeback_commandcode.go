package sources

import (
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Command Code: projects/<slug>/<id>.jsonl, a v3 `session` header with the id
// and cwd, then one `message` envelope per turn chained by parentId. `cmd
// --session <id>` searches every project for it. The cwd is the project
// folder's slug resolved on this machine, and left out when it does not
// resolve.
func init() {
	registerWriteBack("commandcode", func() []string { return []string{CommandCodeRoot()} }, renderCommandCode)
}

func renderCommandCode(s model.Session, turns []model.Message) (string, []byte, error) {
	if !commandCodeIsTranscript(s.Path) || strings.TrimSuffix(filepath.Base(s.Path), ".jsonl") != s.ID {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "the index has no <id>.jsonl path for this session, which is where Command Code looks"}
	}
	header := map[string]any{
		"type":      "session",
		"version":   3,
		"id":        s.ID,
		"timestamp": writeBackStamp(turns[0].Time),
	}
	if cwd := commandCodeSlugDir(filepath.Base(filepath.Dir(s.Path))); cwd != "" {
		header["cwd"] = cwd
	}
	lines := []any{header}
	parent := any(nil)
	for i, m := range turns {
		id := strings.ReplaceAll(writeBackUUID(s.ID, i), "-", "")[:8]
		source := "user"
		if m.Role == "assistant" {
			source = "model"
		}
		lines = append(lines, map[string]any{
			"type":      "message",
			"id":        id,
			"parentId":  parent,
			"timestamp": writeBackStamp(m.Time),
			"message": map[string]any{
				"role":    m.Role,
				"content": []any{map[string]any{"type": "text", "text": m.Text}},
				"meta": map[string]any{
					"source":    source,
					"createdAt": m.Time.UnixMilli(),
					"messageId": writeBackUUID(s.ID+":message", i),
				},
			},
		})
		parent = id
	}
	data, err := jsonLines(lines)
	return s.Path, data, err
}

// commandCodeSlugDir resolves a project folder's slug to a directory that
// exists, or "". The slug is the cwd with its separators turned to dashes and
// no leading one.
func commandCodeSlugDir(slug string) string {
	if slug == "" {
		return ""
	}
	if dir := ResolveEncodedPath(slug); dir != "" {
		return dir
	}
	return ResolveEncodedPath("-" + slug)
}
