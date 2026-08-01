package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// null is not an empty list: len() raises, iteration raises, `jq '.[]'`
// errors. Every other machine-readable output in deja keeps its shape when
// there is nothing to report, and this is the one a script polls (#733).
func TestLogJSONIsAlwaysAList(t *testing.T) {
	withTempStores(t)
	var buf strings.Builder
	if err := runLogTo(&buf, t.TempDir(), []string{"--json"}); err != nil {
		t.Fatal(err)
	}
	var events []map[string]any
	if err := json.Unmarshal([]byte(buf.String()), &events); err != nil {
		t.Fatalf("empty log is not a list: %q (%v)", buf.String(), err)
	}
	if events == nil {
		t.Errorf("decoded to nil from %q", buf.String())
	}
	if len(events) != 0 {
		t.Errorf("empty log returned %d events", len(events))
	}
	if strings.TrimSpace(buf.String()) != "[]" {
		t.Errorf("empty log printed %q", strings.TrimSpace(buf.String()))
	}
}

// A number is not a flag, and saying so sends the reader looking for a flag
// they never typed.
func TestLogRejectsANonPositiveCountAsACount(t *testing.T) {
	withTempStores(t)
	var buf strings.Builder
	err := runLogTo(&buf, t.TempDir(), []string{"0"})
	if err == nil || !strings.Contains(err.Error(), "must be positive") {
		t.Errorf("log 0: %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("log 0 still called a number a flag: %v", err)
	}
	// A word that is not a number is still an unknown flag.
	err = runLogTo(&buf, t.TempDir(), []string{"abc"})
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("log abc: %v", err)
	}
	// A valid count still works.
	buf.Reset()
	if err := runLogTo(&buf, t.TempDir(), []string{"5"}); err != nil {
		t.Errorf("log 5: %v", err)
	}
}
