package index

import (
	"os"
	"path/filepath"
	"testing"
)

// Both command tables are mined from every record the index holds, so rebuilding
// them on an append that cannot have changed them is a scan of records.bin for
// nothing — 2.9s of an 8.6s pass on a 191 MB store. An append of speech leaves
// them alone; an append that carries a command rebuilds them (#3500).
func TestAnAppendRebuildsTheCommandTablesOnlyWhenItCarriesWork(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	// Two sessions running the same command, because the table keeps what
	// recurs — one sighting writes no table to compare against.
	write(t, filepath.Join(claude, "p", "first.jsonl"),
		claudeLine("s-first", "2026-01-02T03:04:05Z", "the exporter retries without a pause")+
			claudeBashLine("s-first", "2026-01-02T03:05:00Z", "go test ./internal/exporter/"))
	write(t, filepath.Join(claude, "p", "second.jsonl"),
		claudeLine("s-second", "2026-01-02T04:04:05Z", "the exporter again")+
			claudeBashLine("s-second", "2026-01-02T04:05:00Z", "go test ./internal/exporter/"))
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	stamp := func() int64 {
		fi, err := os.Stat(filepath.Join(dir, commandsFile))
		if err != nil {
			t.Fatalf("the command table was never written: %v", err)
		}
		return fi.ModTime().UnixNano()
	}
	was := stamp()

	write(t, filepath.Join(claude, "p", "speech.jsonl"),
		claudeLine("s-speech", "2026-01-02T06:00:00Z", "the billing webhook retries twice"))
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if now := stamp(); now != was {
		t.Error("an append of speech rebuilt the command table, which it cannot change")
	}

	write(t, filepath.Join(claude, "p", "work.jsonl"),
		claudeLine("s-work", "2026-01-02T07:00:00Z", "run the suite")+
			claudeBashLine("s-work", "2026-01-02T07:01:00Z", "go test ./internal/exporter/"))
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if now := stamp(); now == was {
		t.Error("an append carrying a command left the command table as it was")
	}
}

// claudeBashLine is one assistant turn running a command, the shape the command
// table is mined from.
func claudeBashLine(sid, ts, cmd string) string {
	return `{"type":"assistant","sessionId":"` + sid + `","cwd":"/w","timestamp":"` + ts +
		`","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"` + cmd + `"}}]}}` + "\n"
}
