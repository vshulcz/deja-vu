package search

import (
	"bytes"
	"strings"
	"testing"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/model"
)

// PrintContext heads its output with the session's project and id — transcript
// text a harness wrote. The body is SafeText'd; the header must be too, or
// `deja show`, `deja ctx` and the MCP context tools print an escape or a bidi
// run straight to a terminal or the agent (#1090).
func TestPrintContextSanitisesTheHeader(t *testing.T) {
	var b bytes.Buffer
	s := model.Session{Harness: "claude", Project: "proj\x1b[31m", ID: "id\u202eevil"}
	PrintContext(&b, s, "")
	header := strings.SplitN(b.String(), "\n", 2)[0]
	for _, r := range header {
		if unicode.IsControl(r) {
			t.Fatalf("context header carried a control rune %U: %q", r, header)
		}
	}
	if strings.ContainsRune(header, '\u202e') {
		t.Fatalf("context header carried a bidi override: %q", header)
	}
	if !strings.Contains(header, "proj") {
		t.Fatalf("header lost the project text: %q", header)
	}
}
