package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Every line that hands the reader a command to paste puts the value in it
// through pasteSafe: raw, an escape byte in a file name or the reader's own
// query reaches the terminal, and a shell metacharacter makes the pasted
// command run something else (#2768, the rest of #1794).
func TestTheHintsThatOfferACommandQuoteWhatTheyOffer(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	// A file whose name carries both, touched and named in one session. The
	// escape is built rather than written: a control byte in a source file is
	// what TestNoControlBytesInTrackedText exists to keep out.
	esc := string(rune(0x1b))
	path := "/api/internal/esc" + esc + "[31m&&x/parser.go"
	line := `{"type":"assistant","timestamp":"2026-07-10T10:00:00Z","sessionId":"aaaa0001-1111-4000-8000-d6e7f8a9b0c1","cwd":"/api","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"` +
		strings.ReplaceAll(path, esc, "") + `","old_string":"a","new_string":"b"}}]}}`
	if err := os.WriteFile(filepath.Join(store, "one.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	hint := roleServedHint(dir, "parser.go")
	if hint == "" {
		t.Skip("this fixture reaches no hint; the guards below have nothing to hold")
	}
	for _, r := range hint {
		if unicode.IsControl(r) && r != '\n' {
			t.Errorf("a control byte reached the terminal: %q", hint)
			break
		}
	}
	if strings.Contains(hint, "&&x") && !strings.Contains(hint, "'") && !strings.Contains(hint, "$'") {
		t.Errorf("a shell metacharacter is unquoted in a command to paste: %q", hint)
	}
}

// The command the file hook offers carries a path that came out of a
// transcript, which is the same kind of value.
func TestTheFileHookQuotesThePathItOffers(t *testing.T) {
	for _, name := range []string{"esc\x1b[31mX.go", "amp&&id.go"} {
		line := fileHookBlameOffer("head", name)
		for _, r := range line {
			if unicode.IsControl(r) && r != '\n' {
				t.Errorf("%q: a control byte reached the terminal: %q", name, line)
				break
			}
		}
		if strings.Contains(line, "blame "+name) {
			t.Errorf("%q: the path is unquoted in a command to paste: %q", name, line)
		}
	}
}
