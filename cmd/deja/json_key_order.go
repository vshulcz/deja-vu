package main

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
)

// Nested key order, for the objects the reader wrote. reorderTopLevel puts the
// top-level keys back, but marshalling sorts every object below it, so an
// install that only added a hook event also rewrote the reader's own entries
// under it — `matcher` after `hooks`, `command` before `type` — and an
// uninstall could not hand the file back as it was (#4210).
//
// Outside any array an object has one place, its path, and keeps the order the
// file had there, with keys deja added after the reader's. Inside an array an
// object is matched by value: the same object, key for key and value for
// value, is one deja did not touch, and copies with one value take the orders
// the file gave them in the order they appear. Anything else is deja's own and
// comes out as marshalling writes it.

// keyOrders is what the reader's file says about key order.
type keyOrders struct {
	byPath  map[string][]string   // path of an object outside any array -> its keys
	byValue map[string][][]string // canonical JSON of an object in an array -> its key orders, in file order
}

// readKeyOrders walks the old file. ok is false when it is not plain JSON, and
// the caller then keeps the order marshalling gives.
func readKeyOrders(old []byte) (keyOrders, bool) {
	ko := keyOrders{byPath: map[string][]string{}, byValue: map[string][][]string{}}
	if len(bytes.TrimSpace(old)) == 0 {
		return ko, false
	}
	dec := json.NewDecoder(bytes.NewReader(old))
	if _, err := ko.read(dec, "", false); err != nil {
		return ko, false
	}
	return ko, true
}

// childPath quotes each segment, so a key holding a slash or a bracket cannot
// pass for two segments.
func childPath(path, key string) string { return path + "/" + strconv.Quote(key) }

// read returns the canonical JSON of what it read when inside an array — the
// only place it is wanted — built from the members' so nothing is marshalled
// twice.
func (ko keyOrders) read(dec *json.Decoder, path string, inArray bool) ([]byte, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, ok := tok.(json.Delim)
	if !ok {
		if !inArray {
			return nil, nil
		}
		return json.Marshal(tok)
	}
	if d == '[' {
		var b bytes.Buffer
		b.WriteByte('[')
		for i := 0; dec.More(); i++ {
			c, err := ko.read(dec, path+"[]", true)
			if err != nil {
				return nil, err
			}
			if i > 0 {
				b.WriteByte(',')
			}
			b.Write(c)
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		b.WriteByte(']')
		return b.Bytes(), nil
	}
	var keys []string
	members := map[string][]byte{}
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return nil, err
		}
		name, _ := k.(string)
		c, err := ko.read(dec, childPath(path, name), inArray)
		if err != nil {
			return nil, err
		}
		if _, dup := members[name]; !dup {
			keys = append(keys, name)
		}
		members[name] = c
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if !inArray {
		ko.byPath[path] = keys
		return nil, nil
	}
	c := canonObject(members)
	ko.byValue[string(c)] = append(ko.byValue[string(c)], keys)
	return c, nil
}

// canonObject is json.Marshal's form of an object whose members are already in
// that form: keys sorted, no space.
func canonObject(members map[string][]byte) []byte {
	keys := make([]string, 0, len(members))
	n := 2
	for k, v := range members {
		keys = append(keys, k)
		n += len(k) + len(v) + 4
	}
	sort.Strings(keys)
	b := bytes.NewBuffer(make([]byte, 0, n))
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		name, _ := json.Marshal(k)
		b.Write(name)
		b.WriteByte(':')
		b.Write(members[k])
	}
	b.WriteByte('}')
	return b.Bytes()
}

func sortedMapKeys(obj map[string]any) []string {
	out := make([]string, 0, len(obj))
	for k := range obj {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// pathOrder is the order for an object outside any array: the reader's keys as
// they had them, then whatever deja added, sorted.
func (ko keyOrders) pathOrder(obj map[string]any, path string) []string {
	sorted := sortedMapKeys(obj)
	written, ok := ko.byPath[path]
	if !ok {
		return sorted
	}
	out := make([]string, 0, len(obj))
	have := map[string]bool{}
	for _, k := range written {
		if _, present := obj[k]; present && !have[k] {
			have[k] = true
			out = append(out, k)
		}
	}
	for _, k := range sorted {
		if !have[k] {
			out = append(out, k)
		}
	}
	return out
}

// valueOrder takes the next order the file gave an object of this value.
func (ko keyOrders) valueOrder(obj map[string]any, canon []byte) []string {
	q := ko.byValue[string(canon)]
	if len(q) == 0 {
		return sortedMapKeys(obj)
	}
	ko.byValue[string(canon)] = q[1:]
	return q[0]
}

// marshalOrdered is json.MarshalIndent with the reader's key order kept where
// the file gives one. The compact form is built here and indented by the
// standard library, so spacing and escaping are what MarshalIndent writes.
func marshalOrdered(ko keyOrders, v any, indent string) ([]byte, error) {
	var b bytes.Buffer
	if _, err := ko.write(&b, v, "", false); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, b.Bytes(), "", indent); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// write appends v's compact JSON to b. Inside an array it also returns v's
// canonical JSON, which an enclosing object needs to find its order.
func (ko keyOrders) write(b *bytes.Buffer, v any, path string, inArray bool) ([]byte, error) {
	switch t := v.(type) {
	case map[string]any:
		if !inArray {
			b.WriteByte('{')
			for i, k := range ko.pathOrder(t, path) {
				if i > 0 {
					b.WriteByte(',')
				}
				name, _ := json.Marshal(k)
				b.Write(name)
				b.WriteByte(':')
				if _, err := ko.write(b, t[k], childPath(path, k), false); err != nil {
					return nil, err
				}
			}
			b.WriteByte('}')
			return nil, nil
		}
		// The members first, each on its own: the order can only be looked up
		// once the whole value is known.
		text := make(map[string][]byte, len(t))
		canon := make(map[string][]byte, len(t))
		for k, e := range t {
			var mb bytes.Buffer
			c, err := ko.write(&mb, e, "", true)
			if err != nil {
				return nil, err
			}
			text[k], canon[k] = mb.Bytes(), c
		}
		c := canonObject(canon)
		b.WriteByte('{')
		for i, k := range ko.valueOrder(t, c) {
			if i > 0 {
				b.WriteByte(',')
			}
			name, _ := json.Marshal(k)
			b.Write(name)
			b.WriteByte(':')
			b.Write(text[k])
		}
		b.WriteByte('}')
		return c, nil
	case []any:
		var cb bytes.Buffer
		b.WriteByte('[')
		cb.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				b.WriteByte(',')
				cb.WriteByte(',')
			}
			c, err := ko.write(b, e, path+"[]", true)
			if err != nil {
				return nil, err
			}
			cb.Write(c)
		}
		b.WriteByte(']')
		cb.WriteByte(']')
		return cb.Bytes(), nil
	default:
		s, err := json.Marshal(t)
		if err != nil {
			return nil, err
		}
		b.Write(s)
		return s, nil
	}
}
