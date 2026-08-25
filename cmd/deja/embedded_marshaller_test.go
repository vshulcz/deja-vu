package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/stats"
	"github.com/vshulcz/deja-vu/internal/usage"
)

var jsonMarshaler = reflect.TypeOf((*json.Marshaler)(nil)).Elem()

func writesItsOwnJSON(t reflect.Type) bool {
	return t.Implements(jsonMarshaler) || reflect.PointerTo(t).Implements(jsonMarshaler)
}

// embeddedMarshallers walks a type graph and reports every struct that embeds a
// type writing its own JSON.
//
// It does not spare a struct that appears to write its own: embedding is how
// the method got there in the first place, and reflect cannot tell a promoted
// MarshalJSON from a declared one. Anything that really means to marshal itself
// while embedding such a type keeps the field unexported or wraps it, and would
// say so here.
func embeddedMarshallers(t reflect.Type, seen map[reflect.Type]bool) []string {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array || t.Kind() == reflect.Map {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || seen[t] {
		return nil
	}
	seen[t] = true
	var found []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous && writesItsOwnJSON(f.Type) {
			found = append(found, fmt.Sprintf("%s embeds %s, which writes its own JSON", t, f.Type))
		}
		found = append(found, embeddedMarshallers(f.Type, seen)...)
	}
	return found
}

// An embedded type's MarshalJSON answers for the whole outer struct, so the
// outer fields vanish without a word. `deja stats --impact --json` lost
// `credited_aloud` that way the moment ImpactReport grew a marshaller of its
// own (#1890), and the three types that write their own JSON are all reported
// somewhere. This walks what deja emits so the next one is caught here.
func TestNoReportedTypeEmbedsAMarshaller(t *testing.T) {
	roots := []any{
		doctorReport{}, stats.Report{}, usage.Summary{}, usage.ImpactReport{},
		blameSessionJSON{}, blameHitJSON{}, model.Session{}, index.SessionMeta{},
	}
	// One map across the roots: a type's fields do not depend on which root
	// reached it, so inspecting it once inspects it for all of them.
	seen := map[reflect.Type]bool{}
	var found []string
	for _, r := range roots {
		found = append(found, embeddedMarshallers(reflect.TypeOf(r), seen)...)
	}
	if len(found) > 0 {
		t.Errorf("these types lose their own fields when marshalled:\n%s", strings.Join(found, "\n"))
	}
	// The walk is worth nothing if it cannot see the shape it is looking for.
	type outer struct {
		usage.Summary
		Extra int `json:"extra"`
	}
	if got := embeddedMarshallers(reflect.TypeOf(outer{}), map[reflect.Type]bool{}); len(got) != 1 {
		t.Errorf("the walk misses the case it exists for: %v", got)
	}
	// And the shape really does drop the outer field, which is why it matters.
	b, err := json.Marshal(outer{Extra: 7})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "extra") {
		t.Errorf("Go no longer lets an embedded marshaller answer for the outer struct: %s", b)
	}
}
