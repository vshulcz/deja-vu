package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// arguments is optional in tools/call, and a wrong type is something an agent
// can correct — if the message names what was wrong instead of handing back the
// Go decoder's own text (#1723).
func TestToolArgumentErrorsAreAboutTheArguments(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{"recall", ``, "query required"},
		{"recall", `null`, "query required"},
		{"recall", `{}`, "query required"},
		{"recall", `{"query":5}`, `recall: "query" must be a string`},
		{"recall_context", ``, "query required"},
		{"blame", `{"path":[]}`, `blame: "path" must be a string`},
		{"fix", ``, "error text required"},
		{"how", ``, "what required"},
		{"remember", ``, "text required"},
		{"recall", `[1,2]`, "recall: arguments must be an object"},
	} {
		_, err := callMCPTool(t.TempDir(), tc.name, json.RawMessage(tc.raw))
		if err == nil {
			t.Errorf("%s(%s): no error at all", tc.name, tc.raw)
			continue
		}
		if got := err.Error(); got != tc.want {
			t.Errorf("%s(%s) = %q, want %q", tc.name, tc.raw, got, tc.want)
		}
		if strings.Contains(err.Error(), "Go struct") || strings.Contains(err.Error(), "JSON input") {
			t.Errorf("%s(%s) leaks the decoder's own text: %q", tc.name, tc.raw, err)
		}
	}
}
