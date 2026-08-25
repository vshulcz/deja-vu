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

// The déjà-vu hook recorded the receiver and the session-start hook did not,
// though it holds the id and uses it two lines away — so the same field meant
// two different things depending on which path wrote it (#1949).
func TestASessionStartDigestRecordsWhoItWentTo(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)

	RecordDigestPolicyInto(dir, KindHook, "a digest of earlier work", "agent-session-1", 2, 4000, "local-only")

	got := Snapshots(dir, 0)
	if len(got) != 1 {
		t.Fatalf("wrote one snapshot, read %d", len(got))
	}
	if got[0].Into != "agent-session-1" {
		t.Errorf("the snapshot does not say where it went: %#v", got[0])
	}
	// And it still records the counting event, as the policy form always has.
	if tot := Totals(dir); tot.Injections != 1 || tot.Bytes == 0 {
		t.Errorf("the counting half was lost: %#v", tot)
	}
}

// The old form still exists and still writes no destination, so a caller that
// genuinely does not know one is not made to invent it. A harness that sends no
// session_id reaches the same place through the new form: the field is omitted,
// not filled with a placeholder.
func TestADigestWithNoKnownDestinationSaysNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)

	RecordDigestPolicy(dir, KindHook, "a digest of earlier work", 2, 4000, "local-only")
	RecordDigestPolicyInto(dir, KindHook, "another digest", "", 2, 4000, "local-only")

	got := Snapshots(dir, 0)
	if len(got) != 2 {
		t.Fatalf("wrote two snapshots, read %d", len(got))
	}
	for _, s := range got {
		if s.Into != "" {
			t.Errorf("a caller with no destination recorded one anyway: %q", s.Into)
		}
	}
}
