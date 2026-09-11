package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A record's role sits outside its compressed body, so a scan for one kind of
// record reads a prefix and skips the rest instead of inflating all of them:
// `deja how` asks for every command in the store and was decompressing all
// 280,000 records to read 20,000, which was 53% of its time. What it hands back
// has to be exactly what filtering after a full decode gives.
func TestARoleScanReturnsWhatAFullScanWouldFilter(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "role-index")
	now := time.Now().UTC().Truncate(time.Second)
	var ss []model.Session
	for i := range 12 {
		ss = append(ss, model.Session{
			Harness: "claude", ID: fmt.Sprintf("s%02d", i), Project: "app", Updated: now,
			Messages: []model.Message{
				{Role: "user", Text: fmt.Sprintf("session %d asks about the retry budget", i), Time: now},
				{Role: "command", Text: fmt.Sprintf("go test ./internal/pool -run TestDrain%d", i), Time: now.Add(time.Second)},
				{Role: "tool-output", Text: strings.Repeat("output line that compresses well\n", 8), Time: now.Add(2 * time.Second)},
				{Role: "edit", Text: fmt.Sprintf("internal/pool/pool%d.go\n-\tone\n+\ttwo", i), Time: now.Add(3 * time.Second)},
				{Role: "assistant", Text: "capped the retry budget at three", Time: now.Add(4 * time.Second)},
			},
		})
	}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	tables := tablesFromManifest(m)
	recs := filepath.Join(dir, "records.bin")

	for _, role := range []string{"command", "edit", "user", "assistant", "tool-output"} {
		var want, got []Record
		if err := eachRecord(recs, tables, func(r Record) {
			if r.Role == role {
				want = append(want, r)
			}
		}); err != nil {
			t.Fatal(err)
		}
		if err := eachRecordInRoles(recs, tables, map[string]bool{role: true}, func(r Record) {
			got = append(got, r)
		}); err != nil {
			t.Fatal(err)
		}
		if len(want) == 0 {
			t.Fatalf("no %s records were written, so the comparison proves nothing", role)
		}
		if len(got) != len(want) {
			t.Fatalf("%s: the role scan returned %d records, the full scan %d", role, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s record %d differs:\n scan: %+v\n full: %+v", role, i, got[i], want[i])
			}
		}
	}

	// Two roles at once keep the order they were written in, which is what a
	// caller pairing a command with its output depends on.
	var pairs []string
	if err := eachRecordInRoles(recs, tables, map[string]bool{"command": true, "tool-output": true}, func(r Record) {
		pairs = append(pairs, r.Role)
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i] != "command" || pairs[i+1] != "tool-output" {
			t.Fatalf("the two roles came back out of order: %v", pairs[:min(8, len(pairs))])
		}
	}
}
