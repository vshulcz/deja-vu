package index

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

func compactionState(id, workspace string, at time.Time) CompactionState {
	return CompactionState{
		SessionID: id, Harness: "codex", Workspace: workspace, Project: "example",
		TranscriptPath: filepath.Join(workspace, "rollout.jsonl"), SourceDigest: "digest-" + id,
		SourceSize: 42, SourceMTime: at, CapturedAt: at,
		Data: model.CompactionContext{Conclusions: []model.ContextFact{{Text: "api_key=ABCDEFGHIJKLMNOPQRSTUV"}}},
	}
}

func TestCompactionPersistsRedactedStateWithoutAnIndex(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	workspace := filepath.Join(t.TempDir(), "workspace")
	at := time.Date(2026, 9, 10, 1, 2, 3, 0, time.UTC)
	saved, err := PutCompaction(dir, compactionState("native-1", workspace, at))
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 1 || strings.Contains(saved.Data.Conclusions[0].Text, "ABCDEFGHIJKLMNOPQRSTUV") {
		t.Fatalf("stored=%#v", saved)
	}
	if IsCurrentVersion(dir) {
		t.Fatal("compaction-only manifest must not claim a searchable current index")
	}
	got, found, err := Compaction(dir, "native-1", workspace)
	if err != nil || !found || got.Revision != 1 {
		t.Fatalf("got=%#v found=%t err=%v", got, found, err)
	}
	if _, found, err := Compaction(dir, "native-1", filepath.Join(workspace, "other")); err != nil || found {
		t.Fatalf("workspace isolation found=%t err=%v", found, err)
	}
}

func TestCompactionDeduplicatesAndRejectsOlderCapture(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	workspace := t.TempDir()
	newer := compactionState("native-1", workspace, time.Unix(20, 0))
	first, err := PutCompaction(dir, newer)
	if err != nil {
		t.Fatal(err)
	}
	duplicate := newer
	duplicate.CapturedAt = newer.CapturedAt.Add(time.Hour)
	if got, err := PutCompaction(dir, duplicate); err != nil || got.Revision != first.Revision || !got.CapturedAt.Equal(first.CapturedAt) {
		t.Fatalf("duplicate=%#v err=%v", got, err)
	}
	older := newer
	older.SourceDigest = "older"
	older.CapturedAt = newer.CapturedAt.Add(-time.Second)
	if _, err := PutCompaction(dir, older); !errors.Is(err, ErrCompactionStale) {
		t.Fatalf("older err=%v, want ErrCompactionStale", err)
	}
}

