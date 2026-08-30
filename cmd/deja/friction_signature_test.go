package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// One failure whose port and host differ per run was a row per run, each row
// holding one session, so none reached the three-session floor and the report
// left out the thing the machine hits most (#2375).
func TestFrictionGroupsOneFailureAcrossItsNumbers(t *testing.T) {
	var lines []string
	for i := 0; i < 4; i++ {
		lines = append(lines, fmt.Sprintf(
			`{"type":"user","sessionId":"s%d","cwd":"/w/p","timestamp":"2026-07-2%dT10:00:00Z",`+
				`"message":{"role":"user","content":[{"type":"tool_result","content":"dial tcp 10.0.0.%d:%d: connect: connection refused"}]}}`,
			i, i+1, 7+i, 5432+i))
	}
	seedFrictionStore(t, lines)

	var out bytes.Buffer
	if err := runFriction(index.DefaultDir(), nil, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "4 sessions") {
		t.Errorf("the four runs are not one wall:\n%s", got)
	}
	if strings.Count(got, "connection refused") != 1 {
		t.Errorf("one failure is listed more than once:\n%s", got)
	}
	// The newest occurrence is what a reader can act on.
	if !strings.Contains(got, "10.0.0.10:5435") {
		t.Errorf("the row does not show the newest of the four:\n%s", got)
	}
}

// Two failures that differ in more than a number stay two rows.
func TestFrictionKeepsDifferentFailuresApart(t *testing.T) {
	var lines []string
	for i := 0; i < 3; i++ {
		lines = append(lines, fmt.Sprintf(
			`{"type":"user","sessionId":"a%d","cwd":"/w/p","timestamp":"2026-07-2%dT10:00:00Z",`+
				`"message":{"role":"user","content":[{"type":"tool_result","content":"dial tcp 10.0.0.%d:5432: connect: connection refused"}]}}`,
			i, i+1, 7+i))
		lines = append(lines, fmt.Sprintf(
			`{"type":"user","sessionId":"b%d","cwd":"/w/p","timestamp":"2026-07-2%dT11:00:00Z",`+
				`"message":{"role":"user","content":[{"type":"tool_result","content":"psql: FATAL: sorry, too many clients already"}]}}`,
			i, i+1))
	}
	seedFrictionStore(t, lines)

	var out bytes.Buffer
	if err := runFriction(index.DefaultDir(), nil, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "connection refused") || !strings.Contains(got, "too many clients") {
		t.Errorf("two different failures did not both survive:\n%s", got)
	}
}
