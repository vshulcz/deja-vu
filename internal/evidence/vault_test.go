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
	pass := "evidence-vault-pass"
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

	// Malformed and truncated receipts.bin edge cases in load/Open/Update.
	badDir := t.TempDir()
	binPath := filepath.Join(badDir, fileName)
	for _, payload := range [][]byte{
		[]byte("BAD!"),
		[]byte("XXXX12345678"),
		[]byte("EVD1\x01\x00\x00\x00"),
		append([]byte("EVD1\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x64\x00\x00\x00"), make([]byte, 16)...),
	} {
		resetEvidenceReaders(t)
		if wErr := os.WriteFile(binPath, payload, 0o600); wErr != nil {
			t.Fatal(wErr)
		}
		if _, ok := load(badDir); ok {
			t.Fatalf("load unexpectedly accepted malformed payload %x", payload)
		}
	}
	// Sealed receipts with corrupted ciphertext fail closed even while unlocked.
	if wErr := os.WriteFile(filepath.Join(dir, fileName), []byte("DJVF1corrupt-payload-bytes"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	resetEvidenceReaders(t)
	if _, ok := load(dir); ok {
		t.Fatal("load accepted corrupt DJVF1 payload while unlocked")
	}

	// Cached Reader.Lookup fails closed once vault is locked.
	t.Setenv("DEJA_VAULT_PASSPHRASE", "")
	if lErr := vault.Lock(dir, pass); lErr != nil {
		t.Fatalf("vault.Lock before Lookup: %v", lErr)
	}
	if got := rdUnlocked.Lookup("claude", "ev-2", ans2); got != nil {
		t.Fatalf("cached Reader.Lookup leaked while locked: %v", got)
	}
	if wErr := write(dir, map[uint64]string{1: "line"}); !errors.Is(wErr, vault.ErrLocked) {
		t.Fatalf("write while locked = %v, want ErrLocked", wErr)
	}
}

func TestEvidenceVaultEdgeCases(t *testing.T) {
	resetEvidenceReaders(t)
	root := t.TempDir()
	t.Setenv("DEJA_VAULT_FILE", filepath.Join(root, "disabled-vault.json"))
	vault.ClearCache()

	// Sealed receipts.bin must fail closed when vault is disabled.
	sealedDir := filepath.Join(root, "sealed.db")
	if err := os.MkdirAll(sealedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sealed, err := vault.SealBytes(bytes.Repeat([]byte{7}, 32), vault.DomainIndexFile, []byte("receipt-data"))
	if err != nil || !vault.IsSealedBytes(sealed) {
		t.Fatalf("SealBytes setup failed: err=%v sealed=%v", err, vault.IsSealedBytes(sealed))
	}
	if wErr := os.WriteFile(filepath.Join(sealedDir, fileName), sealed, 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if _, ok := load(sealedDir); ok {
		t.Fatal("load accepted sealed receipts.bin with disabled vault")
	}

	// Short / corrupt unsealed receipts.bin is rejected by load.
	if wErr := os.WriteFile(filepath.Join(sealedDir, fileName), []byte("BAD"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if _, ok := load(sealedDir); ok {
		t.Fatal("load accepted corrupt receipts.bin")
	}

	// write fails when target parent is a regular file.
	notDir := filepath.Join(root, "file-parent")
	if wErr := os.WriteFile(notDir, []byte("x"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if wErr := write(notDir, map[uint64]string{1: "line"}); wErr == nil {
		t.Fatal("write into file path unexpectedly succeeded")
	}

	// Plaintext Reader.Lookup handles missing and truncated on-disk receipts cleanly.
	plainDir := filepath.Join(root, "plain.db")
	if mkErr := os.MkdirAll(plainDir, 0o700); mkErr != nil {
		t.Fatal(mkErr)
	}
	ans := "Updated edge_tok in edge.go to 42"
	s := model.Session{
		Harness: "claude",
		ID:      "ev-edge",
		Updated: time.Now(),
		Messages: []model.Message{
			{Role: "user", Text: "verify edge_tok"},
			{Role: "edit", Text: "edge.go\nedge_tok = 42"},
			{Role: "assistant", Text: ans},
		},
	}
	Build(plainDir, []model.Session{s}, nil)
	rd := Open(plainDir)
	if rd == nil || len(rd.Lookup("claude", "ev-edge", ans)) == 0 {
		t.Fatalf("plaintext Open/Lookup setup failed: rd=%v", rd)
	}
	raw, rErr := os.ReadFile(filepath.Join(plainDir, fileName))
	if rErr != nil {
		t.Fatal(rErr)
	}
	recPath := filepath.Join(plainDir, fileName)
	hdrOnlyLen := len(magic) + 4 + entrySize
	if wErr := os.WriteFile(recPath, raw[:hdrOnlyLen], 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if got := rd.Lookup("claude", "ev-edge", ans); got != nil {
		t.Fatalf("Lookup with truncated value returned %v, want nil", got)
	}
	if wErr := os.WriteFile(recPath, raw[:len(magic)+6], 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if got := rd.Lookup("claude", "ev-edge", ans); got != nil {
		t.Fatalf("Lookup with truncated entry returned %v, want nil", got)
	}
	if rmErr := os.Remove(recPath); rmErr != nil {
		t.Fatal(rmErr)
	}
	if got := rd.Lookup("claude", "ev-edge", ans); got != nil {
		t.Fatalf("Lookup on missing receipts.bin returned %v, want nil", got)
	}
}
