package digest

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// A Claude transcript records no exit code for a failed command, so the first
// run of a fix — the one whose output ended in `--- FAIL` — was labelled
// "recorded" in the handoff, next to the later runs that passed. The runner's
// own verdict line in the output that follows settles it; output without one
// leaves the run as recorded.
func TestARunWithoutAnExitCodeTakesTheVerdictOfItsOutput(t *testing.T) {
	at := time.Date(2026, 6, 29, 10, 0, 0, 0, time.UTC)
	s := model.Session{
		ID: "e7a3c210", Harness: "claude",
		Messages: []model.Message{
			{Role: "user", Text: "the pool tests fail on CI with connection refused, fix it", Time: at},
			{Role: sources.RoleCommand, Text: "$ go test ./internal/pool/...", Time: at.Add(time.Minute)},
			{Role: sources.RoleToolOutput, Text: "--- FAIL: TestCheckout (0.02s)\nFAIL\nexit status 1", Time: at.Add(2 * time.Minute)},
			{Role: sources.RoleCommand, Text: "$ go test ./internal/store/...", Time: at.Add(3 * time.Minute)},
			{Role: sources.RoleToolOutput, Text: "ok  \texample.com/checkout/internal/store\t0.412s", Time: at.Add(4 * time.Minute)},
			{Role: sources.RoleCommand, Text: "$ npm test", Time: at.Add(5 * time.Minute)},
			{Role: sources.RoleToolOutput, Text: "DONE", Time: at.Add(6 * time.Minute)},
		},
	}
	c := ExtractCompactionContext(s, ExtractOptions{})
	var got []string
	for _, tc := range c.Tests {
		got = append(got, tc.Outcome)
	}
	if strings.Join(got, ",") != "failed,passed,recorded" {
		t.Fatalf("outcomes = %v, want failed,passed,recorded", got)
	}
}
