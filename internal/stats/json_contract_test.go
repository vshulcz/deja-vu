package stats

import (
	"reflect"
	"strings"
	"testing"
)

// collectJSONKeys gathers every json field name a type marshals, recursing
// through structs, slices and pointers. time.Time and the like carry no json
// tags on their exported fields, so they contribute nothing.
func collectJSONKeys(t reflect.Type, into map[string]bool) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
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
		collectJSONKeys(f.Type, into)
	}
}

// docs/json-output.md is the published contract for `deja stats --json`. This
// pins the emitted key set to it in both directions: a field added to the
// Report without a doc entry fails here, and a documented key that leaves the
// struct fails too. recall.raw_bytes and recall.since were emitted but
// undocumented until this pin caught them.
func TestStatsJSONKeysMatchTheDocumentedContract(t *testing.T) {
	documented := map[string]bool{
		// Report
		"schema_version": true, "total_sessions": true, "total_messages": true,
		"repeat_questions": true, "policy_withheld": true, "harnesses": true,
		"top_projects": true, "monthly": true, "sparkline": true, "date_range": true,
		"longest_session": true, "busiest_day": true, "recall": true,
		"week_recalls": true, "week_bytes": true, "week_injected": true,
		"handoffs_received": true, "agent_credits": true, "week_agent_credits": true,
		"sidecar_size": true, "spans": true, "span_files": true,
		// HarnessStats / ProjectStats / MonthStats
		"harness": true, "sessions": true, "messages": true, "project": true, "month": true,
		// DateRangeStats / SessionStat / DayStat
		"start": true, "end": true, "id": true, "title": true, "date": true,
		// recall (usage.Summary)
		"recalls_served": true, "injections": true, "recall_sessions": true,
		"injected_sessions": true, "bytes": true, "injected_bytes": true,
		"raw_bytes": true, "dejavu_moments": true, "empty_result_rate": true, "since": true,
	}
	emitted := map[string]bool{}
	collectJSONKeys(reflect.TypeOf(Report{}), emitted)

	for k := range emitted {
		if !documented[k] {
			t.Errorf("stats.Report emits %q, missing from docs/json-output.md", k)
		}
	}
	for k := range documented {
		if !emitted[k] {
			t.Errorf("docs/json-output.md lists %q, no longer emitted by stats.Report", k)
		}
	}
}
