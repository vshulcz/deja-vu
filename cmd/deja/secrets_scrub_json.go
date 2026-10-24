package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/redact"
)

// The redaction rules are written for decoded message text, the text the index
// holds. Run over the raw file they broke it: a value class that stops at `"`
// took the `\` of an escaped quote with it, so the line stopped parsing, and the
// private-key rule ran from a BEGIN in one record to an END hundreds of records
// later and took every line between (#5622). So the rewrite happens where the
// index reads: inside each JSON string value, decoded, and in a file that is not
// JSON, one line at a time.

// scrubContent is the rewrite scrubOne applies. A variable so a test can hand it
// a broken rule and watch scrubCheck refuse the write.
var scrubContent = scrubTranscript

// scrubNestDepth bounds how deep JSON held in a string is followed, the way the
// change reader bounds it.
const scrubNestDepth = 16

// scrubTranscript redacts b inside each decoded JSON string when b is one JSON
// document, and line by line otherwise: a JSONL line that parses is walked the
// same way, and any other line is redacted as text that ends at its newline.
// Bytes outside a changed string come back as they were.
func scrubTranscript(b []byte, kinds map[string]bool) ([]byte, redact.Counts) {
	counts := redact.Counts{}
	if json.Valid(b) {
		return scrubJSON(b, kinds, counts, 0), counts
	}
	var out []byte
	changed := false
	for line := range bytes.SplitAfterSeq(b, []byte("\n")) {
		clean := scrubLine(line, kinds, counts)
		changed = changed || !bytes.Equal(clean, line)
		out = append(out, clean...)
	}
	if !changed {
		return b, counts
	}
	return out, counts
}

func scrubLine(line []byte, kinds map[string]bool, counts redact.Counts) []byte {
	body := bytes.TrimRight(line, "\r\n")
	tail := line[len(body):]
	var clean []byte
	if json.Valid(body) {
		clean = scrubJSON(body, kinds, counts, 0)
	} else {
		s, c := redact.TextKinds(string(body), kinds)
		addCounts(counts, c)
		clean = []byte(s)
	}
	if bytes.Equal(clean, body) {
		return line
	}
	return append(clean, tail...)
}

// scrubJSON redacts every string value in doc, which must be valid JSON. Object
// keys are left alone. Only a string that changed is re-encoded; everything else,
// whitespace and escaping included, is copied as it was.
func scrubJSON(doc []byte, kinds map[string]bool, counts redact.Counts, depth int) []byte {
	var out []byte
	last := 0
	for i := 0; i < len(doc); i++ {
		if doc[i] != '"' {
			continue
		}
		// Valid JSON, so every quote outside a string opens one, and the
		// literal ends at the first quote no backslash escapes.
		j := i + 1
		for doc[j] != '"' {
			if doc[j] == '\\' {
				j++
			}
			j++
		}
		lit := doc[i : j+1]
		k := j + 1
		for k < len(doc) && (doc[k] == ' ' || doc[k] == '\t' || doc[k] == '\n' || doc[k] == '\r') {
			k++
		}
		isKey := k < len(doc) && doc[k] == ':'
		if !isKey {
			if fixed, ok := scrubLiteral(lit, kinds, counts, depth); ok {
				out = append(out, doc[last:i]...)
				out = append(out, fixed...)
				last = j + 1
			}
		}
		i = j
	}
	if out == nil {
		return doc
	}
	return append(out, doc[last:]...)
}

func scrubLiteral(lit []byte, kinds map[string]bool, counts redact.Counts, depth int) ([]byte, bool) {
	var s string
	if json.Unmarshal(lit, &s) != nil {
		return nil, false
	}
	var clean string
	// A tool result or an argument list is often JSON itself, where every quote
	// arrives escaped once more. It is walked rather than redacted as text, for
	// the same reason the file is.
	if t := strings.TrimSpace(s); depth < scrubNestDepth && len(t) < 4<<20 &&
		(strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")) && json.Valid([]byte(s)) {
		clean = string(scrubJSON([]byte(s), kinds, counts, depth+1))
	} else {
		var c redact.Counts
		clean, c = redact.TextKinds(s, kinds)
		addCounts(counts, c)
	}
	if clean == s {
		return nil, false
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// The clients write `<`, `>` and `&` as they are; Go's default would turn
	// them into < and friends.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(clean); err != nil {
		return nil, false
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), true
}

func addCounts(dst, src redact.Counts) {
	for k, n := range src {
		dst.Add(k, n)
	}
}

// scrubCheck is the last look before the rename, there so a rule that goes wrong
// later cannot cost anyone their history: the rewrite keeps every line, and a
// line or a document that parsed as JSON still does.
func scrubCheck(before, after []byte) error {
	if json.Valid(before) && !json.Valid(after) {
		return errors.New("the rewrite would leave the file no longer valid JSON")
	}
	b := bytes.Split(before, []byte("\n"))
	a := bytes.Split(after, []byte("\n"))
	if len(a) != len(b) {
		return fmt.Errorf("the rewrite would turn %d lines into %d", len(b), len(a))
	}
	broken, first := 0, 0
	for i := range b {
		bl, al := bytes.TrimRight(b[i], "\r"), bytes.TrimRight(a[i], "\r")
		if json.Valid(bl) && !json.Valid(al) {
			if broken == 0 {
				first = i + 1
			}
			broken++
		}
	}
	if broken > 0 {
		return fmt.Errorf("the rewrite would break %d line%s that parse as JSON now, the first is line %d", broken, plural(broken), first)
	}
	return nil
}

// scrubBinary reports whether b is not text: a compressed rollout or a protobuf
// store. The rules have nothing to read there, and a match in it is noise that a
// rewrite would turn into a corrupt file.
func scrubBinary(b []byte) bool {
	if !utf8.Valid(b) {
		return true
	}
	for _, c := range b {
		if c < 0x20 && c != '\t' && c != '\n' && c != '\r' && c != '\f' && c != '\b' && c != 0x1b {
			return true
		}
	}
	return false
}
