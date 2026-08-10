package main

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func topJSONKeys(v any) []string {
	var out []string
	rt := reflect.TypeOf(v)
	for i := 0; i < rt.NumField(); i++ {
		name := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func assertTopKeys(t *testing.T, name string, v any, want ...string) {
	t.Helper()
	got := topJSONKeys(v)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%s keys = %v, want %v (docs/json-output.md)", name, got, want)
	}
}

// `last --json` and `show --json` use documented envelopes. The nested session
// object is pinned by the search contract test; these guard the envelopes that
// wrap it so a field cannot appear or vanish against docs/json-output.md.
func TestLastAndShowEnvelopesAreStable(t *testing.T) {
	assertTopKeys(t, "recentJSON", recentJSON{}, "schema_version", "sessions", "policy_withheld")
	assertTopKeys(t, "sessionJSON", sessionJSON{}, "schema_version", "session", "window")
	assertTopKeys(t, "sessionWindow", sessionWindow{}, "offset", "limit", "total", "returned")
}
