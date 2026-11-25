package hookengine

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

func TestCoordStoreRoundTripAndCorruptRecovery(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	cs := NewCoordStore(dir)

	tx, release := cs.Lock()
	st := tx.Load()
	if st.Version != CoordStateVersion || st.Sessions == nil || st.Stamps == nil {
		release()
		t.Fatalf("unexpected empty CoordState: %+v", st)
	}
	sess := st.EnsureSession("s1", 1700000000)
	sess.Harness = "claude"
	sess.Worktree = "/tmp/repo"
	sess.Edits = map[string]CoordFileTouch{"/tmp/repo/a.go": {AtUnix: 1700000000}}
	st.Stamps["streak:s1:proj"] = CoordStamp{AtUnix: 1700000000, Val: 2}
	tx.Save(st)
	release()

	raw, err := os.ReadFile(cs.Path())
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", cs.Path(), err)
	}
	var parsed CoordState
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("Unmarshal persisted .coord: %v", err)
	}
	if parsed.Version != CoordStateVersion || parsed.Sessions["s1"].Harness != "claude" || parsed.Stamps["streak:s1:proj"].Val != 2 {
		t.Fatalf("unexpected persisted payload: %+v", parsed)
	}

	if err := os.WriteFile(cs.Path(), []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	tx, release = cs.Lock()
	recovered := tx.Load()
	release()
	if recovered.Version != CoordStateVersion || len(recovered.Sessions) != 0 || len(recovered.Stamps) != 0 {
		t.Fatalf("corrupt .coord did not recover cleanly: %+v", recovered)
	}
}

func TestCoordStatePruneTTLAndCaps(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	nowUnix := now.Unix()
	staleUnix := now.Add(-CoordTTL - time.Second).Unix()
	freshUnix := now.Add(-5 * time.Minute).Unix()

	st := CoordState{
		Version:  CoordStateVersion,
		Sessions: map[string]*CoordSession{},
		Stamps:   map[string]CoordStamp{},
	}
	st.Sessions["expired"] = &CoordSession{ID: "expired", UpdatedAt: staleUnix}
	active := &CoordSession{
		ID:                "active",
		UpdatedAt:         nowUnix,
		Edits:             map[string]CoordFileTouch{"stale.go": {AtUnix: staleUnix}},
		Reads:             map[string]CoordFileTouch{},
		VerifyCmd:         "go test ./...",
		VerifyAt:          staleUnix,
		LastSpawnBriefKey: "old-key",
		LastSpawnBriefAt:  staleUnix,
		BriefedAt:         staleUnix,
	}
	for i := range CoordMaxFilesPerSession + 8 {
		active.Edits[fmt.Sprintf("f-%02d.go", i)] = CoordFileTouch{AtUnix: freshUnix + int64(i)}
		active.Reads[fmt.Sprintf("r-%02d.go", i)] = CoordFileTouch{AtUnix: freshUnix + int64(i)}
	}
	st.Sessions["active"] = active

	for i := range CoordMaxSessions + 6 {
		st.Sessions[fmt.Sprintf("s-%03d", i)] = &CoordSession{
			ID:        fmt.Sprintf("s-%03d", i),
			UpdatedAt: freshUnix + int64(i),
		}
	}
	st.Stamps["stale-stamp"] = CoordStamp{AtUnix: staleUnix, Val: 1}
	for i := range CoordMaxStamps + 20 {
		st.Stamps[fmt.Sprintf("st-%03d", i)] = CoordStamp{AtUnix: freshUnix + int64(i), Val: 1}
	}

	PruneCoordState(&st, now)

	if st.Sessions["expired"] != nil {
		t.Fatal("expired session survived prune")
	}
	if len(st.Sessions) != CoordMaxSessions {
		t.Fatalf("len(Sessions) = %d, want %d", len(st.Sessions), CoordMaxSessions)
	}
	keptActive := st.Sessions["active"]
	if keptActive == nil {
		t.Fatal("newest active session was pruned")
	}
	if _, hasStaleEdit := keptActive.Edits["stale.go"]; hasStaleEdit {
		t.Fatal("stale edit survived prune")
	}
	if len(keptActive.Edits) != CoordMaxFilesPerSession || len(keptActive.Reads) != CoordMaxFilesPerSession {
		t.Fatalf("edits=%d reads=%d, want %d each", len(keptActive.Edits), len(keptActive.Reads), CoordMaxFilesPerSession)
	}
	if keptActive.VerifyCmd != "" || keptActive.VerifyAt != 0 || keptActive.LastSpawnBriefKey != "" || keptActive.BriefedAt != 0 {
		t.Fatalf("stale session metadata survived prune: %+v", keptActive)
	}
	if _, hasStaleStamp := st.Stamps["stale-stamp"]; hasStaleStamp {
		t.Fatal("stale stamp survived prune")
	}
	if len(st.Stamps) != CoordMaxStamps {
		t.Fatalf("len(Stamps) = %d, want %d", len(st.Stamps), CoordMaxStamps)
	}
	// Oldest fresh stamps should be evicted first.
	if _, hasOldest := st.Stamps["st-000"]; hasOldest {
		t.Fatal("oldest stamp was not evicted past cap")
	}
}

