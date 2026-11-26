package evidence

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/vault"
)

func resetEvidenceReaders(t *testing.T) {
	t.Helper()
	clearAll := func() {
		readersMu.Lock()
		clear(readers)
		readersMu.Unlock()
		vault.ClearCache()
	}
	clearAll()
	t.Cleanup(clearAll)
}

func TestCanonicalVaultDirAndSuffix(t *testing.T) {
	if got := canonicalVaultDir(""); got != "" {
		t.Fatalf("canonicalVaultDir(\"\") = %q", got)
	}
	wantBase := filepath.Join(t.TempDir(), "index.db")
	for _, suf := range []string{"", ".tmp", ".part", ".old", ".vaultmig", ".old.42", ".old.1.part"} {
		if got := canonicalVaultDir(wantBase + suf); got != wantBase {
			t.Fatalf("canonicalVaultDir(%q) = %q, want %q", wantBase+suf, got, wantBase)
		}
	}
	for _, neg := range []string{".old.", ".old.abc", ".old", "plain"} {
		if base, ok := trimParkedOldSuffix(wantBase + neg); ok {
			t.Fatalf("trimParkedOldSuffix(%q) = (%q, true), want false", wantBase+neg, base)
		}
	}
}

func TestPlainAndVaultLifecycle(t *testing.T) {
	resetEvidenceReaders(t)

	root := t.TempDir()
	dir := filepath.Join(root, "index.db")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	ans1 := "Fixed: retry_limit in payments_client was 0, set it to 5"
	s1 := model.Session{
		Harness: "claude",
		ID:      "ev-1",
		Updated: time.Now().Add(-2 * time.Minute),
		Messages: []model.Message{
			{Role: "user", Text: "payments_client gives up on first timeout"},
			{Role: "command", Text: "grep -n retry_limit payments_client.go"},
			{Role: "tool-output", Text: "12: retry_limit = 0 // payments_client"},
			{Role: "assistant", Text: ans1},
		},
	}
	ans2 := "Updated backoff_ms in retry_loop.go to 250"
	s2 := model.Session{
		Harness: "claude",
		ID:      "ev-2",
		Updated: time.Now().Add(-time.Minute),
		Messages: []model.Message{
			{Role: "user", Text: "fix backoff_ms in retry_loop.go"},
			{Role: "edit", Text: "retry_loop.go\nbackoff_ms = 250"},
			{Role: "assistant", Text: ans2},
		},
	}

	// Update on missing store returns false cleanly.
	if Update(dir, dir, []model.Session{s2}, nil) {
		t.Fatal("Update on empty dir unexpectedly succeeded")
	}

	// Plaintext Build, Update (exercises load), Open memo hit, Lookup, Drop.
	Build(dir, []model.Session{s1}, nil)
	if !Update(dir, dir, []model.Session{s2}, nil) {
		t.Fatal("plaintext Update failed")
	}
	rd := Open(dir)
	if rd == nil || !rd.Covers(s2.Updated) {
		t.Fatalf("plaintext Open/Covers failed: rd=%v", rd)
	}
	if Open(dir) != rd {
		t.Fatal("second Open did not reuse cached reader")
	}
	want2 := strings.Split(Receipts(s2, nil)[Key("claude", "ev-2", ans2)], "\n")
	if got := rd.Lookup("claude", "ev-2", ans2); !slices.Equal(got, want2) {
		t.Fatalf("plaintext Lookup = %v, want %v", got, want2)
	}
	Drop(dir)
	resetEvidenceReaders(t)
	if Open(dir) != nil {
		t.Fatal("Open after Drop should be nil")
	}

	// Sealed receipts fail closed when vault is not active.
	t.Setenv("DEJA_VAULT_FILE", filepath.Join(root, "vault.json"))
	t.Setenv("DEJA_VAULT_BACKEND", "passphrase")
	pass := "evidence-vault-pass" //nolint:gosec // G101: synthetic test passphrase
	t.Setenv("DEJA_VAULT_PASSPHRASE", pass)
	oldIter := vault.KDFIterations
	vault.KDFIterations = 1000
	t.Cleanup(func() { vault.KDFIterations = oldIter })

	if _, iErr := vault.Init(dir, pass, 0); iErr != nil {
		t.Fatalf("vault.Init: %v", iErr)
	}
	Build(dir, []model.Session{s1}, nil)
	raw, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatal(err)
	}
	if !vault.IsSealedBytes(raw) || bytes.Contains(raw, []byte("retry_limit")) {
		t.Fatalf("receipts.bin not sealed or leaked plaintext: %q", raw)
	}
	if !Update(dir, dir, []model.Session{s2}, nil) {
		t.Fatal("encrypted Update failed")
	}
	rdEnc := Open(dir)
	if rdEnc == nil {
		t.Fatal("Open while unlocked returned nil")
	}
	if got := rdEnc.Lookup("claude", "ev-2", ans2); !slices.Equal(got, want2) {
		t.Fatalf("encrypted Lookup = %v, want %v", got, want2)
	}

	// Locking must fail closed on Open, retained Reader.Lookup, and Update/load.
	t.Setenv("DEJA_VAULT_PASSPHRASE", "")
	if lErr := vault.Lock(dir, pass); lErr != nil {
		t.Fatalf("vault.Lock: %v", lErr)
	}
	if got := rdEnc.Lookup("claude", "ev-2", ans2); got != nil {
		t.Fatalf("retained Reader.Lookup returned data while locked: %v", got)
	}
	if Open(dir) != nil {
		t.Fatal("Open while locked returned non-nil reader")
	}
	if Update(dir, dir, []model.Session{s1}, nil) {
		t.Fatal("Update while locked unexpectedly succeeded")
	}

	// Sealed file on disk with disabled vault cannot be loaded either.
	t.Setenv("DEJA_VAULT_FILE", filepath.Join(root, "missing-vault.json"))
	vault.ClearCache()
	if Update(dir, dir, []model.Session{s1}, nil) {
		t.Fatal("load accepted sealed receipts.bin when vault disabled")
	}

	// Unlock restores full access.
	t.Setenv("DEJA_VAULT_FILE", filepath.Join(root, "vault.json"))
	if uErr := vault.Unlock(dir, pass); uErr != nil {
		t.Fatalf("vault.Unlock: %v", uErr)
	}
	t.Setenv("DEJA_VAULT_PASSPHRASE", pass)
	rdUnlocked := Open(dir)
	if rdUnlocked == nil {
		t.Fatal("Open after Unlock returned nil")
	}
	if got := rdUnlocked.Lookup("claude", "ev-2", ans2); !slices.Equal(got, want2) {
		t.Fatalf("Lookup after Unlock = %v, want %v", got, want2)
	}
	if _, err := vault.CheckUnlocked(dir); !errors.Is(err, nil) {
		t.Fatalf("CheckUnlocked unexpected error: %v", err)
	}
}
