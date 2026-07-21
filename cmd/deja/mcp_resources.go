package main

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
)

const mcpResourceLimit = 20

// mcpResourcesList exposes recent sessions as browsable MCP resources, so an
// agent can look around without guessing search terms first.
func mcpResourcesList(dir string) (any, int, string) {
	if err := index.Ensure(dir, "", false, mcpProgress()); err != nil {
		return nil, -32603, err.Error()
	}
	ss, err := index.Recent(dir, mcpResourceLimit*2)
	if err != nil {
		return nil, -32603, err.Error()
	}
	pol := policy.Load()
	resources := make([]map[string]any, 0, mcpResourceLimit)
	for _, s := range ss {
		if !pol.Allows(policy.ActivationMCP, s.Project) {
			continue
		}
		name := strings.TrimSpace(s.Title)
		if name == "" {
			name = s.ID
		}
		desc := s.Project
		if !s.Updated.IsZero() {
			desc += " · " + s.Updated.Format("2006-01-02")
		}
		resources = append(resources, map[string]any{
			"uri":         "deja://session/" + s.Harness + ":" + s.ID,
			"name":        name,
			"description": desc,
			"mimeType":    "text/markdown",
		})
		if len(resources) >= mcpResourceLimit {
			break
		}
	}
	return map[string]any{"resources": resources}, 0, ""
}

// mcpResourceRead serves one session's digest by its deja://session/ URI.
func mcpResourceRead(dir, uri string) (any, int, string) {
	ref, ok := strings.CutPrefix(uri, "deja://session/")
	if !ok {
		return nil, -32602, "unknown resource uri"
	}
	id := ref
	if i := strings.IndexByte(ref, ':'); i >= 0 {
		id = ref[i+1:]
	}
	s, found, err := findByPrefix(dir, id)
	if err != nil {
		return nil, -32603, err.Error()
	}
	if !found {
		return nil, -32602, fmt.Sprintf("no session matches %q", id)
	}
	if !policy.Load().Allows(policy.ActivationMCP, s.Project) {
		return nil, -32602, "blocked by trust policy"
	}
	var b bytes.Buffer
	search.PrintContext(&b, s, "")
	return map[string]any{"contents": []map[string]any{{
		"uri":      uri,
		"mimeType": "text/markdown",
		"text":     b.String(),
	}}}, 0, ""
}
