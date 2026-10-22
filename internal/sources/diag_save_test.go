package sources

import "testing"

// What a pass reported survives a manifest write in the middle of it (#5110).
func TestDiagSaveRestoreSurvivesSnapshot(t *testing.T) {
	DiagSnapshot()
	t.Cleanup(func() { DiagSnapshot() })
	diagMalformedLine("bad.jsonl")
	diagUnusableRecord("store.db", "r1", "unknown data_type brotli")
	diagNoZstd("cold.jsonl.zst")
	saved := DiagSave()
	DiagSnapshot()
	if len(DiagMalformedCounts()) != 0 || len(DiagNoZstdPaths()) != 0 {
		t.Fatal("snapshot kept the counters")
	}
	DiagRestore(saved)
	if got := DiagMalformedCounts(); got["bad.jsonl"] != 1 || got["store.db"] != 1 {
		t.Fatalf("malformed after restore = %v", got)
	}
	if DiagUnusableRecords()["store.db"]["r1"] == "" || DiagReasons()["store.db"] == "" {
		t.Fatal("unusable record not restored")
	}
	if !DiagNoZstdPaths()["cold.jsonl.zst"] {
		t.Fatal("zstd path not restored")
	}
}
