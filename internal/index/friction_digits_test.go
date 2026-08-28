package index

import "testing"

// A host and a port inside an error make every occurrence of one failure a
// different wall: friction never reaches its floor, no fix pair is confirmed
// twice, and search's error tier matches nothing (#2369).
func TestOneErrorIsOneWallAcrossHostsAndPorts(t *testing.T) {
	first, ok := FrictionLine("dial tcp 10.0.0.7:5432: connect: connection refused")
	if !ok {
		t.Fatal("the line is not friction at all, so this measures nothing")
	}
	second, ok := FrictionLine("dial tcp 10.0.0.9:5433: connect: connection refused")
	if !ok {
		t.Fatal("the second line is not friction")
	}
	if frictionHash(first) != frictionHash(second) {
		t.Errorf("one service being down is two walls:\n  %q\n  %q", first, second)
	}
}

// The masking stops where the digits carry the meaning: an exit code says which
// failure this is, and two of them are two failures.
func TestShortCodesKeepTheirIdentity(t *testing.T) {
	a, aok := FrictionLine("make: *** [build] Error 1")
	b, bok := FrictionLine("make: *** [build] Error 2")
	if !aok || !bok {
		t.Fatal("the control lines are not friction")
	}
	if frictionHash(a) == frictionHash(b) {
		t.Errorf("two different failures collapsed into one wall:\n  %q\n  %q", a, b)
	}
}
