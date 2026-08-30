package main

import (
	"encoding/json"
	"testing"
)

// The human report names two tools deja needs and says what each one is for.
// The JSON carried one of them, so a machine checking this install could see
// that sqlite3 was missing and not that git was (#2411).
func TestDoctorJSONReportsBothTools(t *testing.T) {
	tmp := hermeticEnv(t)
	_ = tmp
	out, _ := captureBoth(t, "doctor", "--json")

	var report map[string]any
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("doctor --json emitted something that is not JSON: %v\n%s", err, out)
	}
	for _, tool := range []string{"sqlite3", "git"} {
		component, ok := report[tool].(map[string]any)
		if !ok {
			t.Errorf("the report says nothing about %s: %v", tool, report[tool])
			continue
		}
		state, _ := component["state"].(string)
		switch state {
		case "ok", "missing":
		default:
			t.Errorf("%s.state = %q, want ok or missing", tool, state)
		}
	}
}
