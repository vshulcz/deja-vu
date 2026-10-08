package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// writeBackSession puts a session the agent deleted back in the agent's store,
// rebuilt from the index, so `deja resume` can reopen it (#4617). It writes
// only the file the session was read from, only when nothing is there, and
// only inside the agent's own store directory.
func writeBackSession(dir string, s model.Session, stderr io.Writer) error {
	short := digest.Short(s.ID)
	if !transcriptGone(s) {
		fmt.Fprintf(stderr, "deja: session %s is still in %s's store, so nothing was written\n", short, s.Harness)
		return nil
	}
	f, err := sources.RenderWriteBack(s)
	if err != nil {
		var r *sources.WriteBackRefusal
		if errors.As(err, &r) {
			return fmt.Errorf("cannot write session %s back for %s: %s — `deja show %s` has the conversation", short, s.Harness, r.Reason, short)
		}
		return err
	}
	if fi, err := os.Stat(f.Root); err != nil || !fi.IsDir() {
		return fmt.Errorf("cannot write session %s back: %s's store %s is not on this machine", short, s.Harness, f.Root)
	}
	if err := writeBackWithParts(s, f); err != nil {
		var r *sources.WriteBackRefusal
		if errors.As(err, &r) {
			return fmt.Errorf("cannot write session %s back for %s: %s — `deja show %s` has the conversation", short, s.Harness, r.Reason, short)
		}
		return fmt.Errorf("cannot write session %s back: %w", short, err)
	}
	if _, err := index.AdoptWriteBack(dir, s.Path, f.Path); err != nil {
		// The file is written and the agent can open it; the next index pass
		// reads it like any changed transcript.
		fmt.Fprintf(stderr, "deja: %v\n", err)
	}
	fmt.Fprintf(stderr, "deja: wrote %s back to %s — the conversation only; tool calls and their output stay in deja\n",
		short, f.Path)
	return nil
}

// writeBackCreate writes data to a new file at p. It never replaces a file,
// and refuses a path whose existing directories lead out of root through a
// link.
func writeBackCreate(root, p string, data []byte) error {
	if _, err := os.Lstat(p); err == nil {
		return fmt.Errorf("%s already exists, and deja does not overwrite a transcript", p)
	}
	parent := filepath.Dir(p)
	if !pathInside(root, deepestExisting(parent)) {
		return fmt.Errorf("%s leads out of %s", parent, root)
	}
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	if !pathInside(root, parent) {
		return fmt.Errorf("%s leads out of %s", parent, root)
	}
	out, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s already exists, and deja does not overwrite a transcript", p)
		}
		return err
	}
	_, werr := out.Write(data)
	cerr := out.Close()
	if werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(p)
		return werr
	}
	return nil
}

// deepestExisting is p or the nearest of its parents that exists.
func deepestExisting(p string) string {
	for {
		if _, err := os.Lstat(p); err == nil {
			return p
		}
		up := filepath.Dir(p)
		if up == p {
			return p
		}
		p = up
	}
}

// pathInside reports whether p, with its links resolved, is root or under it.
func pathInside(root, p string) bool {
	r, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	q, err := filepath.EvalSymlinks(p)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(r, q)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
