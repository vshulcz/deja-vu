package main

import (
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
// pins the emitted key set to it in both directions. `policy` (always present)
// and stores[].indexed_sessions were emitted but undocumented until this pin.
func TestDoctorJSONKeysMatchTheDocumentedContract(t *testing.T) {
	documented := map[string]bool{
		// doctorReport
		"schema_version": true, "stores": true, "index": true, "mcp": true,
		"sqlite3": true, "git": true, "version": true, "embed": true, "policy": true,
		"ingest_health": true, "ingest_files": true, "deep": true,
		// doctorStore
		"name": true, "state": true, "paths": true, "files": true,
		"indexed_sessions": true, "indexed_from_elsewhere": true,
		"denied": true, "skipped": true, "partial": true, "unchecked": true,
		// doctorComponent
		"path": true, "stale_stores": true, "sessions_stamped_ahead": true,
		// doctorVersionReport
		"current": true, "latest": true,
		// doctorEmbedReport
		"model": true, "dim": true, "coverage": true, "sidecar": true,
		// doctorPolicyReport / doctorPolicyRule
		"error": true, "activations": true, "ignored": true, "inert": true,
		"rule": true, "withheld": true,
		// doctorSyncReport / doctorPeerReport
		"sync": true, "peers": true, "host": true,
		"last_push": true, "last_pull": true, "sessions_from_there": true,
		"stamped_ahead": true,
		// doctorImportedReport: the machines with no peer row of their own.
		"imported": true, "machine": true, "sessions": true,
		// index.HarnessIngest
		"malformed_lines": true, "clipped_messages": true,
		// index.FileIngest
		"malformed": true, "clipped": true,
		"failed_files": true, "last_error": true,
		// index.DeepReport / index.DeepFinding
		"files_checked": true, "sessions_indexed": true, "sampled_files": true,
		"sampled_postings": true, "stale": true, "findings": true,
		"kind": true, "detail": true,
	}
	emitted := map[string]bool{}
	collectDoctorKeys(reflect.TypeOf(doctorReport{}), emitted)

	for k := range emitted {
		if !documented[k] {
			t.Errorf("doctor --json emits %q, missing from docs/json-output.md", k)
		}
	}
	for k := range documented {
		if !emitted[k] {
			t.Errorf("docs/json-output.md lists %q, no longer emitted by doctor --json", k)
		}
	}
}
