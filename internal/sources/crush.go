package sources

import (
	"bytes"
	"encoding/json"
	"os"
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
	Summary   bool   `json:"summary"`
}

// crushSummaryExpr says in SQL whether a message is a summary Crush wrote when
// it summarised the session. The summary is an assistant row; the session
// points summary_message_id at the newest one, and since 2025-08 every one,
// a failed attempt included, carries is_summary_message (v0.97.1
// internal/agent/agent.go Summarize). An older store has only the pointer, or
// neither.
func crushSummaryExpr(db string) string {
	out, _ := sqliteOutput(db, "select name from pragma_table_info('sessions') where name='summary_message_id'"+
		" union all select name from pragma_table_info('messages') where name='is_summary_message'")
	var conds []string
	for _, col := range strings.Fields(string(out)) {
		switch col {
		case "summary_message_id":
			conds = append(conds, "m.id = coalesce(s.summary_message_id,'')")
		case "is_summary_message":
			conds = append(conds, "m.is_summary_message = 1")
		}
	}
	if len(conds) == 0 {
		return "json('false')"
	}
	return "json(case when " + strings.Join(conds, " or ") + " then 'true' else 'false' end)"
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
		// The watermark is the newest turn's whole second, and a turn written
		// later in that second moves updated_at to the same second: a strict >
		// never asks for it (#4381). Sessions come back whole, so the second
		// re-offered costs a re-read of what is already held, nothing more.
		where = " where " + newerThanEpoch("s.updated_at", t.Add(-time.Second))
	}
	return parseCrushWhere(db, where)
}

func parseCrushWhere(db, where string) ([]model.Session, error) {
	// json_object rather than the shell's -json mode, which is quadratic in
	// what it escapes — see sqliteRows. A parts column is stored JSON.
	q := "select json_object('session_id',cast(s.id as text),'title',cast(s.title as text)," +
		"'parent_session_id',cast(coalesce(s.parent_session_id,'') as text),'updated_at',s.updated_at," +
		"'id',cast(m.id as text),'role',cast(m.role as text),'parts',cast(m.parts as text),'created_at',m.created_at," +
		"'summary'," + crushSummaryExpr(db) + ") " +
		"from sessions s join messages m on m.session_id = s.id" + where + " order by s.id, m.created_at"
	rows, err := crushRows(db, q)
	if err != nil {
		return nil, err
	}
	project := crushProjectName(db)
	byID := map[string]*model.Session{}
	joins := map[string]*crushJoin{}
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
			joins[r.SessionID] = &crushJoin{exits: commandExits{}, changes: map[string][]int{}, calls: map[string][]int{}, dropped: map[int]bool{}}
			order = append(order, r.SessionID)
		}
		at := unixGuess(r.CreatedAt)
		role := r.Role
		if r.Summary {
			role = RoleSummary
		}
		recs, results := crushMessages(role, r.Parts, at)
		joins[r.SessionID].add(s, recs, results)
	}
	out := make([]model.Session, 0, len(order))
	for _, id := range order {
		s := byID[id]
		joins[id].drop(s)
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
		ID         string `json:"id"`
		ToolCallID string `json:"tool_call_id"`
		IsError    bool   `json:"is_error"`
	} `json:"data"`
}

// crushRecord is one record a part gave, with the call it came from.
type crushRecord struct {
	model.Message
	call string
}

// crushResult is what a tool_result part says about its call.
type crushResult struct {
	call, content string
	failed        bool
}

// denied is the result crush stores when the user refuses a permission
// prompt (internal/agent/tools/tools.go NewPermissionDeniedResponse): the call
// never ran, so nothing it names was run, read or changed (#4575).
func (r crushResult) denied() bool {
	return r.failed && strings.TrimSpace(r.content) == "User denied permission"
}

// crushJoin pairs a session's calls with their results, which crush keeps as
// separate parts on separate rows (#4532). A bash result ends "Exit code N"
// when the command failed (internal/agent/tools/bash.go formatOutput), an
// edit or write whose result is an error changed nothing, and a call the user
// denied leaves no record at all.
type crushJoin struct {
	exits   commandExits
	changes map[string][]int
	calls   map[string][]int
	dropped map[int]bool
}

func (j *crushJoin) add(s *model.Session, recs []crushRecord, results []crushResult) {
	for _, r := range recs {
		s.Touch(r.Time)
		if r.call != "" {
			j.calls[r.call] = append(j.calls[r.call], len(s.Messages))
			switch r.Role {
			case RoleCommand:
				j.exits[r.call] = append(j.exits[r.call], len(s.Messages))
			case RoleEdit, RoleWrote:
				j.changes[r.call] = append(j.changes[r.call], len(s.Messages))
			}
		}
		s.Messages = append(s.Messages, r.Message)
	}
	for _, r := range results {
		if r.failed {
			for _, i := range j.changes[r.call] {
				j.dropped[i] = true
			}
		}
		if r.denied() {
			for _, i := range j.calls[r.call] {
				j.dropped[i] = true
			}
		}
		if code, ok := statusCode(lastLine(crushStripCWD(r.content)), "Exit code ", ""); ok {
			j.exits.stamp(s.Messages, r.call, "", code)
		}
		// A result answers the call before it, once: a provider that numbers
		// calls per turn reuses the id, and a later failure under it is not
		// the earlier call's.
		delete(j.exits, r.call)
		delete(j.changes, r.call)
		delete(j.calls, r.call)
	}
}

