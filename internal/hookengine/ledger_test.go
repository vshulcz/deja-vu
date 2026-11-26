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
	"github.com/vshulcz/deja-vu/internal/vault"
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
	h1 := ShortHash("hello")
	h2 := ShortHash("hello")
	if h1 == "" || h1 != h2 || h1 == ShortHash("world") {
		t.Fatal("ShortHash must be deterministic, non-empty, and distinct")
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

func TestLedgerEncryptsLinesWhenVaultEnabledAndFailClosedWhenLocked(t *testing.T) {
	vault.ClearCache()
	t.Cleanup(vault.ClearCache)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))
	t.Setenv("DEJA_VAULT_FILE", filepath.Join(tmp, "vault.json"))
	t.Setenv("DEJA_VAULT_BACKEND", "passphrase")
	t.Setenv("DEJA_VAULT_PASSPHRASE", "ledger-vault-pass")
	oldIter := vault.KDFIterations
	vault.KDFIterations = 1000
	t.Cleanup(func() { vault.KDFIterations = oldIter })

	dir := filepath.Join(tmp, "index.db")
	if _, err := vault.Init(dir, "ledger-vault-pass", 0); err != nil {
		t.Fatalf("vault.Init: %v", err)
	}

	l := NewLedger(dir)
	l.rotateBytes = 256
	l.RecordIDs("conv-canary", "CANARY_LEDGER_TOK_7731")
	l.RecordIDs("conv-canary", "compaction:CANARY_KEEP_881")
	l.RecordIDs("conv-other", "noise-1", "noise-2", "noise-3", "noise-4")

	raw, err := os.ReadFile(dir + ".hookseen")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), vault.NoteLinePrefix) {
		t.Fatalf(".hookseen missing %q prefix: %q", vault.NoteLinePrefix, raw)
	}
	for _, secret := range []string{"conv-canary", "CANARY_LEDGER_TOK_7731", "CANARY_KEEP_881", "conv-other"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("plaintext secret %q leaked into .hookseen: %q", secret, raw)
		}
	}

	seen := l.Read().Injected("conv-canary")
	if !seen["CANARY_LEDGER_TOK_7731"] || !seen["compaction:CANARY_KEEP_881"] {
		t.Fatalf("decryption of encrypted .hookseen lost entries: %v", seen)
	}
	if !l.ForgetCheckpoint("conv-canary", "cp-vault-1") {
		t.Fatal("ForgetCheckpoint failed on encrypted .hookseen")
	}
	rawAfterCP, _ := os.ReadFile(dir + ".hookseen")
	if strings.Contains(string(rawAfterCP), "cp-vault-1") || strings.Contains(string(rawAfterCP), "conv-canary") {
		t.Fatalf("plaintext leaked after ForgetCheckpoint rewrite: %q", rawAfterCP)
	}
	if l.Read().Injected("conv-canary")["CANARY_LEDGER_TOK_7731"] {
		t.Fatal("checkpoint failed to forget encrypted entry")
	}
	if !l.Read().Injected("conv-canary")["compaction:CANARY_KEEP_881"] {
		t.Fatal("compaction mark dropped on encrypted .hookseen")
	}

	// Lock vault: Read returns nil/empty, writes fail closed without plaintext leak.
	t.Setenv("DEJA_VAULT_PASSPHRASE", "")
	if err := vault.Lock(dir, "ledger-vault-pass"); err != nil {
		t.Fatalf("vault.Lock: %v", err)
	}
	if gotLocked := l.Read().Injected("conv-canary"); len(gotLocked) != 0 {
		t.Fatalf("Read when locked returned entries: %v", gotLocked)
	}
	l.RecordIDs("conv-locked", "SECRET_WHEN_LOCKED")
	if rawLocked, err := os.ReadFile(dir + ".hookseen"); err == nil && strings.Contains(string(rawLocked), "SECRET_WHEN_LOCKED") {
		t.Fatalf("wrote plaintext entry while vault locked")
	}
}

