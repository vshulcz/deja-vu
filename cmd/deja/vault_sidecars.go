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
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			if os.IsNotExist(werr) {
				return nil
			}
			return werr
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 || !d.Type().IsRegular() || strings.HasPrefix(name, ".") {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if vault.IsSealedBytes(raw) {
			return nil
		}
		sealed, err := vault.SealBytes(dek, vault.DomainSidecar, raw)
		if err != nil {
			return err
		}
		return atomicfile.Write(p, sealed, 0o600)
	})
}
