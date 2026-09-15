package index

import (
	"fmt"
	"os"
)

// checkIndexPath refuses to build when the index path is a file rather than a
// directory.
//
// The swap parks whatever is at the path as `<dir>.old`, puts the new index in
// place, and then deletes the parked copy — so a file somebody had there was
// removed, with nothing said and an exit code of 0. That is a plausible thing
// to have there: DEJA_INDEX_DIR pointing at a database or an archive by
// mistake, or a sync client leaving a file where the directory belongs. Deleting
// it is not deja's call to make silently.
func checkIndexPath(dir string) error {
	if dir == "" {
		return nil
	}
	fi, err := os.Stat(dir)
	if err != nil || fi.IsDir() {
		return nil
	}
	return fmt.Errorf("the index path %s is a file, not a directory — deja will not replace it; move it aside, or point DEJA_INDEX_DIR at a directory", dir)
}
