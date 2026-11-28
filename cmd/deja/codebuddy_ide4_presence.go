package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// codeBuddyDoctorLocation names every CLI root deja walks, plus only the 4.x
// roots that actually hold conversations. A 4.x root can be an empty app-data
// directory on a machine that never used the IDE's history, and naming it
// would advertise history that is not there.
func codeBuddyDoctorLocation(cliRoots []string, legacyFiles []string) string {
	parts := append([]string{}, cliRoots...)
	seen := make(map[string]bool, len(cliRoots))
	for _, root := range cliRoots {
		seen[root] = true
	}
	for _, root := range sources.CodeBuddyIDE4Roots() {
		for _, file := range legacyFiles {
			if !seen[root] && underConfiguredRoot(root, file) {
				parts = append(parts, root)
				seen[root] = true
				break
			}
		}
	}
	return strings.Join(parts, string(os.PathListSeparator))
}

// underConfiguredRoot reports whether p is root or a path under it. The 4.x
// roots can be a Data directory, a user's directory, a history directory or
// a copy of a workspace, so the configured spelling is the only thing to
// compare.
func underConfiguredRoot(root, p string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// outermostRoots is roots without one that repeats another or sits under
// it, in their order. A Data directory and the history directory inside it
// may both be configured; walking each for files deja did not place counted
// every such file under the history twice.
func outermostRoots(roots []string) []string {
	out := make([]string, 0, len(roots))
	for i, root := range roots {
		covered := false
		for j, other := range roots {
			switch {
			case i == j:
			case filepath.Clean(other) == filepath.Clean(root):
				covered = covered || j < i
			case underConfiguredRoot(other, root):
				covered = true
			}
		}
		if !covered {
			out = append(out, root)
		}
	}
	return out
}
