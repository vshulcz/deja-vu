package sources

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// ClaudeMemDBPath returns the path to the claude-mem SQLite database.
// Honors DEJA_CLAUDE_MEM_DB, falling back to ~/.claude-mem/claude-mem.db.
func ClaudeMemDBPath() string {
	if p := os.Getenv("DEJA_CLAUDE_MEM_DB"); p != "" {
		return p
	}
	return filepath.Join(Home(), ".claude-mem", "claude-mem.db")
}

// IsClaudeMemDB reports whether path matches the configured claude-mem database.
func IsClaudeMemDB(path string) bool {
	db := ClaudeMemDBPath()
	if db == "" {
		return false
	}
	return filepath.Clean(path) == filepath.Clean(db)
}

// ParseClaudeMemDB reads every session in the claude-mem SQLite database.
func ParseClaudeMemDB(db string) ([]model.Session, error) {
	return ParseClaudeMemDBSince(db, time.Time{})
}

// ParseClaudeMemDBSince reads the sessions in the claude-mem SQLite database touched after t.
func ParseClaudeMemDBSince(db string, t time.Time) ([]model.Session, error) {
	fi, statErr := os.Stat(db)
	if statErr != nil || fi.Size() == 0 {
		return nil, nil //nolint:nilerr // missing or empty optional store is not an error
	}
	where := ""
	if !t.IsZero() {
		cutoff := t.Add(-time.Second)
		where = fmt.Sprintf(" where (%s or %s or %s)",
			newerThanEpoch("s.last_activity_epoch", cutoff),
			newerThanEpoch("s.completed_at_epoch", cutoff),
			newerThanEpoch("s.created_at_epoch", cutoff),
		)
	}
	return parseClaudeMemDBWhere(db, where)
}

func parseClaudeMemDBWhere(db, where string) ([]model.Session, error) {
	out, err := sqliteOutput(db, "select count(*) from sqlite_master where type='table' and name='sdk_sessions'")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(out)) == "0" {
		return nil, nil
	}

	qSessions := "select json_object('sdk_session_id',cast(s.sdk_session_id as text)," +
		"'claude_session_id',cast(coalesce(s.claude_session_id,'') as text)," +
		"'project',cast(coalesce(s.project,'') as text)," +
		"'created_at_epoch',s.created_at_epoch," +
		"'completed_at_epoch',coalesce(s.completed_at_epoch,0)," +
		"'last_activity_epoch',coalesce(s.last_activity_epoch,0)) " +
		"from sdk_sessions s" + where + " order by s.created_at_epoch"
	outSessions, err := sqliteOutput(db, qSessions)
	if err != nil {
		return nil, err
	}

	type sessionRow struct {
		SDKSessionID      string `json:"sdk_session_id"`
		ClaudeSessionID   string `json:"claude_session_id"`
		Project           string `json:"project"`
		CreatedAtEpoch    int64  `json:"created_at_epoch"`
		CompletedAtEpoch  int64  `json:"completed_at_epoch"`
		LastActivityEpoch int64  `json:"last_activity_epoch"`
	}
	sRows, err := sqliteObjects[sessionRow](outSessions)
	if err != nil {
		return nil, err
	}
	if len(sRows) == 0 {
		return nil, nil
	}

	sessionsByID := make(map[string]*model.Session, len(sRows))
	var sessionOrder []string
	for _, r := range sRows {
		if r.SDKSessionID == "" {
			continue
		}
		proj := claudeMemProjectName(r.Project)
		started := unixGuess(r.CreatedAtEpoch)
		updated := claudeMemUpdatedTime(r.LastActivityEpoch, r.CompletedAtEpoch, started)
		sessionsByID[r.SDKSessionID] = &model.Session{
			Harness: "claude",
			ID:      "cm-" + r.SDKSessionID,
			Project: proj,
			Path:    db,
			Started: started,
			Updated: updated,
		}
		sessionOrder = append(sessionOrder, r.SDKSessionID)
	}

	appendClaudeMemPrompts(db, where, sessionsByID)
	appendClaudeMemObservations(db, where, sessionsByID)
	appendClaudeMemSummaries(db, where, sessionsByID)

	var result []model.Session
	for _, id := range sessionOrder {
		s := sessionsByID[id]
		if s == nil || len(s.Messages) == 0 {
			continue
		}
		finalizeClaudeMemSession(s)
		result = append(result, *s)
	}
	return result, nil
}

