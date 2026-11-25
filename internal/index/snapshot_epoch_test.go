package index

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/query"
)

func snapshotJSONLine(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

func snapshotToolResultTurn(t *testing.T, sid, ts, text string) string {
	t.Helper()
	r := map[string]any{
		"type":      "user",
		"sessionId": sid,
		"timestamp": ts,
		"cwd":       "/work/app",
		"message": map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{
					"type": "tool_result",
					"content": []any{
						map[string]any{"type": "text", "text": text},
					},
				},
			},
		},
	}
	return snapshotJSONLine(t, r)
}

func snapshotCommandTurn(t *testing.T, sid, ts, cmd string) string {
	t.Helper()
	a := map[string]any{
		"type":      "assistant",
		"sessionId": sid,
		"timestamp": ts,
		"cwd":       "/work/app",
		"message": map[string]any{
			"role": "assistant",
			"content": []any{
				map[string]any{
					"type":  "tool_use",
					"name":  "Bash",
					"input": map[string]any{"command": cmd},
				},
			},
		},
	}
	return snapshotJSONLine(t, a)
}

func snapshotAssistantTurn(t *testing.T, sid, ts, text string) string {
	t.Helper()
	a := map[string]any{
		"type":      "assistant",
		"sessionId": sid,
		"timestamp": ts,
		"cwd":       "/work/app",
		"message": map[string]any{
			"role":    "assistant",
			"content": text,
		},
	}
	return snapshotJSONLine(t, a)
}

func snapshotUserTurn(t *testing.T, sid, ts, text string) string {
	t.Helper()
	m := map[string]any{
		"type":      "user",
		"sessionId": sid,
		"timestamp": ts,
		"cwd":       "/work/app",
		"message": map[string]any{
			"role":    "user",
			"content": text,
		},
	}
	return snapshotJSONLine(t, m)
}

func TestSnapshotEpochAdvancesAcrossAllCachesOnSwapAndScrub(t *testing.T) {
	resetAllSnapshotsForTest()
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	dir := filepath.Join(tmp, "idx")

	// Write 12 baseline sessions large enough that replacing the 4 small target
	// sessions takes the incremental replacement/swap+scrub path rather than
	// triggering a full clean rebuild.
	var filler strings.Builder
	for w := range 60 {
		fmt.Fprintf(&filler, " steadyword%02d", w)
	}
	for i := range 12 {
		sid := fmt.Sprintf("keep-%02d", i)
		p := filepath.Join(claude, "-work-app", sid+".jsonl")
		var body strings.Builder
		for k := range 6 {
			ts := fmt.Sprintf("2026-01-%02dT08:%02d:00Z", 1+i, k)
			body.WriteString(snapshotUserTurn(t, sid, ts, fmt.Sprintf("baseline session %02d turn %d%s", i, k, filler.String())))
		}
		write(t, p, body.String())
	}

	targetPaths := make([]string, 4)
	for i := range 4 {
		sid := fmt.Sprintf("target-%02d", i)
		p := filepath.Join(claude, "-work-app", sid+".jsonl")
		targetPaths[i] = p
		ts0 := fmt.Sprintf("2026-01-20T09:%02d:00Z", i)
		ts1 := fmt.Sprintf("2026-01-20T09:%02d:10Z", i)
		ts2 := fmt.Sprintf("2026-01-20T09:%02d:20Z", i)
		ts3 := fmt.Sprintf("2026-01-20T09:%02d:30Z", i)
		body := snapshotUserTurn(t, sid, ts0, fmt.Sprintf("quokkabloom pairs with snorblewidget in step %d", i)) +
			snapshotToolResultTurn(t, sid, ts1, "connection refused: quokkadb:5432") +
			snapshotCommandTurn(t, sid, ts2, "docker compose restart quokkadb") +
			snapshotAssistantTurn(t, sid, ts3, "restarting quokkadb cleared the failure")
		write(t, p, body)
	}

	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// Warm all six caches for dir.
	if _, err := readManifestCached(dir); err != nil {
		t.Fatal(err)
	}
	cat, err := tokenCatalogCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cat["quokkabloom"] {
		t.Fatalf("expected quokkabloom in initial catalog")
	}

	exactRes, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "quokkabloom", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(exactRes.Sessions) != 4 {
		t.Fatalf("expected 4 sessions matching quokkabloom, got %d", len(exactRes.Sessions))
	}

	neighbors := CooccurNeighbors(dir, "snorblewidget")
	cooccurMap := readCooccur(dir)
	if !slices.Contains(neighbors, "quokkabloom") && !slices.Contains(cooccurMap["snorblewidget"], "quokkabloom") {
		t.Fatalf("expected initial cooccur link between snorblewidget and quokkabloom; neighbors=%v map=%v", neighbors, cooccurMap["snorblewidget"])
	}

	fixes := ReadFixes(dir)
	if !hasFixCommand(fixes, "quokkadb") {
		t.Fatalf("expected initial fix pair for quokkadb, got %+v", fixes)
	}

	bucketsDir := filepath.Join(dir, "buckets")
	if n := countBucketFilesCached(bucketsDir); n == 0 {
		t.Fatalf("expected non-zero cached bucket file count")
	}
	if stale := cachedStale(dir); stale != "" {
		t.Fatalf("unexpected initial stale bucket dir: %s", stale)
	}
	if len(cachedDirs(dir)) == 0 {
		t.Fatalf("expected bucketDirCache to be populated after search")
	}

	e0 := snapshotEpoch(dir)
	if e0 == 0 {
		t.Fatalf("expected non-zero initial snapshotEpoch, got 0")
	}

	// Rewrite all 4 target transcripts removing quokkabloom and quokkabin,
	// advancing mtimes by +2s so replacement + scrub must invalidate derived state.
	later := time.Now().Add(2 * time.Second)
	for i, p := range targetPaths {
		sid := fmt.Sprintf("target-%02d", i)
		ts0 := fmt.Sprintf("2026-01-20T09:%02d:00Z", i)
		ts1 := fmt.Sprintf("2026-01-20T09:%02d:10Z", i)
		ts2 := fmt.Sprintf("2026-01-20T09:%02d:20Z", i)
		body := snapshotUserTurn(t, sid, ts0, fmt.Sprintf("freshpetal builds after clean edit in step %d", i)) +
			snapshotCommandTurn(t, sid, ts1, "go build ./cmd/freshpetal") +
			snapshotAssistantTurn(t, sid, ts2, "clean check")
		write(t, p, body)
		if cerr := os.Chtimes(p, later, later); cerr != nil {
			t.Fatal(cerr)
		}
	}

	if eerr := Ensure(dir, "", false, nil); eerr != nil {
		t.Fatal(eerr)
	}

	e1 := snapshotEpoch(dir)
	if e1 <= e0 {
		t.Fatalf("expected snapshotEpoch to advance after swap+scrub: e0=%d e1=%d", e0, e1)
	}
	if Damaged(dir) {
		t.Fatalf("expected undamaged index after incremental update, got %q", DamageReason(dir))
	}
	if stale := cachedStale(dir); stale != "" {
		t.Fatalf("stale bucket dir survived swap+scrub: %s", stale)
	}

	afterExact, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "quokkabloom", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterExact.Sessions) != 0 {
		t.Fatalf("stale exact search returned %d sessions for scrubbed token quokkabloom", len(afterExact.Sessions))
	}

	afterFuzzy, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "quokkablom", Fuzzy: true, All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterFuzzy.Sessions) != 0 {
		t.Fatalf("stale fuzzy search returned %d sessions for scrubbed token quokkabloom", len(afterFuzzy.Sessions))
	}

	catAfter, err := tokenCatalogCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	if catAfter["quokkabloom"] {
		t.Fatalf("stale catalogCache retained scrubbed token quokkabloom")
	}

	afterNeighbors := CooccurNeighbors(dir, "snorblewidget")
	afterCooccurMap := readCooccur(dir)
	if slices.Contains(afterNeighbors, "quokkabloom") || slices.Contains(afterCooccurMap["snorblewidget"], "quokkabloom") || len(afterCooccurMap["quokkabloom"]) > 0 {
		t.Fatalf("stale cooccur retained scrubbed token quokkabloom: neighbors=%v map[snorblewidget]=%v map[quokkabloom]=%v",
			afterNeighbors, afterCooccurMap["snorblewidget"], afterCooccurMap["quokkabloom"])
	}

	fixesAfter := ReadFixes(dir)
	if hasFixCommand(fixesAfter, "quokkadb") {
		t.Fatalf("stale fixesCache retained scrubbed command quokkadb: %+v", fixesAfter)
	}

	// Verify newly written text is live and searchable in the same warmed process.
	freshRes, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "freshpetal", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(freshRes.Sessions) != 4 {
		t.Fatalf("expected 4 sessions matching freshpetal after update, got %d", len(freshRes.Sessions))
	}
}

