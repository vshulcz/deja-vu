package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/usage"
)

const mcpProtocolVersion = "2024-11-05"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func serveMCP(r io.Reader, w io.Writer) error {
	s := bufio.NewScanner(r)
	// MCP messages are line-delimited JSON here. Allow large, but bounded, client lines.
	s.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	enc := json.NewEncoder(w)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			writeRPCError(enc, nil, -32700, "parse error")
			continue
		}
		if req.ID == nil {
			// Notification. Do not reply.
			continue
		}
		result, code, msg := handleMCP(req)
		if code != 0 {
			writeRPCError(enc, req.ID, code, msg)
			continue
		}
		if err := enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}); err != nil {
			return err
		}
	}
	if err := s.Err(); err != nil {
		// Oversized or malformed client frames are reported as JSON-RPC parse errors,
		// then the stdio server exits gracefully instead of silently hard-stopping.
		writeRPCError(enc, nil, -32700, "parse error")
		if os.Getenv("DEJA_DEBUG") == "1" {
			fmt.Fprintf(os.Stderr, "deja mcp scanner error: %v\n", err)
		}
	}
	return nil
}

func handleMCP(req rpcRequest) (any, int, string) {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "deja", "version": version},
		}, 0, ""
	case "tools/list":
		return map[string]any{"tools": []map[string]any{
			{
				"name":        "recall",
				"description": "Search past coding-agent sessions and return the best matches as dense text under ~4KB. Call before debugging or re-implementing: use a specific error string, function name, or flag. Optionally filter by harness (claude, codex, opencode, aider, gemini, cursor, antigravity, grok, qwen).",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string", "description": "Search terms; specific tokens (error strings, function names, flags) match best. Multiple words are ANDed."}, "harness": map[string]any{"type": "string", "description": "Optional filter: claude, codex, opencode, aider, gemini, cursor, antigravity, grok or qwen."}, "limit": map[string]any{"type": "number", "description": "Max sessions to return (default 5)."}}, "required": []string{"query"}},
			},
			{
				"name":        "recall_context",
				"description": "Return a markdown digest (~8KB) of the best prior session. Call before debugging or re-implementing; query with an error string, function name, or flag. Optionally filter by harness (claude, codex, opencode, aider, gemini, cursor, antigravity, grok, qwen).",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string", "description": "Search terms identifying the session to digest."}, "harness": map[string]any{"type": "string", "description": "Optional harness filter."}}, "required": []string{"query"}},
			},
		}}, 0, ""
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, -32602, "invalid params"
		}
		text, err := callMCPTool(p.Name, p.Arguments)
		if err != nil {
			return nil, -32602, err.Error()
		}
		return toolText(text), 0, ""
	default:
		return nil, -32601, "method not found"
	}
}

func writeRPCError(enc *json.Encoder, id any, code int, msg string) {
	_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}})
}

func toolText(text string) map[string]any {
	return map[string]any{"content": []map[string]string{{"type": "text", "text": text}}}
}

func callMCPTool(name string, raw json.RawMessage) (string, error) {
	switch name {
	case "recall":
		var a struct {
			Query   string `json:"query"`
			Harness string `json:"harness"`
			Limit   int    `json:"limit"`
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", err
		}
		if strings.TrimSpace(a.Query) == "" {
			return "", fmt.Errorf("query required")
		}
		text, sessions, err := recallTextResult(a.Query, a.Harness, a.Limit, 4096)
		if err == nil {
			usage.RecordResult(index.DefaultDir(), usage.KindRecall, len(text), sessions, sessions == 0)
		}
		return text, err
	case "recall_context":
		var a struct {
			Query   string `json:"query"`
			Harness string `json:"harness"`
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", err
		}
		if strings.TrimSpace(a.Query) == "" {
			return "", fmt.Errorf("query required")
		}
		text, sessions, err := recallContextResult(a.Query, a.Harness)
		if err == nil {
			usage.RecordResult(index.DefaultDir(), usage.KindContext, len(text), sessions, sessions == 0)
		}
		return text, err
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}

func recallText(q, harness string, limit, budget int) (string, error) {
	text, _, err := recallTextResult(q, harness, limit, budget)
	return text, err
}

func recallTextResult(q, harness string, limit, budget int) (string, int, error) {
	if limit <= 0 {
		limit = 5
	}
	o := search.Options{Query: q, Harness: harness, All: true}
	if err := index.EnsureForSearch(index.DefaultDir(), o, false, mcpProgress()); err != nil {
		return "", 0, err
	}
	result, err := index.SearchWithRecoveryDetailed(index.DefaultDir(), o, mcpProgress())
	if err != nil {
		return "", 0, err
	}
	ss := result.Sessions
	if result.Fuzzy {
		o.FuzzyVariants = result.Variants
	}
	hits, err := search.Run(ss, o)
	if err != nil {
		return "", 0, err
	}
	if len(hits) == 0 {
		return fmt.Sprintf("No prior deja sessions matched %q.", q), 0, nil
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	var b strings.Builder
	served := 0
	if result.Fuzzy {
		fmt.Fprintf(&b, "No exact match; using close spellings: %s\n", strings.Join(fuzzySummary(result.Variants), ", "))
	}
	fmt.Fprintf(&b, "deja recall for %q (%d match(es))\n", q, len(hits))
	for i, h := range hits {
		fmt.Fprintf(&b, "\n%d. [%s] %s · %s · %d matches", i+1, h.Session.Harness, h.Session.Project, h.Session.ID, h.Count)
		if !h.Session.Updated.IsZero() {
			fmt.Fprintf(&b, " · updated %s", h.Session.Updated.Format("2006-01-02"))
		}
		fmt.Fprintln(&b)
		for _, sn := range h.Snippets {
			fmt.Fprintf(&b, "- %s\n", sn)
		}
		served++
		if b.Len() >= budget {
			break
		}
	}
	out := b.String()
	if len(out) > budget {
		out = trimUTF8(out, budget)
	}
	return out, served, nil
}

func trimUTF8(s string, budget int) string {
	if len(s) <= budget {
		return s
	}
	for budget > 0 && !utf8.RuneStart(s[budget]) {
		budget--
	}
	return s[:budget]
}

func recallContext(q string) (string, error) {
	text, _, err := recallContextResult(q, "")
	return text, err
}

func recallContextResult(q, harness string) (string, int, error) {
	o := search.Options{Query: q, Harness: harness, All: true}
	if err := index.EnsureForSearch(index.DefaultDir(), o, false, mcpProgress()); err != nil {
		return "", 0, err
	}
	result, err := index.SearchWithRecoveryDetailed(index.DefaultDir(), o, mcpProgress())
	if err != nil {
		return "", 0, err
	}
	ss := result.Sessions
	if result.Fuzzy {
		o.FuzzyVariants = result.Variants
	}
	hits, err := search.Run(ss, o)
	if err != nil {
		return "", 0, err
	}
	if len(hits) == 0 {
		return fmt.Sprintf("No prior deja sessions matched %q.", q), 0, nil
	}
	var b bytes.Buffer
	search.PrintContext(&b, hits[0].Session, q)
	return b.String(), 1, nil
}

func mcpProgress() io.Writer {
	if os.Getenv("DEJA_DEBUG") == "1" {
		return os.Stderr
	}
	return io.Discard
}

func fuzzySummary(variants map[string][]string) []string {
	var out []string
	for token, values := range variants {
		for _, value := range values {
			if value != token {
				out = append(out, token+" -> "+value)
			}
		}
	}
	sort.Strings(out)
	return out
}