func claudeMemProjectName(raw string) string {
	proj := projectName(raw)
	if (proj == "" || proj == "-") && raw != "" {
		proj = filepath.Base(raw)
	}
	if proj == "" {
		return "claude"
	}
	return proj
}

func claudeMemUpdatedTime(lastActivity, completed int64, started time.Time) time.Time {
	if updated := unixGuess(lastActivity); !updated.IsZero() {
		return updated
	}
	if updated := unixGuess(completed); !updated.IsZero() {
		return updated
	}
	return started
}

func appendClaudeMemPrompts(db, where string, sessionsByID map[string]*model.Session) {
	hasPrompts, _ := sqliteOutput(db, "select count(*) from sqlite_master where type='table' and name='user_prompts'")
	if strings.TrimSpace(string(hasPrompts)) == "0" {
		return
	}
	qPrompts := "select json_object('sdk_session_id',cast(u.sdk_session_id as text)," +
		"'prompt_text',cast(coalesce(u.prompt_text,'') as text)," +
		"'created_at_epoch',u.created_at_epoch) " +
		"from user_prompts u join sdk_sessions s on u.sdk_session_id = s.sdk_session_id" + where + " order by u.created_at_epoch"
	outPrompts, err := sqliteOutput(db, qPrompts)
	if err != nil {
		return
	}
	type promptRow struct {
		SDKSessionID   string `json:"sdk_session_id"`
		PromptText     string `json:"prompt_text"`
		CreatedAtEpoch int64  `json:"created_at_epoch"`
	}
	pRows, _ := sqliteObjects[promptRow](outPrompts)
	for _, pr := range pRows {
		s := sessionsByID[pr.SDKSessionID]
		txt := strings.TrimSpace(pr.PromptText)
		if s == nil || txt == "" {
			continue
		}
		s.Messages = append(s.Messages, model.Message{
			Role: "user",
			Text: txt,
			Time: unixGuess(pr.CreatedAtEpoch),
		})
	}
}

func appendClaudeMemObservations(db, where string, sessionsByID map[string]*model.Session) {
	hasObs, _ := sqliteOutput(db, "select count(*) from sqlite_master where type='table' and name='observations'")
	if strings.TrimSpace(string(hasObs)) == "0" {
		return
	}
	qObs := "select json_object('sdk_session_id',cast(o.sdk_session_id as text)," +
		"'title',cast(coalesce(o.title,'') as text)," +
		"'subtitle',cast(coalesce(o.subtitle,'') as text)," +
		"'narrative',cast(coalesce(o.narrative,'') as text)," +
		"'text',cast(coalesce(o.text,'') as text)," +
		"'facts',cast(coalesce(o.facts,'') as text)," +
		"'files_modified',cast(coalesce(o.files_modified,'') as text)," +
		"'created_at_epoch',o.created_at_epoch) " +
		"from observations o join sdk_sessions s on o.sdk_session_id = s.sdk_session_id" + where + " order by o.created_at_epoch"
	outObs, err := sqliteOutput(db, qObs)
	if err != nil {
		return
	}
	type obsRow struct {
		SDKSessionID   string `json:"sdk_session_id"`
		Title          string `json:"title"`
		Subtitle       string `json:"subtitle"`
		Narrative      string `json:"narrative"`
		Text           string `json:"text"`
		Facts          string `json:"facts"`
		FilesModified  string `json:"files_modified"`
		CreatedAtEpoch int64  `json:"created_at_epoch"`
	}
	oRows, _ := sqliteObjects[obsRow](outObs)
	indexEdits := IndexEdits()
	for _, orow := range oRows {
		s := sessionsByID[orow.SDKSessionID]
		if s == nil {
			continue
		}
		at := unixGuess(orow.CreatedAtEpoch)
		if text := joinNonEmptyLines(orow.Title, orow.Subtitle, orow.Narrative, orow.Text, orow.Facts); text != "" {
			s.Messages = append(s.Messages, model.Message{
				Role: "assistant",
				Text: text,
				Time: at,
			})
		}
		if !indexEdits {
			continue
		}
		for _, f := range parseClaudeMemFiles(orow.FilesModified) {
			s.Messages = append(s.Messages, model.Message{
				Role: RoleEdit,
				Text: f,
				Time: at,
			})
		}
	}
}

