package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// A config holds blocks the reader wrote on one line — a counter table, a
// permission list, an MCP entry of their own. Round-tripping through
// map[string]any expands every one of them, so an install that added a single
// server rewrote the whole document around it (#2704). #2641 gave back the
// indent and the top-level order; this gives back the one line.

// keepInlineBlocks re-inlines, in the freshly marshalled document, every object
// and array the original wrote on a single line.
//
// One pass over the document, not one pass per block: ~/.claude.json holds an
// entry per checkout, and rescanning after every rewrite made a real one take
// minutes — 233 KB took six seconds, 936 KB took a minute and a half.
func keepInlineBlocks(old, next []byte) []byte {
	inline := inlineContainers(old)
	if len(inline) == 0 {
		return next
	}
	items := inlineArrayItems(old)
	found := scanContainers(next)
	sort.Slice(found, func(i, j int) bool { return found[i].start < found[j].start })
	type rewrite struct {
		start, end int
		text       []byte
	}
	var edits []rewrite
	covered := -1
	for _, c := range found {
		if c.start < covered {
			// Nested inside a block already going back on one line: it comes
			// along with that one.
			continue
		}
		was, ok := inline[c.path]
		if !ok && (!c.viaArray || len(items[arrayOf(c.path)]) == 0) {
			continue
		}
		if !bytes.ContainsRune(next[c.start:c.end], '\n') {
			continue
		}
		flat := compactJSONText(next[c.start:c.end])
		// The reader's own bytes when the value is theirs still: marshalling
		// sorts keys, so re-inlining alone would hand back the same block with
		// its fields shuffled — a rewrite of their line either way.
		switch {
		case ok && sameJSONValue(was, flat):
			flat = was
		case c.viaArray:
			// Inside an array the path is a position, and an entry that moved —
			// deja's own added ahead of it, or taken out — would be matched
			// against a neighbour. Only an exact value match is evidence there,
			// so look for one among the entries the array had.
			if flat = sameArrayItem(items, c.path, flat); flat == nil {
				continue
			}
		default:
			// Changed, so it cannot go back as it was — but what inside it is
			// still the reader's can. Compacting the whole block re-sorted the
			// reader's own hook entry beside the one deja added, and an
			// uninstall then had no original left to restore (#4167).
			flat = relined(next, c, found, inline, items)
		}
		edits = append(edits, rewrite{c.start, c.end, flat})
		covered = c.end
	}
	// Back to front, so every offset ahead of an edit is still the one the scan
	// reported.
	for i := len(edits) - 1; i >= 0; i-- {
		e := edits[i]
		tail := append(append([]byte{}, e.text...), next[e.end:]...)
		next = append(next[:e.start:e.start], tail...)
	}
	return next
}

// relined puts container c of next on one line, giving back the reader's own
// text for every block inside it whose value they wrote that way.
func relined(next []byte, c jsonContainer, found []jsonContainer, inline map[string][]byte, items map[string][][]byte) []byte {
	flat := compactJSONText(next[c.start:c.end])
	if was, ok := inline[c.path]; ok && sameJSONValue(was, flat) {
		return was
	}
	if c.viaArray {
		if was := sameArrayItem(items, c.path, flat); was != nil {
			return was
		}
	}
	var out []byte
	at := c.start
	i := sort.Search(len(found), func(i int) bool { return found[i].start > c.start })
	for ; i < len(found) && found[i].start < c.end; i++ {
		d := found[i]
		if d.start < at {
			// Inside a child already given back whole.
			continue
		}
		out = append(out, compactJSONText(next[at:d.start])...)
		out = append(out, relined(next, d, found, inline, items)...)
		at = d.end
	}
	return append(out, compactJSONText(next[at:c.end])...)
}

// inlineArrayItems maps each array to the entries the reader wrote in it on a
// single line, by the path of the array rather than the entry's position.
func inlineArrayItems(b []byte) map[string][][]byte {
	out := map[string][][]byte{}
	for _, c := range scanContainers(b) {
		parent, last := splitJSONPath(c.path)
		if strings.HasPrefix(last, "#") && !bytes.ContainsRune(b[c.start:c.end], '\n') {
			out[parent] = append(out[parent], b[c.start:c.end])
		}
	}
	return out
}

// sameArrayItem is the reader's text for an array entry with this value, or
// nil when the array held no such entry on one line.
func sameArrayItem(items map[string][][]byte, path string, flat []byte) []byte {
	parent, last := splitJSONPath(path)
	if !strings.HasPrefix(last, "#") {
		return nil
	}
	for _, was := range items[parent] {
		if sameJSONValue(was, flat) {
			return was
		}
	}
	return nil
}

