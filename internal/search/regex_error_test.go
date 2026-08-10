package search

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// An invalid --re pattern must report the pattern the user typed, not deja's
// case-insensitive prefix. Compiling "(?i)"+query echoed `(?i)(` back at someone
// who wrote `(`, which reads like their own text and hides the real mistake.
func TestRegexErrorNamesTheUserPattern(t *testing.T) {
	_, err := Run([]model.Session{}, Options{Query: "(", Regex: true})
	if err == nil {
		t.Fatal("an invalid regex must be reported, not swallowed")
	}
	if strings.Contains(err.Error(), "(?i)") {
		t.Fatalf("the error leaks deja's injected (?i) prefix: %v", err)
	}
	if !strings.Contains(err.Error(), "`(`") && !strings.Contains(err.Error(), ": (") {
		t.Fatalf("the error does not name the user's pattern: %v", err)
	}
}
