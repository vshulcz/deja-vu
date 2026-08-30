package main

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

// Every bad invocation answers in deja's own voice — "is not a duration deja
// understands", "needs an integer from 1 to 100". The --re error was the one
// that handed over Go's regexp wording behind a stage name (#1602).
func TestABadPatternIsRefusedInDejasOwnVoice(t *testing.T) {
	for _, c := range []struct{ pattern, want string }{
		{"retry(", `--re "retry(" is not a pattern deja can use — missing closing )`},
		{"a**", `--re "a**" is not a pattern deja can use — invalid nested repetition operator: "**"`},
		{"[z-a]", `--re "[z-a]" is not a pattern deja can use — invalid character class range: "z-a"`},
	} {
		_, err := regexp.Compile(c.pattern)
		if err == nil {
			t.Fatalf("%q compiles; pick a pattern that does not", c.pattern)
		}
		got := rePatternError(c.pattern, err).Error()
		if got != c.want {
			t.Errorf("rePatternError(%q):\n got %s\nwant %s", c.pattern, got, c.want)
		}
		if strings.Contains(got, "error parsing regexp") || strings.Contains(got, "run:") {
			t.Errorf("%q still names an internal stage or Go's prefix: %s", c.pattern, got)
		}
	}
}

// An error that is not a regexp failure is passed through as it is: this path
// carries whatever RunDetailed returns, and inventing a pattern sentence for
// something else would be worse than plain wrapping.
func TestAnErrorThatIsNotAboutThePatternIsLeftAlone(t *testing.T) {
	err := rePatternError("retry", errors.New("read: disk fell over"))
	if !strings.Contains(err.Error(), "disk fell over") {
		t.Errorf("the underlying error was dropped: %v", err)
	}
	if strings.Contains(err.Error(), "is not a pattern") {
		t.Errorf("an unrelated failure was reported as a bad pattern: %v", err)
	}
}

// The fragment Go names is part of the pattern, so it carries whatever the
// pattern did: an escape byte there reached the terminal through the one line
// written to keep it off (#1794's shape, in #1602's fix).
func TestTheFragmentInTheRefusalCarriesNoControlByte(t *testing.T) {
	// Assembled at run time: a pattern that cannot compile is what
	// staticcheck's SA1000 exists to catch, and here it is the input, so it
	// must not be a constant it can fold.
	pattern := badRangePattern()
	_, err := regexp.Compile(pattern)
	if err == nil {
		t.Fatal("that pattern compiles now; pick another")
	}
	msg := rePatternError(pattern, err).Error()
	for _, r := range msg {
		if unicode.IsControl(r) {
			t.Fatalf("the refusal carries a control byte: %q", msg)
		}
	}
}

// badRangePattern is a character class whose ends are the wrong way round,
// built rather than written: as a literal, staticcheck reads it as a mistake
// instead of as this test's input.
func badRangePattern() string {
	ends := []byte{0x1b, 0x01}
	return "[" + string(ends[0]) + "-" + string(ends[1]) + "]"
}
