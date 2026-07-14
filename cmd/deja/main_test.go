package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintNoMatchesHelpfulMessage(t *testing.T) {
	var b bytes.Buffer
	printNoMatches(&b, "jwt refresh token", 3)
	out := b.String()
	if !strings.Contains(out, `deja: no matches for "jwt refresh token"`) || !strings.Contains(out, "searched 3 sessions across claude/codex/opencode") || !strings.Contains(out, "try fewer words or --re") {
		t.Fatalf("bad no-match message: %q", out)
	}
}
