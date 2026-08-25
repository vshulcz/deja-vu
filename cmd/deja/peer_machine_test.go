package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/peers"
)

// seedPeerBatch writes a batch stamped with the given origin and returns the
// directory holding it.
func seedPeerBatch(t *testing.T, tmp, origin string, n int) string {
	t.Helper()
	batch := filepath.Join(tmp, "batch-"+origin)
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for i := 0; i < n; i++ {
		rec := map[string]any{
			"harness": "claude", "session_id": origin + string(rune('a'+i)), "project": "proj",
			"role": "user", "text": "retry loop", "time": "2026-08-20T10:00:0" + string(rune('0'+i)) + "Z",
			"origin": origin,
		}
		b, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	if err := os.WriteFile(filepath.Join(batch, "deja-sync-1.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return batch
}

// The peers row holds the ssh alias someone typed; an imported session is
// stamped with what the sending machine calls itself. #1876 made the comparison
// case-insensitive, which does nothing when the two names differ outright —
// the normal case for anyone with an ssh config. The row then said a machine
// had sent nothing while its sessions were in the index (#1887).
func TestAPullLearnsWhatTheMachineCallsItself(t *testing.T) {
	tmp := hermeticEnv(t)
	peersFile := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", peersFile)
	if err := os.WriteFile(peersFile, []byte(`{"peers":[{"host":"work","last_pull":"2026-08-20T10:00:00Z"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()

	before := importsByPeerName(dir)
	if _, err := index.Import(dir, seedPeerBatch(t, tmp, "build-box", 3)); err != nil {
		t.Fatal(err)
	}
	learnPeerMachine(dir, "work", before)

	list := peers.Load()
	if len(list) != 1 || list[0].Machine != "build-box" {
		t.Fatalf("the pairing was not learned: %#v", list)
	}

	var text strings.Builder
	doctorPeers(&text, dir, time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC))
	if !strings.Contains(text.String(), "3 sessions from there") {
		t.Errorf("the row still counts none of what arrived:\n%s", text.String())
	}

	var out strings.Builder
	if err := runDoctor(&out, []string{"--json", "--offline"}, stubLookup("1.0.0", false), dir); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Sync struct {
			Peers []struct {
				Sessions int `json:"sessions_from_there"`
			} `json:"peers"`
		} `json:"sync"`
	}
	if err := json.Unmarshal([]byte(out.String()), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Sync.Peers) != 1 || report.Sync.Peers[0].Sessions != 3 {
		t.Errorf("--json counts %#v, want three", report.Sync.Peers)
	}
}

// Only what actually arrived in this exchange is attributed to the host: a
// batch from a third machine sitting in the index must not rename the peer.
func TestLearningIgnoresWhatWasAlreadyThere(t *testing.T) {
	tmp := hermeticEnv(t)
	peersFile := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", peersFile)
	if err := os.WriteFile(peersFile, []byte(`{"peers":[{"host":"work"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if _, err := index.Import(dir, seedPeerBatch(t, tmp, "old-desktop", 2)); err != nil {
		t.Fatal(err)
	}

	before := importsByPeerName(dir)
	if _, err := index.Import(dir, seedPeerBatch(t, tmp, "build-box", 3)); err != nil {
		t.Fatal(err)
	}
	learnPeerMachine(dir, "work", before)

	list := peers.Load()
	if len(list) != 1 || list[0].Machine != "build-box" {
		t.Fatalf("the wrong machine was learned: %#v", list)
	}
}

// An exchange that brought nothing new leaves the row alone.
func TestAnEmptyPullLearnsNothing(t *testing.T) {
	tmp := hermeticEnv(t)
	peersFile := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", peersFile)
	if err := os.WriteFile(peersFile, []byte(`{"peers":[{"host":"work"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	before := importsByPeerName(dir)
	learnPeerMachine(dir, "work", before)
	if list := peers.Load(); len(list) != 1 || list[0].Machine != "" {
		t.Fatalf("a pull that brought nothing wrote a name: %#v", list)
	}
}

// A pull that brings records from two origins teaches nothing: a batch can
// carry a third machine's work, and another process can import while this
// runs, so the safe answer is the count that was there before.
func TestAnAmbiguousPullLearnsNothing(t *testing.T) {
	tmp := hermeticEnv(t)
	peersFile := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", peersFile)
	if err := os.WriteFile(peersFile, []byte(`{"peers":[{"host":"work"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()

	before := importsByPeerName(dir)
	if _, err := index.Import(dir, seedPeerBatch(t, tmp, "build-box", 3)); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Import(dir, seedPeerBatch(t, tmp, "someone-else", 2)); err != nil {
		t.Fatal(err)
	}
	learnPeerMachine(dir, "work", before)

	if list := peers.Load(); len(list) != 1 || list[0].Machine != "" {
		t.Fatalf("a pull carrying two origins wrote a name: %#v", list)
	}
}

// A machine renamed, or an alias repointed at another host, is corrected by
// the next pull rather than kept forever.
func TestARepointedAliasIsRelearned(t *testing.T) {
	tmp := hermeticEnv(t)
	peersFile := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", peersFile)
	if err := os.WriteFile(peersFile, []byte(`{"peers":[{"host":"work","machine":"build-box"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()

	before := importsByPeerName(dir)
	if _, err := index.Import(dir, seedPeerBatch(t, tmp, "test-machine", 2)); err != nil {
		t.Fatal(err)
	}
	learnPeerMachine(dir, "work", before)

	if list := peers.Load(); len(list) != 1 || list[0].Machine != "test-machine" {
		t.Fatalf("the stale pairing survived: %#v", list)
	}
}
