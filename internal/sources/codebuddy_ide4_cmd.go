package sources

import (
	"path/filepath"
	"slices"
	"strings"
)

// CodeBuddyIDE4BookkeepingFile reports a file under one of the 4.x roots
// that is not a conversation: one of the message bodies under a
// conversation's messages/, assets/ or tool-results/ directory, the IDE's
// backup of a conversation index, a workspace's index and its pointers to the
// current and recent conversation, or the IDE's own state beside the users'
// directories. A conversation's own index.json is the file deja reads, not
// bookkeeping.
//
// Doctor walks every .json file under a store root for files it did not place.
// On a 4.x store that walk would report each message and workspace index as a
// transcript deja could not recognise; this names the store's own files, by
// the directories the conversation walk skips, so the row counts
// conversations instead. Those directories are asked first: the IDE keeps
// other files under them than .json, a workspace snapshot under file-tree
// keeps a copy of whatever the agent wrote, .jsonl included, and none of it
// is a transcript.
func CodeBuddyIDE4BookkeepingFile(p string, roots []string) bool {
	if !slices.ContainsFunc(roots, func(root string) bool { return codeBuddyRelTo(root, p) != nil }) {
		return false
	}
	for part := range strings.SplitSeq(filepath.ToSlash(p), "/") {
		if codeBuddyIDE4SkipDirs[part] {
			return true
		}
	}
	if !strings.EqualFold(filepath.Ext(p), ".json") {
		return false
	}
	base := filepath.Base(p)
	switch base {
	case "index_bak.json", ".index_bak.json", "current.json", "recent.json":
		return true
	}
	// The IDE's sign-in and connector state, one file per product, sits
	// directly in the user's directory as CodeBuddyIDE_<what>.json.
	if strings.HasPrefix(base, "CodeBuddyIDE_") {
		return true
	}
	// <history>/<workspace>/index.json is the workspace's list; a
	// conversation's index is one level deeper, <workspace>/<id>/index.json.
	return base == "index.json" && filepath.Base(filepath.Dir(filepath.Dir(p))) == "history"
}

// CodeBuddyIDE4IndexPath reports a path shaped like a 4.x conversation index,
// by the path alone, so the index can still tell a row whose file is gone: an
// index.json three levels under a directory named history, or two or three
// levels under one of roots, the depths a conversation has under a history
// directory by another name and under one workspace's directory. A
// workspace's own index.json has that shape too; the walk never lists one, so
// no row names it.
func CodeBuddyIDE4IndexPath(p string, roots []string) bool {
	if filepath.Base(p) != "index.json" {
		return false
	}
	if filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(p)))) == "history" {
		return true
	}
	for _, root := range roots {
		if n := len(codeBuddyRelTo(root, p)); n == 2 || n == 3 {
			return true
		}
	}
	return false
}

// CodeBuddyIDE4ConvertedTranscript names the CodeBuddy Code transcript the
// 5.x IDE wrote for a 4.x conversation, or "" when it did not. The 4.x
// reader returns no session for a converted conversation; doctor, resume,
// write-back and the index's row ownership follow the transcript instead.
func CodeBuddyIDE4ConvertedTranscript(p string) string {
	id, ok := codeBuddyIDE4Parts(p)
	if !ok {
		return ""
	}
	return codeBuddyIDE4ConvertedIDs()[id]
}
