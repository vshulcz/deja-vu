package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// writeBackWithParts writes the transcript with what its harness needs beside
// it: new files first, then the transcript, then lines added to the store's
// own files. Nothing is written when any new file is already there, and the
// new files go again when the transcript cannot be written.
func writeBackWithParts(s model.Session, f sources.WriteBackFile) error {
	sidecars, appends, err := sources.WriteBackExtras(s, f)
	if err != nil {
		return err
	}
	for _, p := range sidecars {
		if _, err := os.Lstat(p.Path); err == nil {
			return fmt.Errorf("%s already exists, and deja does not overwrite a session's files", p.Path)
		}
	}
	var made []string
	undo := func() {
		for i := len(made) - 1; i >= 0; i-- {
			_ = os.Remove(made[i])
		}
	}
	for _, p := range sidecars {
		if err := writeBackCreate(f.Root, p.Path, p.Data); err != nil {
			undo()
			return err
		}
		made = append(made, p.Path)
	}
	if err := writeBackCreate(f.Root, f.Path, f.Data); err != nil {
		undo()
		return err
	}
	for _, p := range appends {
		if err := writeBackAppend(f.Root, p.Path, p.Data); err != nil {
			return err
		}
	}
	return nil
}

// writeBackAppend adds data to the end of a store file, creating it when it is
// missing. A link out of root is refused.
func writeBackAppend(root, p string, data []byte) error {
	if fi, err := os.Lstat(p); err == nil && !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", p)
	}
	if !pathInside(root, deepestExisting(filepath.Dir(p))) {
		return fmt.Errorf("%s leads out of %s", p, root)
	}
	// A last line with no newline would run into the one added.
	if in, err := os.Open(p); err == nil {
		last := make([]byte, 1)
		if fi, err := in.Stat(); err == nil && fi.Size() > 0 {
			if _, err := in.ReadAt(last, fi.Size()-1); err == nil && last[0] != '\n' {
				data = append([]byte{'\n'}, data...)
			}
		}
		_ = in.Close()
	}
	out, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	_, werr := out.Write(data)
	if cerr := out.Close(); werr == nil {
		werr = cerr
	}
	return werr
}