func TestWriteCooccurWithoutDoesNotMutateCachedMap(t *testing.T) {
	resetAllSnapshotsForTest()
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	initial := map[string][]string{
		"alpha":    {"obsolete", "keeper"},
		"keeper":   {"alpha", "obsolete"},
		"obsolete": {"alpha", "keeper"},
	}
	if err := writeGobAtomic(cooccurPath(dir), initial); err != nil {
		t.Fatal(err)
	}

	// Warm cooccur cache and grab a live slice reference from it.
	warmed := readCooccur(dir)
	if !slices.Contains(warmed["alpha"], "obsolete") {
		t.Fatalf("expected warmed cooccur map to include obsolete, got %v", warmed["alpha"])
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	stopReaders := sync.OnceFunc(func() { close(stop); wg.Wait() })
	defer stopReaders()
	for range 4 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					for _, vals := range warmed {
						for _, v := range vals {
							_ = len(v)
						}
					}
					_ = readCooccur(dir)
					if runtime.GOOS == "windows" {
						time.Sleep(time.Millisecond)
					}
				}
			}
		})
	}

	if err := dropCooccurWords(dir, func(word string) bool { return word == "obsolete" }); err != nil {
		t.Fatal(err)
	}
	stopReaders()

	// Previously fetched map and its neighbor slices must stay immutable.
	if !slices.Contains(warmed["alpha"], "obsolete") || len(warmed["obsolete"]) == 0 {
		t.Fatalf("writeCooccurWithout mutated previously cached map in place: %#v", warmed)
	}

	// Fresh read must observe the pruned disk/cache state without obsolete.
	reloaded := readCooccur(dir)
	if slices.Contains(reloaded["alpha"], "obsolete") || len(reloaded["obsolete"]) != 0 {
		t.Fatalf("readCooccur returned stale obsolete neighbor after dropCooccurWords: %#v", reloaded)
	}
}

func hasFixCommand(pairs []FixPair, sub string) bool {
	for _, p := range pairs {
		if strings.Contains(p.Command, sub) {
			return true
		}
	}
	return false
}

type snapshotXorshift64 uint64

