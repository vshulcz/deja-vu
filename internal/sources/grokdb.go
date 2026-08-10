package sources

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The maintained grok CLI keeps no session files: everything lands in a
// SQLite database next to the config it shares with the other two CLIs. A
// deja that only walks ~/.grok/sessions sees none of that history.
func GrokDB() string {
	return EnvPath("DEJA_GROK_DB", filepath.Join(GrokRoot(), "grok.db"))
}

func LoadGrokDB() []model.Session {
	ss, err := ParseGrokDBSince(GrokDB(), time.Time{})
	if err != nil {
		return nil
	}
	return ss
}

// ParseGrokDBSince reads sessions whose messages changed after t. Message
// bodies are JSON in a text column, and the assistant side is an array of
// content blocks, so the text is pulled out here rather than in SQL.
func ParseGrokDBSince(db string, t time.Time) ([]model.Session, error) {
	// The sqlite3 CLI creates a missing database on open — never let it.
	if fi, err := os.Stat(db); err != nil || fi.Size() == 0 {
		return nil, nil
	}
	where := ""
	if !t.IsZero() {
		where = " and m.created_at > '" + sqlEscape(t.UTC().Format(time.RFC3339Nano)) + "'"
	}
	q := `select s.id as id,s.cwd_last as cwd,s.title as title,` +
		`m.role as role,m.message_json as body,m.created_at as at ` +
		`from sessions s join messages m on m.session_id=s.id` +
		` where m.role in ('user','assistant')` + where +
		` order by s.id,m.seq`
	cmd := exec.Command("sqlite3", "-readonly", "-json", db, ".timeout 5000", q)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(stdout)
	tok, err := dec.Token()
	if err != nil {
		waitErr := cmd.Wait()
		if err == io.EOF {
			// No stdout means two very different things: a query that matched
			// nothing, or one sqlite3 refused to run because the harness
			// changed its schema. Reporting the second as "no sessions" makes
			// a whole harness disappear from recall while doctor still calls
			// the store healthy.
			if waitErr != nil {
				return nil, fmt.Errorf("grok: query failed, the store schema may have changed: %w", waitErr)
			}
			return nil, nil
		}
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		_ = cmd.Wait()
		return nil, fmt.Errorf("bad sqlite json")
	}
	by := map[string]*model.Session{}
	var order []string
	for dec.More() {
		var r struct {
			ID    string `json:"id"`
			CWD   string `json:"cwd"`
			Title string `json:"title"`
			Role  string `json:"role"`
			Body  string `json:"body"`
			At    string `json:"at"`
		}
		if err := dec.Decode(&r); err != nil {
			_ = cmd.Wait()
			return nil, err
		}
		if r.ID == "" {
			continue
		}
		text := grokMessageText(r.Body)
		if strings.TrimSpace(text) == "" {
			continue
		}
		at := grokDBTime(r.At)
		s := by[r.ID]
		if s == nil {
			s = &model.Session{
				ID:      r.ID,
				Harness: "grok",
				Project: projectName(r.CWD),
				Path:    db,
				Title:   r.Title,
				Started: at,
			}
			by[r.ID] = s
			order = append(order, r.ID)
		}
		if s.Started.IsZero() || (!at.IsZero() && at.Before(s.Started)) {
			s.Started = at
		}
		if at.After(s.Updated) {
			s.Updated = at
		}
		s.Messages = append(s.Messages, model.Message{Role: r.Role, Text: text, Time: at})
	}
	if _, err := dec.Token(); err != nil && err != io.EOF {
		_ = cmd.Wait()
		return nil, err
	}
	if err := cmd.Wait(); err != nil {
		return nil, err
	}
	out := make([]model.Session, 0, len(order))
	for _, id := range order {
		out = append(out, *by[id])
	}
	return out, nil
}

// grokMessageText handles both message shapes: a user message carries a plain
// string, an assistant message an array of typed blocks of which only text
// ones are worth indexing.
func grokMessageText(body string) string {
	var doc struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return ""
	}
	var plain string
	if err := json.Unmarshal(doc.Content, &plain); err == nil {
		return plain
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(doc.Content, &blocks); err != nil {
		return ""
	}
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Type != "text" || blk.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(blk.Text)
	}
	return b.String()
}

func grokDBTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