func parseClaudeMemFiles(raw string) []string {
	fm := strings.TrimSpace(raw)
	if fm == "" {
		return nil
	}
	var files []string
	if strings.HasPrefix(fm, "[") {
		_ = json.Unmarshal([]byte(fm), &files)
	}
	if len(files) == 0 {
		for line := range strings.SplitSeq(fm, "\n") {
			for p := range strings.SplitSeq(line, ",") {
				p = strings.TrimSpace(p)
				if p != "" && p != "[" && p != "]" {
					files = append(files, p)
				}
			}
		}
	}
	var out []string
	for _, f := range files {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

func appendClaudeMemSummaries(db, where string, sessionsByID map[string]*model.Session) {
	hasSum, _ := sqliteOutput(db, "select count(*) from sqlite_master where type='table' and name='session_summaries'")
	if strings.TrimSpace(string(hasSum)) == "0" {
		return
	}
	qSum := "select json_object('sdk_session_id',cast(m.sdk_session_id as text)," +
		"'request',cast(coalesce(m.request,'') as text)," +
		"'investigated',cast(coalesce(m.investigated,'') as text)," +
		"'learned',cast(coalesce(m.learned,'') as text)," +
		"'completed',cast(coalesce(m.completed,'') as text)," +
		"'next_steps',cast(coalesce(m.next_steps,'') as text)," +
		"'notes',cast(coalesce(m.notes,'') as text)," +
		"'created_at_epoch',m.created_at_epoch) " +
		"from session_summaries m join sdk_sessions s on m.sdk_session_id = s.sdk_session_id" + where + " order by m.created_at_epoch"
	outSum, err := sqliteOutput(db, qSum)
	if err != nil {
		return
	}
	type sumRow struct {
		SDKSessionID   string `json:"sdk_session_id"`
		Request        string `json:"request"`
		Investigated   string `json:"investigated"`
		Learned        string `json:"learned"`
		Completed      string `json:"completed"`
		NextSteps      string `json:"next_steps"`
		Notes          string `json:"notes"`
		CreatedAtEpoch int64  `json:"created_at_epoch"`
	}
	sumRows, _ := sqliteObjects[sumRow](outSum)
	for _, sr := range sumRows {
		s := sessionsByID[sr.SDKSessionID]
		if s == nil {
			continue
		}
		if s.Title == "" && strings.TrimSpace(sr.Request) != "" {
			s.Title = strings.TrimSpace(sr.Request)
		}
		if text := joinNonEmptyLines(sr.Investigated, sr.Learned, sr.Completed, sr.NextSteps, sr.Notes); text != "" {
			s.Messages = append(s.Messages, model.Message{
				Role: "assistant",
				Text: text,
				Time: unixGuess(sr.CreatedAtEpoch),
			})
		}
	}
}

func joinNonEmptyLines(fields ...string) string {
	var parts []string
	for _, field := range fields {
		if field = strings.TrimSpace(field); field != "" {
			parts = append(parts, field)
		}
	}
	return strings.Join(parts, "\n")
}

func finalizeClaudeMemSession(s *model.Session) {
	sort.SliceStable(s.Messages, func(i, j int) bool {
		return s.Messages[i].Time.Before(s.Messages[j].Time)
	})
	if s.Title == "" {
		for _, m := range s.Messages {
			if m.Role == "user" && m.Text != "" {
				firstLine, _, _ := strings.Cut(m.Text, "\n")
				s.Title = strings.TrimSpace(firstLine)
				break
			}
		}
	}
	if s.Started.IsZero() {
		s.Started = s.Messages[0].Time
	}
	if s.Updated.IsZero() {
		s.Updated = s.Messages[len(s.Messages)-1].Time
	}
	if s.Updated.Before(s.Started) {
		s.Updated = s.Started
	}
}
