package sources

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Cursor keeps IDE chats in per-user SQLite key-value stores (state.vscdb,
// table cursorDiskKV: composerData:<id> session objects + bubbleId:<id>:<b>
// message objects) and CLI agent transcripts as Anthropic-shaped JSONL under
// ~/.cursor/projects/<encoded-path>/agent-transcripts/.

func CursorUserRoot() string {
	if v := os.Getenv("DEJA_CURSOR_ROOT"); v != "" {
		return v
	}
	h := Home()
	if runtime.GOOS == "darwin" {
		return filepath.Join(h, "Library", "Application Support", "Cursor", "User")
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		if _, err := os.Stat(filepath.Join(xdg, "Cursor", "User")); err == nil {
			return filepath.Join(xdg, "Cursor", "User")
		}
	}
	return filepath.Join(h, ".config", "Cursor", "User")
}

func CursorCLIRoot() string {
	return EnvPath("DEJA_CURSOR_CLI_ROOT", filepath.Join(Home(), ".cursor"))
}

// CursorDBs lists state.vscdb stores; modern chats live in globalStorage.
func CursorDBs() []string {
	root := CursorUserRoot()
	var out []string
	if p := filepath.Join(root, "globalStorage", "state.vscdb"); fileExists(p) {
		out = append(out, p)
	}
	entries, err := os.ReadDir(filepath.Join(root, "workspaceStorage"))
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if p := filepath.Join(root, "workspaceStorage", e.Name(), "state.vscdb"); fileExists(p) {
				out = append(out, p)
			}
		}
	}
	return out
}

func CursorTranscripts() []string {
	root := filepath.Join(CursorCLIRoot(), "projects")
	var out []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		if strings.Contains(p, string(filepath.Separator)+"subagents"+string(filepath.Separator)) && os.Getenv("DEJA_INCLUDE_SUBAGENTS") != "1" {
			return nil
		}
		if strings.Contains(p, string(filepath.Separator)+"agent-transcripts"+string(filepath.Separator)) {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func LoadCursor() []model.Session {
	var ss []model.Session
	for _, db := range CursorDBs() {
		got, _ := ParseCursorDB(db)
		ss = append(ss, got...)
	}
	ss = append(ss, parseFiles(CursorTranscripts(), ParseCursorTranscript)...)
	return ss
}

// ParseCursorDBSince returns only messages newer than t: composers whose
// lastUpdatedAt passed the watermark, and within them only bubbles stamped
// after it. Bubbles without timestamps are only picked up by full rebuilds —
// modern Cursor stamps every bubble, so the append path stays correct.
func ParseCursorDBSince(db string, t time.Time) ([]model.Session, error) {
	if t.IsZero() {
		return ParseCursorDB(db)
	}
	return parseCursorDB(db, t.UnixMilli())
}

// ParseCursorDB reads composer sessions with a narrow projection — dumping
// whole JSON values through the sqlite3 pipe on a multi-hundred-MB store
// takes minutes, extracting scalars takes seconds (same lesson as opencode).
func ParseCursorDB(db string) ([]model.Session, error) {
	return parseCursorDB(db, 0)
}

func parseCursorDB(db string, sinceMS int64) ([]model.Session, error) {
	if fi, err := os.Stat(db); err != nil || fi.Size() == 0 {
		return nil, nil
	}
	composerWhere := ""
	bubbleWhere := ""
	if sinceMS > 0 {
		composerWhere = fmt.Sprintf(" and json_extract(value,'$.lastUpdatedAt') > %d", sinceMS)
		bubbleWhere = fmt.Sprintf(" and json_extract(value,'$.timestamp') > %d", sinceMS)
	}
	composers, err := cursorQuery(db, `select key,`+
		`json_extract(value,'$.composerId') as cid,`+
		`json_extract(value,'$.name') as name,`+
		`json_extract(value,'$.createdAt') as created,`+
		`json_extract(value,'$.lastUpdatedAt') as updated `+
		`from cursorDiskKV where key >= 'composerData:' and key < 'composerData;' and value is not null`+composerWhere)
	if err != nil {
		return nil, err
	}
	if len(composers) == 0 {
		return nil, nil
	}
	bubbles, err := cursorQuery(db, `select key,`+
		`json_extract(value,'$.type') as type,`+
		`coalesce(json_extract(value,'$.text'), json_extract(value,'$.rawText')) as text,`+
		`json_extract(value,'$.timestamp') as ts,`+
		`json_extract(value,'$.workspaceProjectDir') as wsdir `+
		`from cursorDiskKV where key >= 'bubbleId:' and key < 'bubbleId;' and value is not null`+bubbleWhere)
	if err != nil {
		return nil, err
	}
	byComposer := map[string][]map[string]any{}
	for _, b := range bubbles {
		key := str(b["key"]) // bubbleId:<composerId>:<bubbleId>
		parts := strings.SplitN(key, ":", 3)
		if len(parts) != 3 {
			continue
		}
		byComposer[parts[1]] = append(byComposer[parts[1]], b)
	}
	var out []model.Session
	for _, c := range composers {
		cid := str(c["cid"])
		if cid == "" {
			cid = strings.TrimPrefix(str(c["key"]), "composerData:")
		}
		s := model.Session{Harness: "cursor", ID: cid, Project: "-", Path: db, Title: str(c["name"])}
		s.Touch(epochMS(c["created"]))
		s.Touch(epochMS(c["updated"]))
		bs := byComposer[cid]
		sort.SliceStable(bs, func(i, j int) bool { return epochMS(bs[i]["ts"]).Before(epochMS(bs[j]["ts"])) })
		for _, b := range bs {
			text := str(b["text"])
			if strings.TrimSpace(text) == "" {
				continue
			}
			if len(text) > 64*1024 {
				text = text[:64*1024]
			}
			role := "assistant"
			if n, ok := numberVal(b["type"]); ok && n == 1 {
				role = "user"
			}
			t := epochMS(b["ts"])
			if t.IsZero() {
				t = s.Started
			}
			s.Touch(t)
			s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: t})
			if s.Project == "-" {
				if ws := str(b["wsdir"]); ws != "" {
					s.Project = projectName(ws)
				}
			}
		}
		if len(s.Messages) > 0 {
			out = append(out, s)
		}
	}
	return out, nil
}

