package index

import "github.com/vshulcz/deja-vu/internal/vault"

func ensureVaultUnlocked(dir string) error {
	canon := canonicalVaultDir(dir)
	_, err := vault.CheckUnlocked(canon)
	if err != nil {
		invalidateSnapshot(canon)
	}
	return err
}
