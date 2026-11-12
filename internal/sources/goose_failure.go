package sources

import "strings"

// GooseTurnState is what goose's store says about the session's current
// turn: the output of the newest shell command that failed, and the files the
// agent's editor calls changed, newest first.
type GooseTurnState struct {
	Failure   string
	Edits     []string
	EditCalls []EditCall
}

// GooseTurn reads the session's current turn from sessions.db, one query.
func GooseTurn(sessionID string) GooseTurnState {
	if sessionID == "" {
		return GooseTurnState{}
	}
	q := `select json_object('role',cast(m.role as text),'content_json',cast(m.content_json as text)) ` +
		`from messages m where m.session_id='` + sqlEscape(sessionID) + `' ` +
		`order by m.created_timestamp desc, m.id desc limit 60`
	for _, db := range GooseDBs() {
		if !nonEmptyFile(db) {
			continue
		}
		if st, done := gooseTurnIn(db, q); done {
			return st
		}
	}
	return GooseTurnState{}
}

// gooseTurnIn reads one store newest-first. done is true when the session was
// found there.
func gooseTurnIn(db, q string) (GooseTurnState, bool) {
	var st GooseTurnState
	cmd, stop := sqliteReadCmd(db, q)
	defer stop()
	dec, err := sqliteRows(cmd)
	if err != nil {
		return st, false
	}
	defer func() { _ = cmd.Wait() }()
	found := false
	seen := map[string]bool{}
	for dec.More() {
		var r map[string]any
		if dec.Decode(&r) != nil {
			return st, found
		}
		found = true
		items, ok := gooseContentArray(r["content_json"])
		if !ok {
			continue
		}
		person := false
		for _, it := range items {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			switch m["type"] {
			case "toolResponse":
				if st.Failure != "" {
					continue
				}
				if code, known := gooseExitCode(m); known && code != 0 {
					st.Failure = gooseToolResult(m)
				}
			case "toolRequest":
				if ec := gooseEditCall(m); ec.FilePath != "" && !seen[ec.FilePath] {
					seen[ec.FilePath] = true
					st.Edits = append(st.Edits, ec.FilePath)
					st.EditCalls = append(st.EditCalls, ec)
				}
			case "text":
				if str(r["role"]) == "user" && withoutGooseTurnContext(str(m["text"])) != "" {
					person = true
				}
			}
		}
		if person {
			return st, true
		}
	}
	return st, found
}

// gooseEditCall is the file and old/new snippet an editor or reader call
// touches: the developer extension's edit, write and read, or text_editor's
// str_replace, insert, write and view.
func gooseEditCall(m map[string]any) EditCall {
	name, args := gooseToolCall(m)
	switch strings.TrimPrefix(name, "developer__") {
	case "edit", "write", "read", "read_file", "view_file":
	case "text_editor":
		switch str(args["command"]) {
		case "str_replace", "insert", "write", "view":
		default:
			return EditCall{}
		}
	default:
		return EditCall{}
	}
	p := firstNonEmpty(str(args["path"]), str(args["file_path"]))
	if p == "" {
		return EditCall{}
	}
	oldS := firstNonEmpty(str(args["old_text"]), str(args["old_str"]), str(args["old_string"]), str(args["before"]))
	newS := firstNonEmpty(str(args["new_text"]), str(args["new_str"]), str(args["new_string"]), str(args["file_text"]), str(args["content"]), str(args["diff"]))
	return EditCall{ToolName: strings.TrimPrefix(name, "developer__"), FilePath: p, OldString: oldS, NewString: newS}
}
