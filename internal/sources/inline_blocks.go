package sources

import (
	"encoding/json"
	"strings"
)

const inlineToolID = "inline"

type inlineBlock struct {
	typ   string
	text  string
	name  string
	input json.RawMessage
	isErr bool
}

func hasInlineBoundary(rest string) bool {
	return rest == "" || rest[0] == ' ' || rest[0] == '\t' || rest[0] == '\n' || rest[0] == '\r'
}

// parseInlineBlock parses exported inline block markers ([thinking],
// [tool_use Name] {...}, [tool_result]...) used in flat or exported JSONL logs.
func parseInlineBlock(s string) (inlineBlock, bool) {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "[") {
		return inlineBlock{}, false
	}
	if rest, ok := strings.CutPrefix(trimmed, "[thinking]"); ok && hasInlineBoundary(rest) {
		return inlineBlock{typ: "thinking", text: strings.TrimSpace(rest)}, true
	}
	if rest, ok := strings.CutPrefix(trimmed, "[tool_use "); ok {
		closeIdx := strings.IndexByte(rest, ']')
		if closeIdx <= 0 {
			return inlineBlock{}, false
		}
		name := strings.TrimSpace(rest[:closeIdx])
		if name == "" || strings.ContainsAny(name, " \t\r\n") {
			return inlineBlock{}, false
		}
		payload := strings.TrimSpace(rest[closeIdx+1:])
		if len(payload) < 2 || payload[0] != '{' || !json.Valid([]byte(payload)) {
			return inlineBlock{}, false
		}
		return inlineBlock{typ: "tool_use", name: name, input: json.RawMessage(payload)}, true
	}
	if rest, ok := strings.CutPrefix(trimmed, "[tool_result]"); ok && hasInlineBoundary(rest) {
		body := strings.TrimLeft(rest, " \t\r\n")
		isErr := strings.HasPrefix(body, "<tool_use_error>")
		if !isErr {
			if code, hasCode := statusCode(firstLine(body), "Exit code ", ""); hasCode && code != 0 {
				isErr = true
			}
		}
		return inlineBlock{typ: "tool_result", text: body, isErr: isErr}, true
	}
	return inlineBlock{}, false
}

// normalizeInlineClaudeText unpacks exported text markers ([thinking],
// [tool_use Name] {...}, [tool_result]...) into synthetic typed blocks so all
// downstream extractors handle them uniformly without altering native JSON logs.
func normalizeInlineClaudeText(s string) (json.RawMessage, bool) {
	blk, ok := parseInlineBlock(s)
	if !ok {
		return nil, false
	}
	switch blk.typ {
	case "thinking":
		b, err := json.Marshal(struct {
			Type     string `json:"type"`
			Thinking string `json:"thinking"`
		}{
			Type:     "thinking",
			Thinking: blk.text,
		})
		return b, err == nil
	case "tool_use":
		b, err := json.Marshal(struct {
			Type  string          `json:"type"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}{
			Type:  "tool_use",
			ID:    inlineToolID,
			Name:  blk.name,
			Input: blk.input,
		})
		return b, err == nil
	case "tool_result":
		b, err := json.Marshal(struct {
			Type      string `json:"type"`
			ToolUseID string `json:"tool_use_id"`
			IsError   bool   `json:"is_error,omitempty"`
			Content   string `json:"content"`
		}{
			Type:      "tool_result",
			ToolUseID: inlineToolID,
			IsError:   blk.isErr,
			Content:   blk.text,
		})
		return b, err == nil
	}
	return nil, false
}

// unpackContentBlocks extracts normalized tool_use/tool_result blocks, tool
// output bodies, and human/assistant speech from string or array content,
// unpacking exported inline markers alongside structured JSON blocks.
func unpackContentBlocks(v any) (blocks []any, results []string, speech string, onlyTool bool) {
	var speechParts []string
	var sawTool, sawSpeech bool

	handleText := func(txt string) {
		if txt == "" {
			return
		}
		blk, ok := parseInlineBlock(txt)
		if !ok {
			sawSpeech = true
			speechParts = append(speechParts, txt)
			return
		}
		switch blk.typ {
		case "thinking":
			return
		case "tool_use":
			var in map[string]any
			if json.Unmarshal(blk.input, &in) == nil && in != nil {
				blocks = append(blocks, map[string]any{
					"type":  "tool_use",
					"id":    inlineToolID,
					"name":  blk.name,
					"input": in,
				})
			}
		case "tool_result":
			sawTool = true
			m := map[string]any{
				"type":        "tool_result",
				"tool_use_id": inlineToolID,
				"content":     blk.text,
			}
			if blk.isErr {
				m["is_error"] = true
			}
			blocks = append(blocks, m)
			if blk.text != "" {
				results = append(results, blk.text)
			}
		}
	}

	switch c := v.(type) {
	case string:
		handleText(c)
	case []any:
		for _, it := range c {
			if s, ok := it.(string); ok {
				handleText(s)
				continue
			}
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			typ, _ := m["type"].(string)
			switch typ {
			case "thinking":
				continue
			case "tool_use":
				blocks = append(blocks, m)
			case "tool_result":
				sawTool = true
				blocks = append(blocks, m)
				txt := contentText(m["content"])
				if txt == "" {
					txt, _ = m["text"].(string)
				}
				if txt != "" {
					results = append(results, txt)
				}
			case "", "text":
				txt, _ := m["text"].(string)
				if txt == "" {
					txt = contentText(m["content"])
				}
				handleText(txt)
			default:
				txt, _ := m["text"].(string)
				if txt == "" {
					txt = contentText(m["content"])
				}
				if txt != "" {
					sawSpeech = true
					speechParts = append(speechParts, txt)
				}
			}
		}
	}
	return blocks, results, strings.Join(speechParts, "\n"), sawTool && !sawSpeech
}

func filterInlineSpeechText(s string) (string, bool) {
	blk, ok := parseInlineBlock(s)
	if !ok {
		return s, s != ""
	}
	switch blk.typ {
	case "thinking", "tool_use":
		return "", false
	case "tool_result":
		if !IndexToolOutput() {
			return "", false
		}
		return blk.text, blk.text != ""
	}
	return s, s != ""
}