// arrayOf is the path of the array an entry sits in, or "" when the last step
// is a name.
func arrayOf(path string) string {
	parent, last := splitJSONPath(path)
	if !strings.HasPrefix(last, "#") {
		return ""
	}
	return parent
}

func splitJSONPath(path string) (parent, last string) {
	i := strings.LastIndexByte(path, 0)
	if i < 0 {
		return "", path
	}
	return path[:i], path[i+1:]
}

// sameJSONValue reports whether two blocks say the same thing, whatever order
// they say it in.
func sameJSONValue(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// jsonContainer is one object or array in a document: where it sits, the path
// of names that reaches it, and whether any step of that path was a position
// in an array rather than a name.
type jsonContainer struct {
	path     string
	start    int
	end      int
	viaArray bool
}

// inlineContainers maps each path the reader wrote on a single line to the text
// they wrote there.
func inlineContainers(b []byte) map[string][]byte {
	out := map[string][]byte{}
	for _, c := range scanContainers(b) {
		if !bytes.ContainsRune(b[c.start:c.end], '\n') {
			out[c.path] = b[c.start:c.end]
		}
	}
	return out
}

// scanContainers walks a JSON document and reports every object and array in
// it. Strings are skipped whole, so a brace inside a value — a path, a regex,
// a permission rule like `Bash(git:*)` — is text rather than structure.
func scanContainers(b []byte) []jsonContainer {
	var out []jsonContainer
	type frame struct {
		path     string
		start    int
		array    bool
		viaArray bool
		index    int
		key      string
	}
	var stack []frame
	for i := 0; i < len(b); i++ {
		switch b[i] {
		case '"':
			end := endOfJSONString(b, i)
			if end < 0 {
				return out
			}
			if len(stack) > 0 && !stack[len(stack)-1].array {
				// A key is a string followed by a colon; a value is not. The
				// quoted form is decoded rather than sliced, because the
				// marshaller escapes `<`, `>` and `&` — a path built from the
				// raw bytes of `/work/R&D` would never match the one built
				// from the reader's file.
				if j := skipJSONSpace(b, end); j < len(b) && b[j] == ':' {
					var key string
					if json.Unmarshal(b[i:end], &key) == nil {
						stack[len(stack)-1].key = key
					}
				}
			}
			i = end - 1
		case '{', '[':
			f := frame{start: i, array: b[i] == '['}
			if len(stack) > 0 {
				top := stack[len(stack)-1]
				name := top.key
				if top.array {
					name = "#" + strconv.Itoa(top.index)
				}
				f.path = top.path + "\x00" + name
				f.viaArray = top.viaArray || top.array
			}
			stack = append(stack, f)
		case '}', ']':
			if len(stack) == 0 {
				return out
			}
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			out = append(out, jsonContainer{path: top.path, start: top.start, end: i + 1, viaArray: top.viaArray})
		case ',':
			if len(stack) > 0 && stack[len(stack)-1].array {
				stack[len(stack)-1].index++
			}
		}
	}
	return out
}

// endOfJSONString returns the offset just past the closing quote of the string
// starting at i, or -1 when the document ends inside it.
func endOfJSONString(b []byte, i int) int {
	for j := i + 1; j < len(b); j++ {
		switch b[j] {
		case '\\':
			j++
		case '"':
			return j + 1
		}
	}
	return -1
}

func skipJSONSpace(b []byte, i int) int {
	for i < len(b) {
		switch b[i] {
		case ' ', '\t', '\r', '\n':
			i++
		default:
			return i
		}
	}
	return i
}

// compactJSONText puts a block back on one line in the shape configs are
// written in: a space after each colon and comma, none inside the brackets. A
// reader who minified their file sees that spacing only in a block deja
// actually changed — an unchanged one goes back exactly as they wrote it.
func compactJSONText(b []byte) []byte {
	var out []byte
	for i := 0; i < len(b); i++ {
		switch b[i] {
		case '"':
			end := endOfJSONString(b, i)
			if end < 0 {
				return append(out, b[i:]...)
			}
			out = append(out, b[i:end]...)
			i = end - 1
		case ' ', '\t', '\r', '\n':
			// Whitespace between values is the indent this is undoing.
		case ':', ',':
			out = append(out, b[i], ' ')
		default:
			out = append(out, b[i])
		}
	}
	return out
}
