package sources

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// GrokHome is where Grok Build itself keeps state; GROK_HOME relocates the
// whole tree (config.toml, sessions). Install and doctor use it.
func GrokHome() string {
	return EnvPath("GROK_HOME", filepath.Join(Home(), ".grok"))
}

// GrokRoot is the session-reading root; DEJA_GROK_ROOT overrides it without
// affecting where install writes.
func GrokRoot() string {
	return EnvPath("DEJA_GROK_ROOT", GrokHome())
}

// Grok Build stores sessions by URL-encoded working directory. summary.json
// carries metadata and updates.jsonl is the authoritative ACP conversation
// stream. Grok can truncate and regrow the stream after a rewind, which looks
// like growth from the outside — the index compares the recorded prefix hash
// before resuming a parse, and reparses in full when it moved.

func GrokSessionFiles() []string {
	return walkFiles(filepath.Join(GrokRoot(), "sessions"), func(p string) bool {
		return filepath.Base(p) == "updates.jsonl"
	})
}

func LoadGrok() []model.Session {
	return parseFiles(GrokSessionFiles(), ParseGrokFile)
}

type grokSummary struct {
	Info struct {
		ID  string `json:"id"`
		CWD string `json:"cwd"`
	} `json:"info"`
	SessionSummary string `json:"session_summary"`
	GeneratedTitle string `json:"generated_title"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	// Grok Build records the spawn edge itself: a forked child names the
	// session it came from, a plain subagent says only what it is. deja reads
	// what is written and invents nothing (#1385).
	SessionKind     string `json:"session_kind"`
	ParentSessionID string `json:"parent_session_id"`
	AgentName       string `json:"agent_name"`
}

func ParseGrokFile(path string) ([]model.Session, error) {
	return parseGrokFileFromOffset(path, 0)
}

// ParseGrokFileFromOffset reads only the bytes appended past offset. A live
// session's updates.jsonl grows all day and the index used to reparse and
// rewrite the whole thing on every touch (#1522); the ingest layer only calls
// this once the recorded prefix hash still matches, so a rewind that truncates
// and regrows still goes the long way.
func ParseGrokFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parseGrokFileFromOffset(path, offset)
}

func parseGrokFileFromOffset(path string, offset int64) ([]model.Session, error) {
	doc := readGrokSummary(path)
	id := doc.Info.ID
	if id == "" {
		id = filepath.Base(filepath.Dir(path))
	}
	cwd := doc.Info.CWD
	if cwd == "" {
		cwd = grokCWDFromPath(path)
	}
	title := doc.GeneratedTitle
	if title == "" {
		title = doc.SessionSummary
	}
	s := model.Session{ID: id, Harness: "grok", Project: projectName(cwd), Path: path, Title: title,
		Kind: doc.SessionKind, Parent: doc.ParentSessionID, Agent: doc.AgentName}
	if t, err := time.Parse(time.RFC3339Nano, doc.CreatedAt); err == nil {
		s.Touch(t)
	}
	if t, err := time.Parse(time.RFC3339Nano, doc.UpdatedAt); err == nil {
		s.Touch(t)
	}

	// A streamed reply arrives in chunks that share a key and get joined. Tool
	// records now land between those chunks, so the join remembers where the
	// speech was rather than assuming it was the message just added.
	lastKey, lastSpeech := "", -1
	err := scanGrokUpdatesFrom(path, offset, func(event grokUpdateEvent) {
		role := ""
		switch event.Params.Update.Kind {
		case "user_message_chunk":
			role = "user"
		case "agent_message_chunk":
			role = "assistant"
		case "tool_call", "tool_call_update":
			// What the agent did, which deja indexes for every other harness
			// that records it: `--role tool`, friction and the fix pairs all
			// read this. Grok kept the whole run in the transcript and the
			// parser took only the talk, so a Grok user searching `--role tool`
			// got nothing and `show` returned a conversation that looked
			// complete (#1321). Same shape as the pi parser before #1240.
			role = RoleToolOutput
		default:
			return
		}
		text := grokContentText(event.Params.Update.Content)
		if text == "" && role == RoleToolOutput {
			text = grokToolText(event.Params.Update.Content)
		}
		if text == "" && role == RoleToolOutput {
			// A call with no output yet still says what ran.
			text = grokToolTitle(event)
		}
		if text == "" {
			return
		}
		t := parseTimeAny(event.Timestamp)
		if t.IsZero() {
			t = parseTimeAny(event.Params.Meta.AgentTimestamp)
		}
		s.Touch(t)

		if role == RoleToolOutput {
			s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: t})
			return
		}
		key := grokMessageKey(role, event)
		if key != "" && key == lastKey && lastSpeech >= 0 {
			s.Messages[lastSpeech].Text += text
			return
		}
		s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: t})
		lastKey, lastSpeech = key, len(s.Messages)-1
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}

type grokUpdateEvent struct {
	Timestamp json.Number `json:"timestamp"`
	Params    struct {
		Update struct {
			Kind     string          `json:"sessionUpdate"`
			Content  json.RawMessage `json:"content"`
			Title    string          `json:"title"`
			ToolKind string          `json:"kind"`
			Meta     struct {
				PromptIndex *int `json:"promptIndex"`
			} `json:"_meta"`
		} `json:"update"`
		Meta struct {
			PromptID       string      `json:"promptId"`
			AgentTimestamp json.Number `json:"agentTimestampMs"`
		} `json:"_meta"`
	} `json:"params"`
}

func grokMessageKey(role string, event grokUpdateEvent) string {
	if role == "assistant" {
		if event.Params.Meta.PromptID != "" {
			return role + ":" + event.Params.Meta.PromptID
		}
		return ""
	}
	if event.Params.Update.Meta.PromptIndex != nil {
		return role + ":" + strconv.Itoa(*event.Params.Update.Meta.PromptIndex)
	}
	return ""
}

// grokToolTitle names the call when it carried no output: "bash" reads as work
// done, an empty record reads as nothing happening.
func grokToolTitle(event grokUpdateEvent) string {
	title := strings.TrimSpace(event.Params.Update.Title)
	kind := strings.TrimSpace(event.Params.Update.ToolKind)
	switch {
	case title != "" && kind != "":
		return kind + ": " + title
	case title != "":
		return title
	default:
		return kind
	}
}

// grokToolText reads the shape ACP uses for a tool call's output, which wraps
// each block one level deeper than a spoken message:
//
//	[{"type":"content","content":{"type":"text","text":"..."}}]
//
// Kept next to the Grok parser rather than folded into the shared extractors:
// those are read by every other harness, and this nesting is this protocol's.
func grokToolText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var blocks []struct {
		Text    string `json:"text"`
		Content struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var b strings.Builder
	for _, block := range blocks {
		txt := block.Text
		if txt == "" && (block.Content.Type == "" || block.Content.Type == "text") {
			txt = block.Content.Text
		}
		if txt == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(txt)
	}
	return b.String()
}

func grokContentText(raw json.RawMessage) string {
	var block struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &block) == nil && block.Text != "" {
		if block.Type == "" || block.Type == "text" {
			return block.Text
		}
		return ""
	}
	var v any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	_ = d.Decode(&v)
	return textFromContent(v)
}

var grokUserChunk = []byte(`"user_message_chunk"`)
var grokAgentChunk = []byte(`"agent_message_chunk"`)
var grokToolCall = []byte(`"sessionUpdate":"tool_call"`)
var grokToolUpdate = []byte(`"sessionUpdate":"tool_call_update"`)

// Most update lines are large tool events. Filter them before JSON decoding so
// indexing cost is dominated by reading the log rather than materializing tool
// payloads that will be discarded.
func scanGrokUpdates(path string, fn func(grokUpdateEvent)) error {
	return scanGrokUpdatesFrom(path, 0, fn)
}

func scanGrokUpdatesFrom(path string, offset int64, fn func(grokUpdateEvent)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return err
		}
	}
	r := bufio.NewReaderSize(f, 1024*1024)
	for {
		line, err := r.ReadBytes('\n')
		if bytes.Contains(line, grokUserChunk) || bytes.Contains(line, grokAgentChunk) || bytes.Contains(line, grokToolCall) || bytes.Contains(line, grokToolUpdate) {
			var event grokUpdateEvent
			if json.Unmarshal(line, &event) == nil {
				fn(event)
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func readGrokSummary(updatePath string) grokSummary {
	var doc grokSummary
	b, err := os.ReadFile(filepath.Join(filepath.Dir(updatePath), "summary.json"))
	if err == nil {
		_ = json.Unmarshal(b, &doc)
	}
	return doc
}

// GrokCWDForSession recovers the working directory needed by grok --resume.
func GrokCWDForSession(updatePath string) string {
	if updatePath == "" {
		return ""
	}
	if cwd := readGrokSummary(updatePath).Info.CWD; cwd != "" {
		return cwd
	}
	return grokCWDFromPath(updatePath)
}

func grokCWDFromPath(updatePath string) string {
	if updatePath == "" {
		return ""
	}
	group := filepath.Dir(filepath.Dir(updatePath))
	if b, err := os.ReadFile(filepath.Join(group, ".cwd")); err == nil {
		if cwd := strings.TrimSpace(string(b)); cwd != "" {
			return cwd
		}
	}
	cwd, err := url.PathUnescape(filepath.Base(group))
	if err != nil {
		return ""
	}
	return cwd
}
