package main

import (
	"bytes"
	"encoding/json"
	"sort"
)

// Nested key order, for the objects the reader wrote. reorderTopLevel puts the
// top-level keys back, but marshalling sorts every object below it, so an
// install that only added a hook event also rewrote the reader's own entries
// under it — `matcher` after `hooks`, `command` before `type` — and an
// uninstall could not hand the file back as it was (#4210).
//
// An object keeps the reader's order when the file holds the same object, key
// for key and value for value: that is one deja did not touch. One deja
// changed keeps it too when it sits where only one object can, outside any
// array — the `hooks` map an event was added to — with keys deja added after
// the reader's. Anything else is deja's own and comes out as marshalling
// writes it.

// keyOrders is what the reader's file says about key order.
type keyOrders struct {
	byValue map[string][]string // canonical JSON of an object -> its keys as written
	byPath  map[string][]string // path of an object outside any array -> its keys
}

// readKeyOrders walks the old file. ok is false when it is not plain JSON,
// and the caller then keeps the order marshalling gives.
func readKeyOrders(old []byte) (keyOrders, bool) {
	ko := keyOrders{byValue: map[string][]string{}, byPath: map[string][]string{}}
	if len(bytes.TrimSpace(old)) == 0 {
		return ko, false
	}
	dec := json.NewDecoder(bytes.NewReader(old))
	if _, err := ko.read(dec, "", false); err != nil {
		return ko, false
	}
	return ko, true
}

func (ko keyOrders) read(dec *json.Decoder, path string, inArray bool) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	switch d {
	case '{':
		obj := map[string]any{}
		var keys []string
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return nil, err
			}
			name, _ := k.(string)
			v, err := ko.read(dec, path+"."+name, inArray)
			if err != nil {
				return nil, err
			}
			if _, dup := obj[name]; !dup {
				keys = append(keys, name)
			}
			obj[name] = v
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		if canon, err := json.Marshal(obj); err == nil {
			if _, seen := ko.byValue[string(canon)]; !seen {
				ko.byValue[string(canon)] = keys
			}
		}
		if !inArray {
			ko.byPath[path] = keys
		}
		return obj, nil
	case '[':
		arr := []any{}
		for dec.More() {
			v, err := ko.read(dec, path+"[]", true)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return arr, nil
	}
	return nil, nil
}

// orderFor is the key order to write an object in.
func (ko keyOrders) orderFor(obj map[string]any, path string, inArray bool) []string {
	sorted := make([]string, 0, len(obj))
	for k := range obj {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	if canon, err := json.Marshal(obj); err == nil {
		if keys, ok := ko.byValue[string(canon)]; ok && len(keys) == len(obj) {
			return keys
		}
	}
	if inArray {
		return sorted
	}
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

// marshalOrdered is json.MarshalIndent with the reader's key order kept where
// orderFor finds one. The compact form is built here and indented by the
// standard library, so spacing and escaping are what MarshalIndent writes.
func marshalOrdered(ko keyOrders, v any, indent string) ([]byte, error) {
	var b bytes.Buffer
	if err := ko.write(&b, v, "", false); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, b.Bytes(), "", indent); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func (ko keyOrders) write(b *bytes.Buffer, v any, path string, inArray bool) error {
	switch t := v.(type) {
	case map[string]any:
		b.WriteByte('{')
		for i, k := range ko.orderFor(t, path, inArray) {
			if i > 0 {
				b.WriteByte(',')
			}
			name, err := json.Marshal(k)
			if err != nil {
				return err
			}
			b.Write(name)
			b.WriteByte(':')
			if err := ko.write(b, t[k], path+"."+k, inArray); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := ko.write(b, e, path+"[]", true); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	default:
		s, err := json.Marshal(t)
		if err != nil {
			return err
		}
		b.Write(bytes.TrimRight(s, "\n"))
	}
	return nil
}
