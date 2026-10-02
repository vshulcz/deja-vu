package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A workspace read off a Roo CLI task is whatever history_item.json says, so
// resumeWord sees attacker-written text. These are the ones that broke out of
// the quoted word, or into roo's options, in some shell the line is pasted into.
func TestResumeWordRefusesWhatSomeShellActsOn(t *testing.T) {
	for _, s := range []string{
		// fish reads \' inside single quotes as a quote, so the word runs on
		// past its end and the next quoted word turns inside out: with the cd
		// in front, `;id;` ran.
		`/tmp/;id;\`,
		`C:\`,
		// PowerShell closes a single-quoted string on any of these, and the
		// outer double-quoted -Command on the double ones.
		"C:\\x\u2019; calc; \u2019",
		"C:\\x\u2018",
		"C:\\x\u201a",
		"C:\\x\u201b",
		"C:\\x\u201c; calc",
		"C:\\x\u201d",
		"C:\\x\u201e",
		// A leading dash is an option to roo, not its -w value.
		"--yolo",
		"-w",
	} {
		if w, ok := resumeWord(s); ok {
			t.Errorf("resumeWord(%q) = %q, want refused", s, w)
		}
	}
}

// PowerShell, which a Windows resume line runs in, reads a bare a,b as two
// arguments and a bare @x as a splatted variable, so those are quoted.
func TestResumeWordQuotesWhatPowerShellSplits(t *testing.T) {
	for _, s := range []string{"C:/a,--yolo", "@x", "50%"} {
		w, ok := resumeWord(s)
		if !ok || w != "'"+s+"'" {
			t.Errorf("resumeWord(%q) = %q, %v; want it quoted", s, w, ok)
		}
	}
}

// Whatever resumeWord lets onto a command, --exec reads back as the one
// argument it was, and no character a shell acts on is left outside quotes.
func FuzzResumeWordRoundTrips(f *testing.F) {
	for _, s := range []string{"", "/tmp/proj", `C:\Users\JOHNSM~1\My Projects\app`, "a b", "bob's", "$(id)", "`id`", "a\nb", `x\`, "-x", "a,b", "\u2019", "é", "a\u00a0b", `\\srv\share`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		w, ok := resumeWord(s)
		if !ok {
			return
		}
		if !utf8.ValidString(s) {
			t.Fatalf("resumeWord(%q) = %q; --exec reads an invalid byte back as U+FFFD", s, w)
		}
		line := "roo -w " + w + " --session-id 01a07bf9-8882-7703-a3fa-245deb8ea753"
		got, err := resumeArgv(line)
		if err != nil || len(got) != 5 || got[2] != s {
			t.Fatalf("resumeWord(%q) = %q, read back as %q, %v", s, w, got, err)
		}
		if strings.HasPrefix(s, "-") || strings.HasSuffix(s, `\`) || strings.ContainsAny(s, "'\"$`\u2018\u2019\u201a\u201b\u201c\u201d\u201e") {
			t.Fatalf("resumeWord(%q) = %q, want refused", s, w)
		}
		if !strings.HasPrefix(w, "'") {
			for _, r := range w {
				if !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_+:./-", r) {
					t.Fatalf("resumeWord(%q) = %q bare with %q in it", s, w, r)
				}
			}
		}
	})
}
