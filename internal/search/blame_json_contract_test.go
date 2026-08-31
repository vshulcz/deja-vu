package search

import (
	"reflect"
	"strings"
	"testing"
)

// docs/json-output.md promises `deja blame --json` returns a stable array whose
// element shape only grows additively. It lists no fields, so this snapshot is
// the record of that shape: a renamed or removed field fails here, and a new
// one fails too so the addition is a conscious act, not a silent break.
func TestBlameHitShapeIsStable(t *testing.T) {
	want := map[string]bool{
		"session": true, "title": true, "count": true, "snippets": true,
		"score": true, "specificity": true, "tier": true,
		"lifecycle": true, "lifecycle_note": true, "lifecycle_at": true,
	}
	got := map[string]bool{}
	rt := reflect.TypeOf(BlameHit{})
	for i := 0; i < rt.NumField(); i++ {
		name := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			got[name] = true
		}
	}
	for k := range got {
		if !want[k] {
			t.Errorf("BlameHit gained field %q — document it in docs/json-output.md and add it here", k)
		}
	}
	for k := range want {
		if !got[k] {
			t.Errorf("BlameHit lost field %q — a breaking change to the blame --json contract", k)
		}
	}
}
