package sources

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Crush (charmbracelet/crush) keeps one SQLite store per project rather than
// one per machine:
//
//	<project>/.crush/crush.db                       sessions and messages
//	${XDG_DATA_HOME:-~/.local/share}/crush/projects.json   where those projects are
//
// The registry is what makes the stores findable at all — nothing under the
// data home holds a transcript, and a walk of the filesystem looking for
// `.crush` directories is not something deja does. Verified against crush
// v0.92.0 by running one: the store landed beside the project, the registry
// gained its path and data_dir, and `crush projects` prints the same pair.
//
// A message's `parts` column is a JSON array of {type, data}: `text` carries
// data.text, `tool_call` carries the name and a JSON string of arguments,
// `tool_result` carries what the tool printed with a <cwd> tag appended, and
// `finish` is bookkeeping (#2949).
//
// The stamp columns are commented "Unix timestamp in milliseconds" in Crush's
// own schema and hold whole seconds in the store v0.92.0 writes — the update
// trigger sets strftime('%s','now'). Both units go through unixGuess.

// CrushDataHome is where Crush keeps its own state, including the project
// registry. DEJA_CRUSH_ROOT points a stand at another one.
func CrushDataHome() string {
	if p := os.Getenv("DEJA_CRUSH_ROOT"); p != "" {
		return p
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "crush")
	}
	return filepath.Join(Home(), ".local", "share", "crush")
}

type crushProject struct {
	Path    string `json:"path"`
	DataDir string `json:"data_dir"`
}

