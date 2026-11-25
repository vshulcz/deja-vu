package hookengine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
	"github.com/vshulcz/deja-vu/internal/evidence"
)

func TestSeenKeySanitizationAndPrefixGuards(t *testing.T) {
	if got := SeenKey("sess\tid\nwith space"); got != "sess_id_with_space" {
		t.Fatalf("SeenKey = %q, want sess_id_with_space", got)
	}
	if !IsSeenField("clean-token:123") {
		t.Fatal("IsSeenField rejected clean token")
	}
	if IsSeenField("") || IsSeenField("bad token") || IsSeenField("bad\nline") {
		t.Fatal("IsSeenField accepted unsafe or empty field")
	}
	for _, k := range []string{"start:s1", "once:s1", "agy:s1", "agy-compact:s1"} {
		if !IsPrefixedKey(k) {
			t.Errorf("IsPrefixedKey(%q) = false, want true", k)
		}
	}
	if IsPrefixedKey("plain-session-1") || IsPrefixedKey("task:parent:1") {
		t.Fatal("IsPrefixedKey matched non-reserved key")
	}
	if ShortHash("hello") != ShortHash("hello") || ShortHash("hello") == ShortHash("world") {
		t.Fatal("ShortHash must be deterministic and distinct")
	}
}

func TestSeenLinesQueries(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	lines := SeenLines{
		"",
		"s1 first " + now.Add(-2*time.Hour).Format(time.RFC3339) + " projA",
		"s1 ask:old " + now.Add(-90*time.Minute).Format(time.RFC3339),
		"s2 other " + now.Add(-10*time.Minute).Format(time.RFC3339) + " projA",
		"s1 second " + now.Add(-5*time.Minute).Format(time.RFC3339) + " projB",
		"s1 ask:fresh " + now.Add(-15*time.Minute).Format(time.RFC3339),
		"s1 third",
	}

	inj := lines.Injected("s1")
	for _, want := range []string{"first", "ask:old", "second", "ask:fresh", "third"} {
		if !inj[want] {
			t.Errorf("Injected missing %q", want)
		}
	}
	if inj["other"] {
		t.Error("Injected leaked other session token")
	}

	rec := lines.Recent("s1", 2)
	if !rec["third"] || !rec["ask:fresh"] || rec["first"] {
		t.Fatalf("Recent(s1, 2) = %v, want {third, ask:fresh}", rec)
	}

	projA := lines.RecentInProject("projA", 2)
	if !projA["other"] || !projA["first"] || projA["second"] {
		t.Fatalf("RecentInProject(projA, 2) = %v, want {first, other}", projA)
	}

	if !lines.AskedWithin("s1", "ask:fresh", now, time.Hour) {
		t.Error("AskedWithin should be true within window")
	}
	if lines.AskedWithin("s1", "ask:old", now, time.Hour) {
		t.Error("AskedWithin should be false past window")
	}
	if lines.AskedWithin("s1", "third", now, time.Hour) {
		t.Error("AskedWithin should ignore line without timestamp")
	}
}

func TestLedgerClaimOnceAndForgetPreservingCompactionMarks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	l := NewLedger(dir)

	if !l.ClaimOnce("once:s1", "tok1") {
		t.Fatal("first ClaimOnce returned false")
	}
	if l.ClaimOnce("once:s1", "tok1") {
		t.Fatal("duplicate ClaimOnce returned true")
	}
	if !l.ClaimOnce("once:s1", "tok2") {
		t.Fatal("ClaimOnce for second token returned false")
	}

	tx, release, _ := l.Lock()
	tx.RecordForProject("parent", "proj", "hit-1", "compaction:pkt-1")
	tx.RecordIDs(SessionStartKeyPrefix+"parent", "start-hit")
	tx.RecordIDs(OnceDigestKey("compaction:parent"), "unavailable")
	tx.RecordIDs("start:once:parent", "nested-should-stay-untouched")
	release()

	l.ForgetSessions("parent", OnceDigestKey("compaction:parent"))

	gotParent := l.Read().Injected("parent")
	if !gotParent["compaction:pkt-1"] {
		t.Fatalf("compaction delivery mark was dropped: %v", gotParent)
	}
	if gotParent["hit-1"] {
		t.Fatalf("session hit survived ForgetSessions: %v", gotParent)
	}
	if len(l.Read().Injected(SessionStartKeyPrefix+"parent")) != 0 {
		t.Fatal("session-start row survived ForgetSessions")
	}
	if len(l.Read().Injected(OnceDigestKey("compaction:parent"))) != 0 {
		t.Fatal("compaction failure row survived ForgetSessions")
	}

	// Passing an already-prefixed key must not double-prefix and remove unrelated keys.
	l.ForgetSessions("once:parent")
	tx, release, _ = l.Lock()
	tx.RecordIDs(SessionStartKeyPrefix+"once:parent", "keep-me")
	release()
	l.ForgetSessions("once:parent")
	if !l.Read().Injected(SessionStartKeyPrefix + "once:parent")["keep-me"] {
		t.Fatal("Forgetting prefixed key dropped double-prefixed key")
	}
}

