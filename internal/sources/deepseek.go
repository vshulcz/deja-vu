package sources

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// DeepSeek Harness (`dsh`) keeps one append-only log per session under
// $DSH_HOME/sessions/<workspace-slug>/session-<uuid>/session.jsonl.zstd. The
// file is a JSONL stream written as consecutive zstd frames by default, with
// raw lines available as a configuration; both are read here, chosen by the
// extension the harness wrote.
//
// The first line is the session header (id, createdAt, cwd). Every line after
// it is one event: {type, seq, time, data}. Three of those types carry the
// conversation.
//
// A person's turn is `user/message` with data.source.kind == "user". The same
// type also carries what plugins splice in — the sandbox policy snapshot, the
// skill catalogue — under a different source kind, and those are the harness
// talking to itself, not history worth recalling.
//
// The agent's turn is written twice. It streams as `assistant/chunk` deltas —
// and long runs of those are packed into rows of another type entirely, so the
// stream alone is not readable without unpacking it — and then lands complete
// in one `assistant/message` whose data.message.content is a block array. The
// complete one is what deja reads; the deltas are the fallback for a run that
// was interrupted before it landed.
//
// Reasoning blocks in that array are the model thinking out loud, not what it
// told the person, so they stay out of the transcript.
//
// Tool output is its own event, `tool/result`, whose content nests a
// tool-result block around the text.
//
// Verified against dsh 0.1.1-rc.2 driven by a local model, over sessions that
// answered, called a tool, and failed before answering.

// DSHHome is the harness's own home directory, following its DSH_HOME variable.
func DSHHome() string {
	if p := os.Getenv("DSH_HOME"); p != "" {
		return p
	}
	return filepath.Join(Home(), ".dsh")
}

// DeepSeekRoot is where dsh keeps session logs. DEJA_DEEPSEEK_ROOT relocates
// the read for tests and for a relocated install.
func DeepSeekRoot() string {
	return EnvPath("DEJA_DEEPSEEK_ROOT", filepath.Join(DSHHome(), "sessions"))
}

func DeepSeekSessionFiles() []string {
	return walkFiles(DeepSeekRoot(), func(p string) bool {
		base := filepath.Base(p)
		return base == "session.jsonl" || base == "session.jsonl.zstd"
	})
}

func LoadDeepSeek() []model.Session {
	return parseFiles(DeepSeekSessionFiles(), ParseDeepSeekFile)
}

func ParseDeepSeekFile(path string) ([]model.Session, error) {
	raw, err := readDeepSeekLog(path)
	if err != nil {
		return nil, err
	}
	s := model.Session{
		Harness: "deepseek",
		ID:      strings.TrimPrefix(filepath.Base(filepath.Dir(path)), "session-"),
		Project: "-",
		Path:    path,
	}
	// Deltas of the step being streamed, kept only until that step's complete
	// message arrives; a step that ends without one was interrupted, and then
	// the deltas are all there is.
	var pending []string
	var pendingAt time.Time
	flush := func() {
		text := strings.TrimSpace(strings.Join(pending, ""))
		pending = nil
		if text == "" {
			return
		}
		// The answer is stamped with the last delta that made it: a message
		// without a time sorts to the beginning of history and is dropped by
		// every consumer that filters by date.
		s.Messages = append(s.Messages, model.Message{Role: "assistant", Text: text, Time: pendingAt})
	}
	for _, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var e map[string]any
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		typ, _ := e["type"].(string)
		data, _ := e["data"].(map[string]any)
		at := parseTimeAny(e["time"])
		switch typ {
		case "session":
			if id, _ := e["id"].(string); id != "" {
				s.ID = strings.TrimPrefix(id, "session-")
			}
			if cwd, _ := e["cwd"].(string); cwd != "" {
				s.Project = projectName(cwd)
			}
			s.Touch(parseTimeAny(e["createdAt"]))
		case "session/title":
			// The last one wins. dsh names a session twice — a stand-in it cuts
			// out of the opening message, then the real one when its title
			// model answers — and the log is append-only, so the latest event
			// is the name the harness is showing. Keeping the first listed
			// every dsh session under a sentence cut mid-phrase (#2551).
			if title, _ := data["title"].(string); title != "" {
				s.Title = title
			}
		case "user/message":
			if !deepSeekSpokenByUser(data) {
				continue
			}
			flush()
			if text := deepSeekContentText(data["content"]); text != "" {
				s.Messages = append(s.Messages, model.Message{Role: "user", Text: text, Time: at})
				s.Touch(at)
			}
		case "assistant/chunk":
			chunk, _ := data["chunk"].(map[string]any)
			if kind, _ := chunk["type"].(string); kind == "text-delta" {
				if text, _ := chunk["text"].(string); text != "" {
					pending = append(pending, text)
					pendingAt = at
					s.Touch(at)
				}
			}
		case "text-chunks":
			// A run of three or more consecutive deltas is stored as one packed
			// row rather than as the events themselves, so a reader that knows
			// only `assistant/chunk` loses exactly the long answers.
			at := parseTimeAny(e["time0"])
			for _, part := range deepSeekPackedTexts(data["texts"]) {
				pending = append(pending, part)
				pendingAt = at
				s.Touch(at)
			}
		case "assistant/message":
			// The complete message supersedes whatever of it was streamed.
			pending = nil
			msg, _ := data["message"].(map[string]any)
			if text := deepSeekContentText(msg["content"]); text != "" {
				s.Messages = append(s.Messages, model.Message{Role: "assistant", Text: text, Time: at})
				s.Touch(at)
			}
		case "step/end", "turn/end":
			flush()
		case "tool/result":
			flush()
			msg, _ := data["message"].(map[string]any)
			if text := deepSeekToolText(msg["content"]); text != "" {
				s.Messages = append(s.Messages, model.Message{Role: "tool-output", Text: text, Time: at})
				s.Touch(at)
			}
		}
	}
	flush()
	if len(s.Messages) == 0 {
		return nil, nil
	}
	return []model.Session{s}, nil
}