func TestCoordStoreRecordOutcome(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	cs := NewCoordStore(dir)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

	// Passing non-verify command when .coord does not exist must not create any sidecar files.
	if got := cs.RecordOutcome(now, CoordOutcome{SessionID: "s1", StreakKey: "k1"}); got != 0 {
		t.Fatalf("initial success outcome = %d, want 0", got)
	}
	if cs.Exists() {
		t.Fatal("non-verify success created .coord file unexpectedly")
	}
	if _, err := os.Stat(cs.Path() + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("non-verify success created .coord.lock unexpectedly: err=%v", err)
	}

	// Consecutive failures increment streak.
	for want := 1; want <= 3; want++ {
		if got := cs.RecordOutcome(now, CoordOutcome{SessionID: "s1", StreakKey: "k1", Failed: true}); got != want {
			t.Fatalf("failure #%d = %d, want %d", want, got, want)
		}
	}

	// Passing verify command clears streak and records verification command.
	if got := cs.RecordOutcome(now.Add(time.Minute), CoordOutcome{
		SessionID: "s1",
		StreakKey: "k1",
		VerifyCmd: "go test ./internal/hookengine",
		Worktree:  "/tmp/wt",
	}); got != 0 {
		t.Fatalf("verify reset outcome = %d, want 0", got)
	}

	tx, release := cs.Lock()
	st := tx.LoadPruned(now.Add(2 * time.Minute))
	release()
	if _, exists := st.Stamps["k1"]; exists {
		t.Fatal("streak stamp survived success reset")
	}
	sess := st.Sessions["s1"]
	if sess == nil || sess.VerifyCmd != "go test ./internal/hookengine" || sess.Worktree != "/tmp/wt" {
		t.Fatalf("verify metadata missing: %+v", sess)
	}
}

func TestCoordStoreConcurrentUpdateRace(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	cs := NewCoordStore(dir)
	base := time.Now().UTC()

	var wg sync.WaitGroup
	for g := range 12 {
		wg.Go(func() {
			sid := fmt.Sprintf("sess-%d", g%3)
			skey := "streak:" + sid
			for i := range 20 {
				if i%4 == 3 {
					cs.RecordOutcome(base, CoordOutcome{
						SessionID: sid,
						StreakKey: skey,
						VerifyCmd: "go test ./...",
					})
				} else {
					cs.RecordOutcome(base, CoordOutcome{
						SessionID: sid,
						StreakKey: skey,
						Failed:    true,
					})
				}
			}
		})
	}
	wg.Wait()

	tx, release := cs.Lock()
	st := tx.LoadPruned(base)
	release()
	if len(st.Sessions) == 0 {
		t.Fatal("expected recorded verification sessions after concurrent updates")
	}
}

func TestCoordStoreEmptyAndDirectHelpers(t *testing.T) {
	var nilStore *CoordStore
	if nilStore.Path() != "" || nilStore.Exists() {
		t.Fatal("nil CoordStore reported path or existence")
	}
	empty := NewCoordStore("   ")
	if empty.Path() != "" || empty.Exists() {
		t.Fatal("blank CoordStore reported path or existence")
	}
	tx, release := empty.Lock()
	st := tx.Load()
	tx.Save(st)
	release()

	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	if got := empty.RecordOutcome(now, CoordOutcome{Failed: true}); got != 1 {
		t.Fatalf("empty failed outcome = %d, want 1", got)
	}
	if got := empty.RecordOutcome(now, CoordOutcome{Failed: false}); got != 0 {
		t.Fatalf("empty success outcome = %d, want 0", got)
	}
	PruneCoordState(nil, now)

	dir := t.TempDir() + "/index.db"
	cs := NewCoordStore(dir)
	loaded := cs.LoadState()
	loaded.EnsureSession("unlocked-s1", now.Unix())
	cs.SaveState(loaded)
	if !cs.Exists() {
		t.Fatal("SaveState did not persist .coord")
	}
	if got := cs.LoadState(); got.Sessions["unlocked-s1"] == nil {
		t.Fatal("LoadState did not reload SaveState session")
	}
}
