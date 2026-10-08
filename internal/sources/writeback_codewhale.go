package sources

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// CodeWhale: sessions/<id>.json, one JSON document holding the metadata and
// the messages as Anthropic-style blocks. `codewhale resume <id>` reads it
// from any directory. The index does not keep the workspace the session ran
// in, so it is left empty and CodeWhale works in the directory it is resumed
// from.
func init() {
	registerWriteBack("codewhale", CodeWhaleRoots, renderCodeWhale)
}

func renderCodeWhale(s model.Session, turns []model.Message) (string, []byte, error) {
	base := filepath.Base(s.Path)
	if filepath.Ext(base) != ".json" || codeWhaleSidecars[base] || strings.TrimSuffix(base, ".json") != s.ID {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "the index has no sessions/<id>.json path for this session, which is where CodeWhale looks"}
	}
	messages := make([]any, 0, len(turns))
	for _, m := range turns {
		messages = append(messages, map[string]any{
			"role":    m.Role,
			"content": []any{map[string]any{"type": "text", "text": m.Text}},
		})
	}
	title := s.Title
	if title == "" {
		title = turns[0].Text
	}
	doc := map[string]any{
		"schema_version": 1,
		"metadata": map[string]any{
			"id":            s.ID,
			"title":         title,
			"created_at":    writeBackStamp(turns[0].Time),
			"updated_at":    writeBackStamp(turns[len(turns)-1].Time),
			"message_count": len(turns),
			"total_tokens":  0,
			"model":         "",
			"workspace":     "",
		},
		"messages": messages,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return "", nil, err
	}
	return s.Path, buf.Bytes(), nil
}
