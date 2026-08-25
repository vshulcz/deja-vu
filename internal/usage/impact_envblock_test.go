package usage

import (
	"testing"
)

// The impact line reads "N served instead of M of raw transcripts". The
// environment block has no transcripts behind it — it is a summary of what this
// machine keeps hitting, recorded with no raw size — so its bytes would raise
// that numerator against an unchanged denominator and understate the saving.
// #1963 proposed counting them; these are the numbers that say otherwise.
func TestTheImpactRatioIgnoresTheEnvironmentBlock(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		RecordResultRaw(dir, KindRecall, 900, 2, false, 9000)
	}
	before := Impact(dir)

	for i := 0; i < 10; i++ {
		RecordResultRaw(dir, KindHook, 501, 0, true, 0)
	}
	after := Impact(dir)

	if after.ServedBytes != before.ServedBytes || after.RawBytes != before.RawBytes {
		t.Errorf("ten environment blocks moved the ratio: served %d→%d, raw %d→%d",
			before.ServedBytes, after.ServedBytes, before.RawBytes, after.RawBytes)
	}
	if after.Injections != 0 {
		t.Errorf("injections = %d; no session start here began with project memory", after.Injections)
	}
	// And the real work is still counted, so this is not a test that passes on
	// an empty report.
	if after.ServedBytes != 2700 || after.RawBytes != 27000 {
		t.Errorf("the recalls stopped counting: served=%d raw=%d", after.ServedBytes, after.RawBytes)
	}
}
