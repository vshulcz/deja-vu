package usage

import (
	"reflect"
	"strings"
	"testing"
)

func topLevelJSONKeys(v any) map[string]bool {
	keys := map[string]bool{}
	rt := reflect.TypeOf(v)
	for i := 0; i < rt.NumField(); i++ {
		name := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			keys[name] = true
		}
	}
	return keys
}

func assertShape(t *testing.T, name string, got, want map[string]bool) {
	t.Helper()
	for k := range got {
		if !want[k] {
			t.Errorf("%s gained field %q — a change to a documented-stable --json shape", name, k)
		}
	}
	for k := range want {
		if !got[k] {
			t.Errorf("%s lost field %q — a breaking change to the --json contract", name, k)
		}
	}
}

// `deja log --json` returns []Event and `deja log --last --json` returns one
// Snapshot. docs/json-output.md promises both shapes stay stable and only grow
// additively; these snapshots enforce that.
func TestLogJSONShapesAreStable(t *testing.T) {
	assertShape(t, "Event", topLevelJSONKeys(Event{}), map[string]bool{
		"t": true, "kind": true, "bytes": true, "sessions": true,
		"empty": true, "raw": true, "ids": true,
	})
	assertShape(t, "Snapshot", topLevelJSONKeys(Snapshot{}), map[string]bool{
		"t": true, "kind": true, "sessions": true, "bytes": true,
		"policy": true, "terms": true, "digest": true,
	})
}