func cursorQuery(db, q string) ([]map[string]any, error) {
	cmd := exec.Command("sqlite3", "-readonly", "-json", db, ".timeout 5000", q)
	b, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("cursor sqlite: %w", err)
	}
	if len(b) == 0 {
		return nil, nil
	}
	var rows []map[string]any
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	if err := dec.Decode(&rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func numberVal(v any) (int64, bool) {
	switch x := v.(type) {
	case json.Number:
		n, err := x.Int64()
		return n, err == nil
	case float64:
		return int64(x), true
	}
	return 0, false
}

func epochMS(v any) time.Time {
	if n, ok := numberVal(v); ok && n > 0 {
		return time.UnixMilli(n)
	}
	return time.Time{}
}

// ParseCursorTranscript reads a CLI agent transcript: Anthropic wire-shaped
// JSONL; control lines (turn_ended etc.) carry no role and are skipped.
func ParseCursorTranscript(path string) ([]model.Session, error) {
	id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	s := model.Session{Harness: "cursor", ID: id, Project: cursorTranscriptProject(path), Path: path}
	fi, err := os.Stat(path)
	if err == nil {
		s.Touch(fi.ModTime()) // transcripts carry no timestamps
	}
	err = scanJSONLFromOffset(path, 0, func(m map[string]any) {
		role, _ := m["role"].(string)
		if role != "user" && role != "assistant" {
			return
		}
		msg, _ := m["message"].(map[string]any)
		if msg == nil {
			return
		}
		txt := textFromContent(msg["content"])
		if txt == "" {
			return
		}
		s.Messages = append(s.Messages, model.Message{Role: role, Text: txt, Time: s.Updated})
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}

// cursorTranscriptProject decodes ~/.cursor/projects/<Users-me-work-foo>/...
// back to a path with a greedy existence-checked walk; hyphens in real dir
// names survive because the literal branch is tried when the split fails.
func cursorTranscriptProject(path string) string {
	dir := path
	for filepath.Base(filepath.Dir(dir)) != "projects" && dir != "/" && dir != "." {
		dir = filepath.Dir(dir)
	}
	encoded := filepath.Base(dir)
	if encoded == "" || encoded == "/" || encoded == "." {
		return "-"
	}
	if resolved := resolveEncodedPath("-" + encoded); resolved != "" {
		return projectName(resolved)
	}
	parts := strings.Split(encoded, "-")
	if len(parts) >= 2 {
		return filepath.Join(parts[len(parts)-2], parts[len(parts)-1])
	}
	return encoded
}
