//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func replaceExecutable(staged, destination string) error {
	removeStaleUpdateBackups(destination)
	// A long-running process can keep its old image locked after an update.
	// Use this update's random staging suffix so that lock cannot block the next one.
	backup := filepath.Join(filepath.Dir(destination), "."+filepath.Base(destination)+".old-"+filepath.Base(staged))
	if err := os.Rename(destination, backup); err != nil {
		return err
	}
	if err := os.Rename(staged, destination); err != nil {
		if rollbackErr := os.Rename(backup, destination); rollbackErr != nil {
			return fmt.Errorf("install update: %v (rollback failed: %v)", err, rollbackErr)
		}
		return err
	}
	// Windows may keep the running old executable locked until this process exits.
	_ = os.Remove(backup)
	return nil
}

func removeStaleUpdateBackups(destination string) {
	pattern := filepath.Join(filepath.Dir(destination), "."+filepath.Base(destination)+".old-.deja-update-*")
	backups, _ := filepath.Glob(pattern)
	for _, backup := range backups {
		// A still-running old process keeps its image locked; a later update retries.
		_ = os.Remove(backup)
	}
}
