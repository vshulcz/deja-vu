package index

import "testing"

// The bare-prefix rule drops a line for opening with "Error: ", because that
// alone says nothing. It ran before the phrase list, so it also dropped the
// lines that go on to say something the list knows — a missing module, a
// refused connection — and those are the ones an agent hits twice (#2432).
func TestAGenericPrefixWithSomethingBehindIt(t *testing.T) {
	said := []string{
		`Error: Cannot find module ./config`,
		`error: cannot find package "example.com/x" in any of the module caches`,
		`Error: connection refused while dialing the pool`,
		`error: permission denied while trying to connect to the Docker daemon socket`,
	}
	for _, l := range said {
		if _, ok := FrictionLine(l); !ok {
			t.Errorf("a line naming a wall the list knows was dropped for its prefix:\n  %s", l)
		}
	}

	// A prefix with nothing behind it is still nothing: these say only that
	// something failed, which every run says differently.
	bare := []string{
		`Error: 1`,
		`error: exit status 2`,
		`Error: something went wrong, see above`,
	}
	for _, l := range bare {
		if _, ok := FrictionLine(l); ok {
			t.Errorf("a line that names nothing was read as a wall:\n  %s", l)
		}
	}
}
