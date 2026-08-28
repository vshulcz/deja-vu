package index

import "testing"

// The phrase list is what turns a line of tool output into a wall deja can
// count, name in the environment block and answer with `deja fix`. Measured
// against errors an agent actually hits, it recognised 5 of 24 — the missing
// ones are databases refusing, registries refusing, servers refusing, and
// builds that could not resolve something (#2434).
func TestTheWallsAnAgentActuallyHits(t *testing.T) {
	walls := []string{
		`ERROR:  relation "orders" does not exist`,
		`ERROR:  duplicate key value violates unique constraint "orders_pkey"`,
		`ERROR:  deadlock detected`,
		`Error 1045: Access denied for user 'app'@'localhost'`,
		`docker: Error response from daemon: pull access denied for acme/api`,
		`curl: (7) Failed to connect to localhost port 5432 after 0 ms`,
		`ImportError: cannot import name settings from app`,
		`ld: symbol(s) not found for architecture arm64`,
		`error: failed to push some refs to origin`,
		`Error acquiring the state lock: ConditionalCheckFailedException`,
		`fatal: unable to access https://example.invalid/: Could not resolve host`,
		`write /tmp/build: no space left on device`,
	}
	for _, l := range walls {
		if _, ok := FrictionLine(l); !ok {
			t.Errorf("a wall deja will meet again is not friction:\n  %s", l)
		}
	}

	// Prose is not a wall. These are sentences a person or an agent writes
	// about the same subjects, and counting them would fill `deja friction`
	// with things nobody tripped over.
	prose := []string{
		`the orders table does not exist yet, we create it in the migration`,
		`access denied is what you get without the role, so add it first`,
		`I could not connect the two ideas in that paragraph, rewriting it`,
		`we should push some refs to origin once the tests are green`,
		`the plan is to resolve the host name from the config instead`,
	}
	for _, l := range prose {
		if _, ok := FrictionLine(l); ok {
			t.Errorf("a sentence about a wall was counted as one:\n  %s", l)
		}
	}
}