// CrushProjects reads the registry: each project Crush has been run in, and
// the directory it keeps that project's store in.
func CrushProjects() []crushProject {
	b, err := os.ReadFile(filepath.Join(CrushDataHome(), "projects.json"))
	if err != nil {
		return nil
	}
	var doc struct {
		Projects []crushProject `json:"projects"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return nil
	}
	return doc.Projects
}

// CrushDBs lists the stores that exist. A registry entry for a project that has
// since been deleted is ordinary — the registry is append-mostly — so a missing
// file is skipped rather than reported.
func CrushDBs() []string {
	var out []string
	for _, p := range CrushProjects() {
		dir := p.DataDir
		if dir == "" && p.Path != "" {
			dir = filepath.Join(p.Path, ".crush")
		}
		if dir == "" {
			continue
		}
		db := filepath.Join(dir, "crush.db")
		if fi, err := os.Stat(db); err == nil && fi.Size() > 0 {
			out = append(out, db)
		}
	}
	return out
}

func LoadCrush() []model.Session {
	var out []model.Session
	for _, db := range CrushDBs() {
		got, _ := ParseCrushDB(db)
		out = append(out, got...)
	}
	return out
}

type crushRow struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Title     string `json:"title"`
	Parent    string `json:"parent_session_id"`
	Role      string `json:"role"`
	Parts     string `json:"parts"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// ParseCrushDB reads one project's store. The project is the directory the
// store sits under: Crush records no cwd on the session row, and the store
// living beside the work is the whole attribution.
func ParseCrushDB(db string) ([]model.Session, error) {
	return ParseCrushDBSince(db, time.Time{})
}

// ParseCrushDBSince reads the sessions touched after t, which is what lets an
// incremental pass skip a store nothing has happened in.
func ParseCrushDBSince(db string, t time.Time) ([]model.Session, error) {
	if fi, err := os.Stat(db); err != nil || fi.Size() == 0 {
		return nil, nil
	}
	where := ""
	if !t.IsZero() {
		where = " where " + newerThanEpoch("s.updated_at", t)
	}
	q := "select s.id as session_id, s.title as title, coalesce(s.parent_session_id,'') as parent_session_id, " +
		"s.updated_at as updated_at, m.id as id, m.role as role, m.parts as parts, m.created_at as created_at " +
		"from sessions s join messages m on m.session_id = s.id" + where + " order by s.id, m.created_at"
	rows, err := crushRows(db, q)
	if err != nil {
		return nil, err
	}
	project := crushProjectName(db)
	byID := map[string]*model.Session{}
	var order []string
	for _, r := range rows {
		s := byID[r.SessionID]
		if s == nil {
			s = &model.Session{
				Harness: "crush", ID: r.SessionID, Path: db,
				Project: project, Title: crushPlainText(firstLineTrim(r.Title)),
			}
			if r.Parent != "" {
				// Crush spawns subagent sessions the way Claude does, and the
				// row that names the parent is the only place the edge exists.
				s.Kind = "subagent"
				s.Parent = r.Parent
			}
			byID[r.SessionID] = s
			order = append(order, r.SessionID)
		}
		at := unixGuess(r.CreatedAt)
		for _, m := range crushMessages(r.Role, r.Parts, at) {
			s.Touch(m.Time)
			s.Messages = append(s.Messages, m)
		}
	}
	out := make([]model.Session, 0, len(order))
	for _, id := range order {
		s := byID[id]
		if len(s.Messages) == 0 {
			continue
		}
		out = append(out, *s)
	}
	return out, nil
}

// CrushProjectDir is the directory a store belongs to: <project>/.crush. It is
// where the work happened, and where `crush --session` has to be run — Crush
// finds a session in the store under the current directory and nowhere else.
func CrushProjectDir(db string) string {
	dir := filepath.Dir(db)
	if strings.EqualFold(filepath.Base(dir), ".crush") {
		dir = filepath.Dir(dir)
	}
	return dir
}

// crushProjectName is that directory's name.
func crushProjectName(db string) string { return projectName(CrushProjectDir(db)) }

// crushPlainText drops what a store may hold and an index may not. The parts
// column is JSON inside a database, so the sweep over file fixtures never
// reaches it, and a NUL here travels to every consumer that forwards Text
// without the display layer's sanitiser (#1740).
//
// Only the NUL: a \u0000 escape is well-formed JSON and decodes to a real one.
// Bad UTF-8 and lone surrogates do not survive the two decodes this text comes
// through — encoding/json replaces them with U+FFFD on the way into a string.
func crushPlainText(s string) string {
	if strings.IndexByte(s, 0) < 0 {
		return s
	}
	return strings.ReplaceAll(s, "\x00", "")
}

type crushPart struct {
	Type string `json:"type"`
	Data struct {
		Text       string `json:"text"`
		Name       string `json:"name"`
		Input      string `json:"input"`
		Content    string `json:"content"`
		ToolCallID string `json:"tool_call_id"`
		IsError    bool   `json:"is_error"`
	} `json:"data"`
}

// crushMessages turns one row's parts into what deja indexes: speech, the
// commands a run actually executed, and what a tool printed.
func crushMessages(role, parts string, at time.Time) []model.Message {
	var list []crushPart
	if json.Unmarshal([]byte(parts), &list) != nil {
		return nil
	}
	var out []model.Message
	for _, p := range list {
		switch p.Type {
		case "text":
			if text := strings.TrimSpace(p.Data.Text); text != "" {
				out = append(out, model.Message{Role: role, Text: crushPlainText(capParsedMessage(text)), Time: at})
			}
		case "tool_call":
			// The arguments are a JSON string of the tool's own schema, so the
			// command and the path are one decode away rather than in columns.
			var args struct {
				Command  string `json:"command"`
				FilePath string `json:"file_path"`
			}
			if json.Unmarshal([]byte(p.Data.Input), &args) != nil {
				continue
			}
			if IndexToolPaths() && args.FilePath != "" {
				out = append(out, model.Message{Role: RoleFiles, Text: crushPlainText(args.FilePath), Time: at})
			}
			if !IndexCommands() || p.Data.Name != "bash" {
				continue
			}
			cmd := strings.TrimSpace(args.Command)
			if cmd != "" && worthIndexing(cmd) {
				out = append(out, model.Message{Role: RoleCommand, Text: crushPlainText(cmd), Time: at})
			}
		case "tool_result":
			text := strings.TrimSpace(crushStripCWD(p.Data.Content))
			if text == "" {
				continue
			}
			out = append(out, model.Message{Role: RoleToolOutput, Text: crushPlainText(capParsedMessage(text)), Time: at})
		}
	}
	return out
}

// crushStripCWD drops the <cwd>…</cwd> tag Crush appends to a tool result. It
// is the same directory on every line of every session, and left in it is a
// path that matches a search for the project name in every tool output there
// is.
func crushStripCWD(s string) string {
	at := strings.Index(s, "<cwd>")
	if at < 0 {
		return s
	}
	end := strings.Index(s[at:], "</cwd>")
	if end < 0 {
		return s[:at]
	}
	return s[:at] + s[at+end+len("</cwd>"):]
}

func crushRows(db, q string) ([]crushRow, error) {
	out, err := exec.Command("sqlite3", "-readonly", "-json", sqliteTarget(db), ".timeout 5000", q).Output()
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		return nil, nil
	}
	var rows []crushRow
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}
