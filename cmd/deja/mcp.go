package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
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
				"description": "Recall prior coding-agent sessions matching a query as dense text under about 4KB by default.",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}, "harness": map[string]any{"type": "string"}, "limit": map[string]any{"type": "number"}}, "required": []string{"query"}},
			},
			{
				"name":        "recall_context",
				"description": "Return the deja ctx markdown digest for the best matching prior session.",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}, "required": []string{"query"}},
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
		text, err := recallText(a.Query, a.Harness, a.Limit, 4096)
		if err == nil {
			usage.Record(index.DefaultDir(), usage.KindRecall, len(text))
		}
		return text, err
	case "recall_context":
		var a struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", err
		}
		if strings.TrimSpace(a.Query) == "" {
			return "", fmt.Errorf("query required")
		}
		text, err := recallContext(a.Query)
		if err == nil {
			usage.Record(index.DefaultDir(), usage.KindContext, len(text))
		}
		return text, err
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}

func recallText(q, harness string, limit, budget int) (string, error) {
	if limit <= 0 {
		limit = 5
	}
	o := search.Options{Query: q, Harness: harness, All: true}
	if err := index.EnsureForSearch(index.DefaultDir(), o, false, mcpProgress()); err != nil {
		return "", err
	}
	ss, err := index.SearchWithRecovery(index.DefaultDir(), o, mcpProgress())
	if err != nil {
		return "", err
	}
	hits, err := search.Run(ss, o)
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return fmt.Sprintf("No prior deja sessions matched %q.", q), nil
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	var b strings.Builder
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
		if b.Len() >= budget {
			break
		}
	}
	out := b.String()
	if len(out) > budget {
		out = trimUTF8(out, budget)
	}
	return out, nil
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
	o := search.Options{Query: q, All: true}
	if err := index.EnsureForSearch(index.DefaultDir(), o, false, mcpProgress()); err != nil {
		return "", err
	}
	ss, err := index.SearchWithRecovery(index.DefaultDir(), o, mcpProgress())
	if err != nil {
		return "", err
	}
	hits, err := search.Run(ss, o)
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return fmt.Sprintf("No prior deja sessions matched %q.", q), nil
	}
	var b bytes.Buffer
	search.PrintContext(&b, hits[0].Session, q)
	return b.String(), nil
}

func mcpProgress() io.Writer {
	if os.Getenv("DEJA_DEBUG") == "1" {
		return os.Stderr
	}
	return io.Discard
}