func (r *snapshotXorshift64) next() uint64 {
	x := uint64(*r)
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	*r = snapshotXorshift64(x)
	return x
}

func (r *snapshotXorshift64) intn(n int) int {
	v := r.next() >> 16
	out := 0
	for b := range 30 {
		if (v>>b)&1 != 0 {
			out |= 1 << b
		}
	}
	return out % n
}

func snapshotAlphaCode(v, width int) string {
	buf := make([]byte, width)
	for i := width - 1; i >= 0; i-- {
		buf[i] = "abcdefghijklmnopqrstuvwxyz"[v%26]
		v /= 26
	}
	return string(buf)
}

func snapshotSessionBody(t *testing.T, sid, tok, peer, fixSvc string, baseTime time.Time, padCount int) string {
	t.Helper()
	var b strings.Builder
	pad := strings.Repeat(" note", padCount)
	b.WriteString(snapshotUserTurn(t, sid, baseTime.Format(time.RFC3339),
		fmt.Sprintf("Please investigate module %s alongside %s%s.", tok, peer, pad)))
	b.WriteString(snapshotAssistantTurn(t, sid, baseTime.Add(time.Second).Format(time.RFC3339),
		fmt.Sprintf("Inspecting module %s and neighbor %s.", tok, peer)))
	if fixSvc != "" {
		b.WriteString(snapshotToolResultTurn(t, sid, baseTime.Add(2*time.Second).Format(time.RFC3339),
			"Error: service "+fixSvc+" connection refused\nexit status 1"))
		b.WriteString(snapshotCommandTurn(t, sid, baseTime.Add(3*time.Second).Format(time.RFC3339),
			"docker compose restart "+fixSvc))
		b.WriteString(snapshotToolResultTurn(t, sid, baseTime.Add(4*time.Second).Format(time.RFC3339),
			"service "+fixSvc+" restarted ok"))
		b.WriteString(snapshotAssistantTurn(t, sid, baseTime.Add(5*time.Second).Format(time.RFC3339),
			"Restarted "+fixSvc+" and fixed the error."))
		b.WriteString(snapshotUserTurn(t, sid, baseTime.Add(6*time.Second).Format(time.RFC3339),
			"Thanks, moving on to the next endpoint."))
	}
	return b.String()
}

func warmAllSnapshotCaches(t *testing.T, dir, searchTok, cooccurTok string) {
	t.Helper()
	if _, err := readManifestCached(dir); err != nil {
		t.Fatalf("readManifestCached(%q): %v", dir, err)
	}
	if _, err := tokenCatalogCached(dir); err != nil {
		t.Fatalf("tokenCatalogCached(%q): %v", dir, err)
	}
	if _, err := SearchWithRecoveryDetailed(dir, query.Options{Query: searchTok, All: true}, nil); err != nil {
		t.Fatalf("SearchWithRecoveryDetailed(%q, %q): %v", dir, searchTok, err)
	}
	_ = CooccurNeighbors(dir, cooccurTok)
	_ = ReadFixes(dir)
	_ = countBucketFilesCached(filepath.Join(dir, "buckets"))
}

