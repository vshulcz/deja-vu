package index

import "testing"

// A double quote anywhere in a line dropped it as source code. Tools quote the
// thing they could not find — a relation, a branch, a module, an image — so the
// rule threw away the errors an agent trips over most, while the same error
// without quotes was learned (#2431).
func TestAnErrorThatQuotesWhatItCouldNotFind(t *testing.T) {
	// Both of these are in the phrase list already — "connection refused" and
	// "fatal:" — and were dropped for their quotes alone.
	friction := []string{
		`psql: error: connection to server at "localhost", port 5432 failed: Connection refused`,
		`fatal: repository "https://example.invalid/x.git" not found`,
	}
	for _, l := range friction {
		if _, ok := FrictionLine(l); !ok {
			t.Errorf("an error deja will keep seeing is not friction:\n  %s", l)
		}
	}

	// Source is still source: a line that quotes an error is not one.
	source := []string{
		`echo "connection refused" >&2`,
		`  "message": "connection refused",`,
		`// panic: connection refused`,
		`printf "permission denied\n"`,
		`if [[ $out =~ "connection refused" ]]; then`,
	}
	for _, l := range source {
		if _, ok := FrictionLine(l); ok {
			t.Errorf("a line about an error was read as one:\n  %s", l)
		}
	}
}
