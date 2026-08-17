package main

import (
	"strings"
	"testing"
)

// A synced index holds paths as the machine that wrote them spelled them, so a
// Windows transcript puts `C:\src\app\x.go` in front of a Unix reader. Splitting
// on filepath.Separator alone made such a path a single segment, so its head was
// never removed: the Unix twin of the same file trimmed to four segments and the
// Windows one printed whole, past the 56-wide column it shares. CrossBase
// (commands.go) was written for the same reason on the same kind of path.
func TestTrimPathTrimsAWindowsOriginPathToo(t *testing.T) {
	cases := []struct{ in, want string }{
		{`C:\src\app\internal\deep\handler.go`, "…/app/internal/deep/handler.go"},
		{`C:\a\b\c.go`, "C:/a/b/c.go"},
		// Mixed, which a path that crossed a sync can be.
		{`C:\src/app\internal/deep\x.go`, "…/app/internal/deep/x.go"},
		// And the Unix behaviour is untouched.
		{"/a/b/c/d.go", "a/b/c/d.go"},
		{"/a/b/c/d/e.go", "…/b/c/d/e.go"},
		{"retry.go", "retry.go"},
	}
	for _, c := range cases {
		if got := trimPath(c.in); got != c.want {
			t.Errorf("trimPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// The column is 56 wide; a real Windows checkout path is longer than that
	// untrimmed, which is the whole point.
	long := `C:\Users\someone\source\repos\Company\Project\src\Services\PaymentHandler.cs`
	if got := trimPath(long); len(got) > 56 || !strings.HasPrefix(got, "…/") {
		t.Errorf("a long windows path came back as %q (%d chars)", got, len(got))
	}
}
