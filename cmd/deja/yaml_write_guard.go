package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// yamlWriteBreaks is the last check on a YAML config before it is written: the
// shapes deja's block writers produce when they append to a document they did
// not expect, and which no reader of the file can parse. Every YAML writer
// splices text at the end of the file or after a key, so a document that is a
// one-line flow collection (`{}`, which is what a YAML library writes for an
// empty map) got deja's block after it, and a key written inline
// (`mcp_servers: {}`) got a second key of the same name. Hermes then fell back
// to its defaults and goose dropped the config without a word (#4555).
//
// Only what the write introduces: a file that was already like this is the
// reader's, and the writers that handle a shape themselves (dsh replaces a
// `[]` document) never produce it.
func yamlWriteBreaks(path string, old, next []byte) error {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
	default:
		return nil
	}
	if yamlShapeProblem(lfText(old)) != "" {
		return nil
	}
	if problem := yamlShapeProblem(lfText(next)); problem != "" {
		return configParseError(path, fmt.Errorf("%s — deja edits block YAML, so write it as a block", problem))
	}
	return nil
}

// yamlShapeProblem names the first document that is a one-line flow collection
// with more after it, or that repeats a top-level key.
func yamlShapeProblem(text string) string {
	flow := ""
	seen := map[string]string{}
	for line := range strings.SplitSeq(text, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "" || strings.HasPrefix(trimmed, "#"):
			continue
		case line == "---" || strings.HasPrefix(line, "--- ") || line == "...":
			flow, seen = "", map[string]string{}
			// A document may open on the marker's own line: `--- {}` is the
			// one-line flow document as much as `{}` is.
			rest := strings.TrimSpace(stripYAMLComment(strings.TrimPrefix(line, "---")))
			if strings.HasPrefix(rest, "{") || strings.HasPrefix(rest, "[") {
				if strings.HasSuffix(rest, "}") || strings.HasSuffix(rest, "]") {
					flow = rest
				}
			}
			continue
		case strings.HasPrefix(line, "%"):
			continue
		}
		if flow != "" {
			return fmt.Sprintf("the document is %#q on one line, and nothing can follow it", flow)
		}
		if line[0] == '{' || line[0] == '[' {
			code := strings.TrimSpace(stripYAMLComment(line))
			if len(seen) == 0 && (strings.HasSuffix(code, "}") || strings.HasSuffix(code, "]")) {
				flow = code
			}
			continue
		}
		if line[0] == ' ' || line[0] == '\t' || line[0] == '-' {
			continue
		}
		key, ok := yamlRootKey(line)
		if !ok {
			continue
		}
		if first, dup := seen[key]; dup {
			return fmt.Sprintf("%q would be a top-level key twice (the first is %#q)", key, first)
		}
		seen[key] = strings.TrimSpace(stripYAMLComment(line))
	}
	return ""
}

// yamlRootKey reads the key of a top-level `key: value` line.
func yamlRootKey(line string) (string, bool) {
	if q := line[0]; q == '"' || q == '\'' {
		end := strings.IndexByte(line[1:], q)
		if end < 0 || !strings.HasPrefix(line[end+2:], ":") {
			return "", false
		}
		return line[1 : end+1], true
	}
	for i := range len(line) {
		if line[i] == ':' && (i+1 == len(line) || line[i+1] == ' ' || line[i+1] == '\t') {
			return line[:i], true
		}
	}
	return "", false
}
