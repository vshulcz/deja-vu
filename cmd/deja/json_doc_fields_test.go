package main

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// docs/json-output.md is the contract for --json consumers, and the session
// object is the part they all parse. `title` and `orig_id` shipped on the wire
// without ever appearing there, so check every field mechanically instead of
// trusting the hand-written examples to keep up.
func TestJSONDocCoversSessionFields(t *testing.T) {
	b, err := os.ReadFile("../../docs/json-output.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)

	typ := reflect.TypeOf(model.Session{})
	for i := 0; i < typ.NumField(); i++ {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		if !strings.Contains(doc, `"`+name+`"`) && !strings.Contains(doc, "`"+name+"`") {
			t.Errorf("session field %q is emitted in --json but not in docs/json-output.md", name)
		}
	}
}