func TestCompactionBoundsRetentionAndHonorsPrivacy(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	workspace := t.TempDir()
	for i := 0; i < compactionStateCap+1; i++ {
		state := compactionState("native-"+string(rune('a'+i)), workspace, time.Unix(int64(i+1), 0))
		if _, err := PutCompaction(dir, state); err != nil {
			t.Fatal(err)
		}
	}
	core, err := readCompactionCore(dir)
	if err != nil || len(core.Compactions) != compactionStateCap {
		t.Fatalf("states=%d err=%v", len(core.Compactions), err)
	}
	if _, found, err := Compaction(dir, "native-a", workspace); err != nil || found {
		t.Fatalf("oldest found=%t err=%v", found, err)
	}

	home := t.TempDir()
	setHome(t, home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	if err := writeTombstones(map[string]bool{"codex:native-b": true}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := Compaction(dir, "native-b", workspace); err != nil || found {
		t.Fatalf("forgotten found=%t err=%v", found, err)
	}
	if _, err := PutCompaction(dir, compactionState("native-b", workspace, time.Now())); !errors.Is(err, ErrCompactionForgotten) {
		t.Fatalf("put forgotten err=%v", err)
	}

	t.Setenv("DEJA_EXCLUDE_PROJECTS", "private-*")
	excluded := compactionState("native-private", workspace, time.Now())
	excluded.Project = "private-app"
	if _, err := PutCompaction(dir, excluded); !errors.Is(err, ErrCompactionIgnored) {
		t.Fatalf("put excluded err=%v", err)
	}
}

func TestCompactionRemovalMatchesForgetSelectors(t *testing.T) {
	states := map[string]CompactionState{}
	workspace := t.TempDir()
	state := compactionState("native-1", workspace, time.Unix(20, 0))
	states[compactionKey(state.SessionID, workspace)] = state
	m := Manifest{Compactions: states}
	keys := removeMatchingCompactions(&m, ForgetOptions{Project: "exam"}, "")
	if len(keys) != 1 || keys[0] != "codex:native-1" || len(m.Compactions) != 0 {
		t.Fatalf("keys=%v states=%#v", keys, m.Compactions)
	}
}

func TestCompactionCoreSurvivesTheFirstIndexBuildBoundary(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	workspace := t.TempDir()
	state := compactionState("native-1", workspace, time.Unix(20, 0))
	if _, err := PutCompaction(dir, state); err != nil {
		t.Fatal(err)
	}
	carried := compactionsForRebuild(dir, nil)
	if len(carried) != 1 {
		t.Fatalf("carried=%#v", carried)
	}
	if err := writeManifest(dir, Manifest{Version: version, Files: map[string]FileState{}, Sessions: map[string]SessionMeta{}, Compactions: carried}); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil || len(m.Compactions) != 1 {
		t.Fatalf("manifest=%#v err=%v", m.Compactions, err)
	}
	if got := compactionsForRebuild(dir, map[string]bool{"codex:native-1": true}); len(got) != 0 {
		t.Fatalf("tombstoned state carried=%#v", got)
	}
}

func TestCompactionBoundsWholePersistedState(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	state := compactionState("native-1", t.TempDir(), time.Now())
	state.Project = strings.Repeat("p", compactionStateSize)
	if _, err := PutCompaction(dir, state); err == nil || !strings.Contains(err.Error(), "24 KiB") {
		t.Fatalf("oversized state err=%v", err)
	}
}

func TestForgetRemovesCompactionPacketAndTombstonesItsSession(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	dir := filepath.Join(home, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	workspace := t.TempDir()
	state := compactionState("native-forget", workspace, time.Now())
	if err := os.MkdirAll(filepath.Join(dir, "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeManifest(dir, Manifest{
		Version: version, Files: map[string]FileState{}, Sessions: map[string]SessionMeta{
			"codex:native-forget": {ID: "native-forget", Harness: "codex", Project: "example", Updated: state.CapturedAt},
		},
		Compactions: map[string]CompactionState{compactionKey(state.SessionID, state.Workspace): state},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := Forget(dir, ForgetOptions{Session: "native-forget"}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := Compaction(dir, state.SessionID, workspace); err != nil || found {
		t.Fatalf("forgotten compaction found=%t err=%v", found, err)
	}
	if !Tombstoned("codex:native-forget") {
		t.Fatal("forget did not tombstone the compaction session")
	}
}

func TestForgetRemovesCompactionOnlyPackets(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    ForgetOptions
	}{
		{name: "session", o: ForgetOptions{Session: "native-only"}},
		{name: "project", o: ForgetOptions{Project: "example"}},
		{name: "before", o: ForgetOptions{Before: time.Unix(21, 0)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			setHome(t, home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
			dir := filepath.Join(home, "index.db")
			workspace := t.TempDir()
			state := compactionState("native-only", workspace, time.Unix(20, 0))
			if _, err := PutCompaction(dir, state); err != nil {
				t.Fatal(err)
			}
			result, err := Forget(dir, tc.o)
			if err != nil || result.Sessions != 1 || result.Tombstones != 1 {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			if _, found, err := Compaction(dir, state.SessionID, workspace); err != nil || found {
				t.Fatalf("forgotten compaction found=%t err=%v", found, err)
			}
			if _, err := PutCompaction(dir, state); !errors.Is(err, ErrCompactionForgotten) {
				t.Fatalf("recapture err=%v", err)
			}
		})
	}
}

func TestForgetCompactionOnlyExactSessionDoesNotUsePrefix(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	dir := filepath.Join(home, "index.db")
	workspace := t.TempDir()
	first := compactionState("native-only", workspace, time.Unix(20, 0))
	second := compactionState("native-only-extra", workspace, time.Unix(21, 0))
	if _, err := PutCompaction(dir, first); err != nil {
		t.Fatal(err)
	}
	if _, err := PutCompaction(dir, second); err != nil {
		t.Fatal(err)
	}
	result, err := Forget(dir, ForgetOptions{Session: first.SessionID})
	if err != nil || result.Sessions != 1 || result.Keys[0] != "codex:native-only" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if _, found, err := Compaction(dir, first.SessionID, workspace); err != nil || found {
		t.Fatalf("exact compaction found=%t err=%v", found, err)
	}
	if _, found, err := Compaction(dir, second.SessionID, workspace); err != nil || !found {
		t.Fatalf("prefix compaction found=%t err=%v", found, err)
	}
}

func TestForgetCompactionOnlyPersistsMirrorBeforeFirstIndex(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "index")
	t.Setenv("DEJA_INDEX_DIR", dir)
	path := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "project", "dead.jsonl")
	write(t, path, claudeLine("dead-session", "2026-01-02T03:04:05Z", "compaction privacy mirror needle"))
	state := compactionState("dead-session", filepath.Dir(path), time.Unix(20, 0))
	state.Harness = "claude"
	state.Project = "project"
	state.TranscriptPath = path
	if _, err := PutCompaction(dir, state); err != nil {
		t.Fatal(err)
	}
	if _, err := Forget(dir, ForgetOptions{Session: state.SessionID}); err != nil {
		t.Fatal(err)
	}
	if !readTombstoneFile(tombstoneMirrorPath(dir))["claude:dead-session"] {
		t.Fatal("compaction-only forget did not write the index-local tombstone mirror")
	}
	if err := os.RemoveAll(privacyDir()); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := Search(dir, search.Options{Query: "compaction privacy mirror", All: true}); err != nil || len(got) != 0 {
		t.Fatalf("rebuilt forgotten source=%#v err=%v", got, err)
	}
}
