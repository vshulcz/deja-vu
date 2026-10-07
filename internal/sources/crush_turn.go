package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// CrushTurnState is what a Crush session's store says at a PreToolUse, the
// only hook Crush fires: the newest message the person sent, and the output
// of the newest tool call when it was a command that failed.
type CrushTurnState struct {
	Prompt, PromptID   string
	Failure, FailureID string
}

// CrushTurn reads the session's newest rows from the store of the project cwd
// is in.
func CrushTurn(cwd, sessionID string) CrushTurnState {
	var st CrushTurnState
	db := crushDBFor(cwd)
	if db == "" || sessionID == "" {
		return st
	}
	q := `select json_object('id',m.id,'session_id',m.session_id,'role',m.role,'parts',m.parts) ` +
		`from messages m where m.session_id='` + sqlEscape(sessionID) + `' ` +
		`order by m.created_at desc, m.rowid desc limit 40`
	rows, err := crushRows(db, q)
	if err != nil {
		return st
	}
	sawResult := false
	for _, r := range rows {
		var parts []crushPart
		if json.Unmarshal([]byte(r.Parts), &parts) != nil {
			continue
		}
		switch r.Role {
		case "tool":
			for _, p := range parts {
				if p.Type != "tool_result" || sawResult {
					continue
				}
				sawResult = true
				out := crushStripCWD(p.Data.Content)
				if code, ok := statusCode(lastLine(out), "Exit code ", ""); ok && code != 0 {
					st.Failure, st.FailureID = strings.TrimSpace(out), p.Data.ToolCallID
				}
			}
		case "user":
			var say []string
			for _, p := range parts {
				if p.Type == "text" && strings.TrimSpace(p.Data.Text) != "" {
					say = append(say, p.Data.Text)
				}
			}
			if len(say) > 0 {
				st.Prompt, st.PromptID = strings.Join(say, "\n"), r.ID
				return st
			}
		}
	}
	return st
}

// crushDBFor is the store of the registered project that holds cwd, the
// deepest one when they nest, or the default place beside cwd.
func crushDBFor(cwd string) string {
	if cwd == "" {
		return ""
	}
	best, bestLen := "", -1
	for _, p := range CrushProjects() {
		if p.Path == "" || !pathWithin(cwd, p.Path) || len(p.Path) <= bestLen {
			continue
		}
		dir := p.DataDir
		if dir == "" {
			dir = filepath.Join(p.Path, ".crush")
		}
		best, bestLen = filepath.Join(dir, "crush.db"), len(p.Path)
	}
	if best == "" {
		best = filepath.Join(cwd, ".crush", "crush.db")
	}
	if fi, err := os.Stat(best); err != nil || fi.Size() == 0 {
		return ""
	}
	return best
}

func pathWithin(p, root string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
