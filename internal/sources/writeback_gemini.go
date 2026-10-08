package sources

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Gemini CLI: tmp/<project>/chats/session-<time>-<id8>.jsonl, a header line
// carrying the session id and the sha256 of the directory it ran in, then one
// line per turn. A .json session from an older Gemini goes back as the single
// document those were. `gemini --resume <id>` lists only the chats of the
// project it is run from, which the header's projectHash names.
func init() {
	registerWriteBack("gemini", geminiWriteBackRoots, renderGemini)
}

func geminiWriteBackRoots() []string {
	return []string{filepath.Join(GeminiRoot(), "tmp")}
}

var geminiHashDir = regexp.MustCompile(`^[0-9a-f]{64}$`)

func renderGemini(s model.Session, turns []model.Message) (string, []byte, error) {
	if s.Kind == "subagent" {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "session is a sub-agent run filed under its parent's chat, which Gemini CLI does not reopen on its own"}
	}
	if filepath.Base(filepath.Dir(s.Path)) != "chats" || !strings.HasSuffix(s.Path, ".jsonl") && !strings.HasSuffix(s.Path, ".json") {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "the index has no chats/session-….jsonl path for this session"}
	}
	hash := ""
	if dir := GeminiProjectDir(s.Path); dir != "" {
		sum := sha256.Sum256([]byte(dir))
		hash = hex.EncodeToString(sum[:])
	} else if id := filepath.Base(geminiIDDir(s.Path)); geminiHashDir.MatchString(id) {
		hash = id
	}
	if hash == "" {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "neither .project_root nor projects.json names the directory this session ran in, and Gemini CLI files a session under that directory's hash"}
	}
	start := turns[0].Time
	if !s.Started.IsZero() && s.Started.Before(start) {
		start = s.Started
	}
	last := turns[len(turns)-1].Time
	msgs := make([]any, 0, len(turns))
	for i, m := range turns {
		rec := map[string]any{
			"id":        writeBackUUID(s.ID, i),
			"timestamp": writeBackStamp(m.Time),
		}
		if m.Role == "user" {
			rec["type"] = "user"
			rec["content"] = []any{map[string]any{"text": m.Text}}
		} else {
			rec["type"] = "gemini"
			rec["content"] = m.Text
		}
		msgs = append(msgs, rec)
	}
	head := map[string]any{
		"sessionId":   s.ID,
		"projectHash": hash,
		"startTime":   writeBackStamp(start),
		"lastUpdated": writeBackStamp(last),
	}
	if strings.HasSuffix(s.Path, ".json") {
		head["messages"] = msgs
		data, err := json.MarshalIndent(head, "", "  ")
		return s.Path, append(data, '\n'), err
	}
	head["kind"] = "main"
	data, err := jsonLines(append([]any{head}, msgs...))
	return s.Path, data, err
}
