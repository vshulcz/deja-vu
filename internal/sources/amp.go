package sources

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// AmpConfigDir is where Amp keeps settings.json and its system plugins. Amp
// uses the XDG path on every platform — `amp --help` names
// ~/.config/amp/settings.json on macOS too — and honours AMP_SETTINGS_FILE for
// the settings file itself.
func AmpConfigDir() string {
	base := filepath.Join(Home(), ".config")
	if p := os.Getenv("XDG_CONFIG_HOME"); p != "" {
		base = p
	}
	return filepath.Join(base, "amp")
}

// AmpSettingsFile is the file Amp reads its settings from, including the
// amp.mcpServers block. AMP_SETTINGS_FILE moves it, and Amp itself respects
// that variable, so an install has to follow it or write to a file nothing
// reads.
func AmpSettingsFile() string {
	if p := os.Getenv("AMP_SETTINGS_FILE"); p != "" {
		return filepath.Clean(p)
	}
	return filepath.Join(AmpConfigDir(), "settings.json")
}

// AmpRoot returns Amp's thread store root, overridable by deja without
// changing Amp's own environment.
func AmpRoot() string {
	return filepath.Clean(EnvPath("DEJA_AMP_ROOT", filepath.Join(ampDataHome(), "threads")))
}

func ampDataHome() string {
	if p := os.Getenv("XDG_DATA_HOME"); p != "" {
		return filepath.Join(p, "amp")
	}
	return filepath.Join(Home(), ".local", "share", "amp")
}

// AmpThreadsServerSide reports a machine that runs Amp and has no thread file
// to read. Amp builds from 0.0.1774963753 (2026-03-31) on keep threads on
// ampcode.com and use the data directory only for bin, logs and pids, so the
// directory without threads is the current client, not an unused one. Reading
// them needs `amp threads export` and a login, which deja does not do (#4355).
func AmpThreadsServerSide() bool {
	if os.Getenv("DEJA_AMP_ROOT") != "" || len(AmpThreadFiles()) > 0 {
		return false
	}
	fi, err := os.Stat(ampDataHome())
	return err == nil && fi.IsDir()
}

// AmpThreadFiles lists Amp's one-thread-per-JSON files.
func AmpThreadFiles() []string {
	return walkFiles(AmpRoot(), func(p string) bool {
		return strings.HasSuffix(p, ".json")
	})
}

// LoadAmp loads every readable Amp thread.
func LoadAmp() []model.Session {
	return parseFiles(AmpThreadFiles(), ParseAmpFile)
}

type ampThread struct {
	ID      string      `json:"id"`
	Title   string      `json:"title"`
	Created json.Number `json:"created"`
	Env     struct {
		Initial struct {
			Trees []struct {
				URI string `json:"uri"`
			} `json:"trees"`
		} `json:"initial"`
	} `json:"env"`
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
		// Either shape, epoch or ISO: a type the struct did not expect failed
		// the whole thread, and these times are optional.
		Meta struct {
			SentAt any `json:"sentAt"`
		} `json:"meta"`
		Usage struct {
			Timestamp any `json:"timestamp"`
		} `json:"usage"`
	} `json:"messages"`
}

// ampDialect is Amp's tool vocabulary, read off the 0.0.1774959077 bundle: the
// shell is Bash and takes `cmd`, the file tools take `path`, edit_file replaces
// old_str with new_str and create_file writes `content` (#4356).
var ampDialect = toolDialect{
	pathKey: "path",
	pathTools: map[string]bool{
		"Read": true, "read_file": true, "edit_file": true, "create_file": true, "undo_edit": true,
	},
	shellTool:  "Bash",
	commandKey: "cmd",
	editTools:  map[string]bool{"edit_file": true, "create_file": true},
	oldKey:     "old_str",
	newKey:     "new_str",
}

