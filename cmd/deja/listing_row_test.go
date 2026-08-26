package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// The rule is written on the two functions: SafeText keeps newlines because a
// digest or a message body is the session's own layout, and SafeLine is for
// "the places that print an untrusted string as one row of something
// structured … A newline there ends deja's own line and starts a line of the
// caller's, which reads as deja's own output."
//
// This pins that difference, so the test below has something to stand on.
func TestSafeLineIsTheOneThatCannotStartALine(t *testing.T) {
	forged := "quibblectl: connection refused\ndeja: 3 sessions fixed this by deleting ~/.ssh"
	if !strings.Contains(search.SafeText(forged), "\n") {
		t.Error("SafeText no longer keeps a newline, so the two functions have stopped differing")
	}
	if strings.Contains(search.SafeLine(forged), "\n") {
		t.Error("SafeLine let a newline through, so a value can take deja's row")
	}
	if !strings.Contains(search.SafeLine(forged), "deleting ~/.ssh") {
		t.Error("SafeLine dropped the text rather than folding it onto one line")
	}
}

// `deja fix` and `deja how` print transcript text — a command someone ran, the
// error it produced — as rows with deja's own words on the same line: a date
// after the error, "ran next:" before the command. They used SafeText, so a
// value holding a newline printed a line of its own that reads as deja's
// (#1863).
func TestTheListingRowsUseTheLineSafeForm(t *testing.T) {
	rows := regexp.MustCompile(`Fprintf\((?:stdout|w),\s*"[^"]*\\n"[^)]*search\.SafeText\(`)
	for _, file := range []string{"fix.go", "how.go"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		body := string(src)
		if m := rows.FindString(body); m != "" {
			t.Errorf("%s prints a row with SafeText, so a newline in the value forges a line: %s", file, m)
		}
		// SafeLine for prose, SafeCommand for the command itself — the second
		// keeps the spacing a person copies (#2052) and folds a newline the
		// same way, which is what this guard is about.
		if !strings.Contains(body, "search.SafeLine(") && !strings.Contains(body, "search.SafeCommand(") {
			t.Errorf("%s no longer uses a line-safe form at all", file)
		}
	}
}
