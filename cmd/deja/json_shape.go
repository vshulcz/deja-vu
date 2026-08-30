package main

import (
	"bytes"
	"encoding/json"
	"strings"
)

// marshalConfigLike renders a config in the shape the reader wrote it in.
//
// The goose writer explains why it edits text rather than round-tripping: a
// config holds settings someone wrote by hand, and re-serialising drops the
// ordering they left there. The JSON writers do round-trip — fourteen of them
// unmarshal into map[string]any and MarshalIndent with two spaces — so an
// install that touched one block rewrote the whole document: a four-space file
// came back at two, and the top-level keys came back alphabetised, because Go
// sorts map keys (#2640).
//
// Two of those are cheap to give back and this is the one place all fourteen
// pass through. What it does not give back is an inline object or array staying
// inline: that needs the writer to stop round-tripping at all.
func marshalConfigLike(old []byte, root map[string]any) ([]byte, error) {
	next, err := json.MarshalIndent(root, "", jsonIndentOf(old))
	if err != nil {
		return nil, err
	}
	return keepInlineBlocks(old, reorderTopLevel(old, next, jsonIndentOf(old))), nil
}

// jsonIndentOf is the indent unit the file already used. Two spaces for a file
// deja is creating, which has no shape to keep, and for one written on a single
// line, where there is nothing to read an indent from.
func jsonIndentOf(old []byte) string {
	for _, line := range strings.Split(string(old), "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" || trimmed == line {
			continue
		}
		return line[:len(line)-len(trimmed)]
	}
	return "  "
}

// reorderTopLevel puts the keys back in the order the reader had them.
//
// Keys deja added are left where marshalling put them: they were not in the
// reader's file, so there is no order of theirs to restore, and appending them
// at the end would be a choice rather than a restoration.
func reorderTopLevel(old, next []byte, indent string) []byte {
	order := topLevelKeyOrder(old)
	if len(order) < 2 {
		return next
	}
	blocks, head, tail, ok := topLevelBlocks(next, indent)
	if !ok {
		return next
	}
	seen := map[string]bool{}
	var out [][]byte
	for _, k := range order {
		if b, present := blocks[k]; present && !seen[k] {
			seen[k] = true
			out = append(out, b)
		}
	}
	// Whatever marshalling produced that the reader did not have, in the order
	// it came out.
	for _, k := range marshalledKeyOrder(next, indent) {
		if !seen[k] {
			seen[k] = true
			out = append(out, blocks[k])
		}
	}
	if len(out) != len(blocks) {
		return next
	}
	var b bytes.Buffer
	b.Write(head)
	for i, block := range out {
		b.Write(block)
		if i < len(out)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.Write(tail)
	return b.Bytes()
}

// topLevelKeyOrder reads the keys of a JSON object in the order they appear.
func topLevelKeyOrder(b []byte) []string {
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return nil
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil
	}
	var keys []string
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return nil
		}
		name, ok := k.(string)
		if !ok {
			return nil
		}
		keys = append(keys, name)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil
		}
	}
	return keys
}

// topLevelBlocks cuts MarshalIndent's output into one text block per key, with
// the opening and closing braces kept aside. It relies on the shape
// MarshalIndent produces — every top-level key starts a line at exactly one
// indent — and gives up rather than guess on anything else.
func topLevelBlocks(next []byte, indent string) (blocks map[string][]byte, head, tail []byte, ok bool) {
	lines := strings.Split(strings.TrimRight(string(next), "\n"), "\n")
	if len(lines) < 2 || lines[0] != "{" || lines[len(lines)-1] != "}" {
		return nil, nil, nil, false
	}
	blocks = map[string][]byte{}
	var cur string
	var buf []string
	flush := func() {
		if cur != "" {
			joined := strings.Join(buf, "\n")
			blocks[cur] = []byte(strings.TrimSuffix(joined, ","))
		}
	}
	for _, line := range lines[1 : len(lines)-1] {
		if strings.HasPrefix(line, indent) && !strings.HasPrefix(line, indent+" ") && !strings.HasPrefix(line, indent+"\t") {
			if name, isKey := topLevelKeyOf(line, indent); isKey {
				flush()
				cur, buf = name, []string{line}
				continue
			}
		}
		if cur == "" {
			return nil, nil, nil, false
		}
		buf = append(buf, line)
	}
	flush()
	return blocks, []byte("{\n"), []byte("}"), true
}

// topLevelKeyOf reads the key a MarshalIndent line opens, if it opens one.
func topLevelKeyOf(line, indent string) (string, bool) {
	rest := strings.TrimPrefix(line, indent)
	if !strings.HasPrefix(rest, `"`) {
		return "", false
	}
	var name string
	end := strings.Index(rest[1:], `":`)
	if end < 0 {
		return "", false
	}
	if err := json.Unmarshal([]byte(rest[:end+2]), &name); err != nil {
		return "", false
	}
	return name, true
}

// marshalledKeyOrder is topLevelKeyOrder for what marshalling produced.
func marshalledKeyOrder(next []byte, indent string) []string {
	var keys []string
	lines := strings.Split(string(next), "\n")
	for _, line := range lines {
		if !strings.HasPrefix(line, indent) || strings.HasPrefix(line, indent+" ") || strings.HasPrefix(line, indent+"\t") {
			continue
		}
		if name, ok := topLevelKeyOf(line, indent); ok {
			keys = append(keys, name)
		}
	}
	return keys
}
