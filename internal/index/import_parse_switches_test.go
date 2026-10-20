package index

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// A batch from a machine that indexed everything: a session with a prompt,
// tool output and a command, and a sub-agent run.
const switchedBatch = `{"harness":"claude","session_id":"p1","project":"alpha","role":"user","text":"why does the zonkobuffer overflow","time":"2026-07-01T10:00:00Z","origin":"mini"}
{"harness":"claude","session_id":"p1","project":"alpha","role":"tool-output","text":"zonkoerror: buffer overflow at 0x10","time":"2026-07-01T10:00:01Z","origin":"mini"}
{"harness":"claude","session_id":"p1","project":"alpha","role":"command","text":"go test ./zonko","time":"2026-07-01T10:00:02Z","origin":"mini"}
{"harness":"claude","session_id":"agent-p2","project":"alpha","role":"user","text":"look for the zonkobuffer cap","time":"2026-07-01T10:00:03Z","origin":"mini","kind":"sidechain"}
`

func writeSwitchedBatch(t *testing.T, home string) string {
	t.Helper()
	// Every switch starts on, whatever the shell running the tests set.
	for _, k := range []string{"DEJA_INDEX_TOOL_OUTPUT", "DEJA_INDEX_COMMANDS", "DEJA_INDEX_PATHS", "DEJA_INDEX_EDITS", "DEJA_INDEX_WRITES", "DEJA_INCLUDE_SUBAGENTS"} {
		t.Setenv(k, "")
	}
	batch := filepath.Join(home, "batch")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(batch, "deja-sync-test.jsonl"), []byte(switchedBatch), 0o600); err != nil {
		t.Fatal(err)
	}
	return batch
}

// importedRoles is every imported session's stored roles, keyed by its id.
func importedRoles(t *testing.T, dir string) map[string]string {
	t.Helper()
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string][]string{}
	if err := eachRecord(filepath.Join(dir, "records.bin"), tablesFromManifest(m), func(r Record) {
		if meta, ok := m.Sessions[r.Key]; ok && r.SourcePath == syncImportPath {
			by[meta.OrigID] = append(by[meta.OrigID], r.Role)
		}
	}); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for id, roles := range by {
		sort.Strings(roles)
		out[id] = strings.Join(roles, ",")
	}
	return out
}

// DEJA_INDEX_TOOL_OUTPUT=0, DEJA_INDEX_COMMANDS=0 and DEJA_INCLUDE_SUBAGENTS=0
// held for what this machine's readers parse, and a peer's batch walked the
// same tool output, commands and sub-agent runs in past them (#5425).
func TestImportHoldsAPeersRecordsToTheLocalSwitches(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	batch := writeSwitchedBatch(t, home)
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "0")
	t.Setenv("DEJA_INDEX_COMMANDS", "0")
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "0")
	claude := filepath.Join(home, "claude", "-tmp-local")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(home, "claude"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(home, "notes.jsonl"))
	local := filepath.Join(claude, "l.jsonl")
	line := func(text, at string) string {
		return `{"type":"user","message":{"role":"user","content":"` + text + `"},"timestamp":"` + at + `","sessionId":"l1","cwd":"/tmp/local"}` + "\n"
	}
	if err := os.WriteFile(local, []byte(line("a local question", "2026-07-02T10:00:00Z")), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	n, err := Import(dir, batch)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("only the prompt is on under these switches, %d records imported", n)
	}
	if got := ImportSkippedSwitched(); got != 3 {
		t.Errorf("three records were switched off here, the count says %d", got)
	}
	if got := importedRoles(t, dir); len(got) != 1 || got["p1"] != "user" {
		t.Errorf("stored %v, want only p1's prompt", got)
	}

	// An index pass after the import keeps the session as it arrived: the
	// append-only pass over a grown local transcript and a rebuild both carry
	// imported records.
	f, err := os.OpenFile(local, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line("a second local question", "2026-07-02T10:01:00Z")); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	before, _ := readManifest(dir)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if after, _ := readManifest(dir); !after.BuiltAt.After(before.BuiltAt) {
		t.Fatalf("the append pass did not run")
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if got := importedRoles(t, dir); len(got) != 1 || got["p1"] != "user" {
		t.Errorf("after index passes stored %v, want only p1's prompt", got)
	}

	// What a switch left out was not entered in the ledger, so the same batch
	// imported with the switches on brings it in.
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "")
	t.Setenv("DEJA_INDEX_COMMANDS", "")
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")
	if n, err := Import(dir, batch); err != nil || n != 3 {
		t.Errorf("re-import with the switches on: %d records, %v; want 3", n, err)
	}
	got := importedRoles(t, dir)
	if got["p1"] != "command,tool-output,user" || got["agent-p2"] != "user" {
		t.Errorf("after re-import stored %v", got)
	}
}

// Control: with every switch on the whole batch lands, the run keeps its kind
// and nothing is counted, so the test above is not passing on an import that
// drops these records regardless.
func TestImportTakesEveryRoleWithTheSwitchesOn(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "")
	t.Setenv("DEJA_INDEX_COMMANDS", "")
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")
	batch := writeSwitchedBatch(t, home)
	dir := filepath.Join(home, "index.db")

	n, err := Import(dir, batch)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Errorf("every record is on, %d imported", n)
	}
	if got := ImportSkippedSwitched(); got != 0 {
		t.Errorf("nothing is switched off, the count says %d", got)
	}
	got := importedRoles(t, dir)
	if got["p1"] != "command,tool-output,user" || got["agent-p2"] != "user" {
		t.Errorf("stored %v", got)
	}
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, m := range metas {
		kinds[m.OrigID] = m.Kind
	}
	if kinds["agent-p2"] != "sidechain" || kinds["p1"] != "" {
		t.Errorf("kinds after import: %v", kinds)
	}
}

// The sender names a sub-agent run's kind, so the receiver can apply its own
// DEJA_INCLUDE_SUBAGENTS to it.
func TestExportCarriesASubagentRunsKind(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	batch := writeSwitchedBatch(t, home)
	dir := filepath.Join(home, "index.db")
	if _, err := Import(dir, batch); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(home, "out")
	if _, err := ExportRelay(dir, out, ""); err != nil {
		t.Fatal(err)
	}
	paths, _ := filepath.Glob(filepath.Join(out, "*.jsonl"))
	kinds := map[string]string{}
	for _, p := range paths {
		if err := readSyncFile(p, func(sr SyncRecord) error {
			kinds[sr.SessionID] = string(sr.Kind)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if kinds["agent-p2"] != "sidechain" || kinds["p1"] != "" {
		t.Errorf("exported kinds: %v", kinds)
	}
}
