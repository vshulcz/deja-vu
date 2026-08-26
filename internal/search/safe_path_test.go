package search

import "testing"

// A path is an identifier, not prose: what deja prints has to be what deja can
// find again. SafeLine collapses runs of whitespace, which is right for a digest
// row and wrong here — blame printed "/tmp/app/two spaces.go" for a file named
// with two, and restoring that printed path found nothing (#2044).
func TestSafePathKeepsTheSpacesInAName(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"/tmp/app/two  spaces.go", "/tmp/app/two  spaces.go"},
		{"/tmp/app/one space.go", "/tmp/app/one space.go"},
		{"/tmp/app/pool.go", "/tmp/app/pool.go"},
		// Still one line, and still free of what a terminal would obey.
		// A control byte becomes a space rather than vanishing, which is what
		// SafeText has done since #1090: a name is easier to recognise with a
		// gap where the byte was than silently shortened.
		{"/tmp/app/pool\x1b[31m.go", "/tmp/app/pool [31m.go"},
		{"/tmp/app/a\nb.go", "/tmp/app/a b.go"},
		{"/tmp/app/a\tb.go", "/tmp/app/a b.go"},
		// SafeText has already turned the carriage return into a space by the
		// time SafePath maps the newline, so a CRLF costs two.
		{"/tmp/app/a\r\nb.go", "/tmp/app/a  b.go"},
		{"  /tmp/app/pool.go  ", "/tmp/app/pool.go"},
		{"", ""},
	} {
		if got := SafePath(c.in); got != c.want {
			t.Errorf("SafePath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
