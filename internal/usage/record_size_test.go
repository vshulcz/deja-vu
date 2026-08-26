package usage

import (
	"strings"
	"testing"
	"time"
)

// `RecordSize` says a record is three times its answer at worst. Two of those
// bytes are the newlines and quotes a digest is full of; the third is a byte
// that is not valid UTF-8, written as the replacement character, which nothing
// strips. Two classes are kept out of the multiplier instead of allowed for:
// control bytes, which SafeText removes, and the angle brackets and ampersands
// encoding/json would escape to six bytes each — the writer turns that escaping
// off, because they are ordinary text (#1982).
func TestWhatARecordWeighs(t *testing.T) {
	const budget = 8192
	for _, c := range []struct {
		name   string
		body   string
		within bool
	}{
		{"plain text", strings.Repeat("the pgbouncer pool kept timing out ", 234), true},
		{"a digest full of newlines", strings.Repeat("- session `abc`\n  user: something happened\n", 195), true},
		{"every byte a newline", strings.Repeat("\n", budget), true},
		{"every byte a quote", strings.Repeat(`"`, budget), true},
		{"cyrillic", strings.Repeat("отладка ", 1024), true},
		{"angle brackets, as code has them", strings.Repeat("<T> & Vec<U> ", 630), true},
		{"a shell pipeline", strings.Repeat("cat a.txt | grep x > out.txt && ", 256), true},
		{"bytes that are not valid UTF-8", strings.Repeat("\xff", budget), true},
		// The one class the bound does not cover, kept so the reason is on the
		// record: six bytes each, and RecordSize would be wrong by twice.
		{"control bytes, which cannot reach here", strings.Repeat("\x01", budget), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, err := marshalSnapshot(Snapshot{
				Time: time.Now().UTC(), Kind: KindBlame, Sessions: 3,
				Bytes: len(c.body), Policy: "local+imported",
				Terms: []string{"pgbouncer", "pool"}, Into: "agent-session-1",
				Digest: c.body,
			})
			if err != nil {
				t.Fatal(err)
			}
			within := len(b) <= RecordSize(len(c.body))
			if within != c.within {
				t.Errorf("record is %d bytes for a body of %d; RecordSize says %d",
					len(b), len(c.body), RecordSize(len(c.body)))
			}
		})
	}
}

// And the room holds the largest answer deja serves, at that bound.
func TestTheLargestAnswerFitsTheRoomWithEscaping(t *testing.T) {
	const largest = 8192 // blame and recall_context, the widest budgets today
	if got := RecordSize(largest); got > RecordRoom {
		t.Errorf("the largest answer weighs %d against %d of room", got, RecordRoom)
	}
	// Not by so much that the check is vacuous: an answer twice as wide would
	// not fit, which is the change this is here to catch.
	if RecordSize(2*largest) <= RecordRoom {
		t.Errorf("a budget of %d would still pass, so this bound constrains nothing", 2*largest)
	}
}

// A record's size must not depend on which Go release built deja. An invalid
// byte is written as the replacement character, and 1.25 escapes that as six
// bytes where 1.27 writes it raw as three — so the digest is coerced to valid
// UTF-8 before it is marshalled, and the answer is three either way (#1982).
func TestARecordIsTheSameSizeOnEveryToolchain(t *testing.T) {
	body := strings.Repeat("\xff", 8192)
	b, err := marshalSnapshot(Snapshot{
		Time: time.Now().UTC(), Kind: KindBlame, Sessions: 1,
		Bytes: len(body), Digest: body,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `\ufffd`) {
		t.Errorf("the record carries escaped replacement characters, which cost six bytes each")
	}
	if len(b) > RecordSize(len(body)) {
		t.Errorf("record is %d bytes for a body of %d; the bound says %d", len(b), len(body), RecordSize(len(body)))
	}
}
