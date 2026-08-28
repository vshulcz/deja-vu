package usage

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

// The reader caps a line at 4 MB and nothing capped the write, so a digest past
// that was written and then unreadable: `deja log` showed nothing, and the next
// rotation rewrote the file without it. A record deja cannot read is worse than
// a record it clipped (#2222).
func TestADigestTooLargeToReadBackIsClippedRatherThanLost(t *testing.T) {
	for _, size := range []int{8 << 10, 100 << 10, snapshotDigestMax, snapshotDigestMax + 1, 5 << 20, 16 << 20} {
		dir := t.TempDir()
		digest := strings.Repeat("z", size)
		RecordDigestTerms(dir, KindResource, digest, 1, 0, nil, "s1")

		got := snapshotsFrom(SnapshotPath(dir), 0)
		if len(got) != 1 {
			fi, _ := os.Stat(SnapshotPath(dir))
			t.Errorf("a %d B digest wrote %d bytes and read back as %d records", size, fi.Size(), len(got))
			continue
		}
		// What survives is the head of what was served, so the log still says
		// what the agent was given.
		if !strings.HasPrefix(got[0].Digest, strings.Repeat("z", 4096)) {
			t.Errorf("a %d B digest came back as something else: %.80q", size, got[0].Digest)
		}
		if size <= snapshotDigestMax {
			// Anything inside the budget is kept whole, and everything deja
			// actually injects is far inside it.
			if got[0].Digest != digest {
				t.Errorf("a %d B digest was clipped to %d, though it is within the budget", size, len(got[0].Digest))
			}
			continue
		}
		// The kept head is inside the budget. The record itself is a little
		// longer than the head, because it says it was clipped.
		head := got[0].Digest[:strings.LastIndex(got[0].Digest, "\n… (clipped")]
		if len(head) > snapshotDigestMax {
			t.Errorf("a %d B digest kept %d bytes, over the %d budget", size, len(head), snapshotDigestMax)
		}
		if len(head) >= size {
			t.Errorf("a %d B digest was not clipped: %d bytes kept", size, len(head))
		}
		// And the clipping says so, rather than ending mid-sentence.
		if !strings.Contains(got[0].Digest, "clipped") {
			t.Errorf("a clipped digest does not say it was clipped: %.120q", got[0].Digest[len(got[0].Digest)-120:])
		}
		// Bytes is what was served, not what was kept: the log is about the
		// injection, and clipping is this file's business.
		if got[0].Bytes != size {
			t.Errorf("bytes = %d, want the %d that were served", got[0].Bytes, size)
		}
	}
}

// The shapes that make escaping expensive, and the cut itself. A digest of
// newlines doubles in JSON, invalid bytes triple, and a cut in the middle of a
// rune is a mojibake tail on the last line someone reads (#2222).
func TestClippingSurvivesTheShapesThatCostMost(t *testing.T) {
	for _, c := range []struct {
		name   string
		digest string
	}{
		{"newlines, which double", strings.Repeat("\n", 6<<20)},
		{"quotes, which double", strings.Repeat(`"`, 6<<20)},
		{"invalid bytes, which triple", strings.Repeat(string([]byte{0xff}), 6<<20)},
		{"multi-byte runes", strings.Repeat("é", 3<<20)},
		{"one long rune run", strings.Repeat("🙂", 2<<20)},
		// One byte in front, so the budget falls inside a rune rather than
		// between two: this is what backs the cut up.
		{"a rune the cut lands inside", "a" + strings.Repeat("日", 1<<20)},
	} {
		dir := t.TempDir()
		RecordDigestTerms(dir, KindResource, c.digest, 1, 0, nil, "s1")
		got := snapshotsFrom(SnapshotPath(dir), 0)
		if len(got) != 1 {
			fi, _ := os.Stat(SnapshotPath(dir))
			t.Errorf("%s: wrote %d bytes and read back %d records", c.name, fi.Size(), len(got))
			continue
		}
		if !strings.Contains(got[0].Digest, "clipped") {
			t.Errorf("%s: not clipped, %d bytes came back", c.name, len(got[0].Digest))
		}
		// The kept head is still the text that was served, decodable as it was
		// written rather than ending in half a character.
		head := strings.TrimSuffix(got[0].Digest, got[0].Digest[strings.LastIndex(got[0].Digest, "\n… (clipped"):])
		if !utf8.ValidString(head) && utf8.ValidString(c.digest) {
			t.Errorf("%s: the kept head is not valid UTF-8 though the digest was", c.name)
		}
	}
}
