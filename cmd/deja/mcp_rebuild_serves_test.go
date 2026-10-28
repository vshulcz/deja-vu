package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// rebuildRunning puts a version rebuild in flight over dir the way the
// detached warmup leaves it: a published status and a sentinel. The sentinel
// is stamped past warmupRetryAfter, the age a long rebuild reaches, where its
// dedup no longer holds.
func rebuildRunning(t *testing.T, dir string) {
	t.Helper()
	now := time.Now()
	st := `{"phase":"reading transcripts","done":3,"total":40,"updated":` + strconv.FormatInt(now.UnixNano(), 10) + `}`
	if err := os.WriteFile(warmupStatusPath(dir), []byte(st), 0o600); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-warmupRetryAfter - time.Minute).UnixNano()
	if err := os.WriteFile(filepath.Join(dir, "warmup.sentinel"), []byte(strconv.FormatInt(old, 10)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func countSpawns(t *testing.T) *int {
	t.Helper()
	// hermeticEnv sets a guard sentinel so no test spawns; these count instead.
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	saved := spawnWarmup
	n := 0
	spawnWarmup = func(_, _ string) error { n++; return nil }
	t.Cleanup(func() { spawnWarmup = saved })
	return &n
}

// While the rebuild a version bump starts is running, recall answered "deja is
// indexing… ask again then" over a snapshot it can read. In real transcripts 72
// of 82 no-answer recalls landed there. It answers from the old index, and asks
// for no second build while the first one runs.
func TestRecallAnswersDuringAVersionRebuild(t *testing.T) {
	dir := staleStore(t)
	spawns := countSpawns(t)
	if err := index.SetManifestVersionForTest(dir, index.CurrentVersionForTest()-1); err != nil {
		t.Fatal(err)
	}
	rebuildRunning(t, dir)
	if got := buildingNowForAgent(dir); got != "" {
		t.Errorf("a version rebuild over a readable index sends the agent away: %q", got)
	}
	got, err := callMCPTool(dir, "recall", json.RawMessage(`{"query":"quokkabloom retry budget"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "quokkabloom") || strings.Contains(got, "ask again") {
		t.Errorf("recall did not answer from the previous index during a rebuild:\n%s", got)
	}
	if *spawns != 0 {
		t.Errorf("recall during a running rebuild spawned %d more builds", *spawns)
	}
	// A miss says where the answer came from, not that nothing can be recalled.
	miss := emptyRecallAnswer(dir, "zzyzxvorpal")
	if strings.Contains(miss, "Nothing can be recalled") || !strings.Contains(miss, "previous index") {
		t.Errorf("a miss during a version rebuild: %q", miss)
	}
}

// Text written below the redaction floor is not quoted while it is re-read, so
// that store still declines during the rebuild.
func TestRecallDeclinesBelowTheRedactionFloorDuringARebuild(t *testing.T) {
	dir := staleStore(t)
	countSpawns(t)
	if err := index.SetManifestVersionForTest(dir, 1); err != nil {
		t.Fatal(err)
	}
	rebuildRunning(t, dir)
	if index.AnswersWhileRebuilding(dir) {
		t.Fatal("premise: a store below the floor answers while rebuilding")
	}
	if got := buildingNowForAgent(dir); !strings.Contains(got, "ask again") {
		t.Errorf("a store below the redaction floor was served during a rebuild: %q", got)
	}
	if miss := emptyRecallAnswer(dir, "zzyzxvorpal"); !strings.Contains(miss, "Nothing can be recalled") {
		t.Errorf("a miss below the floor: %q", miss)
	}
}

// blame reads without waiting (#1784), so it answers from the previous index
// too, and its stale read asks for no second build. remember waits on a
// blocking Ensure and still declines.
func TestBlameAndRememberDuringAVersionRebuild(t *testing.T) {
	dir := staleStore(t)
	spawns := countSpawns(t)
	if err := index.SetManifestVersionForTest(dir, index.CurrentVersionForTest()-1); err != nil {
		t.Fatal(err)
	}
	rebuildRunning(t, dir)
	got, err := callMCPTool(dir, "blame", json.RawMessage(`{"path":"/w/s/exporter.go"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "ask again") {
		t.Errorf("blame declined over a readable previous index:\n%s", got)
	}
	got, err = callMCPTool(dir, "remember", json.RawMessage(`{"text":"the quokkabloom exporter runs nightly"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "ask again") {
		t.Errorf("remember stopped declining during a rebuild:\n%s", got)
	}
	if *spawns != 0 {
		t.Errorf("blame or remember during a running rebuild spawned %d more builds", *spawns)
	}
}
