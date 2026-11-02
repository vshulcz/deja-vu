package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/evidence"
)

// TestDDDStageCSyncImportRefreshesReceiptsAndCommandState pins Bugs #40, #41, and #43:
//  1. Sync import (`refreshImportSidecars` -> `refreshAppendedSidecars`) updates
//     `receipts.bin` for imported sessions so `evidence.Open(dir).Lookup` finds
//     backing command/output receipts for peer turns recorded before the local
//     index build (Bug #40).
//  2. Sync import updates `commands.gob`, `sessionfacts.gob`, and `commands-state.bin`
//     in lockstep via `buildCommandTables` instead of leaving `commands-state.bin`
//     stale behind `sessionfacts.gob` (Bug #41).
//  3. `writeCommandFails` removes `commandfails.gob` when the table becomes empty
//     during an in-place update (Bug #43).
func TestDDDStageCSyncImportRefreshesReceiptsAndCommandState(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	write(t, filepath.Join(claude, "exporter", "local.jsonl"),
		claudeLine("s-local", "2026-01-02T03:04:05Z", "check the exporter suite")+
			claudeBashLine("s-local", "2026-01-02T03:05:00Z", "go test ./internal/exporter/"))

	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	inDir := filepath.Join(tmp, "sync-in")
	if err := os.MkdirAll(inDir, 0o700); err != nil {
		t.Fatal(err)
	}
	answer := "Fixed: retry_limit in payments_client was 0, set it to 5, and go test ./internal/exporter/ passed"
	batch := strings.Join([]string{
		`{"harness":"claude","session_id":"peer-1","project":"exporter","role":"user","text":"payments_client gives up on the first timeout, fix it","time":"2026-01-01T10:00:00Z"}`,
		`{"harness":"claude","session_id":"peer-1","project":"exporter","role":"command","text":"$ go test ./internal/exporter/\n[exit 0]","time":"2026-01-01T10:00:05Z"}`,
		`{"harness":"claude","session_id":"peer-1","project":"exporter","role":"tool-output","text":"12: retry_limit = 0 // payments_client\nok  \tgithub.com/example/exporter\t0.142s","time":"2026-01-01T10:00:06Z"}`,
		`{"harness":"claude","session_id":"peer-1","project":"exporter","role":"assistant","text":"` + answer + `","time":"2026-01-01T10:00:10Z"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(inDir, "batch.jsonl"), []byte(batch), 0o600); err != nil {
		t.Fatal(err)
	}

	added, err := Import(dir, inDir)
	if err != nil || added != 4 {
		t.Fatalf("Import(dir, inDir) = (%d, %v); want (4, nil)", added, err)
	}

	// Bug #40: receipts.bin must cover and hold the receipt for the imported peer session.
	peerID := ImportedSessionID("claude", "peer-1")
	peerKey := "claude:" + peerID
	rc := evidence.Open(dir)
	if rc == nil {
		t.Fatal("evidence.Open(dir) = nil after Import")
	}
	lines := rc.Lookup("claude", peerID, answer)
	if len(lines) == 0 {
		t.Fatalf("receipts.bin has no receipt for imported peer session %s (Bug #40)", peerKey)
	}

	// Bug #41: commands.gob, sessionfacts.gob, and commands-state.bin must all be updated and aligned.
	if !commandsHold(dir, "go test ./internal/exporter/") {
		t.Fatalf("commands.gob does not hold recurring command after peer import")
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "records.bin"))
	if err != nil {
		t.Fatal(err)
	}
	st, facts := carriedCommandsState(dir, m, len(m.DeadRecords), fi.Size())
	if st == nil || len(facts) == 0 {
		t.Fatalf("carriedCommandsState returned nil after Import (commands-state.bin out of sync with sessionfacts.gob, Bug #41)")
	}
	if fact, ok := facts[peerKey]; !ok || len(fact.Commands) == 0 {
		t.Fatalf("facts[%s] = (%+v, %v); want imported command fact", peerKey, fact, ok)
	}
	if st.Size != fi.Size() {
		t.Fatalf("commands-state.bin Size = %d; want records.bin size %d", st.Size, fi.Size())
	}

	// Bug #43: writeCommandFails removes commandfails.gob when the table becomes empty.
	writeCommandFails(dir, []CommandFailure{{Head: "make broken", Line: "FAIL", Sessions: 2, Project: "exporter"}})
	if _, err := os.Stat(commandFailsPath(dir)); err != nil {
		t.Fatalf("commandfails.gob not written: %v", err)
	}
	writeCommandFails(dir, nil)
	if _, err := os.Stat(commandFailsPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("writeCommandFails(dir, nil) left stale commandfails.gob on disk: err=%v (Bug #43)", err)
	}
}