func TestLedgerForgetCheckpointAtomicityAndIdempotence(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	l := NewLedger(dir)

	tx, release, _ := l.Lock()
	tx.RecordIDs("conv-1", "q1", "compaction:keep")
	tx.RecordIDs(AgyDigestKey("conv-1"), "agy-digest", "q:abc")
	tx.RecordIDs(SessionStartKeyPrefix+"conv-1", "s-hit")
	release()

	if l.ForgetCheckpoint("conv-1", "") || l.ForgetCheckpoint("", "m1") {
		t.Fatal("empty conversation or marker must return false")
	}
	if !l.ForgetCheckpoint("conv-1", "2026-11-20T12:00:00Z") {
		t.Fatal("first ForgetCheckpoint returned false")
	}
	if l.ForgetCheckpoint("conv-1", "2026-11-20T12:00:00Z") {
		t.Fatal("duplicate ForgetCheckpoint returned true")
	}

	gotConv := l.Read().Injected("conv-1")
	if gotConv["q1"] || !gotConv["compaction:keep"] {
		t.Fatalf("unexpected conv-1 entries after checkpoint: %v", gotConv)
	}
	if len(l.Read().Injected(AgyDigestKey("conv-1"))) != 0 {
		t.Fatal("agy digest entries survived ForgetCheckpoint")
	}
	if len(l.Read().Injected(SessionStartKeyPrefix+"conv-1")) != 0 {
		t.Fatal("start entries survived ForgetCheckpoint")
	}

	// Next checkpoint marker in same conversation is acted on independently.
	tx, release, _ = l.Lock()
	tx.RecordIDs("conv-1", "q2")
	release()
	if !l.ForgetCheckpoint("conv-1", "2026-11-20T13:00:00Z") {
		t.Fatal("second distinct checkpoint marker returned false")
	}
	if l.Read().Injected("conv-1")["q2"] {
		t.Fatal("q2 survived second ForgetCheckpoint")
	}
}

func TestLedgerMidLineRepairAndRotation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	p := dir + ".hookseen"
	var seed strings.Builder
	seed.WriteString("old-sess compaction:keep-me 2026-10-01T00:00:00Z\n")
	seed.WriteString("active-sess early-mine-token\n")
	for i := range ledgerTailLines + 150 {
		fmt.Fprintf(&seed, "noise-%04d item-%04d\n", i, i)
	}
	seed.WriteString("truncated-line-without-newline")
	if err := os.WriteFile(p, []byte(seed.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	l := NewLedger(dir)
	l.rotateBytes = 2048 // trigger rotation on next append
	l.RecordIDs("active-sess", "fresh-token")

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "truncated-line-without-newlineactive-sess") {
		t.Fatalf("append glued onto unterminated tail line")
	}
	if !l.Read().Injected("old-sess")["compaction:keep-me"] {
		t.Fatal("rotation dropped early compaction mark")
	}
	activeGot := l.Read().Injected("active-sess")
	if !activeGot["fresh-token"] || !activeGot["early-mine-token"] {
		t.Fatalf("active session tokens after rotation = %v", activeGot)
	}
	if got := l.Read().Recent("active-sess", 0); len(got) != 0 {
		t.Fatalf("Recent with zero window = %v, want empty", got)
	}
	if got := l.Read().RecentInProject("", 5); len(got) != 0 {
		t.Fatalf("RecentInProject with empty project = %v, want empty", got)
	}
}

