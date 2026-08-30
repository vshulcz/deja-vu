package index

import "testing"

// Source that carries an error string is a line about an error, not one. The
// bare-quote rule used to catch this and cost far more than it caught (#2430);
// what is left is the shapes source has around the quote — an assignment, a
// call, a struct field, a code span — which tool output does not (#2436).
func TestASourceLineCarryingAnErrorIsNotAWall(t *testing.T) {
	source := []string{
		`in := "the build failed: pgbouncer pool timed out and retried"`,
		`t.Fatalf("compressed dump not found: %d sessions", len(ss))`,
		`{Role: "tool-output", Text: "psql: connection refused on port 5432"},`,
		"`Error: Cannot find module ./config`,",
		`want = "fatal: repository not found, try again"`,
	}
	for _, l := range source {
		if _, ok := FrictionLine(l); ok {
			t.Errorf("a source line quoting an error was counted as one:\n  %s", l)
		}
	}

	// And the errors themselves, which quote what they could not find.
	output := []string{
		`psql: error: connection to server at "localhost", port 5432 failed: Connection refused`,
		`fatal: repository "https://example.invalid/x.git" not found`,
		`ERROR:  relation "orders" does not exist`,
		`curl: (7) Failed to connect to localhost port 5432 after 0 ms`,
		`docker: Error response from daemon: pull access denied for "acme/api"`,
	}
	for _, l := range output {
		if _, ok := FrictionLine(l); !ok {
			t.Errorf("an error was mistaken for source:\n  %s", l)
		}
	}
}
