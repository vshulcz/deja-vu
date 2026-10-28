package main

import (
	"encoding/json"
	"maps"
	"sort"
	"strings"
	"time"
)

// mcpLateMinRoom is the least room a kept answer needs to ride on a reply. A
// header and frame with a line or two of page is all that fits below it, and
// the answer waits for the next reply instead.
const mcpLateMinRoom = 1024

// withLateLocked puts the answers of other recalls that finished after their
// deadline note on top of a tools/call reply, each once. Only answers that
// came from sessions go out: a miss or a building note told the agent nothing
// it needs to read twice. The reply as a whole stays within the largest page
// a deja call returns.
func (s *mcpServer) withLateLocked(key string, result any) any {
	if s.contextOnly || !strings.HasPrefix(key, "tools/call\x00") {
		return result
	}
	m, ok := result.(map[string]any)
	if !ok || m["isError"] != nil {
		return result
	}
	content, ok := m["content"].([]map[string]string)
	if !ok || len(content) == 0 || content[0]["type"] != "text" {
		return result
	}
	var keys []string
	for k, l := range s.late {
		if k != key && l.served > 0 && l.code == 0 && time.Since(l.at) < mcpLateTTL {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return result
	}
	sort.Slice(keys, func(i, j int) bool { return s.late[keys[i]].at.Before(s.late[keys[j]].at) })
	const sep = "\n\n"
	text := content[0]["text"]
	var blocks []string
	used := len(text)
	for _, k := range keys {
		l := s.late[k]
		page := mcpResultText(l.result)
		if page == "" {
			continue
		}
		room := contextMCPBudget - used - len(sep)
		if room < mcpLateMinRoom {
			break
		}
		head := "deja: earlier answer for " + l.label + ", which finished after that call was answered with a wait note:"
		block := fitFramed(append([]string{head}, mcpPageParts(page)...), "\n", room)
		if !strings.Contains(block, "\n") {
			continue
		}
		blocks = append(blocks, block)
		used += len(block) + len(sep)
		delete(s.late, k)
	}
	if len(blocks) == 0 {
		return result
	}
	out := maps.Clone(m)
	c := make([]map[string]string, len(content))
	copy(c, content)
	first := maps.Clone(content[0])
	first["text"] = strings.Join(blocks, sep) + sep + text
	c[0] = first
	out["content"] = c
	return out
}

// mcpPageParts splits a page into the note deja put above its frame and the
// frame, so a cut to fit closes the frame rather than running through it.
func mcpPageParts(page string) []string {
	at := -1
	for _, h := range []string{recallRuleFrameHeader, recallFrameHeader} {
		if i := strings.Index(page, h); i >= 0 && (at < 0 || i < at) {
			at = i
		}
	}
	if at <= 0 {
		return []string{page}
	}
	return []string{strings.TrimRight(page[:at], "\n"), page[at:]}
}

// mcpResultText is the text of a tool reply built by toolText.
func mcpResultText(result any) string {
	m, ok := result.(map[string]any)
	if !ok || m["isError"] != nil {
		return ""
	}
	content, ok := m["content"].([]map[string]string)
	if !ok || len(content) == 0 {
		return ""
	}
	return content[0]["text"]
}

// mcpLateLabel names a tool call by its mode and query, the way the agent
// wrote them.
func mcpLateLabel(req rpcRequest) string {
	var p struct {
		Name      string `json:"name"`
		Arguments struct {
			Mode  string `json:"mode"`
			Query string `json:"query"`
			Q     string `json:"q"`
		} `json:"arguments"`
	}
	_ = json.Unmarshal(req.Params, &p)
	what := strings.TrimSpace(p.Arguments.Mode)
	if what == "" || p.Name != "deja" {
		what = p.Name
	}
	q := p.Arguments.Query
	if strings.TrimSpace(q) == "" {
		q = p.Arguments.Q
	}
	if strings.TrimSpace(q) == "" {
		return what
	}
	return what + " " + `"` + clampEcho(q) + `"`
}