func TestMigrateLedgerSidecarAndDecodeEdges(t *testing.T) {
	dek := make([]byte, 32)
	for i := range dek {
		dek[i] = byte(i + 1)
	}
	sealedValid, err := vault.SealLine(dek, vault.DomainSidecar, []byte("conv1 tok1"))
	if err != nil {
		t.Fatalf("SealLine: %v", err)
	}

	if mErr := migrateLedgerSidecar("", dek); mErr != nil {
		t.Fatalf("migrateLedgerSidecar empty dir = %v", mErr)
	}
	if mErr := migrateLedgerSidecar(t.TempDir(), nil); mErr != nil {
		t.Fatalf("migrateLedgerSidecar nil dek = %v", mErr)
	}

	dir := filepath.Join(t.TempDir(), "index.db")
	if mErr := migrateLedgerSidecar(dir, dek); mErr != nil {
		t.Fatalf("migrateLedgerSidecar missing file = %v", mErr)
	}

	mixed := "\nplain-session tok0\n" + sealedValid + "\n" + vault.NoteLinePrefix + "corrupt!!!\n"
	if wErr := os.WriteFile(dir+".hookseen", []byte(mixed), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if mErr := migrateLedgerSidecar(dir, dek); mErr != nil {
		t.Fatalf("migrateLedgerSidecar mixed = %v", mErr)
	}
	raw, rErr := os.ReadFile(dir + ".hookseen")
	if rErr != nil {
		t.Fatal(rErr)
	}
	if strings.Contains(string(raw), "plain-session") || !strings.Contains(string(raw), sealedValid) {
		t.Fatalf("unexpected migrated .hookseen content: %q", raw)
	}
	if mErr := migrateLedgerSidecar(dir, dek); mErr != nil {
		t.Fatalf("migrateLedgerSidecar idempotent = %v", mErr)
	}
	rawAgain, rErr := os.ReadFile(dir + ".hookseen")
	if rErr != nil || string(rawAgain) != string(raw) {
		t.Fatalf("second migrateLedgerSidecar mutated file: err=%v", rErr)
	}

	if entries, ok := decodeLedgerEntries(nil, []byte(sealedValid+"\n")); ok || entries != nil {
		t.Fatalf("decodeLedgerEntries(nil) accepted sealed prefix: (%v, %v)", entries, ok)
	}
	plainEntries, ok := decodeLedgerEntries(nil, []byte("\nplain1\nplain2\n"))
	if !ok || len(plainEntries) != 2 {
		t.Fatalf("decodeLedgerEntries(nil) plain = (%v, %v)", plainEntries, ok)
	}
	dec, ok := decodeLedgerEntries(dek, []byte("\n"+sealedValid+"\n"+vault.NoteLinePrefix+"corrupt\nplain-legacy\n"))
	if !ok || len(dec) != 2 || dec[0].plain != "conv1 tok1" || dec[1].plain != "plain-legacy" {
		t.Fatalf("decodeLedgerEntries(dek) = (%+v, %v)", dec, ok)
	}
	plainEnc, eErr := encodeLedgerLine(nil, "abc")
	if eErr != nil || plainEnc != "abc" {
		t.Fatalf("encodeLedgerLine(nil) = (%q, %v)", plainEnc, eErr)
	}

	// RotateFile encrypts plain lines on active vault and fails closed when locked.
	rotDir := filepath.Join(t.TempDir(), "index.db")
	if mkErr := os.MkdirAll(rotDir, 0o700); mkErr != nil {
		t.Fatal(mkErr)
	}
	cfgDir := t.TempDir()
	t.Setenv("DEJA_VAULT_FILE", filepath.Join(cfgDir, "vault.json"))
	t.Setenv("DEJA_VAULT_BACKEND", "passphrase")
	t.Setenv("DEJA_VAULT_PASSPHRASE", "rot-vault-pass")
	oldIter := vault.KDFIterations
	vault.KDFIterations = 1000
	t.Cleanup(func() { vault.KDFIterations = oldIter })
	vault.ClearCache()
	if _, iErr := vault.Init(rotDir, "rot-vault-pass", 0); iErr != nil {
		t.Fatalf("vault.Init: %v", iErr)
	}
	rotPath := rotDir + ".hookseen"
	var rotLines strings.Builder
	rotLines.WriteString("sid1 compaction:mark1\n")
	rotLines.WriteString("sid1 tok_plain\n")
	for i := range ledgerTailLines + 5 {
		fmt.Fprintf(&rotLines, "other tok_%d\n", i)
	}
	if wErr := os.WriteFile(rotPath, []byte(rotLines.String()), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	RotateFile(rotPath, "sid1")
	rotOut, rErr2 := os.ReadFile(rotPath)
	if rErr2 != nil || strings.Contains(string(rotOut), "tok_plain") || !strings.Contains(string(rotOut), vault.NoteLinePrefix) {
		t.Fatalf("RotateFile unlocked failed to seal plain lines: %q (%v)", rotOut, rErr2)
	}

	t.Setenv("DEJA_VAULT_PASSPHRASE", "")
	if lErr := vault.Lock(rotDir, "rot-vault-pass"); lErr != nil {
		t.Fatalf("vault.Lock: %v", lErr)
	}
	RotateFile(rotPath, "sid1")
	tx := &SeenTx{ledger: NewLedger(rotPath)}
	tx.rewriteOmitting([]string{"sid1"}, []string{"sid1 append_locked"})
}

func TestLedgerCorruptAndMigrationErrorBranches(t *testing.T) {
	vault.ClearCache()
	t.Cleanup(vault.ClearCache)
	oldIter := vault.KDFIterations
	vault.KDFIterations = 1000
	t.Cleanup(func() { vault.KDFIterations = oldIter })

	tmp := t.TempDir()
	uDir := filepath.Join(tmp, "index.db")
	if mkErr := os.MkdirAll(uDir, 0o700); mkErr != nil {
		t.Fatal(mkErr)
	}
	t.Setenv("DEJA_VAULT_FILE", filepath.Join(tmp, "vault.json"))
	t.Setenv("DEJA_VAULT_BACKEND", "passphrase")
	t.Setenv("DEJA_VAULT_PASSPHRASE", "ledger-corrupt-pass")
	if _, iErr := vault.Init(uDir, "ledger-corrupt-pass", 0); iErr != nil {
		t.Fatal(iErr)
	}

	uPath := uDir + ".hookseen"
	corruptBody := vault.NoteLinePrefix + "corrupt\n"
	if wErr := os.WriteFile(uPath, []byte(corruptBody), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if seen := NewLedger(uPath).Read(); seen != nil {
		t.Fatalf("Read corrupt = %v, want nil", seen)
	}
	NewLedger(uPath).ForgetSessions("s1")
	got, rErr := os.ReadFile(uPath)
	if rErr != nil || string(got) != corruptBody {
		t.Fatalf("ForgetSessions mutated corrupt ledger: %q (%v)", got, rErr)
	}
	RotateFile(uPath, "s1")

	dDir := filepath.Join(tmp, "dir-err.db")
	if mkErr := os.MkdirAll(dDir+".hookseen", 0o700); mkErr != nil {
		t.Fatal(mkErr)
	}
	if mErr := migrateLedgerSidecar(dDir, make([]byte, 32)); mErr == nil {
		t.Fatal("migrateLedgerSidecar on directory succeeded")
	}
}
