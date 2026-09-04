package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/search"
)

// The chain from #2962: a container syncs into a server, and a laptop then
// pulls the server. The server exported nothing at all — `--full` included —
// so the middle of a chain was a dead end, and migrating off that machine lost
// everything it had gathered. Passing a peer's work on is still not the
// default (#2450); this is the opt-in path.
func TestASyncedMachineCanBePulledFrom(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))

	base := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	fromContainer := filepath.Join(tmp, "from-container")
	writeSyncBatch(t, fromContainer, []SyncRecord{
		{Harness: "claude", SessionID: "container-sess", Project: "app", Role: "user",
			Text: "relayneedle why does the loader stall", Time: base, Origin: "container-1"},
		{Harness: "claude", SessionID: "container-sess", Project: "app", Role: "assistant",
			Text: "relayneedle the cache was cold; warming it settled that", Time: base.Add(time.Minute), Origin: "container-1"},
	})

	server := filepath.Join(tmp, "server.db")
	if n, err := Import(server, fromContainer); err != nil || n != 2 {
		t.Fatalf("server import n=%d err=%v", n, err)
	}

	// The default still holds: a peer's work is not this machine's to pass on.
	if n, err := ExportFull(server, filepath.Join(tmp, "default-out")); err != nil || n != 0 {
		t.Fatalf("a plain export sent %d records of another machine's work (err=%v)", n, err)
	}

	// What the laptop asks for, once someone says these are all their own
	// machines.
	toLaptop := filepath.Join(tmp, "to-laptop")
	n, err := ExportRelay(server, toLaptop, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("the server exported %d records, want the 2 it holds", n)
	}

	// Under the container's name, not the server's, and under the project the
	// work actually happened in.
	var relayed []SyncRecord
	batches, _ := filepath.Glob(filepath.Join(toLaptop, "*.jsonl"))
	for _, p := range batches {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var sr SyncRecord
			if err := json.Unmarshal([]byte(line), &sr); err != nil {
				t.Fatalf("batch line does not decode: %v", err)
			}
			relayed = append(relayed, sr)
		}
	}
	if len(relayed) != 2 {
		t.Fatalf("batch holds %d records, want 2", len(relayed))
	}
	for _, sr := range relayed {
		if string(sr.Origin) != "container-1" {
			t.Errorf("relayed record signed %q, want the machine the work happened on", sr.Origin)
		}
		if sr.Project != "app" {
			t.Errorf("project = %q, want the original name", sr.Project)
		}
		if sr.SessionID != "container-sess" {
			t.Errorf("session = %q, want the id it had on the container", sr.SessionID)
		}
	}

	// And the laptop can read it.
	laptop := filepath.Join(tmp, "laptop.db")
	if n, err := Import(laptop, toLaptop); err != nil || n != 2 {
		t.Fatalf("laptop import n=%d err=%v", n, err)
	}
	ss, err := Search(laptop, search.Options{Query: "relayneedle", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) == 0 {
		t.Fatal("the work did not survive the second hop")
	}

	// A laptop that pulls the same work straight from the container gets one
	// copy, not two: the relay kept the identity the dedupe is keyed on.
	if n, err := Import(laptop, fromContainer); err != nil || n != 0 {
		t.Fatalf("re-import from the origin added %d records, want none", n)
	}

	// The server does not send the container its own work back.
	toContainer := filepath.Join(tmp, "to-container")
	if n, err := ExportRelay(server, toContainer, "container-1"); err != nil || n != 0 {
		t.Fatalf("the server echoed %d records to the machine they came from (err=%v)", n, err)
	}
}
