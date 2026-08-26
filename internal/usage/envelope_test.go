package usage

import (
	"strings"
	"testing"
	"time"
)

// `RecordSize` allows 512 bytes for the envelope — the stamp, the kind, the
// policy name, the terms, the receiving session — and every case that measures
// the escaping uses a body big enough that 3n swamps that half. This measures
// the envelope on its own.
//
// The terms are the part that grows: a prompt containing one long word puts
// that word in the record, and `prompt.Terms` treats a path or a hash as a
// single token (#1988).
func TestTheEnvelopeFitsWhatItIsAllowed(t *testing.T) {
	body := "a digest"
	for _, c := range []struct {
		name string
		snap Snapshot
	}{
		{"a bare record", Snapshot{Time: time.Now().UTC(), Kind: KindHook, Digest: body}},
		{"every field at a plausible size", Snapshot{
			Time: time.Now().UTC(), Kind: KindRecall, Sessions: 99, Bytes: len(body),
			Policy: "local+imported+promoted+whatever-a-policy-name-can-be",
			Terms:  []string{"pgbouncer", "transaction", "prepared", "statements", "timeout", "retry"},
			Into:   strings.Repeat("s", 128), Digest: body,
		}},
		{"terms from a prompt holding a long path", Snapshot{
			Time: time.Now().UTC(), Kind: KindDejaVu, Sessions: 2, Bytes: len(body),
			Terms: []string{strings.Repeat("pgbouncer/", 400), "timeout"}, Digest: body,
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, err := marshalSnapshot(c.snap)
			if err != nil {
				t.Fatal(err)
			}
			if n := len(b); n > RecordSize(len(body)) {
				t.Errorf("record is %d bytes for a digest of %d; RecordSize says %d",
					n, len(body), RecordSize(len(body)))
			}
		})
	}
}