// drop takes out the changes whose result refused them and the calls the user
// denied.
func (j *crushJoin) drop(s *model.Session) {
	if len(j.dropped) == 0 {
		return
	}
	kept := s.Messages[:0]
	for i, m := range s.Messages {
		if !j.dropped[i] {
			kept = append(kept, m)
		}
	}
	s.Messages = kept
}

// crushMessages turns one row's parts into what deja indexes: speech, the
// commands a run actually executed, and what a tool printed, and what each
// result said about its call.
func crushMessages(role, parts string, at time.Time) ([]crushRecord, []crushResult) {
	var list []crushPart
	if json.Unmarshal([]byte(parts), &list) != nil {
		return nil, nil
	}
	var out []crushRecord
	var results []crushResult
	for _, p := range list {
		switch p.Type {
		case "text":
			if text := strings.TrimSpace(p.Data.Text); text != "" {
				out = append(out, crushRecord{Message: model.Message{Role: role, Text: crushPlainText(capParsedMessage(text)), Time: at}})
			}
		case "tool_call":
			// The arguments are a JSON string of the tool's own schema, so the
			// command and the path are one decode away rather than in columns.
			var args struct {
				Command   string `json:"command"`
				FilePath  string `json:"file_path"`
				OldString string `json:"old_string"`
				NewString string `json:"new_string"`
				Content   string `json:"content"`
				// lsp_replace_symbol's new text and what to do with it.
				Replacement string `json:"replacement"`
				Action      string `json:"action"`
				Edits       []struct {
					OldString string `json:"old_string"`
					NewString string `json:"new_string"`
				} `json:"edits"`
			}
			if json.Unmarshal([]byte(p.Data.Input), &args) != nil {
				continue
			}
			if IndexToolPaths() && args.FilePath != "" {
				out = append(out, crushRecord{model.Message{Role: RoleFiles, Text: crushPlainText(args.FilePath), Time: at}, p.Data.ID})
			}
			// edit, multiedit and write carry both sides of the change, which is
			// what restore and blame read (#4377), and lsp_replace_symbol the
			// written one. Only those: a view names a file it did not change.
			switch p.Data.Name {
			case "edit", "multiedit", "write", "lsp_replace_symbol":
				path := crushPlainText(args.FilePath)
				old := []string{args.OldString}
				written := []string{args.NewString, args.Content}
				// lsp_replace_symbol writes its replacement in place of the
				// symbol, before it or after it; a delete writes nothing. The
				// replaced symbol is only in the result's metadata (#4533).
				if args.Action != "delete" {
					written = append(written, args.Replacement)
				}
				for _, e := range args.Edits {
					old = append(old, e.OldString)
					written = append(written, e.NewString)
				}
				if IndexEdits() && path != "" && !strings.ContainsAny(path, "\n\r") {
					for _, span := range old {
						if span == "" {
							continue
						}
						if len(span) > editSpanMax {
							span = span[:editSpanMax]
						}
						out = append(out, crushRecord{model.Message{Role: RoleEdit, Text: path + "\n" + crushPlainText(span), Time: at}, p.Data.ID})
					}
				}
				if IndexWrites() {
					for _, w := range written {
						if rec := WroteRecord(path, w); rec != "" {
							out = append(out, crushRecord{model.Message{Role: RoleWrote, Text: rec, Time: at}, p.Data.ID})
						}
					}
				}
			}
			if !IndexCommands() || p.Data.Name != "bash" {
				continue
			}
			cmd := strings.TrimSpace(args.Command)
			if cmd != "" && worthIndexing(cmd) {
				out = append(out, crushRecord{model.Message{Role: RoleCommand, Text: crushPlainText(cmd), Time: at}, p.Data.ID})
			}
		case "tool_result":
			if p.Data.ToolCallID != "" {
				results = append(results, crushResult{p.Data.ToolCallID, p.Data.Content, p.Data.IsError})
			}
			text := strings.TrimSpace(crushStripCWD(p.Data.Content))
			if text == "" {
				continue
			}
			out = append(out, crushRecord{Message: model.Message{Role: RoleToolOutput, Text: crushPlainText(capParsedMessage(text)), Time: at}})
		}
	}
	return out, results
}

// crushStripCWD drops the <cwd>…</cwd> tag Crush appends to a tool result. It
// is the same directory on every line of every session, and left in it is a
// path that matches a search for the project name in every tool output there
// is. Only the tag at the end is crush's: one earlier is what the command
// printed, and cutting there lost the exit line below it.
func crushStripCWD(s string) string {
	t := strings.TrimRight(s, " \t\r\n")
	if !strings.HasSuffix(t, "</cwd>") {
		return s
	}
	at := strings.LastIndex(t, "<cwd>")
	if at < 0 {
		return s
	}
	return t[:at]
}

func crushRows(db, q string) ([]crushRow, error) {
	out, err := sqliteOutput(db, q)
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		return nil, nil
	}
	// One json_object per line, not an array.
	var rows []crushRow
	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var r crushRow
		if err := dec.Decode(&r); err != nil {
			return nil, err
		}
		rows = append(rows, r)
	}
	return rows, nil
}
