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
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/usage"
)

const mcpProtocolVersion = "2024-11-05"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// isNotification reports whether a request omitted id (a JSON-RPC notification,
// which must get no reply). A literal null id counts as absent too.
func isNotification(id json.RawMessage) bool {
	return len(id) == 0 || string(id) == "null"
}

const mcpMaxFrame = 10 * 1024 * 1024

func serveMCP(dir string, r io.Reader, w io.Writer) error {
	br := bufio.NewReaderSize(r, 64*1024)
	enc := json.NewEncoder(w)
	for {
		line, overlong, err := readMCPLine(br, mcpMaxFrame)
		if overlong {
			// One oversized frame is reported as a parse error and skipped; the
			// server keeps serving instead of tearing down the whole session.
			writeRPCError(enc, nil, -32700, "parse error")
		} else if trimmed := strings.TrimSpace(string(line)); trimmed != "" {
			var req rpcRequest
			if uerr := json.Unmarshal([]byte(trimmed), &req); uerr != nil {
				writeRPCError(enc, nil, -32700, "parse error")
			} else if !isNotification(req.ID) {
				result, code, msg := handleMCP(dir, req)
				if code != 0 {
					writeRPCError(enc, req.ID, code, msg)
				} else if eerr := enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}); eerr != nil {
					return eerr
				}
			}
		}
		if err != nil {
			if err != io.EOF && os.Getenv("DEJA_DEBUG") == "1" {
				fmt.Fprintf(os.Stderr, "deja mcp read error: %v\n", err)
			}
			return nil
		}
	}
}

// readMCPLine reads one newline-delimited frame. A frame longer than max is
// drained and reported via overlong=true rather than buffered whole, so a
// hostile or corrupt client can't exhaust memory or kill the loop.
func readMCPLine(br *bufio.Reader, max int) (line []byte, overlong bool, err error) {
	for {
		chunk, e := br.ReadSlice('\n')
		if e == bufio.ErrBufferFull {
			if len(line)+len(chunk) > max {
				// Drain the rest of this overlong frame up to the next newline.
				for e == bufio.ErrBufferFull {
					_, e = br.ReadSlice('\n')
				}
				return nil, true, e
			}
			line = append(line, chunk...)
			continue
		}
		line = append(line, chunk...)
		return line, false, e
	}
}

