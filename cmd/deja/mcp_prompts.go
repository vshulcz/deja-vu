package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// A slash command, in the only form a harness can offer without deja shipping a
// file into it. Gemini and qwen both ask an MCP server for prompts/list on
// startup (measured on gemini-cli 0.55.1 and qwen-code 0.20.0 against a
// recording server) and put what comes back behind a slash; deja answered
// -32601, so the one surface a person drives by hand was missing everywhere
// except the harnesses deja writes a command file for. Codex asks for tools and
// never for prompts, so it gains nothing here.
const dejaPromptName = "deja"

func dejaPrompt() map[string]any {
	return map[string]any{
		"name":        dejaPromptName,
		"title":       "Search past sessions",
		"description": "Search this machine's past coding sessions — an error, a decision, a file — and paste what they settled into the conversation.",
		"arguments": []map[string]any{{
			"name":        "query",
			"description": "An exact error string, function name or path, or the question in your own words.",
			"required":    true,
		}},
	}
}

// mcpPromptGet runs the search and hands back its result as the message the
// user is about to send. The alternative — telling the model to call the tool —
// costs a round trip to reach the same rows, and the point of typing the
// command is that the answer is already there.
func mcpPromptGet(dir string, params json.RawMessage) (any, int, string) {
	var p struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, -32602, "invalid params"
	}
	if p.Name != dejaPromptName {
		return nil, -32602, fmt.Sprintf("unknown prompt %q", p.Name)
	}
	query := strings.TrimSpace(p.Arguments["query"])
	if query == "" {
		return nil, -32602, "query required"
	}
	args, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		return nil, -32603, err.Error()
	}
	text, err := callMCPTool(dir, "recall", args)
	if err != nil {
		return nil, -32602, err.Error()
	}
	if strings.TrimSpace(text) == "" {
		// Said in words rather than left blank: a host pastes this straight
		// into the conversation, and an empty paste reads as a broken command
		// rather than as an answer.
		text = "deja: nothing in this machine's past sessions matches " + quoted(query)
	}
	return map[string]any{
		"description": "What this machine's past sessions say about " + quoted(query),
		"messages": []map[string]any{{
			"role":    "user",
			"content": map[string]any{"type": "text", "text": defuseHostTriggers(text)},
		}},
	}, 0, ""
}

// defuseHostTriggers breaks the two tokens a host acts on when it pastes this
// prompt result straight into its own input. opencode runs !`cmd` through the
// shell and attaches @path files it finds, and gemini and qwen read the same
// `@` mention; so a recalled backtick command would execute and a recalled
// @path would ship an arbitrary file to the model. The recall is reference
// text about a past session, not an instruction to this host, so a space goes
// in to keep the token inert — !`x` stays legible as "! `x`" and the model
// still reads every word. @ is split unless a word character precedes it:
// opencode attached x,@path as readily as a bare @path, while an address like
// a@b is no mention and is left alone.
func defuseHostTriggers(s string) string {
	r := []rune(s)
	var b strings.Builder
	b.Grow(len(s) + 16)
	for i, c := range r {
		b.WriteRune(c)
		if i+1 >= len(r) {
			continue
		}
		n := r[i+1]
		switch {
		case c == '!' && n == '`':
			b.WriteByte(' ')
		case c == '@' && !unicode.IsSpace(n) && (i == 0 || !isWordRune(r[i-1])):
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// quoted wraps a query for a one-line message without a formatter that would
// escape the user's own punctuation.
func quoted(s string) string {
	return "\"" + s + "\""
}

func isWordRune(c rune) bool {
	return c == '_' || unicode.IsLetter(c) || unicode.IsDigit(c)
}
