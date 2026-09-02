package main

import (
	"strings"
	"testing"
)

// Every plugin deja writes builds a search out of what the user typed, and a
// query that names one of deja's own flags — `--json`, `--all-matches` — dies
// in flag parsing. The caller turns the failed call into "", so the reader is
// told their history holds nothing, which is worse than an error.
//
// Measured against a real store: of eight dash-leading queries, six failed
// without `--` and all eight answered with it.
func TestGeneratedPluginsEndTheFlagsBeforeTheQuery(t *testing.T) {
	for _, tc := range []struct {
		name string
		js   string
		// how the plugin spells the guarded call, whitespace-free
		want string
	}{
		{"cline", clinePluginJS("/bin/deja"), `["search","--",query]`},
		{"pi", piExtensionTS("/bin/deja"), `["search","--",query]`},
		{"deepseek", dshCommandJS("/bin/deja"), `["search","--",query]`},
		{"hermes", hermesPluginPy("/bin/deja"), `["search","--",query]`},
	} {
		compact := strings.Join(strings.Fields(tc.js), "")
		if !strings.Contains(compact, tc.want) {
			t.Errorf("%s never sends the terminator", tc.name)
		}
		// And still sends the plain form, or every ordinary query would carry
		// a terminator a deja that predates it cannot read.
		if !strings.Contains(compact, `["search",query]`) {
			t.Errorf("%s always sends the terminator", tc.name)
		}
		// The guard has to be on the query's own shape.
		if !strings.Contains(compact, `query.startsWith("-")`) &&
			!strings.Contains(compact, `query.startswith("-")`) {
			t.Errorf("%s decides without looking at the query", tc.name)
		}
	}
}