func TestSnapshotSlowReaderCannotPoisonNewerEpoch(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	resetAllSnapshotsForTest()
	t.Cleanup(resetAllSnapshotsForTest)

	// Part A: empty-path guards.
	if got := snapshotEpoch(""); got != 0 {
		t.Fatalf("snapshotEpoch empty = %d, want 0", got)
	}
	if got := cachedBucketDirs(""); got != nil {
		t.Fatalf("cachedBucketDirs empty = %#v, want nil", got)
	}
	invalidateSnapshot("")
	invalidateDerivedSnapshot("")
	invalidateFixesFor("")
	moveSnapshot("", "")
	if iDir, tgt := indexDirForBucketsPath(""); iDir != "" || tgt != "" {
		t.Fatalf("indexDirForBucketsPath empty = (%q, %q), want empty", iDir, tgt)
	}
	if storeManifestAtEpoch("", 1, nil, nil, Manifest{}) {
		t.Fatal("storeManifestAtEpoch accepted empty dir")
	}
	if storeCatalogAtEpoch("", 1, "sig", newTokenIndex(nil)) {
		t.Fatal("storeCatalogAtEpoch accepted empty dir")
	}
	if storeBucketDirAtEpoch("", 1, "p", &cachedBucketDir{}) {
		t.Fatal("storeBucketDirAtEpoch accepted empty dir")
	}
	if storeCooccurAtEpoch("", 1, nil, map[string][]string{"a": {"b"}}) {
		t.Fatal("storeCooccurAtEpoch accepted empty dir")
	}
	if storeFixesAtEpoch("", 1, nil, []FixPair{{Command: "x"}}) {
		t.Fatal("storeFixesAtEpoch accepted empty dir")
	}
	if storeBucketFileCountAtEpoch("", 1, "b", 0, 1) {
		t.Fatal("storeBucketFileCountAtEpoch accepted empty dir")
	}

	// Build an index with >= 3 sessions sharing a co-occurring pair plus a fix pair
	// so every cache layer holds real on-disk artifacts.
	claudeProj := filepath.Join(tmp, "claude", "proj")
	base := time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC)
	for i := range 3 {
		sid := fmt.Sprintf("slowreader%02d", i)
		fixSvc := ""
		if i == 0 {
			fixSvc = "quokkadb"
		}
		body := snapshotSessionBody(t, sid, "alphaanchor", "bravoneighbor", fixSvc, base.Add(time.Duration(i)*time.Hour), i+1)
		write(t, filepath.Join(claudeProj, sid+".jsonl"), body)
	}

	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatalf("Ensure(dir): %v", err)
	}
	warmAllSnapshotCaches(t, dir, "alphaanchor", "alphaanchor")

	e0 := snapshotEpoch(dir)
	if e0 == 0 {
		t.Fatal("expected non-zero snapshotEpoch after warming all caches")
	}
	mfi, _ := os.Stat(filepath.Join(dir, "manifest.gob"))
	sfi, _ := os.Stat(filepath.Join(dir, "sessions.bin"))
	cfi, _ := os.Stat(cooccurPath(dir))
	ffi, _ := os.Stat(filepath.Join(dir, "fixes.gob"))
	bfi, _ := os.Stat(filepath.Join(dir, "buckets"))
	bmtime := int64(1)
	if bfi != nil {
		bmtime = bfi.ModTime().UnixNano()
	}

	invalidateDerivedSnapshot(dir)
	e1 := snapshotEpoch(dir)
	if e1 <= e0 {
		t.Fatalf("invalidateDerivedSnapshot epoch = %d, want > %d", e1, e0)
	}

	// Simulate slow readers attempting to populate all 6 slots under stale epoch e0.
	if storeManifestAtEpoch(dir, e0, mfi, sfi, Manifest{Version: 999}) {
		t.Fatal("storeManifestAtEpoch accepted stale epoch")
	}
	if storeCatalogAtEpoch(dir, e0, "poisonsig", newTokenIndex(map[string]bool{"poisonword": true})) {
		t.Fatal("storeCatalogAtEpoch accepted stale epoch")
	}
	if storeBucketDirAtEpoch(dir, e0, "__poison_bucket_path__", &cachedBucketDir{}) {
		t.Fatal("storeBucketDirAtEpoch accepted stale epoch")
	}
	if storeCooccurAtEpoch(dir, e0, cfi, map[string][]string{"poisonword": {"ghost"}}) {
		t.Fatal("storeCooccurAtEpoch accepted stale epoch")
	}
	if storeFixesAtEpoch(dir, e0, ffi, []FixPair{{Command: "poisoncmd"}}) {
		t.Fatal("storeFixesAtEpoch accepted stale epoch")
	}
	if storeBucketFileCountAtEpoch(dir, e0, filepath.Join(dir, "buckets"), bmtime, 99999) {
		t.Fatal("storeBucketFileCountAtEpoch accepted stale epoch")
	}

	m, merr := readManifestCached(dir)
	if merr != nil || m.Version == 999 {
		t.Fatalf("manifest cache poisoned or errored: version=%d err=%v", m.Version, merr)
	}
	cat, cerr := tokenCatalogCached(dir)
	if cerr != nil || cat["poisonword"] {
		t.Fatalf("catalog cache poisoned or errored: poisonword=%v err=%v", cat["poisonword"], cerr)
	}
	if cachedBucketDirs(dir)["__poison_bucket_path__"] != nil {
		t.Fatal("bucket dir cache accepted stale sentinel key")
	}
	if co := readCooccur(dir); slices.Contains(co["poisonword"], "ghost") || len(co["poisonword"]) != 0 {
		t.Fatalf("cooccur cache poisoned: %#v", co["poisonword"])
	}
	if fx := ReadFixes(dir); hasFixCommand(fx, "poisoncmd") {
		t.Fatalf("fixes cache poisoned: %#v", fx)
	}
	if cnt := countBucketFilesCached(filepath.Join(dir, "buckets")); cnt == 99999 || cnt <= 0 {
		t.Fatalf("bucket file count cache poisoned or invalid: %d", cnt)
	}

	// Verify monotonic epoch advance across every invalidator.
	prev := snapshotEpoch(dir)
	checkBump := func(name string, fn func()) {
		t.Helper()
		fn()
		cur := snapshotEpoch(dir)
		if cur <= prev {
			t.Fatalf("%s did not advance epoch: cur=%d prev=%d", name, cur, prev)
		}
		prev = cur
	}
	checkBump("dropBucketDirs", func() { dropBucketDirs(dir) })
	checkBump("dropCooccurCache", func() { dropCooccurCache(dir) })
	checkBump("dropBucketFileCount", func() { dropBucketFileCount(filepath.Join(dir, "buckets")) })
	checkBump("invalidateFixesFor", func() { invalidateFixesFor(dir) })
	checkBump("invalidateFixesCache", func() { invalidateFixesCache() })

	srcDir := filepath.Join(tmp, "srcidx")
	if err := Ensure(srcDir, "", false, nil); err != nil {
		t.Fatalf("Ensure(srcDir): %v", err)
	}
	if _, err := readManifestCached(srcDir); err != nil {
		t.Fatalf("readManifestCached(srcDir): %v", err)
	}
	checkBump("moveSnapshot", func() { moveSnapshot(srcDir, dir) })
	if got := snapshotEpoch(srcDir); got != 0 {
		t.Fatalf("moveSnapshot left srcDir epoch = %d, want 0", got)
	}

	// Verify 4-entry MRU eviction when warming 6 distinct index directories.
	resetAllSnapshotsForTest()
	dirs := make([]string, 6)
	for i := range dirs {
		dirs[i] = filepath.Join(tmp, fmt.Sprintf("mru-%d", i))
		if err := Ensure(dirs[i], "", false, nil); err != nil {
			t.Fatalf("Ensure(%s): %v", dirs[i], err)
		}
		if _, err := readManifestCached(dirs[i]); err != nil {
			t.Fatalf("readManifestCached(%s): %v", dirs[i], err)
		}
	}
	for i := range 2 {
		if got := snapshotEpoch(dirs[i]); got != 0 {
			t.Fatalf("expected evicted dir[%d] epoch 0, got %d", i, got)
		}
	}
	for i := 2; i < len(dirs); i++ {
		if got := snapshotEpoch(dirs[i]); got == 0 {
			t.Fatalf("expected warm dir[%d] non-zero epoch", i)
		}
	}

	unsortedDir := &cachedBucketDir{
		entries: []bucketEntry{{tok: "tb", off: 10, n: 4}, {tok: "ta", off: 14, n: 6}},
		sorted:  false,
	}
	if e, ok := unsortedDir.find("ta"); !ok || e.off != 14 {
		t.Fatalf("unsorted cachedBucketDir.find(ta) = (%+v, %v)", e, ok)
	}
	if _, ok := unsortedDir.find("tc"); ok {
		t.Fatal("unsorted cachedBucketDir.find(tc) unexpectedly succeeded")
	}

	// Part B: -race concurrent readers against mutating writer loop.
	raceTmp := t.TempDir()
	setHome(t, filepath.Join(raceTmp, "home"))
	raceClaude := filepath.Join(raceTmp, "claude", "proj")
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(raceTmp, "claude"))
	raceDir := filepath.Join(raceTmp, "raceidx")

	seedIDs := []string{"r00000000000", "r01000000001", "r02000000002"}
	for i, sid := range seedIDs {
		fixSvc := ""
		if i == 0 {
			fixSvc = "racedb"
		}
		tok := "racetok" + snapshotAlphaCode(i, 4)
		body := snapshotSessionBody(t, sid, tok, "racepeer", fixSvc, base.Add(time.Duration(i)*time.Minute), 1)
		p := filepath.Join(raceClaude, sid+".jsonl")
		write(t, p, body)
		mt := base.Add(time.Duration(i+1) * time.Minute)
		_ = os.Chtimes(p, mt, mt)
	}
	if err := Ensure(raceDir, "", false, nil); err != nil {
		t.Fatalf("Ensure(raceDir): %v", err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	stopReaders := sync.OnceFunc(func() { close(stop); wg.Wait() })
	defer stopReaders()
	for g := range 6 {
		gid := g
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				switch gid % 6 {
				case 0:
					_, _ = readManifestCached(raceDir)
				case 1:
					_, _ = tokenCatalogCached(raceDir)
				case 2:
					_, _ = SearchWithRecoveryDetailed(raceDir, query.Options{Query: "racetokaaaa", All: true}, nil)
				case 3:
					_ = CooccurNeighbors(raceDir, "racepeer")
				case 4:
					_ = ReadFixes(raceDir)
				case 5:
					_ = countBucketFilesCached(filepath.Join(raceDir, "buckets"))
				}
				if runtime.GOOS == "windows" {
					time.Sleep(time.Millisecond)
				}
			}
		})
	}

	r02Path := filepath.Join(raceClaude, seedIDs[2]+".jsonl")
	r02Time := base.Add(10 * time.Minute)
	for step := range 10 {
		switch step % 5 {
		case 0:
			sid := fmt.Sprintf("r99%09d", step)
			body := snapshotSessionBody(t, sid, "racetoknewk", "racepeer", "", base.Add(time.Duration(20+step)*time.Minute), 1)
			p := filepath.Join(raceClaude, sid+".jsonl")
			write(t, p, body)
			mt := base.Add(time.Duration(20+step) * time.Minute)
			_ = os.Chtimes(p, mt, mt)
			if err := Ensure(raceDir, "", false, nil); err != nil {
				t.Fatalf("step %d create Ensure: %v", step, err)
			}
		case 1:
			r02Time = r02Time.Add(2 * time.Second)
			f, err := os.OpenFile(r02Path, os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatalf("step %d open append: %v", step, err)
			}
			app := snapshotUserTurn(t, seedIDs[2], r02Time.Format(time.RFC3339),
				"appending "+snapshotAlphaCode(step, 4)+" detail") +
				snapshotAssistantTurn(t, seedIDs[2], r02Time.Add(time.Second).Format(time.RFC3339), "acknowledged")
			if _, werr := f.WriteString(app); werr != nil {
				_ = f.Close()
				t.Fatalf("step %d write append: %v", step, werr)
			}
			_ = f.Close()
			_ = os.Chtimes(r02Path, r02Time, r02Time)
			if aerr := Ensure(raceDir, "", false, nil); aerr != nil {
				t.Fatalf("step %d append Ensure: %v", step, aerr)
			}
		case 2:
			r02Time = r02Time.Add(2 * time.Second)
			body := snapshotSessionBody(t, seedIDs[2], "racetok"+snapshotAlphaCode(step+50, 4), "racepeer", "", r02Time, (step%3)+1)
			write(t, r02Path, body)
			_ = os.Chtimes(r02Path, r02Time, r02Time)
			if err := Ensure(raceDir, "", false, nil); err != nil {
				t.Fatalf("step %d rewrite Ensure: %v", step, err)
			}
		case 3:
			if _, err := Forget(raceDir, ForgetOptions{Session: seedIDs[1]}); err != nil {
				t.Fatalf("step %d Forget: %v", step, err)
			}
		case 4:
			if _, err := Unforget(raceDir, seedIDs[1], nil); err != nil {
				t.Fatalf("step %d Unforget: %v", step, err)
			}
		}
	}

	stopReaders()

	if err := Ensure(raceDir, "", false, nil); err != nil {
		t.Fatalf("final Ensure(raceDir): %v", err)
	}
	if Damaged(raceDir) {
		t.Fatalf("raceDir marked damaged: %s", DamageReason(raceDir))
	}
	rep, err := DeepVerify(raceDir)
	if err != nil {
		t.Fatalf("DeepVerify(raceDir): %v", err)
	}
	if !rep.Clean() || len(rep.Stale) != 0 {
		t.Fatalf("DeepVerify not clean: findings=%v stale=%v", rep.Findings, rep.Stale)
	}
}

