package index

import (
	"strings"
	"testing"
)

// The line bound was set to what a person can recognise, and real errors carry
// paths: a go build error naming a module and a file, a docker pull naming a
// registry, a k8s message naming a namespace and a pod. Measured over the store
// on this machine, the 120-character cap dropped 2,319 lines that name a wall
// the list knows — a third again of the 7,053 it recognised (#2438).
func TestALongErrorIsStillAWall(t *testing.T) {
	walls := []string{
		`/Users/someone/code/service/internal/handler/orders.go:118:24: undefined: repository.FindOrderByExternalReference in the orders package`,
		`docker: Error response from daemon: pull access denied for registry.example.internal/team/service, repository does not exist or may require authorisation`,
		`psql: error: connection to server at "db.internal.example.com" (10.42.0.7), port 5432 failed: Connection refused — is the server running there?`,
	}
	for _, l := range walls {
		// Longer than the bound these were dropped by, so each case still
		// stands for the errors that carry a path.
		if n := len([]rune(l)); n <= 120 {
			t.Fatalf("this case no longer tests the bound it was written for: %d runes", n)
		}
		if _, ok := FrictionLine(l); !ok {
			t.Errorf("a wall was dropped for its length (%d runes):\n  %s", len([]rune(l)), l)
		}
	}

	// A bound there still is: a whole page of output pasted as one line is not
	// a wall anyone can recognise, and hashing it would make a wall of its own
	// every run.
	if _, ok := FrictionLine("connection refused " + strings.Repeat("x", 400)); ok {
		t.Errorf("a line with no bound at all was counted as a wall")
	}
}
