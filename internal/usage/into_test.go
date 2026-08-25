package usage

import (
	"testing"
)

// `into` exists so the log says to whom a digest went, not only what went — the
// comment on the field measures why: the agent's own "deja-vu recalled" line
// appeared after 22 of 1218 injections, which counts reporting rather than use.
// The déjà-vu hook recorded it and the session-start hook did not, so the field
// meant two different things depending on which path wrote it (#1949).
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
// genuinely does not know one is not made to invent it.
func TestADigestWithNoKnownDestinationSaysNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)

	RecordDigestPolicy(dir, KindHook, "a digest of earlier work", 2, 4000, "local-only")

	got := Snapshots(dir, 0)
	if len(got) != 1 {
		t.Fatalf("wrote one snapshot, read %d", len(got))
	}
	if got[0].Into != "" {
		t.Errorf("a caller with no destination recorded one anyway: %q", got[0].Into)
	}
}
