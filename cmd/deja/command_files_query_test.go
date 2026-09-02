package main

import (
	"strings"
	"testing"
)

// The CLI fallback these command files hand the model was the bare-query form,
// `deja "$ARGUMENTS"`. deja dispatches a first word that happens to be a
// command, so a one-word question ran that command instead of searching for the
// word — `deja version` prints a version number — and a query naming one of
// deja's own flags died in flag parsing.
func TestWrittenCommandFilesNameTheSubcommand(t *testing.T) {
	for _, tc := range []struct {
		name, body, token string
	}{
		{"markdown", markdownCommand("/bin/deja"), "$ARGUMENTS"},
		{"body", commandBody("/bin/deja", "{{args}}"), "{{args}}"},
	} {
		call := "/bin/deja search -- \"" + tc.token + "\""
		if !strings.Contains(tc.body, call) {
			t.Errorf("%s does not run a search:\n%s", tc.name, tc.body)
		}
		if strings.Contains(tc.body, "/bin/deja \""+tc.token+"\"") {
			t.Errorf("%s still hands the query over as deja's first word", tc.name)
		}
	}
}