func handleMCP(dir string, req rpcRequest) (any, int, string) {
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
				"description": "Search the user's own past coding sessions across every AI tool they've used (Claude Code, Codex, Cursor, opencode, aider, gemini, and others) and return the best matches as dense text under ~4KB. Call this the moment the user implies work already happened — 'didn't we fix this before?', 'what was that error again', 'we already set this up', 'how did we solve X last time', 'what did we decide about Y' — and always before debugging an error or re-implementing something that might already exist. Query with the most specific token available: an exact error string, function name, file path, or flag (multiple words are ANDed). Do NOT use this for general knowledge or library/API docs — only this user's prior sessions. Follow up with recall_context when one session looks right and you need its full story. Optionally filter by harness. When a result genuinely helps the task at hand — you reuse a fix, skip re-debugging, or confirm a prior decision — tell the user in one digest.Short line what deja-vu recalled and how you used it (e.g. \"deja-vu recalled: we hit this JWT skew in March — reusing that fix\"). Even a partial hint counts if it changed your approach. Say nothing about recalls that did not help.",
				"annotations": map[string]any{"title": "Search past sessions", "readOnlyHint": true, "openWorldHint": false},
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string", "description": "Search terms; specific tokens (error strings, function names, flags) match best. Multiple words are ANDed."}, "harness": map[string]any{"type": "string", "description": "Optional filter: claude, codex, opencode, aider, gemini, cursor, antigravity, grok or qwen."}, "limit": map[string]any{"type": "number", "description": "Max sessions to return (default 5)."}}, "required": []string{"query"}},
			},
			{
				"name":        "recall_context",
				"description": "Return a full markdown digest (~8KB) of the single best-matching prior session — problem, decisions, outcome — when a bare recall hit is not enough and you need the reasoning behind it. Use after recall, or directly when the user asks 'remind me how we handled X' or 'what was the whole story with Y'. Query terms are matched against transcript text, so use tokens likely to appear verbatim: an error string, function name, or flag. Not for browsing many sessions — use recall for that; this returns one deep digest. When a result genuinely helps the task at hand — you reuse a fix, skip re-debugging, or confirm a prior decision — tell the user in one digest.Short line what deja-vu recalled and how you used it (e.g. \"deja-vu recalled: we hit this JWT skew in March — reusing that fix\"). Even a partial hint counts if it changed your approach. Say nothing about recalls that did not help.",
				"annotations": map[string]any{"title": "Digest one past session", "readOnlyHint": true, "openWorldHint": false},
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string", "description": "Search terms identifying the session to digest."}, "harness": map[string]any{"type": "string", "description": "Optional harness filter."}}, "required": []string{"query"}},
			},
			{
				"name":        "blame",
				"description": "Before editing, refactoring, or deleting a file, find the prior sessions that discussed it so you know why it is shaped the way it is. Call whenever you are about to change a file, or when the user asks 'why is this like this', 'what was this for', 'is it safe to remove this'. Most specific mentions come first. This is session history across AI tools, not git blame — it explains intent and past decisions, not commit authorship. Give an absolute path, relative path, or bare filename.",
				"annotations": map[string]any{"title": "Why is this file like this", "readOnlyHint": true, "openWorldHint": false},
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string", "description": "Absolute, relative, or bare filename."}, "harness": map[string]any{"type": "string"}, "project": map[string]any{"type": "string"}, "since": map[string]any{"type": "string", "description": "Age such as 30d or 24h."}, "limit": map[string]any{"type": "number"}, "all": map[string]any{"type": "boolean"}}, "required": []string{"path"}},
			},
			{
				"name":        "remember",
				"description": "Store one durable decision or conclusion so a future session can recall it. Call right after a decision is settled, a tricky bug is resolved, or the user says 'remember this', 'note that for next time', 'don't forget we chose X'. Write a single self-contained fact (e.g. 'We use Postgres advisory locks for the job queue because Redis lost messages under load'). Do NOT store transcripts, routine conversation, or anything already obvious from the code. text is required; project defaults to notes.",
				"annotations": map[string]any{"title": "Remember a decision", "readOnlyHint": false},
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string", "description": "A durable fact, decision, or conclusion to remember."}, "project": map[string]any{"type": "string", "description": "Optional project name; defaults to notes."}}, "required": []string{"text"}},
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
		text, err := callMCPTool(dir, p.Name, p.Arguments)
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

func callMCPTool(dir, name string, raw json.RawMessage) (string, error) {
	switch name {
	case "recall":
		var a struct {
			Query   string  `json:"query"`
			Harness string  `json:"harness"`
			Limit   float64 `json:"limit"`
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", err
		}
		if strings.TrimSpace(a.Query) == "" {
			return "", fmt.Errorf("query required")
		}
		text, sessions, err := recallTextResult(dir, a.Query, a.Harness, int(a.Limit), 4096-recallFrameOverhead)
		if err == nil {
			text = frameRecall(text)
			usage.RecordDigest(dir, usage.KindRecall, text, sessions)
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
		text, sessions, err := recallContextResult(dir, a.Query, a.Harness)
		if err == nil {
			text = frameRecall(text)
			usage.RecordDigest(dir, usage.KindContext, text, sessions)
		}
		return text, err
	case "blame":
		var a struct {
			Path    string  `json:"path"`
			Harness string  `json:"harness"`
			Project string  `json:"project"`
			Since   string  `json:"since"`
			Limit   float64 `json:"limit"`
			All     bool    `json:"all"`
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", err
		}
		if strings.TrimSpace(a.Path) == "" {
			return "", fmt.Errorf("path required")
		}
		var since time.Duration
		if a.Since != "" {
			var err error
			since, err = parseDur(a.Since)
			if err != nil {
				return "", err
			}
		}
		return blameTextResult(dir, search.BlameOptions{Harness: a.Harness, Project: a.Project, Since: since, All: a.All}, a.Path, int(a.Limit))
	case "remember":
		var a struct {
			Text    string `json:"text"`
			Project string `json:"project"`
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", err
		}
		if strings.TrimSpace(a.Text) == "" {
			return "", fmt.Errorf("text required")
		}
		if strings.TrimSpace(a.Project) == "" {
			a.Project = "notes"
		}
		if err := sources.AppendNote(a.Project, a.Text, time.Now()); err != nil {
			return "", err
		}
		if err := index.EnsureForSearch(dir, search.Options{All: true}, false, mcpProgress()); err != nil {
			return "", err
		}
		return fmt.Sprintf("Remembered under %s.", strings.TrimSpace(a.Project)), nil
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}

func blameTextResult(dir string, o search.BlameOptions, path string, limit int) (string, error) {
	target, err := search.ResolveBlamePath(path)
	if err != nil {
		return "", err
	}
	hits, err := findBlameHits(dir, target, o, mcpProgress())
	if err != nil {
		return "", err
	}
	if limit <= 0 {
		limit = 10
	}
	if !o.All && len(hits) > limit {
		hits = hits[:limit]
	}
	b, err := json.Marshal(hits)
	return string(b), err
}

func recallText(dir, q, harness string, limit, budget int) (string, error) {
	text, _, err := recallTextResult(dir, q, harness, limit, budget)
	return text, err
}

func recallTextResult(dir, q, harness string, limit, budget int) (string, int, error) {
	if limit <= 0 {
		limit = 5
	}
	o := search.Options{Query: q, Harness: harness, All: true}
	if err := index.EnsureForSearch(dir, o, false, mcpProgress()); err != nil {
		return "", 0, err
	}
	result, err := index.SearchWithRecoveryDetailed(dir, o, mcpProgress())
	if err != nil {
		return "", 0, err
	}
	ss := result.Sessions
	o.Tier = result.Tier
	if result.Stemmed {
		o.Stemmed = true
		o.FuzzyVariants = result.Variants
	} else if result.Fuzzy {
		o.FuzzyVariants = result.Variants
	}
	if o.Tier == search.TierClose && o.FuzzyVariants == nil {
		o.FuzzyVariants = result.Variants
	}
	hits, err := search.Run(ss, o)
	if err != nil {
		return "", 0, err
	}
	if os.Getenv("DEJA_EMBED") != "off" {
		hits = maybeRerank(dir, hits, o, os.Stderr)
	}
	var semantic bool
	hits, semantic = maybeSemantic(dir, hits, o, os.Stderr)
	o.Semantic = semantic
	if len(hits) == 0 {
		return fmt.Sprintf("No prior deja sessions matched %q.", q), 0, nil
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	var b strings.Builder
	served := 0
	if result.Stemmed {
		fmt.Fprintf(&b, "No exact match; using word forms: %s\n", strings.Join(fuzzySummary(result.Variants), ", "))
	} else if result.Fuzzy {
		fmt.Fprintf(&b, "No exact match; using close spellings: %s\n", strings.Join(fuzzySummary(result.Variants), ", "))
	}
	fmt.Fprintf(&b, "deja recall for %q (%d match(es))\n", q, len(hits))
	for i, h := range hits {
		fmt.Fprintf(&b, "\n%d. [%s] %s · %s · %d matches", i+1, h.Session.Harness, h.Session.Project, h.Session.ID, h.Count)
		if !h.Session.Updated.IsZero() {
			fmt.Fprintf(&b, " · updated %s (%s)", h.Session.Updated.Format("2006-01-02"), search.RelativeDate(h.Session.Updated))
		}
		fmt.Fprintln(&b)
		if h.Superseded != "" {
			fmt.Fprintf(&b, "[earlier attempt — a newer session in this project covers the same ground, updated %s]\n", h.Superseded)
		}
		if h.Tier != search.TierExact {
			fmt.Fprintf(&b, "[%s]\n", h.Tier)
		}
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

func recallContext(dir, q string) (string, error) {
	text, _, err := recallContextResult(dir, q, "")
	return text, err
}

func recallContextResult(dir, q, harness string) (string, int, error) {
	o := search.Options{Query: q, Harness: harness, All: true}
	if err := index.EnsureForSearch(dir, o, false, mcpProgress()); err != nil {
		return "", 0, err
	}
	result, err := index.SearchWithRecoveryDetailed(dir, o, mcpProgress())
	if err != nil {
		return "", 0, err
	}
	ss := result.Sessions
	o.Tier = result.Tier
	if result.Stemmed {
		o.Stemmed = true
		o.FuzzyVariants = result.Variants
	} else if result.Fuzzy {
		o.FuzzyVariants = result.Variants
	}
	if o.Tier == search.TierClose && o.FuzzyVariants == nil {
		o.FuzzyVariants = result.Variants
	}
	hits, err := search.Run(ss, o)
	if err != nil {
		return "", 0, err
	}
	var semantic bool
	hits, semantic = maybeSemantic(dir, hits, o, os.Stderr)
	if semantic {
		o.Tier = search.TierSemantic
	}
	if len(hits) == 0 {
		return fmt.Sprintf("No prior deja sessions matched %q.", q), 0, nil
	}
	var b bytes.Buffer
	search.PrintContext(&b, hits[0].Session, q)
	text := b.String()
	if hits[0].Tier != search.TierExact {
		text = "[" + hits[0].Tier + "]\n" + text
	}
	return text, 1, nil
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
			if value == "" {
				out = append(out, token+" (ignored: no session matches it with the rest)")
				continue
			}
			if value != token {
				out = append(out, token+" -> "+value)
			}
		}
	}
	sort.Strings(out)
	return out
}
