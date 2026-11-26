package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
	"github.com/vshulcz/deja-vu/internal/vault"
)

func init() {
	vault.RegisterMigrator("cmd_sidecars", migrateCmdSidecars)
}

func migrateCmdSidecars(indexDir string, dek []byte) error {
	if indexDir == "" || len(dek) != 32 {
		return nil
	}
	root := deferredRoot(indexDir)
	fi, err := os.Lstat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	dirRoot, err := os.OpenRoot(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer func() { _ = dirRoot.Close() }()
	return fs.WalkDir(dirRoot.FS(), ".", func(rel string, d fs.DirEntry, werr error) error {
		if werr != nil {
			if os.IsNotExist(werr) {
				return nil
			}
			return werr
		}
		name := d.Name()
		if d.IsDir() {
			if rel != "." && strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 || !d.Type().IsRegular() || strings.HasPrefix(name, ".") {
			return nil
		}
		raw, rErr := dirRoot.ReadFile(rel)
		if rErr != nil {
			if os.IsNotExist(rErr) {
				return nil
			}
			return rErr
		}
		if vault.IsSealedBytes(raw) {
			return nil
		}
		sealed, sErr := vault.SealBytes(dek, vault.DomainSidecar, raw)
		if sErr != nil {
			return sErr
		}
		return atomicfile.Write(filepath.Join(root, filepath.FromSlash(rel)), sealed, 0o600)
	})
}
