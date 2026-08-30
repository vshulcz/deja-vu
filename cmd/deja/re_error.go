package main

import (
	"errors"
	"fmt"
	"regexp/syntax"
	"strconv"
)

// rePatternError says a pattern cannot be used, in the same voice the other
// bad flags answer in: `--since bogus` is "not a duration deja understands",
// `--limit abc` "needs an integer from 1 to 100". This one used to hand over
// Go's `error parsing regexp: …` behind a function name (#1602).
//
// Go's message already names what is wrong with the pattern — "missing closing
// )", "invalid character class range: z-a" — so the fix is to keep that half
// and drop the prefix and the repeated pattern, not to write a worse
// explanation of our own.
func rePatternError(pattern string, err error) error {
	var perr *syntax.Error
	if !errors.As(err, &perr) {
		// Not the compile failing — whatever else RunDetailed returns travels
		// as it is rather than being described as a bad pattern.
		return err
	}
	what := string(perr.Code)
	if perr.Expr != "" && perr.Expr != pattern {
		// Quoted like the pattern itself: Go prints the offending fragment
		// raw, so a pattern holding an escape byte put one on the terminal
		// through the very line that exists to keep them off it.
		what += ": " + strconv.Quote(perr.Expr)
	}
	return fmt.Errorf("--re %q is not a pattern deja can use — %s", pattern, what)
}
