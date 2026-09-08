package main

import (
	"strings"
	"testing"
)

// `read: [a.md, b.md]` is a list, and the writer took it for the scalar form —
// so it wrote `- [a.md, b.md]` as one entry and aider looked for a file with
// that name. The reader's two files were gone from its view and the install
// reported success (#3197).
func TestAiderKeepsAFlowListOfReadFiles(t *testing.T) {
	const ctx = "/home/me/.config/deja/aider-context.md"

	got, err := addAiderReadEntry("read: [CONVENTIONS.md, notes.md]\n", ctx)
	if err != nil {
		t.Fatalf("a plain flow list was refused: %v", err)
	}
	for _, want := range []string{"  - CONVENTIONS.md\n", "  - notes.md\n", "  - " + ctx + "\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("the rewritten config lost %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "- [") {
		t.Errorf("the list is still nested inside one entry:\n%s", got)
	}

	// Quotes, spacing and an empty list are the same list.
	for _, in := range []string{
		`read: ["a b.md", 'c.md']`,
		"read: [a.md,b.md]",
		"read: [ a.md , b.md ]",
	} {
		got, err := addAiderReadEntry(in+"\n", ctx)
		if err != nil {
			t.Errorf("%s was refused: %v", in, err)
			continue
		}
		if strings.Contains(got, "- [") || !strings.Contains(got, "  - "+ctx) {
			t.Errorf("%s came out wrong:\n%s", in, got)
		}
	}

	// A shape it cannot take apart is refused, not rewritten. Aider's config is
	// the reader's file, and a wrong rewrite is worse than no install.
	for _, in := range []string{
		"read: [a.md, [b.md, c.md]]",
		`read: [{file: a.md}]`,
		`read: ["unclosed, b.md]`,
		"read: [a.md, ]",
		// A flow list YAML would continue on the next line: there is no
		// closing bracket on this one, so what the list holds is not here.
		"read: [a.md, b.md",
	} {
		if _, err := addAiderReadEntry(in+"\n", ctx); err == nil {
			t.Errorf("%s was rewritten instead of refused", in)
		}
	}

	// The shapes that already worked still do.
	got, err = addAiderReadEntry("read: CONVENTIONS.md\n", ctx)
	if err != nil || !strings.Contains(got, "  - CONVENTIONS.md\n") || !strings.Contains(got, "  - "+ctx) {
		t.Fatalf("the scalar form broke: %v\n%s", err, got)
	}
	got, err = addAiderReadEntry("read:\n  - CONVENTIONS.md\n", ctx)
	if err != nil || !strings.Contains(got, "  - CONVENTIONS.md\n") || !strings.Contains(got, "  - "+ctx) {
		t.Fatalf("the block form broke: %v\n%s", err, got)
	}
	got, err = addAiderReadEntry("", ctx)
	if err != nil || !strings.HasPrefix(got, "read:\n") {
		t.Fatalf("an empty config broke: %v\n%s", err, got)
	}
}
