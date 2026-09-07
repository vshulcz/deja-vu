package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Qwen frames a command's output as a labelled report before handing it to the
// model. The "Output:" label in front of the first line is what stopped a build
// failure reading as an error, so the fix pair said nothing about a failure the
// store had already settled.
func TestQwenShellReportIsUnwrapped(t *testing.T) {
	raw := json.RawMessage(`{"llmContent":"Command: go build ./...\nDirectory: (root)\nOutput: ./main.go:12:2: undefined: zorbquuxHelper\nError: (none)\nExit Code: 0\nSignal: 0\nProcess Group PGID: 2726"}`)
	got := strings.TrimSpace(toolResponseText(raw))
	if got != "./main.go:12:2: undefined: zorbquuxHelper" {
		t.Errorf("qwen's shell report was not unwrapped: %q", got)
	}
}
