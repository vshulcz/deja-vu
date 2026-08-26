package usage

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// `RecordSize` says an answer doubles at worst. That rests on escaping being
// two bytes per character, which holds for the newlines and quotes a digest is
// full of — and not for a control byte, which costs six. The bound is only true
// because control bytes never reach a digest, and this is the arithmetic half
// of that claim: the sanitising half lives in internal/search.
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
		// The case the bound does not cover, kept here so the reason is on the
		// record: six bytes each, and RecordSize would be wrong by 3x.
		{"control bytes, which cannot reach here", strings.Repeat("\x01", budget), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, err := json.Marshal(Snapshot{
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
