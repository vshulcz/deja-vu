package index

import (
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"
)

// The friction check as it was before the phrase matcher (#5586), kept as
// the oracle the new one has to agree with line for line.

func legacyIsFriction(l string) bool {
	if n := utf8.RuneCountInString(l); n < frictionLineMin || n > frictionLineMax {
		return false
	}
	if legacyNamedTestFailure(l) {
		return true
	}
	if looksLikeSource(l) {
		return false
	}
	if isDejaOwnReport(l) {
		return false
	}
	low := strings.ToLower(l)
	for _, p := range []string{
		"command not found", "modulenotfounderror", "no module named",
		"not found: ", "cannot find", "no such file or directory",
		"undefined:", "connection refused", "permission denied",
		"panic:", "segmentation fault", "exception in thread",
		"nullpointerexception", "index out of range", "stack overflow",
		"typeerror:", "referenceerror:", "keyerror:", "attributeerror:",
		"fatal:", "npm err!", "rpc failed", "timeout expired",
		"statement timeout", "connection reset", "connection timed out",
		"] error ", "build failed", "compilation failed", "cannot be resolved",
		"duplicate key value", "deadlock detected", "access denied for",
		"failed to connect to", "cannot import name", "symbol(s) not found",
		"undefined symbol:", "undefined reference to",
		"failed to push some refs", "acquiring the state lock",
		"no space left on device",
		"go.mod file not found", "does not contain main module",
		"timed out after",
		"tool_use_error", "inputvalidationerror",
		"has been modified since read", "no such tool available",
		"jsondecodeerror", "valueerror:", "runtimeerror:", "oserror:",
		"filenotfounderror:", "importerror:", "unicodedecodeerror:",
	} {
		if strings.Contains(low, p) {
			return true
		}
	}
	if strings.HasPrefix(low, "error") && strings.Contains(low, "does not exist") {
		return true
	}
	if noSuchTableRE.MatchString(low) &&
		(strings.HasPrefix(low, "no such table:") || strings.Contains(low, "error")) {
		return true
	}
	return false
}
func legacyTrimTimestamp(l string) string {
	if m := leadingTimestamp.FindString(l); m != "" {
		return strings.TrimSpace(l[len(m):])
	}
	return l
}
func legacyNamedTestFailure(l string) bool {
	m := goTestFailure.FindStringSubmatch(l)
	if m == nil {
		return false
	}
	// Source that quotes the shape is not a failure — the same reason the
	// quote and comment guards below exist.
	if strings.ContainsAny(l, "\"$") {
		return false
	}
	return len(m[1]) > 3
}

func frictionTestLines() []string {
	lines := []string{ //nolint:prealloc // a fixture literal the loop below grows
		"",
		"zsh:1: command not found: timeout",
		"bash: line 3: COMMAND NOT FOUND: whatever-tool",
		"Error: Cannot find module './config' from app.js",
		"ERROR:  relation \"orders\" does not exist at character 15",
		"the orders table does not exist yet, create it first",
		"Error: no such table: part (code 1) while syncing",
		"no such table: part",
		"there is no such table: the schema only has runs, really",
		"sqlite3.OperationalError: no such table: users found here",
		"--- FAIL: TestSpillWorkersAllocate (0.02s)",
		"--- FAIL: TestX (0.00s)",
		"--- SKIP: TestSomethingLong because it is slow",
		"    --- FAIL: TestIndented (0.01s)",
		"echo \"App not found: $APP\" >&2 # in the deploy script",
		"dial tcp 127.0.0.1:5432: connect: Connection Refused by peer",
		"panic: runtime error: index out of range [3] with length 3",
		"npm ERR! code ERESOLVE unable to resolve dependency tree",
		"[ERROR] Failed to execute goal on project app: compilation failed",
		"TypeError: Cannot read properties of undefined (reading 'x')",
		"KEYERROR: something went wrong with the Key here",
		"Ошибка: команда не найдена: timeout, command not found",
		"İmport error: CANNOT İMPORT NAME 'x' from 'y' module here",
		"json.decoder.JSONDecodeError: Expecting value: line 1 column 1",
		"2026-10-22T12:00:00Z ERROR connection reset by peer while reading",
		"[2026-10-22 12:00:00] fatal: not a git repository (or any parent)",
		"E   ModuleNotFoundError: No module named 'requests' here",
		"deja: fix · 2026-10-01",
		strings.Repeat("x", 199) + " permission denied",
		"a permission denied b " + strings.Repeat("я", 180),
	}
	r := rand.New(rand.NewSource(7)) //nolint:gosec // a seeded fixture, not crypto
	words := []string{"error", "Error", "ERROR", "not", "found", "no", "such", "table:", "does", "exist",
		"command", "Permission", "denied", "panic:", "--- FAIL:", "TestFoo", "echo", "\"", "$(", "K",
		"ё", "ß", "x", " ", "  ", ":", "1", "2026-10-22", "[", "]", "fatal:", "cannot", "find", "timed",
		"out", "after", "symbol(s)", "\xff", "İ", "undefined:", "npm", "err!", "E "}
	for range 20000 {
		var b strings.Builder
		for n := r.Intn(30); n > 0; n-- {
			b.WriteString(words[r.Intn(len(words))])
			if r.Intn(3) > 0 {
				b.WriteByte(' ')
			}
		}
		lines = append(lines, b.String())
	}
	return lines
}

// One pass over a line has to find what seventy strings.Contains on its
// lowered copy found, capitals and non-ASCII lines included.
func TestFrictionMatcherAgreesWithThePhraseList(t *testing.T) {
	for _, l := range frictionTestLines() {
		if got, want := isFriction(l), legacyIsFriction(l); got != want {
			t.Fatalf("isFriction(%q) = %v, want %v", l, got, want)
		}
		if got, want := trimTimestamp(l), legacyTrimTimestamp(l); got != want {
			t.Fatalf("trimTimestamp(%q) = %q, want %q", l, got, want)
		}
		if got, want := namedTestFailure(l), legacyNamedTestFailure(l); got != want {
			t.Fatalf("namedTestFailure(%q) = %v, want %v", l, got, want)
		}
	}
	// Every phrase, in any case, is a hit on its own.
	for _, p := range frictionPhrases {
		l := "ab " + strings.ToUpper(p) + " cd " + strings.Repeat("z", 20)
		if frictionPhrasesIn(l)&hitPhrase == 0 || !utf8.ValidString(l) {
			t.Errorf("%q is not found in %q", p, l)
		}
	}
}

// A line with no phrase in it costs no allocation now: lowering a copy of
// every line of tool output was most of what isFriction allocated (#5586).
func TestAnOrdinaryLineIsCheckedWithoutAllocating(t *testing.T) {
	l := "Compiling github.com/example/app/internal/server v0.0.0 (Took 1.2s)"
	if n := testing.AllocsPerRun(100, func() { _ = isFriction(l) }); n != 0 {
		t.Fatalf("isFriction allocated %v times for a line with no phrase", n)
	}
}
