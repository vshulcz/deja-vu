package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// An imported record keeps the sender's offset. The lines that prove the
// memory arrived formatted it as it came, so a batch from +14 was dated a day
// ahead of what `last` said on the same machine seconds later (#1050).
func TestSyncImportProofDatesInTheReadersZone(t *testing.T) {
	stamp := time.Date(2026, 8, 6, 14, 45, 0, 0, time.UTC)
	local := stamp.Local().Format("Jan 2")
	var sender *time.Location
	for _, offset := range []int{14 * 3600, -11 * 3600} {
		if zone := time.FixedZone("sender", offset); stamp.In(zone).Format("Jan 2") != local {
			sender = zone
			break
		}
	}
	if sender == nil {
		t.Skip("no sender zone here dates that instant differently from the reader")
	}
	sent := stamp.In(sender).Format("Jan 2")

	tmp := hermeticEnv(t)
	batch := filepath.Join(tmp, "batch")
	if err := os.MkdirAll(batch, 0o700); err != nil {
		t.Fatal(err)
	}
	rec := `{"harness":"deja","session_id":"deja-2026-08-06-boat","project":"boat","role":"user",` +
		`"text":"the forward hatch gasket has gone hard","time":"` + stamp.In(sender).Format(time.RFC3339Nano) + `"}`
	if err := os.WriteFile(filepath.Join(batch, "deja-sync-aaaa-1.jsonl"), []byte(rec+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := captureRunStderr(t, "sync", "import", batch)
	if err != nil {
		t.Fatal(err)
	}
	var proof string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "imported:boat") {
			proof = line
		}
	}
	if proof == "" {
		t.Fatalf("no line proving the import arrived:\n%s", out)
	}
	if strings.Contains(proof, sent) {
		t.Errorf("dated in the sender's zone (%s), not the reader's (%s): %q", sent, local, proof)
	}
	if !strings.Contains(proof, local) {
		t.Errorf("does not carry the reader's date %s: %q", local, proof)
	}
}
