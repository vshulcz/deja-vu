package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// collectDoctorKeys gathers every json field name doctorReport marshals,
// through structs, slices, pointers and maps. Map keys are dynamic data
// (harness names, activation names), so only the value type is walked.
func collectDoctorKeys(t reflect.Type, into map[string]bool) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array || t.Kind() == reflect.Map {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		if name != "" {
			into[name] = true
		}
		collectDoctorKeys(f.Type, into)
	}
}

// docs/json-output.md is the published contract for `deja doctor --json`. This
// pins every key doctorReport can marshal to the doctor section, and every key
// of the section's examples to something doctor can marshal. It used to compare
// against a list kept in this file, which called seven keys documented that the
// section never named.
func TestDoctorJSONKeysMatchTheDocumentedContract(t *testing.T) {
	section := docSection(t, jsonOutputDoc(t), "## `deja doctor --json`")
	emitted := map[string]bool{}
	collectDoctorKeys(reflect.TypeOf(doctorReport{}), emitted)
	for _, k := range sortedSet(emitted) {
		if !namesKey(section, k) {
			t.Errorf("doctor --json emits %q, and its section in docs/json-output.md never names it", k)
		}
	}
	for _, m := range jsonBlockRE.FindAllStringSubmatch(section, -1) {
		var v any
		if json.Unmarshal([]byte(m[1]), &v) != nil {
			continue
		}
		shown := map[string]bool{}
		// Keyed by harness, path and activation name in the examples.
		collectJSONKeys(v, []string{"ingest_health", "ingest_files", "activations", "commands"}, nil, shown)
		for _, k := range sortedSet(shown) {
			if !emitted[k] {
				t.Errorf("docs/json-output.md shows %q under doctor --json, which doctor no longer emits", k)
			}
		}
	}
}
