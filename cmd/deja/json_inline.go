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
		case arrayOf(c.path) != "":
			// An entry of an array: its path is a position, and an entry that
			// moved — deja's own added ahead of it, or taken out — would be
			// matched against a neighbour. Only an exact value match is
			// evidence there, so look for one among the entries the array had.
			if flat = items.take(c.path, flat); flat == nil {
				continue
			}
		case ok && sameJSONValue(was, flat):
			flat = was
		case c.viaArray:
			// Changed, and reached through an array position, so the reader's
			// block at this path may be a neighbour's: leave it expanded.
			continue
		default:
			// Changed, so it cannot go back as it was — but what inside it is
			// still the reader's can (#4167).
			flat = relined(next[c.start:c.end], c.path, was, configStyle, items)
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

// relined puts one block of next back on one line in the reader's shape: their
// text for every part whose value is still theirs, their key order, and the
// spacing they wrote around colons and commas. Compacting the marshalled block
// instead re-sorted the reader's own hook entry beside the one deja added, and
// an uninstall then had no original left to restore (#4167).
//
// was is the reader's text at this place, or nil where they had nothing; st is
// the spacing to use when was says nothing about it.
func relined(next []byte, path string, was []byte, st inlineStyle, items arrayItems) []byte {
	next = bytes.TrimSpace(next)
	flat := compactJSONText(next)
	if was != nil && sameJSONValue(was, flat) {
		return was
	}
	st = st.of(was)
	if keys, keyText, vals, ok := objectMembers(next); ok {
		_, wasKeyText, wasVals, _ := objectMembers(was)
		wasOrder := topLevelKeyOrder(was)
		order := make([]string, 0, len(keys))
		placed := map[string]bool{}
		for _, k := range append(wasOrder, keys...) {
			if _, present := vals[k]; present && !placed[k] {
				placed[k] = true
				order = append(order, k)
			}
		}
		var out bytes.Buffer
		out.WriteByte('{')
		for i, k := range order {
			if i > 0 {
				out.WriteString(st.comma)
			}
			kt := keyText[k]
			if w, ok := wasKeyText[k]; ok {
				kt = w
			}
			out.Write(kt)
			out.WriteString(st.colon)
			out.Write(relined(vals[k], path+"\x00"+k, wasVals[k], st, items))
		}
		out.WriteByte('}')
		return out.Bytes()
	}
	if elems, ok := arrayElems(next); ok {
		wasElems, _ := arrayElems(was)
		var out bytes.Buffer
		out.WriteByte('[')
		for i, e := range elems {
			if i > 0 {
				out.WriteString(st.comma)
			}
			p := path + "\x00#" + strconv.Itoa(i)
			if w := items.take(p, compactJSONText(e)); w != nil {
				out.Write(w)
				continue
			}
			// A position is no evidence of identity, so the reader's entry at
			// this index guides only the shape; relined still hands its text
			// back only for an equal value.
			var guide []byte
			if i < len(wasElems) {
				guide = wasElems[i]
			}
			out.Write(relined(e, p, guide, st, items))
		}
		out.WriteByte(']')
		return out.Bytes()
	}
	return flat
}

// inlineStyle is the spacing a one-line block was written with.
type inlineStyle struct{ colon, comma string }

// configStyle is the spacing compactJSONText writes, for a block the reader
// gave no shape to.
var configStyle = inlineStyle{colon: ": ", comma: ", "}

// of reads the spacing from the reader's text. A block with one key has no
// comma to read, so its comma follows its colon: `{"other":true}` is a
// minified file, not one waiting for ", ".
func (st inlineStyle) of(was []byte) inlineStyle {
	colon, comma := st.readFrom(was)
	switch {
	case colon && !comma:
		st.comma = "," + strings.TrimPrefix(st.colon, ":")
	case comma && !colon:
		st.colon = ":" + strings.TrimPrefix(st.comma, ",")
	}
	return st
}

