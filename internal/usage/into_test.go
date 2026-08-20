package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The injection log said what was injected and never to whom, so the only way
// to tell whether a recall was used was the sentence the block asks the agent
// to say. Measured on a real store, that sentence follows 22 of 1218
// injections — a measure of reporting, not of use. With the receiving session
// recorded, a reader can pair an injection with what the agent did next.
func TestSnapshotRecordsWhoReceivedIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	RecordDigestInto(dir, KindDejaVu, "a block worth reading", "ses_1234", 2, 900,
		[]string{"pgbouncer"}, "claude:abc")

	b, err := os.ReadFile(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(b))
	if line == "" {
		t.Fatal("nothing was written to the injection log")
	}
	var got Snapshot
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatal(err)
	}
	if got.Into != "ses_1234" {
		t.Errorf("into = %q, want the agent session that received the block", got.Into)
	}
	if got.Kind != KindDejaVu || got.Sessions != 2 {
		t.Errorf("the rest of the record changed: %+v", got)
	}
}

// A caller that does not know the session still writes a usable record, and the
// field is omitted rather than written empty — older readers see what they saw.
func TestSnapshotWithoutAReceiverOmitsTheField(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	RecordDigestTerms(dir, KindDejaVu, "a block", 1, 100, nil)

	b, err := os.ReadFile(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "\"into\"") {
		t.Errorf("empty receiver was written out: %s", strings.TrimSpace(string(b)))
	}
}
