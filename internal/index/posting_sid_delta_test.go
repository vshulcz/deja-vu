package index

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Session deltas use modulo-2^32 arithmetic; losing a decrease or boundary ID would make #492 return the wrong session.
func TestPostingSessionDeltaRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		blocks [][]posting
	}{
		{
			name: "single posting",
			blocks: [][]posting{
				{
					{Off: 11, Sid: 7},
				},
			},
		},
		{
			name: "ascending session IDs",
			blocks: [][]posting{
				{
					{Off: 1, Sid: 1},
					{Off: 5, Sid: 2},
					{Off: 20, Sid: 100},
				},
			},
		},
		{
			name: "decreasing session ID",
			blocks: [][]posting{
				{
					{Off: 1, Sid: 100},
					{Off: 2, Sid: 7},
				},
			},
		},
		{
			name: "zero session ID",
			blocks: [][]posting{
				{
					{Off: 1, Sid: 0},
				},
			},
		},
		{
			name: "session ID high bit",
			blocks: [][]posting{
				{
					{Off: 1, Sid: 1 << 31},
				},
			},
		},
		{
			name: "maximum session ID",
			blocks: [][]posting{
				{
					{Off: 1, Sid: math.MaxUint32},
				},
			},
		},
		{
			name: "session ID wrap",
			blocks: [][]posting{
				{
					{Off: 1, Sid: math.MaxUint32},
					{Off: 2, Sid: 0},
				},
			},
		},
		{
			name: "both tool values",
			blocks: [][]posting{
				{
					{Off: 1, Sid: 42, Tool: false},
					{Off: 2, Sid: 42, Tool: true},
				},
			},
		},
		{
			name: "previous session resets between blocks",
			blocks: [][]posting{
				{
					{Off: 1, Sid: 77},
					{Off: 2, Sid: 80},
				},
				{
					{Off: 3, Sid: 77},
					{Off: 5, Sid: 80},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for i, want := range tt.blocks {
				got := decodePostings(encodePostings(want))
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("block %d: got %#v, want %#v", i, got, want)
				}
			}
		})
	}
}

// Repeated session IDs must beat absolute encoding; otherwise #492 pays a rebuild without the measured storage gain.
func TestPostingSessionDeltaWireShrinks(t *testing.T) {
	const sid uint32 = 1 << 28

	posts := make([]posting, 64)
	for i := range posts {
		posts[i] = posting{Off: int64(i + 1), Sid: sid}
	}

	var scratch [binary.MaxVarintLen64]byte
	var prevOff int64
	absoluteLen := 0
	for _, p := range posts {
		absoluteLen += binary.PutUvarint(scratch[:], uint64(p.Off-prevOff))
		absoluteLen += binary.PutUvarint(scratch[:], uint64(p.Sid)<<1)
		prevOff = p.Off
	}

	if got := len(encodePostings(posts)); got >= absoluteLen {
		t.Fatalf("encoded length: got %#v, want less than %#v", got, absoluteLen)
	}
}

// A cut varint is an incomplete posting, not a zero-valued posting; #492 must preserve that partial-read boundary.
func TestPostingSessionDeltaTruncatedVarint(t *testing.T) {
	tests := []struct {
		name string
		wire []byte
	}{
		{
			name: "truncated offset",
			wire: []byte{0x80},
		},
		{
			name: "truncated session",
			wire: []byte{1, 0x80},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decodePostings(tt.wire)
			want := []posting{}
			if len(got) != 0 {
				t.Fatalf("decodePostings: got %#v, want %#v", got, want)
			}
		})
	}
}

// TestPostingSessionDeltaIsZigzagOnTheWire pins the encoding itself, not only
// that it round-trips. A plain modular delta round-trips too, so the table
// above cannot tell the two apart; a step backwards is where they diverge.
// Session 100 then 7 writes the deltas 100 and -93 as zigzag 200 and 185, two
// varint bytes each. Without zigzag the second would be 0xffffffa3, five
// bytes, on every posting whose session id steps back (#492).
func TestPostingSessionDeltaIsZigzagOnTheWire(t *testing.T) {
	got := encodePostings([]posting{{Off: 1, Sid: 100}, {Off: 2, Sid: 7}})
	want := []byte{0x01, 0x90, 0x03, 0x01, 0xf2, 0x02}
	if !bytes.Equal(got, want) {
		t.Fatalf("encodePostings wire = % x, want % x", got, want)
	}
	back := decodePostings(got)
	if len(back) != 2 || back[0].Sid != 100 || back[1].Sid != 7 {
		t.Fatalf("decodePostings round trip = %#v", back)
	}
}

// TestOpenBucketDirRefusesTheOlderMagic is the other half of the format break.
// The version bump rebuilds a writable index, but a directory that cannot be
// locked is served with no version check at all, so a v31 bucket has to fail
// here rather than decode its session ids under the new rule (#492).
func TestOpenBucketDirRefusesTheOlderMagic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x00.bin")
	old := append([]byte("DJB1"), 0x00)
	if err := os.WriteFile(path, old, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openBucketDir(path); !errors.Is(err, errCorruptIndex) {
		t.Fatalf("openBucketDir on a DJB1 bucket = %v, want errCorruptIndex", err)
	}
}
