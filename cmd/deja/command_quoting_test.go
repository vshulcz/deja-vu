package main

import (
	"strings"
	"testing"
)

// The command file tells the reader — a model or a person — what to run when
// the MCP tools are not there. They copy it into a shell verbatim, so a home
// like "/Users/John Smith" must not be pasted bare: the shell would run
// "/Users/John" and pass the rest as arguments.
func TestCommandSnippetQuotesAPathWithSpaces(t *testing.T) {
	body := commandBody("/Users/John Smith/bin/deja", "the user's request")
	if strings.Contains(body, "\n/Users/John Smith/bin/deja ") {
		t.Fatalf("the path is pasted bare into a shell snippet:\n%s", body)
	}
	if !strings.Contains(body, `'/Users/John Smith/bin/deja'`) {
		t.Fatalf("the path is not quoted:\n%s", body)
	}
}

// An ordinary path stays plain: quoting it would suggest that escaping matters
// here when it does not.
func TestCommandSnippetLeavesAPlainPathAlone(t *testing.T) {
	body := commandBody("/usr/local/bin/deja", "the user's request")
	if strings.Contains(body, `'/usr/local/bin/deja'`) {
		t.Fatalf("an ordinary path was quoted:\n%s", body)
	}
	// The snippet names the subcommand now; what this test is about is that the
	// path itself is left unquoted.
	if !strings.Contains(body, "/usr/local/bin/deja search -- \"the user's request\"") {
		t.Fatalf("the snippet lost its shape:\n%s", body)
	}
}

func TestShellQuoteIfNeeded(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"/usr/local/bin/deja", "/usr/local/bin/deja"},
		{"/Users/John Smith/deja", `'/Users/John Smith/deja'`},
		{"/opt/it's/deja", `'/opt/it'"'"'s/deja'`},
		{"/opt/$HOME/deja", `'/opt/$HOME/deja'`},
	} {
		if got := shellQuoteIfNeeded(tc.in); got != tc.want {
			t.Errorf("shellQuoteIfNeeded(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
