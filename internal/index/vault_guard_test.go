package index

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/vault"
)

func TestVaultGuardFailsClosed(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_VAULT_FILE", filepath.Join(tmp, "vault.json"))
	t.Setenv("DEJA_VAULT_BACKEND", "passphrase")
	t.Setenv("DEJA_VAULT_PASSPHRASE", "index-vault-pass")
	oldIter := vault.KDFIterations
	vault.KDFIterations = 1000
	t.Cleanup(func() { vault.KDFIterations = oldIter })

	dir := filepath.Join(tmp, "index.db")
	if _, err := vault.Init(dir, "index-vault-pass", 0); err != nil {
		t.Fatalf("vault.Init: %v", err)
	}
	t.Setenv("DEJA_VAULT_PASSPHRASE", "")
	if err := vault.Lock(dir, "index-vault-pass"); err != nil {
		t.Fatalf("vault.Lock: %v", err)
	}

	if err := EnsureChecked(&FreshCheck{dir: dir}, false, nil); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("EnsureChecked locked = %v, want ErrLocked", err)
	}
	if err := Ensure(dir, "", false, nil); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("Ensure locked = %v, want ErrLocked", err)
	}
	if _, err := EnsureUnlessBusy(dir, nil); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("EnsureUnlessBusy locked = %v, want ErrLocked", err)
	}
	if err := EnsureForSearch(dir, query.Options{}, false, nil); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("EnsureForSearch locked = %v, want ErrLocked", err)
	}
	if _, err := EnsureForSearchNoWait(dir, query.Options{}, nil); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("EnsureForSearchNoWait locked = %v, want ErrLocked", err)
	}
	if _, _, err := openRecordLog(dir); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("openRecordLog locked = %v, want ErrLocked", err)
	}
	if _, err := readManifest(dir); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("readManifest locked = %v, want ErrLocked", err)
	}
}