// ParseAmpFile parses one Amp thread. A user turn carries meta.sentAt and an
// assistant turn usage.timestamp; a turn with neither, such as one holding only
// tool results, takes the time of the turn before it, and the first one the
// thread's created time.
func ParseAmpFile(path string) ([]model.Session, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var thread ampThread
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if err := dec.Decode(&thread); err != nil {
		return nil, fmt.Errorf("decode Amp thread %s: %w", path, err)
	}
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode Amp thread %s: trailing data", path)
		}
		return nil, fmt.Errorf("decode Amp thread %s: trailing data: %w", path, err)
	}
	if thread.ID == "" {
		return nil, fmt.Errorf("decode Amp thread %s: missing id", path)
	}
	created := parseTimeAny(thread.Created)
	if created.IsZero() {
		return nil, fmt.Errorf("decode Amp thread %s: invalid created timestamp", path)
	}

	project := thread.Title
	if len(thread.Env.Initial.Trees) > 0 {
		project = ampProject(thread.Env.Initial.Trees[0].URI, project)
	}
	session := model.Session{
		ID:      thread.ID,
		Harness: "amp",
		Project: projectName(project),
		Path:    path,
		Title:   thread.Title,
		Started: created,
		Updated: created,
	}
	ts := created
	exits := commandExits{}
	for _, item := range thread.Messages {
		if item.Role != "user" && item.Role != "assistant" {
			continue
		}
		if t := parseTimeAny(item.Meta.SentAt); !t.IsZero() {
			ts = t
		} else if t := parseTimeAny(item.Usage.Timestamp); !t.IsZero() {
			ts = t
		}
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		_ = json.Unmarshal(item.Content, &blocks)
		var texts []string
		for _, block := range blocks {
			if block.Type == "text" && block.Text != "" {
				texts = append(texts, block.Text)
			}
		}
		if len(texts) > 0 {
			session.Touch(ts)
			session.Messages = append(session.Messages, model.Message{
				Role: item.Role,
				Text: strings.Join(texts, "\n"),
				Time: ts,
			})
		}
		from := len(session.Messages)
		for _, rec := range ampWorkRecords(item.Content, ts) {
			session.Touch(ts)
			session.Messages = append(session.Messages, rec)
		}
		ampJoinExits(session.Messages, from, item.Content, exits)
	}
	if len(session.Messages) == 0 {
		return nil, nil
	}
	return []model.Session{session}, nil
}

// ampWorkRecords turns one message's tool blocks into work records: the files
// a call named, the span an edit replaced and what it wrote, the command it
// ran, and what a run printed.
func ampWorkRecords(raw json.RawMessage, ts time.Time) []model.Message {
	var blocks []any
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var out []model.Message
	if IndexToolPaths() {
		if p := toolPathsIn(blocks, ampDialect); p != "" {
			out = append(out, model.Message{Role: RoleFiles, Text: p, Time: ts})
		}
	}
	if IndexWrites() {
		for _, w := range wroteRecordsIn(blocks, ampDialect) {
			out = append(out, model.Message{Role: RoleWrote, Text: w, Time: ts})
		}
	}
	if IndexEdits() {
		for _, span := range editSpansIn(blocks, ampDialect) {
			out = append(out, model.Message{Role: RoleEdit, Text: span, Time: ts})
		}
	}
	if IndexCommands() {
		for _, cmd := range commandsIn(blocks, ampDialect) {
			out = append(out, model.Message{Role: RoleCommand, Text: cmd, Time: ts})
		}
	}
	if IndexToolOutput() {
		for _, body := range ampToolResults(blocks) {
			out = append(out, model.Message{Role: RoleToolOutput, Text: capParsedMessage(body), Time: ts})
		}
	}
	return out
}

// ampToolResults reads what each run printed. A tool_result holds a run, not
// content: {status, result} where a Bash result is {output, exitCode}, and a
// failed run carries an error instead.
func ampToolResults(blocks []any) []string {
	var out []string
	for _, it := range blocks {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := m["type"].(string); t != "tool_result" {
			continue
		}
		run, _ := m["run"].(map[string]any)
		var body string
		switch r := run["result"].(type) {
		case string:
			body = r
		case map[string]any:
			body, _ = r["output"].(string)
		}
		if body == "" {
			switch e := run["error"].(type) {
			case string:
				body = e
			case map[string]any:
				body, _ = e["message"].(string)
			}
		}
		if body = strings.TrimSpace(body); body != "" {
			out = append(out, body)
		}
	}
	return out
}

// ampJoinExits notes the commands a message's tool calls appended from index
// from on, and stamps those its tool results report on. A finished run's
// result is {output, exitCode}, in the user message after the call; Amp
// writes -1 when the process gave it no code, which says nothing (#4530).
func ampJoinExits(msgs []model.Message, from int, raw json.RawMessage, exits commandExits) {
	if !IndexCommands() || !bytes.Contains(raw, []byte(`"tool_`)) {
		return
	}
	var blocks []any
	if json.Unmarshal(raw, &blocks) != nil {
		return
	}
	exits.note(msgs, from, commandCallsIn(blocks, ampDialect))
	for _, it := range blocks {
		m, _ := it.(map[string]any)
		run, _ := m["run"].(map[string]any)
		if m["type"] != "tool_result" || run["status"] != "done" {
			continue
		}
		res, _ := run["result"].(map[string]any)
		if code, ok := piExitCode(res["exitCode"]); ok && code >= 0 {
			exits.stamp(msgs, str(m["toolUseID"]), "", code)
		}
	}
}

func ampProject(uri, fallback string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" || u.Path == "" {
		return fallback
	}
	return u.Path
}