func snapshotFixSignatures(pairs []FixPair) []string {
	out := make([]string, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, fmt.Sprintf("%d|%s|%s|%t", p.Sig, p.Command, p.Key, p.Candidate))
	}
	slices.Sort(out)
	return out
}

func snapshotSortedIDs(res SearchResult) []string {
	out := make([]string, 0, len(res.Sessions))
	for _, s := range res.Sessions {
		out = append(out, s.ID)
	}
	slices.Sort(out)
	return out
}

func assertSnapshotParity(t *testing.T, liveDir, cleanDir string, sampleToks []string) {
	t.Helper()
	lm, err := readManifestCached(liveDir)
	if err != nil {
		t.Fatalf("readManifestCached(liveDir): %v", err)
	}
	cm, err := readManifestCached(cleanDir)
	if err != nil {
		t.Fatalf("readManifestCached(cleanDir): %v", err)
	}
	if len(lm.Sessions) != len(cm.Sessions) {
		t.Fatalf("session count mismatch: live=%d clean=%d", len(lm.Sessions), len(cm.Sessions))
	}
	for k, lv := range lm.Sessions {
		cv, ok := cm.Sessions[k]
		if !ok {
			t.Fatalf("session %q in live manifest missing in clean manifest", k)
		}
		if lv.ID != cv.ID || lv.Harness != cv.Harness || lv.Project != cv.Project || lv.Words != cv.Words {
			t.Fatalf("session %q meta mismatch: live=%+v clean=%+v", k, lv, cv)
		}
	}

	lcat, err := tokenCatalogCached(liveDir)
	if err != nil {
		t.Fatalf("tokenCatalogCached(liveDir): %v", err)
	}
	ccat, err := tokenCatalogCached(cleanDir)
	if err != nil {
		t.Fatalf("tokenCatalogCached(cleanDir): %v", err)
	}
	if !maps.Equal(lcat, ccat) {
		var extraLive, missingLive []string
		for k := range lcat {
			if !ccat[k] {
				extraLive = append(extraLive, k)
			}
		}
		for k := range ccat {
			if !lcat[k] {
				missingLive = append(missingLive, k)
			}
		}
		slices.Sort(extraLive)
		slices.Sort(missingLive)
		t.Fatalf("tokenCatalog mismatch (%s vs %s): len(live)=%d len(clean)=%d extraLive=%v missingLive=%v",
			liveDir, cleanDir, len(lcat), len(ccat), extraLive, missingLive)
	}

	for _, tok := range sampleToks {
		lres, lerr := SearchWithRecoveryDetailed(liveDir, query.Options{Query: tok, All: true}, nil)
		if lerr != nil {
			t.Fatalf("live search %q: %v", tok, lerr)
		}
		cres, cerr := SearchWithRecoveryDetailed(cleanDir, query.Options{Query: tok, All: true}, nil)
		if cerr != nil {
			t.Fatalf("clean search %q: %v", tok, cerr)
		}
		lids := snapshotSortedIDs(lres)
		cids := snapshotSortedIDs(cres)
		if !slices.Equal(lids, cids) {
			t.Fatalf("search %q IDs mismatch: live=%v clean=%v", tok, lids, cids)
		}

		lco := slices.Clone(CooccurNeighbors(liveDir, tok))
		cco := slices.Clone(CooccurNeighbors(cleanDir, tok))
		slices.Sort(lco)
		slices.Sort(cco)
		if !slices.Equal(lco, cco) {
			t.Fatalf("CooccurNeighbors %q mismatch: live=%v clean=%v", tok, lco, cco)
		}
	}

	lsub, err := SearchWithRecoveryDetailed(liveDir, query.Options{Query: "qtok", All: true}, nil)
	if err != nil {
		t.Fatalf("live substring search qtok: %v", err)
	}
	csub, err := SearchWithRecoveryDetailed(cleanDir, query.Options{Query: "qtok", All: true}, nil)
	if err != nil {
		t.Fatalf("clean substring search qtok: %v", err)
	}
	if !slices.Equal(snapshotSortedIDs(lsub), snapshotSortedIDs(csub)) {
		t.Fatalf("substring search qtok IDs mismatch: live=%v clean=%v", snapshotSortedIDs(lsub), snapshotSortedIDs(csub))
	}
	for _, vs := range lsub.Variants {
		for _, v := range vs {
			if isBlankedWord(v) {
				t.Fatalf("live substring search qtok leaked blanked variant %q", v)
			}
		}
	}

	lfx := snapshotFixSignatures(ReadFixes(liveDir))
	cfx := snapshotFixSignatures(ReadFixes(cleanDir))
	if !slices.Equal(lfx, cfx) {
		t.Fatalf("ReadFixes mismatch:\nlive=%v\nclean=%v", lfx, cfx)
	}
}

