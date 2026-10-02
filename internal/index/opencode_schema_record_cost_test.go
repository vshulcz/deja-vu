package index

import (
	"path/filepath"
	"testing"
)

// fromDatabase runs for every record the index holds on each pass that
// rewrites it. Telling an OpenCode-schema session from the harness's own files
// by asking the registry what its project directory was cost ~68 µs a record,
// against ~20 ns for any other harness: seconds per pass on a large opencode
// history (#4396).
func TestOpencodeSchemaRecordsAreJudgedWithoutTheRegistry(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	passStoreHarness.Range(func(k, _ any) bool { passStoreHarness.Delete(k); return true })
	for _, h := range []string{"opencode", "kilocode", "zcode"} {
		r := Record{Key: h + ":ses_1", SourcePath: filepath.Join(tmp, "coding", "proj")}
		if !fromDatabase(r) {
			t.Fatalf("%s: a session naming its project directory is not the store's", h)
		}
		// A registry walk is ~860 allocations; the path checks are a handful,
		// more on Windows, where filepath does more work. The bound tells the
		// two apart, not one platform's count from another's.
		if allocs := testing.AllocsPerRun(200, func() { fromDatabase(r) }); allocs > 100 {
			t.Errorf("%s: fromDatabase allocates %.0f times a record; it walks the registry", h, allocs)
		}
	}
}

// The harness's own files are not the database's: a Kilo extension task and a
// ZCode transcript are read per file and dropped when their file changes.
func TestOpencodeSchemaOwnFilesAreNotTheStore(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	for h, p := range map[string]string{
		"kilocode": filepath.Join(tmp, "tasks", "t1", "api_conversation_history.json"),
		"zcode":    filepath.Join(tmp, ".zcode", "sessions", "z1.jsonl"),
	} {
		if inOpencodeSchemaDB(h, p) {
			t.Errorf("%s: its own transcript counted as the database's", h)
		}
	}
}
