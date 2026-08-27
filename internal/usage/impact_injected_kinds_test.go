package usage

import "testing"

// The impact report is a ratio: what deja served against the transcripts behind
// it. The per-prompt recall and the tool-time line are digests distilled from
// real transcripts, and both were dropped whole — bytes and raw — so the ratio
// was computed from a subset of what deja handed over. The documented exclusion
// is narrower: the environment block, which is an empty hook event carrying a
// summary of the machine rather than a digest (#2204).
func TestImpactCountsEveryInjectionThatCarriedADigest(t *testing.T) {
	dir := t.TempDir()
	RecordResultRaw(dir, KindHook, 600, 1, false, 6000)
	RecordResultRaw(dir, KindDejaVu, 500, 1, false, 5000)
	RecordResultRaw(dir, KindTool, 400, 1, false, 4000)
	RecordResultRaw(dir, KindRecall, 300, 1, false, 3000)

	tot := Totals(dir)
	imp := Impact(dir)
	// The premise: `deja stats` counts all four, so the two reports are about
	// the same events and any gap below is this report's own.
	if tot.Bytes != 1800 || tot.RawBytes != 18000 {
		t.Fatalf("stats counts %d B over %d raw, so this measures nothing", tot.Bytes, tot.RawBytes)
	}
	if imp.ServedBytes != 1800 || imp.RawBytes != 18000 {
		t.Errorf("impact counts %d B over %d raw; the two reports disagree about what deja served",
			imp.ServedBytes, imp.RawBytes)
	}

	// The environment block stays out of both halves, which is what the report
	// documents: it is a summary of the machine, not a digest of transcripts.
	quiet := t.TempDir()
	RecordResultRaw(quiet, KindHook, 700, 0, true, 0)
	RecordResultRaw(quiet, KindDejaVu, 800, 0, true, 0)
	if r := Impact(quiet); r.ServedBytes != 0 || r.Injections != 0 {
		t.Errorf("an injection that carried no session was counted: %d B, %d injections", r.ServedBytes, r.Injections)
	}

	// And `injections` keeps the meaning the docs give it — session starts that
	// began with project memory — rather than growing to mean every door.
	if imp.Injections != 1 {
		t.Errorf("injections is %d; the documented count is session starts, of which there was one", imp.Injections)
	}
}