func TestStatefulIncrementalMatchesCleanRebuildProperty(t *testing.T) {
	baseTmp := hermeticIndexEnv(t)
	resetAllSnapshotsForTest()
	t.Cleanup(resetAllSnapshotsForTest)

	type modelSession struct {
		num           int
		id            string
		path          string
		gen           int
		hasFix        bool
		mtime         time.Time
		turnTime      time.Time
		currentTokens []string
		currentFixCmd string
		forgotten     bool
	}

	seeds := []uint64{0x9e3779b97f4a7c15, 0xbf58476d1ce4e5b9, 0x94d049bb133111eb}
	baseStart := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

	for seedIdx, seed := range seeds {
		rng := snapshotXorshift64(seed)
		seedDir := filepath.Join(baseTmp, fmt.Sprintf("seed-%d", seedIdx))
		setHome(t, filepath.Join(seedDir, "home"))
		claudeRoot := filepath.Join(seedDir, "claude")
		claudeProj := filepath.Join(claudeRoot, "proj")
		t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
		liveDir := filepath.Join(seedDir, "live")
		resetAllSnapshotsForTest()

		sessions := make([]*modelSession, 0, 10)
		var retiredTokens []string
		var retiredFixCmds []string
		tokSeq := 1
		spreadCode := func() string {
			v := (tokSeq*7919 + 101) % (26 * 26 * 26 * 26)
			tokSeq++
			var buf [8]byte
			for i := 3; i >= 0; i-- {
				ch := "abcdefghijklmnopqrstuvwxyz"[v%26]
				v /= 26
				buf[i*2] = ch
				buf[i*2+1] = ch
			}
			return string(buf[:])
		}

		nextTokPair := func() (string, string) {
			code := spreadCode()
			return "qtok" + code + "k", "qpeer" + code + "k"
		}
		nextSvcTok := func() string {
			return "qsvc" + spreadCode() + "k"
		}

		for i := range 10 {
			sid := fmt.Sprintf("sess%08d", i)
			p := filepath.Join(claudeProj, sid+".jsonl")
			tok, peer := nextTokPair()
			fixSvc := ""
			hasFix := i%3 == 0
			if hasFix {
				fixSvc = nextSvcTok()
			}
			ts := baseStart.Add(time.Duration(i) * time.Hour)
			write(t, p, snapshotSessionBody(t, sid, tok, peer, fixSvc, ts, 1))
			mt := ts.Add(10 * time.Second)
			_ = os.Chtimes(p, mt, mt)
			sessions = append(sessions, &modelSession{
				num:           i,
				id:            sid,
				path:          p,
				gen:           1,
				hasFix:        hasFix,
				mtime:         mt,
				turnTime:      mt,
				currentTokens: []string{tok, peer},
				currentFixCmd: fixSvc,
			})
		}

		if err := Ensure(liveDir, "", false, nil); err != nil {
			t.Fatalf("seed %d initial Ensure: %v", seedIdx, err)
		}

		nextSessNum := 10
		appendSeq := 0
		for step := range 15 {
			var liveIdx, forgotIdx []int
			for idx, s := range sessions {
				if s.forgotten {
					forgotIdx = append(forgotIdx, idx)
				} else {
					liveIdx = append(liveIdx, idx)
				}
			}

			// Build eligible action set for this step.
			actions := []string{"create", "tail", "rewrite"}
			if len(liveIdx) > 3 {
				actions = append(actions, "forget")
			}
			if len(forgotIdx) > 0 {
				actions = append(actions, "unforget")
			}
			act := actions[rng.intn(len(actions))]
			isTombstoneOp := false

			switch act {
			case "create":
				num := nextSessNum
				nextSessNum++
				sid := fmt.Sprintf("sess%08d", num)
				p := filepath.Join(claudeProj, sid+".jsonl")
				tok, peer := nextTokPair()
				fixSvc := ""
				hasFix := num%3 == 0
				if hasFix {
					fixSvc = nextSvcTok()
				}
				ts := baseStart.Add(time.Duration(20+step) * time.Hour)
				write(t, p, snapshotSessionBody(t, sid, tok, peer, fixSvc, ts, 1))
				mt := ts.Add(10 * time.Second)
				_ = os.Chtimes(p, mt, mt)
				sessions = append(sessions, &modelSession{
					num:           num,
					id:            sid,
					path:          p,
					gen:           1,
					hasFix:        hasFix,
					mtime:         mt,
					turnTime:      mt,
					currentTokens: []string{tok, peer},
					currentFixCmd: fixSvc,
				})

			case "tail":
				s := sessions[liveIdx[rng.intn(len(liveIdx))]]
				appendSeq++
				appTok, appPeer := nextTokPair()
				s.turnTime = s.turnTime.Add(time.Minute)
				s.mtime = s.mtime.Add(2 * time.Second)
				f, err := os.OpenFile(s.path, os.O_APPEND|os.O_WRONLY, 0o600)
				if err != nil {
					t.Fatalf("seed %d step %d open append: %v", seedIdx, step, err)
				}
				app := snapshotUserTurn(t, s.id, s.turnTime.Format(time.RFC3339),
					fmt.Sprintf("Follow-up note for %s and %s #%d.", appTok, appPeer, appendSeq)) +
					snapshotAssistantTurn(t, s.id, s.turnTime.Add(time.Second).Format(time.RFC3339),
						"Logged follow-up "+appTok+".")
				if _, err := f.WriteString(app); err != nil {
					_ = f.Close()
					t.Fatalf("seed %d step %d write append: %v", seedIdx, step, err)
				}
				_ = f.Close()
				_ = os.Chtimes(s.path, s.mtime, s.mtime)
				s.currentTokens = append(s.currentTokens, appTok, appPeer)

			case "rewrite":
				s := sessions[liveIdx[rng.intn(len(liveIdx))]]
				retiredTokens = append(retiredTokens, s.currentTokens...)
				if s.currentFixCmd != "" {
					retiredFixCmds = append(retiredFixCmds, s.currentFixCmd)
				}
				s.gen++
				tok, peer := nextTokPair()
				fixSvc := ""
				if s.hasFix {
					fixSvc = nextSvcTok()
				}
				s.turnTime = s.turnTime.Add(time.Minute)
				s.mtime = s.mtime.Add(2 * time.Second)
				write(t, s.path, snapshotSessionBody(t, s.id, tok, peer, fixSvc, s.turnTime, (s.gen%4)+1))
				_ = os.Chtimes(s.path, s.mtime, s.mtime)
				s.currentTokens = []string{tok, peer}
				s.currentFixCmd = fixSvc

			case "forget":
				s := sessions[liveIdx[rng.intn(len(liveIdx))]]
				if _, err := Forget(liveDir, ForgetOptions{Session: s.id}); err != nil {
					t.Fatalf("seed %d step %d Forget(%s): %v", seedIdx, step, s.id, err)
				}
				s.forgotten = true
				isTombstoneOp = true

			case "unforget":
				s := sessions[forgotIdx[rng.intn(len(forgotIdx))]]
				if _, err := Unforget(liveDir, s.id, nil); err != nil {
					t.Fatalf("seed %d step %d Unforget(%s): %v", seedIdx, step, s.id, err)
				}
				s.forgotten = false
				isTombstoneOp = true
			}

			if !isTombstoneOp {
				if step%2 == 0 {
					if err := Ensure(liveDir, "", false, nil); err != nil {
						t.Fatalf("seed %d step %d (%s) Ensure: %v", seedIdx, step, act, err)
					}
				} else {
					fresh, _, chk := CheckUpToDate(liveDir, "")
					if !fresh {
						if chk != nil {
							if err := EnsureChecked(chk, false, nil); err != nil {
								t.Fatalf("seed %d step %d (%s) EnsureChecked: %v", seedIdx, step, act, err)
							}
						} else if err := Ensure(liveDir, "", false, nil); err != nil {
							t.Fatalf("seed %d step %d (%s) fallback Ensure: %v", seedIdx, step, act, err)
						}
					}
				}
			}

			var sampleLive []string
			var absentTokens []string
			var absentFixCmds []string
			absentTokens = append(absentTokens, retiredTokens...)
			absentFixCmds = append(absentFixCmds, retiredFixCmds...)
			for _, s := range sessions {
				if s.forgotten {
					absentTokens = append(absentTokens, s.currentTokens...)
					if s.currentFixCmd != "" {
						absentFixCmds = append(absentFixCmds, s.currentFixCmd)
					}
				} else if len(s.currentTokens) > 0 {
					sampleLive = append(sampleLive, s.currentTokens[0])
				}
			}

			warmTok := ""
			if len(sampleLive) > 0 {
				warmTok = sampleLive[0]
			}
			warmAllSnapshotCaches(t, liveDir, warmTok, warmTok)

			if Damaged(liveDir) {
				t.Fatalf("seed %d step %d (%s): liveDir damaged: %s", seedIdx, step, act, DamageReason(liveDir))
			}
			rep, err := DeepVerify(liveDir)
			if err != nil {
				t.Fatalf("seed %d step %d (%s) DeepVerify: %v", seedIdx, step, act, err)
			}
			if !rep.Clean() || len(rep.Stale) != 0 {
				t.Fatalf("seed %d step %d (%s) DeepVerify unclean: findings=%v stale=%v", seedIdx, step, act, rep.Findings, rep.Stale)
			}

			cat, err := tokenCatalogCached(liveDir)
			if err != nil {
				t.Fatalf("seed %d step %d tokenCatalogCached: %v", seedIdx, step, err)
			}
			for _, tok := range absentTokens {
				res, err := SearchWithRecoveryDetailed(liveDir, query.Options{Query: tok, All: true}, nil)
				if err != nil {
					t.Fatalf("seed %d step %d search absent %q: %v", seedIdx, step, tok, err)
				}
				if len(res.Sessions) != 0 {
					t.Fatalf("seed %d step %d (%s): absent token %q still matched %d sessions", seedIdx, step, act, tok, len(res.Sessions))
				}
				if cat[tok] {
					t.Fatalf("seed %d step %d (%s): absent token %q still in catalog", seedIdx, step, act, tok)
				}
				if ns := CooccurNeighbors(liveDir, tok); len(ns) != 0 {
					t.Fatalf("seed %d step %d (%s): absent token %q still has cooccur neighbors %v", seedIdx, step, act, tok, ns)
				}
			}
			fx := ReadFixes(liveDir)
			for _, cmd := range absentFixCmds {
				if hasFixCommand(fx, cmd) {
					t.Fatalf("seed %d step %d (%s): absent fix command %q still present", seedIdx, step, act, cmd)
				}
			}

			if step == 4 || step == 9 || step == 14 {
				cleanDir := filepath.Join(seedDir, fmt.Sprintf("clean-%d", step))
				if err := Ensure(cleanDir, "", true, nil); err != nil {
					t.Fatalf("seed %d step %d Ensure(cleanDir): %v", seedIdx, step, err)
				}
				assertSnapshotParity(t, liveDir, cleanDir, sampleLive)
			}
		}
	}
}
