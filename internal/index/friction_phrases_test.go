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
		`ld: undefined symbol: pthread_setname_np`,
		`ld.lld: error: undefined symbol: pthread_setname_np`,
		`/usr/bin/ld: server.c:(.text+0x1): undefined reference to 'pthread_setname_np'`,
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
		`this causes an undefined symbol when linking the cgo archive`,
		`I think this may be an undefined symbol problem in the build`,
	}
	for _, l := range prose {
		if _, ok := FrictionLine(l); ok {
			t.Errorf("a sentence about a wall was counted as one:\n  %s", l)
		}
	}

	// A line that names a linker error is still source when it is a script,
	// a comment, or a quoted fixture — the same shapes #2430/#2431 reject.
	about := []string{
		`echo "undefined symbol: pthread_setname_np" >&2`,
		`// ld: undefined symbol: pthread_setname_np was the wall last week`,
		`want = "undefined reference to 'pthread_setname_np'"`,
		`# ld: undefined symbol: pthread_setname_np in the notes`,
	}
	for _, l := range about {
		if _, ok := FrictionLine(l); ok {
			t.Errorf("a line about a linker error was read as one:\n  %s", l)
		}
	}
}

// dyld's first line is `Symbol not found: _foo` (Apple Loader.cpp). That
// already matches "not found: ", so #1551 does not add a bare
// "symbol not found" phrase.
func TestADyldMissingSymbolIsAlreadyAWall(t *testing.T) {
	// Public abort: https://stackoverflow.com/questions/50247343
	l := `dyld: Symbol not found: __ZdaPvm`
	if _, ok := FrictionLine(l); !ok {
		t.Errorf("dyld's missing-symbol line is not friction:\n  %s", l)
	}
}
