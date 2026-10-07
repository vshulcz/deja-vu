package sources

import "strings"

// GooseTurnState is what goose's store says about the session's current
// turn: the output of the newest shell command that failed, and the files the
// agent's editor calls changed, newest first.
type GooseTurnState struct {
	Failure string
	Edits   []string
}

// GooseTurnFailure is the output of the newest shell command that failed in
// this goose session's current turn, read from sessions.db: goose hands its
// hooks no tool output (PostToolUseFailure carries the tool's name and input
// only), and the store has the response by the time the turn ends. A turn
// starts at the person's last message, so a failure the agent already moved
// past in an earlier turn is not brought back. "" when there is none or the
// store cannot be read.
func GooseTurnFailure(sessionID string) string {
	return GooseTurn(sessionID).Failure
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
				if p := gooseEditPath(m); p != "" && !seen[p] {
					seen[p] = true
					st.Edits = append(st.Edits, p)
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

// gooseEditPath is the file an editor call changes: the developer
// extension's edit and write, or text_editor's str_replace, insert and write.
func gooseEditPath(m map[string]any) string {
	name, args := gooseToolCall(m)
	switch strings.TrimPrefix(name, "developer__") {
	case "edit", "write":
	case "text_editor":
		switch str(args["command"]) {
		case "str_replace", "insert", "write":
		default:
			return ""
		}
	default:
		return ""
	}
	return strings.TrimSpace(str(args["path"]))
}
