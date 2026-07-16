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

// Grok Build stores sessions by URL-encoded working directory. summary.json
// carries metadata and updates.jsonl is the authoritative ACP conversation
// stream. Grok can truncate and regrow the stream after a rewind, so changed
// files are always reparsed in full.

func GrokRoot() string {
	if root := os.Getenv("DEJA_GROK_ROOT"); root != "" {
		return root
	}
	return EnvPath("GROK_HOME", filepath.Join(Home(), ".grok"))
}

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
}

func ParseGrokFile(path string) ([]model.Session, error) {
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
	s := model.Session{ID: id, Harness: "grok", Project: projectName(cwd), Path: path, Title: title}
	if t, err := time.Parse(time.RFC3339Nano, doc.CreatedAt); err == nil {
		s.Touch(t)
	}
	if t, err := time.Parse(time.RFC3339Nano, doc.UpdatedAt); err == nil {
		s.Touch(t)
	}

	lastKey := ""
	err := scanGrokUpdates(path, func(event grokUpdateEvent) {
		role := ""
		switch event.Params.Update.Kind {
		case "user_message_chunk":
			role = "user"
		case "agent_message_chunk":
			role = "assistant"
		default:
			return
		}
		text := grokContentText(event.Params.Update.Content)
		if text == "" {
			return
		}
		t := parseTimeAny(event.Timestamp)
		if t.IsZero() {
			t = parseTimeAny(event.Params.Meta.AgentTimestamp)
		}
		s.Touch(t)

		key := grokMessageKey(role, event)
		if key != "" && key == lastKey && len(s.Messages) > 0 {
			s.Messages[len(s.Messages)-1].Text += text
			return
		}
		s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: t})
		lastKey = key
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
			Kind    string          `json:"sessionUpdate"`
			Content json.RawMessage `json:"content"`
			Meta    struct {
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

// Most update lines are large tool events. Filter them before JSON decoding so
// indexing cost is dominated by reading the log rather than materializing tool
// payloads that will be discarded.
func scanGrokUpdates(path string, fn func(grokUpdateEvent)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1024*1024)
	for {
		line, err := r.ReadBytes('\n')
		if bytes.Contains(line, grokUserChunk) || bytes.Contains(line, grokAgentChunk) {
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
