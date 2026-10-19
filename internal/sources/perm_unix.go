//go:build unix

package sources

import (
	"io/fs"
	"os"
	"slices"
	"sync"
	"syscall"
)

var ids = sync.OnceValues(func() (int, []int) {
	groups, _ := os.Getgroups()
	return os.Geteuid(), append(groups, os.Getegid())
})

// ModeUnreadable reports a file this process may not read. A chmod changes
// neither size nor mtime, so an incremental pass called a locked transcript
// unchanged and the index up to date (#5393). The mode bits of the stat the
// walk already holds answer for nearly every file without another syscall, so
// the walk every search takes costs no more; only a file they refuse is asked
// about again with access(2), which knows about ACLs and every group.
func ModeUnreadable(path string, fi fs.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	uid, groups := ids()
	perm := fi.Mode().Perm()
	var refused bool
	switch {
	case uid == 0:
		return false
	case int(st.Uid) == uid:
		refused = perm&0o400 == 0
	case slices.Contains(groups, int(st.Gid)):
		refused = perm&0o040 == 0
	default:
		refused = perm&0o004 == 0
	}
	return refused && syscall.Access(path, 4) != nil
}