func TestLedgerEmptyEdgeCasesAndLockContention(t *testing.T) {
	if BlockFingerprint("a   b\tc") != BlockFingerprint("a b c") {
		t.Fatal("BlockFingerprint should normalize whitespace")
	}
	if AskKey([]string{"beta", "alpha"}) != AskKey([]string{"alpha", "beta"}) {
		t.Fatal("AskKey should be order-independent")
	}
	if got := OnceDigestKey("s1"); got != "once:s1" {
		t.Fatalf("OnceDigestKey = %q, want once:s1", got)
	}

	var nilSeen *SeenTx
	if nilSeen.Load() != nil || nilSeen.ClaimOnce("k", "v") || nilSeen.ForgetCheckpoint("c", "m") {
		t.Fatal("nil SeenTx should fail closed")
	}

	empty := NewLedger("  ")
	if empty.Read() != nil || empty.ClaimOnce("k", "v") || empty.ForgetCheckpoint("c", "m") {
		t.Fatal("empty Ledger should behave as safe no-op")
	}
	empty.RecordIDs("s1", "t1")
	empty.RecordForProject("s1", "p", "t1")
	empty.ForgetSessions("s1")

	dir := filepath.Join(t.TempDir(), "index.db")
	l := NewLedger(dir)
	if l.ClaimOnce("k", "bad token") {
		t.Fatal("ClaimOnce accepted invalid token with space")
	}
	// Missing .hookseen file on ForgetCheckpoint must create it cleanly.
	if !l.ForgetCheckpoint("conv-new", "cp-1") {
		t.Fatal("ForgetCheckpoint on missing file returned false")
	}

	l.RecordForProject("s1", "", AskKey([]string{"go", "test"}))
	if !l.Read().Asked("s1", AskKey([]string{"test", "go"}), time.Hour) {
		t.Fatal("SeenLines.Asked failed immediately after RecordForProject")
	}

	// Verify cross-handle advisory lock contention sets contended=true.
	advRelease, ok, busy := atomicfile.TakeAdvisoryLock(dir+".hookseen.lock", false)
	if !ok || busy {
		t.Fatalf("setup TakeAdvisoryLock failed: ok=%v busy=%v", ok, busy)
	}
	done := make(chan bool, 1)
	go func() {
		_, rel, contended := l.Lock()
		rel()
		done <- contended
	}()
	time.Sleep(30 * time.Millisecond)
	advRelease()
	if !<-done {
		t.Fatal("Ledger.Lock reported contended=false despite external advisory lock")
	}
}

func TestLedgerConcurrentWritersAndCheckpoints(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	l := NewLedger(dir)
	l.rotateBytes = 4096

	// Seed a durable compaction delivery mark before contention.
	l.RecordIDs("durable", "compaction:keep-under-load")

	var wg sync.WaitGroup
	for g := range 16 {
		wg.Go(func() {
			conv := fmt.Sprintf("conv-%d", g%4)
			for i := range 25 {
				l.RecordIDs(conv, fmt.Sprintf("tok-%d-%d", g, i))
				if i%7 == 0 {
					l.ForgetCheckpoint(conv, fmt.Sprintf("cp-%d-%d", g, i))
				}
				_ = l.Read().Injected(conv)
			}
		})
	}
	wg.Wait()

	raw, err := os.ReadFile(dir + ".hookseen")
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		t.Fatalf("ledger file empty or missing trailing newline")
	}
	if !l.Read().Injected("durable")["compaction:keep-under-load"] {
		t.Fatal("durable compaction mark lost under concurrent load and rotation")
	}
}

func TestCoalesceRecallMessages(t *testing.T) {
	if got := CoalesceRecallMessages([]string{"", "  \n\t "}); got != "" {
		t.Fatalf("whitespace-only messages coalesced to %q", got)
	}

	plain := "unframed standalone reminder"
	if got := CoalesceRecallMessages([]string{"", plain}); got != plain {
		t.Fatalf("single unframed message changed: got %q, want %q", got, plain)
	}

	single := SealRecall("already framed body")
	if got := CoalesceRecallMessages([]string{single}); got != single {
		t.Fatalf("single framed message re-wrapped:\ngot:  %q\nwant: %q", got, single)
	}

	m1 := SealRecall("fact one")
	m2 := SealRecall("fact one") // duplicate should collapse
	m3 := SealRecall(evidence.LabelUserRule + " never mock sqlite")
	m4 := "plain supplement"
	got := CoalesceRecallMessages([]string{m1, m2, m3, m4})
	if count := strings.Count(got, "<deja-recall>"); count != 1 {
		t.Fatalf("expected exactly 1 <deja-recall> opening tag, got %d:\n%s", count, got)
	}
	if !strings.HasPrefix(got, RuleFrameHeader) {
		t.Fatalf("expected RuleFrameHeader when merged message carries user rule:\n%s", got)
	}
	if strings.Count(got, "fact one") != 1 {
		t.Fatalf("duplicate part was not deduplicated:\n%s", got)
	}
	if !strings.Contains(got, "plain supplement") {
		t.Fatalf("missing plain supplement:\n%s", got)
	}
}