// deepSeekSpokenByUser separates what a person typed from what a plugin spliced
// into the same event type. Without it every session opens on the sandbox
// policy snapshot and the skill catalogue, which is the harness describing
// itself.
func deepSeekSpokenByUser(data map[string]any) bool {
	source, _ := data["source"].(map[string]any)
	kind, _ := source["kind"].(string)
	return kind == "user"
}

func deepSeekContentText(v any) string {
	parts, ok := v.([]any)
	if !ok {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
		return ""
	}
	var out []string
	for _, p := range parts {
		block, ok := p.(map[string]any)
		if !ok {
			continue
		}
		// "reasoning" is the model thinking out loud; "text" is what it said.
		if t, _ := block["type"].(string); t != "" && t != "text" {
			continue
		}
		if text, _ := block["text"].(string); text != "" {
			out = append(out, text)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// deepSeekPackedTexts reads the texts of a packed chunk row. The row keeps the
// deltas verbatim in order; the timings beside them reconstruct each member's
// own clock, which a transcript does not need.
func deepSeekPackedTexts(v any) []string {
	parts, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if text, _ := p.(string); text != "" {
			out = append(out, text)
		}
	}
	return out
}

// deepSeekToolText unwraps tool output, which nests one block array inside
// another: the outer block says which call this answers, the inner one carries
// the text the tool printed.
func deepSeekToolText(v any) string {
	blocks, ok := v.([]any)
	if !ok {
		return deepSeekContentText(v)
	}
	var out []string
	for _, b := range blocks {
		block, ok := b.(map[string]any)
		if !ok {
			continue
		}
		if text := deepSeekContentText(block["content"]); text != "" {
			out = append(out, text)
			continue
		}
		if text, _ := block["text"].(string); text != "" {
			out = append(out, text)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// readDeepSeekLog returns the log as plain JSONL. The default encoding is a
// chain of zstd frames, and deja carries no Go dependencies, so the frames go
// through the same `zstd` CLI the Zed store already needs — see SkipReason for
// what a machine without it is told.
func readDeepSeekLog(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(path, ".zstd") {
		return raw, nil
	}
	if len(raw) == 0 {
		return nil, nil
	}
	cmd := exec.Command("zstd", "-d", "-c", "-q")
	cmd.Stdin = bytes.NewReader(raw)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("deepseek: zstd -d %s: %w: %s", filepath.Base(path), err,
			strings.TrimSpace(errBuf.String()))
	}
	return out.Bytes(), nil
}