func (st *inlineStyle) readFrom(was []byte) (colon, comma bool) {
	for i := 0; i < len(was) && (!colon || !comma); i++ {
		switch was[i] {
		case '"':
			end := endOfJSONString(was, i)
			if end < 0 {
				return colon, comma
			}
			i = end - 1
		case ':', ',':
			sep := string(was[i])
			if i+1 < len(was) && was[i+1] == ' ' {
				sep += " "
			}
			if was[i] == ':' && !colon {
				colon, st.colon = true, sep
			} else if was[i] == ',' && !comma {
				comma, st.comma = true, sep
			}
		}
	}
	return colon, comma
}

// objectMembers reads an object's keys in order, with the text each key was
// written as and the raw value under it. ok is false for anything but an
// object.
func objectMembers(b []byte) (keys []string, keyText, vals map[string][]byte, ok bool) {
	dec := json.NewDecoder(bytes.NewReader(b))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, nil, nil, false
	}
	keyText, vals = map[string][]byte{}, map[string][]byte{}
	for dec.More() {
		start := dec.InputOffset()
		t, err := dec.Token()
		if err != nil {
			return nil, nil, nil, false
		}
		k, _ := t.(string)
		text := bytes.Trim(b[start:dec.InputOffset()], ", \t\r\n")
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, nil, nil, false
		}
		if _, dup := vals[k]; !dup {
			keys = append(keys, k)
		}
		keyText[k], vals[k] = text, v
	}
	return keys, keyText, vals, true
}

// arrayElems reads an array's entries as raw values. ok is false for anything
// but an array.
func arrayElems(b []byte) ([][]byte, bool) {
	dec := json.NewDecoder(bytes.NewReader(b))
	if t, err := dec.Token(); err != nil || t != json.Delim('[') {
		return nil, false
	}
	var out [][]byte
	for dec.More() {
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, false
		}
		out = append(out, v)
	}
	return out, true
}

// arrayItems holds the entries the reader wrote on one line in each array,
// keyed by the array's path and then by value, in the order they were written.
// Matching by value finds an entry that moved; decoding each once keeps a long
// array linear rather than decoding every entry for every lookup; and taking
// an entry when it is used hands two equal entries written differently back
// once each, in their own order.
type arrayItems map[string]map[string][][]byte

func inlineArrayItems(b []byte) arrayItems {
	out := arrayItems{}
	for _, c := range scanContainers(b) {
		parent := arrayOf(c.path)
		if parent == "" || bytes.ContainsRune(b[c.start:c.end], '\n') {
			continue
		}
		key, ok := canonicalJSON(b[c.start:c.end])
		if !ok {
			continue
		}
		if out[parent] == nil {
			out[parent] = map[string][][]byte{}
		}
		out[parent][key] = append(out[parent][key], b[c.start:c.end])
	}
	return out
}

// take hands back, and uses up, the reader's text for an entry of this value
// in the array path sits in.
func (a arrayItems) take(path string, flat []byte) []byte {
	byValue := a[arrayOf(path)]
	if len(byValue) == 0 {
		return nil
	}
	key, ok := canonicalJSON(flat)
	if !ok || len(byValue[key]) == 0 {
		return nil
	}
	was := byValue[key][0]
	byValue[key] = byValue[key][1:]
	return was
}

// canonicalJSON is a value's text with keys sorted, so two writings of the same
// value compare equal.
func canonicalJSON(b []byte) (string, bool) {
	var v any
	if json.Unmarshal(b, &v) != nil {
		return "", false
	}
	out, err := json.Marshal(v)
	return string(out), err == nil
}

// arrayOf is the path of the array an entry sits in, or "" when the last step
// is a name.
func arrayOf(path string) string {
	i := strings.LastIndexByte(path, 0)
	if i < 0 || !strings.HasPrefix(path[i+1:], "#") {
		return ""
	}
	return path[:i]
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
// written in: a space after each colon and comma, none inside the brackets.
// relined uses the reader's own spacing instead wherever they showed one.
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
