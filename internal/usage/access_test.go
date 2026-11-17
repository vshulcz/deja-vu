package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/vault"
)

func withAccessVaultEnv(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv(vault.EnvVaultFile, filepath.Join(tmp, "vault.json"))
	t.Setenv(vault.EnvVaultPassphrase, "test-passphrase-123")
	t.Setenv(vault.EnvVaultKey, "")
	oldIter := vault.KDFIterations
	vault.KDFIterations = 1000
	t.Cleanup(func() { vault.KDFIterations = oldIter })
	return filepath.Join(tmp, "index.db")
}

func TestRecordAccessContentFreeAndExcludedFromCounters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	RecordAccess(dir, "mcp", "claude-code", "read", "billing", "deny", "project outside client scope")

	events := Events(dir, 10)
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	ev := events[0]
	if ev.Kind != KindAccess || ev.Surface != "mcp" || ev.Client != "claude-code" ||
		ev.Action != "read" || ev.ProjectScope != "billing" || ev.Decision != "deny" ||
		ev.DenyReason != "project outside client scope" || ev.Bytes != 0 {
		t.Fatalf("unexpected access event: %+v", ev)
	}
	if recalls, _, injected := TodayDemand(dir); recalls != 0 || injected != 0 {
		t.Fatalf("access event leaked into demand counters: recalls=%d injected=%d", recalls, injected)
	}
}

func TestSnapshotsEncryptWhenVaultEnabled(t *testing.T) {
	dir := withAccessVaultEnv(t)

	// Unencrypted snapshots pass through unsealSnapshots unchanged.
	RecordDigestFrom(dir, KindHook, "plain-before-init", "sess-0", 1, 50, []string{"t0"}, nil, nil)
	if before := Snapshots(dir, 5); len(before) != 1 || before[0].Digest != "plain-before-init" {
		t.Fatalf("plaintext Snapshots = %+v", before)
	}

	if _, err := vault.Init(dir, "test-passphrase-123", time.Hour); err != nil {
		t.Fatal(err)
	}
	canary := "TOP_SECRET_DIGEST_SNIPPET"
	RecordDigestFrom(dir, KindHook, canary, "sess-1", 1, 100, []string{"secret-term"}, []string{"billing"}, []string{"id1"})

	raw, err := os.ReadFile(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), canary) || strings.Contains(string(raw), "secret-term") {
		t.Fatalf("plaintext canary/terms leaked in .injections.jsonl: %s", raw)
	}
	snaps := Snapshots(dir, 5)
	if len(snaps) < 2 || snaps[0].Digest != canary {
		t.Fatalf("decrypted Snapshots = %+v", snaps)
	}

	// Lock vault: Snapshots must fail closed to placeholder without crashing.
	t.Setenv(vault.EnvVaultPassphrase, "")
	if err := vault.Lock(dir, "test-passphrase-123"); err != nil {
		t.Fatal(err)
	}
	// Put an encrypted snapshot line back to test locked Snapshots fallback.
	if err := os.WriteFile(SnapshotPath(dir), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	RecordDigest(dir, KindHook, "should-not-write", 1, 10) // fail-closed write skip
	lockedSnaps := Snapshots(dir, 5)
	if len(lockedSnaps) < 1 || strings.Contains(lockedSnaps[0].Digest, canary) {
		t.Fatalf("locked Snapshots = %+v", lockedSnaps)
	}

	// Unlock before testing mixed plaintext + corrupt enc:v1 snapshot lines so OpenTree does not overwrite the fixture.
	t.Setenv(vault.EnvVaultPassphrase, "test-passphrase-123")
	if err := vault.Unlock(dir, "test-passphrase-123"); err != nil {
		t.Fatal(err)
	}
	mixed := `{"t":"2026-11-17T00:00:00Z","kind":"hook","bytes":5,"digest":"plain"}` + "\n" +
		`{"t":"2026-11-17T00:00:01Z","kind":"hook","bytes":5,"digest":"` + vault.NoteLinePrefix + `not-base64!"}` + "\n"
	if err := os.WriteFile(SnapshotPath(dir), []byte(mixed), 0o600); err != nil {
		t.Fatal(err)
	}
	decMixed := Snapshots(dir, 5)
	if len(decMixed) != 2 {
		t.Fatalf("mixed Snapshots count = %d, want 2", len(decMixed))
	}
	if decMixed[0].Digest != "[encrypted — run deja unlock]" || decMixed[1].Digest != "plain" {
		t.Fatalf("mixed Snapshots = %+v", decMixed)
	}

	// Cover Summary.MarshalJSON with zero and non-zero Since.
	bZero, err := json.Marshal(Summary{})
	if err != nil || strings.Contains(string(bZero), `"since"`) {
		t.Fatalf("zero Summary JSON = %s (%v)", bZero, err)
	}
	bSince, err := json.Marshal(Summary{Since: time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)})
	if err != nil || !strings.Contains(string(bSince), `"since"`) {
		t.Fatalf("since Summary JSON = %s (%v)", bSince, err)
	}
}
