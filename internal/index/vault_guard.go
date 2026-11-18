package index

import "github.com/vshulcz/deja-vu/internal/vault"

func ensureVaultUnlocked(dir string) error {
	_, err := vault.CheckUnlocked(dir)
	return err
}
