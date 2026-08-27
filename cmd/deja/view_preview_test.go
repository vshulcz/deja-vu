package main

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The preview is cut by bytes, so a non-ASCII transcript can land the cut in
// the middle of a rune and the page then shows a replacement glyph.
func TestSessionPreviewCutsOnARuneBoundary(t *testing.T) {
	var msgs []model.Message
	for i := 0; i < 90; i++ {
		msgs = append(msgs, model.Message{Role: "user", Text: "x" + strings.Repeat("é", 40)})
	}
	// The trailing cut moved to clipForPage, which runs after the redaction —
	// cutting before it would slice a secret in half and print the half (#2100).
	out := clipForPage(sessionPreview(msgs))
	if len(out) <= viewPreviewBytes {
		t.Fatalf("fixture did not reach the cap: %d bytes", len(out))
	}
	if !utf8.ValidString(out) {
		t.Fatalf("preview is not valid UTF-8, tail: %q", out[len(out)-16:])
	}
	if strings.ContainsRune(out, utf8.RuneError) {
		t.Fatalf("preview ends in a replacement char, tail: %q", out[len(out)-16:])
	}
	if !strings.HasSuffix(out, "…") {
		t.Fatalf("preview lost its ellipsis, tail: %q", out[len(out)-16:])
	}
}
