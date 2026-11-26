package sources

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// CodeBuddy IDE 4.x, the IDE before it ran CodeBuddy Code underneath, kept its
// chats in the extension's own data directory, one directory per conversation:
//
//	<data>/<user>/CodeBuddyIDE/<user>/history/<workspace-md5>/index.json
//	<data>/<user>/CodeBuddyIDE/<user>/history/<workspace-md5>/<id>/index.json
//	<data>/<user>/CodeBuddyIDE/<user>/history/<workspace-md5>/<id>/messages/<msg>.json
//
// where <data> is CodeBuddyExtension/Data under the platform's application
// data directory — resolveExtensionUserDataPath in the 5.x IDE's genie
// extension: ~/Library/Application Support on macOS, ~/AppData/Local on
// Windows and ~/.local/share everywhere else. A signed-out IDE files under the
// user "default", and an older one put the history one level higher, so a
// conversation is any <id>/index.json two levels under a directory named
// history, wherever that sits under the root.
//
// The workspace index lists the conversations with their title and type; the
// conversation's index lists its messages and requests in order; each message
// is a file of its own, rewritten in place while a reply streams. The 5.x IDE
// converts a conversation into a CodeBuddy Code transcript when it is opened,
// and leaves the old directory where it was. Once that transcript is on disk
// it is the conversation: it holds everything the old one did and every turn
// after the upgrade, so the 4.x copy is not read
// (CodeBuddyIDE4ConvertedTranscript).

// codeBuddyIDE4Marker is the file the 5.x converter leaves in the sidecar
// directory of a transcript it wrote, naming the conversation it came from
// (CONVERTED_SESSION_OWNER_MARKER in the genie extension).
const codeBuddyIDE4Marker = ".codebuddy-ide-4-conversion.json"

// codeBuddyIDE4FileMax bounds one file of the store: the IDE's own recorder
// keeps a message under it, and a larger one is not a conversation file.
const codeBuddyIDE4FileMax = 64 << 20

// CodeBuddyIDE4DefaultRoot is CodeBuddyExtension/Data where the IDE keeps it
// on this platform.
func CodeBuddyIDE4DefaultRoot() string {
	return codeBuddyIDE4RootFor(runtime.GOOS, Home())
}

func codeBuddyIDE4RootFor(goos, home string) string {
	switch goos {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "CodeBuddyExtension", "Data")
	case "windows":
		return filepath.Join(home, "AppData", "Local", "CodeBuddyExtension", "Data")
	default:
		return filepath.Join(home, ".local", "share", "CodeBuddyExtension", "Data")
	}
}

// CodeBuddyIDE4Roots is every directory the 4.x history is looked for under.
// DEJA_CODEBUDDY_LEGACY_ROOTS, a path list, replaces the default: a Data
// directory, one user's directory, a history directory, one workspace's
// directory, or a copy of any of them from another machine. On Windows the
// genie extension resolves the data directory under AppData\Local; a Roaming
// one beside it, where an older build or a profile sync may have left it, is
// read too when it is there.
func CodeBuddyIDE4Roots() []string {
	if list := os.Getenv("DEJA_CODEBUDDY_LEGACY_ROOTS"); list != "" {
		var out []string
		for _, p := range filepath.SplitList(list) {
			if p != "" {
				out = append(out, filepath.Clean(p))
			}
		}
		return out
	}
	out := []string{CodeBuddyIDE4DefaultRoot()}
	if runtime.GOOS == "windows" {
		roaming := filepath.Join(Home(), "AppData", "Roaming", "CodeBuddyExtension", "Data")
		if st, err := os.Stat(roaming); err == nil && st.IsDir() {
			out = append(out, roaming)
		}
	}
	return out
}

// codeBuddyIDE4Parts is the conversation id of a conversation index, or ok
// false for any other path. A conversation's index sits two levels under a
// directory named history, or two levels under a root that is a history
// directory, or one level under a root that is a workspace's directory: a
// root a user points at a copy of part of the store. One level under any
// other root — a history directory by whatever name, a Data directory — is a
// workspace's own index or a user's state, not a conversation, so there the
// directory has to hold a conversation's messages directory, which a
// workspace's never does. A copied workspace that lost its own index.json is
// still read by that.
func codeBuddyIDE4Parts(p string) (id string, ok bool) {
	if filepath.Base(p) != "index.json" {
		return "", false
	}
	dir := filepath.Dir(p)
	ws := filepath.Dir(dir)
	id = filepath.Base(dir)
	if !codeBuddyIDE4SafeID(id) {
		return "", false
	}
	if filepath.Base(filepath.Dir(ws)) == "history" && codeBuddyIDE4SafeID(filepath.Base(ws)) {
		return id, true
	}
	for _, root := range CodeBuddyIDE4Roots() {
		switch rel := codeBuddyRelTo(root, p); {
		case len(rel) == 2:
			// <workspace root>/<id>/index.json. The messages directory is
			// asked first: it is one stat, and the root's index is a read.
			if codeBuddyIDE4HasMessages(dir) || !codeBuddyIDE4LooksLikeHistory(root) {
				return id, true
			}
		case len(rel) == 3 && codeBuddyIDE4SafeID(rel[0]):
			// <history root>/<workspace>/<id>/index.json, unless the
			// root is a workspace and this is a conversation's file.
			if _, err := os.Lstat(filepath.Join(root, "index.json")); err != nil || codeBuddyIDE4LooksLikeHistory(root) {
				return id, true
			}
		}
	}
	return "", false
}

// codeBuddyIDE4HasMessages reports a directory holding a messages directory,
// as a conversation's does and a workspace's does not.
func codeBuddyIDE4HasMessages(dir string) bool {
	fi, err := os.Lstat(filepath.Join(dir, "messages"))
	return err == nil && fi.IsDir()
}

// codeBuddyIDE4LooksLikeHistory reports a root whose children are workspace
// directories, each with its own index.json, rather than one workspace whose
// children are conversations.
func codeBuddyIDE4LooksLikeHistory(root string) bool {
	if filepath.Base(root) == "history" {
		return true
	}
	b, err := codeBuddyIDE4ReadFile(filepath.Join(root, "index.json"))
	if err != nil {
		return true
	}
	var idx struct {
		Conversations []json.RawMessage `json:"conversations"`
	}
	return json.Unmarshal(b, &idx) != nil || idx.Conversations == nil
}

// codeBuddyIDE4SafeID is an id the store names a file or directory by:
// letters, digits, - and _, as the IDE writes them. Anything else is not
// joined into a path. "Lw==", the signed-out user's directory, never reaches
// here: only a workspace and a conversation are checked.
func codeBuddyIDE4SafeID(s string) bool {
	if s == "" || len(s) > 200 {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

// IsCodeBuddyIDE4Session reports a 4.x conversation index under one of the
// roots. It looks at the path, and at most the root's own index and the
// conversation's messages directory: the incremental index asks it of every
// path it holds.
func IsCodeBuddyIDE4Session(p string) bool {
	if filepath.Base(p) != "index.json" {
		return false
	}
	under := false
	for _, root := range CodeBuddyIDE4Roots() {
		if codeBuddyRelTo(root, p) != nil {
			under = true
			break
		}
	}
	if !under {
		return false
	}
	_, ok := codeBuddyIDE4Parts(p)
	return ok
}

// CodeBuddyIDE4SessionFiles lists the conversation indexes, each once when
// two configured roots overlap, and each conversation once when the store
// holds it twice. The walk skips what it cannot hold — the message bodies, a
// conversation's assets, a copy the IDE keeps before it rewrites — so
// listing a store of a hundred thousand message files stats a few thousand
// entries.
func CodeBuddyIDE4SessionFiles() []string {
	files, _ := CodeBuddyIDE4SessionFilesAndCopies()
	return files
}

// CodeBuddyIDE4SessionFilesAndCopies is CodeBuddyIDE4SessionFiles and the
// conversation indexes it left out as copies of a listed one.
//
// The IDE can hold one conversation in two workspace directories: a store
// on Linux kept 204 conversations, byte for byte, in the directories of two
// spellings of one workspace. Read from both, each was filed as two
// transcripts clashing over one id, and its unreadable records were counted
// twice. A conversation id is the IDE's own random one, so the copies are one
// conversation; the one whose index was written last is read, as the one the
// IDE went on writing, and the smaller path when they were written at once,
// so the choice does not depend on the walk's order.
func CodeBuddyIDE4SessionFilesAndCopies() (files, copies []string) {
	var all []string
	seen := map[string]bool{}
	for _, root := range CodeBuddyIDE4Roots() {
		for _, p := range walkCodeBuddyIDE4(root) {
			if !seen[p] {
				seen[p] = true
				all = append(all, p)
			}
		}
	}
	chosen := map[string]string{}
	var mod map[string]time.Time
	for _, p := range all {
		id, _ := codeBuddyIDE4Parts(p)
		held, ok := chosen[id]
		if !ok {
			chosen[id] = p
			continue
		}
		if mod == nil {
			mod = map[string]time.Time{}
		}
		if codeBuddyIDE4Newer(p, held, mod) {
			chosen[id] = p
		}
	}
	for _, p := range all {
		id, _ := codeBuddyIDE4Parts(p)
		if chosen[id] == p {
			files = append(files, p)
		} else {
			copies = append(copies, p)
		}
	}
	return files, copies
}

// codeBuddyIDE4Newer reports whether the conversation index a is read in
// place of b, a copy of the same conversation: written later, or at the same
// time from the smaller path.
func codeBuddyIDE4Newer(a, b string, mod map[string]time.Time) bool {
	at := func(p string) time.Time {
		t, ok := mod[p]
		if !ok {
			if fi, err := os.Stat(p); err == nil {
				t = fi.ModTime()
			}
			mod[p] = t
		}
		return t
	}
	if ta, tb := at(a), at(b); !ta.Equal(tb) {
		return ta.After(tb)
	}
	return a < b
}

// codeBuddyIDE4SkipDirs are directories under a conversation, or beside the
// history, that hold no conversation index: message bodies and attachments,
// and the IDE's caches, logs, checkpoints, workspace snapshots and plans.
var codeBuddyIDE4SkipDirs = map[string]bool{
	"messages": true, "assets": true, "tool-results": true,
	"Cache": true, "Logs": true, "Public": true,
	"connectors": true, "genie-cache": true, "plan-task": true,
	"check-point": true, "file-tree": true,
}

func walkCodeBuddyIDE4(root string) []string {
	var out []string
	walked, under := walkRoot(root)
	_ = filepath.WalkDir(walked, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsPermission(err) {
				diagWalkError(p, err)
			}
			return nil
		}
		if d.IsDir() {
			if p != walked && (codeBuddyIDE4SkipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if q := under(p); isRegularWalkEntry(d) && d.Name() == "index.json" {
			if _, ok := codeBuddyIDE4Parts(q); ok {
				out = append(out, q)
			}
		}
		return nil
	})
	return out
}

// LoadCodeBuddyIDE4 reads every 4.x conversation on this machine.
func LoadCodeBuddyIDE4() []model.Session {
	return parseFiles(CodeBuddyIDE4SessionFiles(), ParseCodeBuddyIDE4File)
}

// CodeBuddyAllFiles is every file the codebuddy harness reads: the CodeBuddy
// Code and WorkBuddy transcripts, and the 4.x IDE's conversations.
func CodeBuddyAllFiles() []string {
	return append(CodeBuddySessionFiles(), CodeBuddyIDE4SessionFiles()...)
}

// LoadCodeBuddyAll reads both stores. Neither loader is wrapped by the read
// switches; the registry wraps this one.
func LoadCodeBuddyAll() []model.Session {
	return append(LoadCodeBuddy(), LoadCodeBuddyIDE4()...)
}

// codeBuddyIDE4Entry is a conversation as the workspace index lists it.
type codeBuddyIDE4Entry struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	Name          string `json:"name"`
	CreatedAt     any    `json:"createdAt"`
	LastMessageAt any    `json:"lastMessageAt"`
}

// codeBuddyIDE4Workspaces caches each workspace index by its state, since every
// conversation of the workspace reads it and the IDE rewrites it on every turn.
// A workspace whose index changes replaces its own entry, so the cache holds
// one entry a workspace. Each entry has its own lock, held while its index is
// read, so the workers parsing one workspace's conversations at once decode
// its index once, and a workspace index that is slow to read holds up only
// the conversations that need it.
var (
	codeBuddyIDE4WorkspacesMu sync.Mutex
	codeBuddyIDE4Workspaces   = map[string]*codeBuddyIDE4Workspace{}
)

// codeBuddyIDE4RacyWindow is how recently a workspace index may have been
// written for its size and mtime not to vouch for its content, as git treats
// a racily clean index entry. A filesystem stamps a write from a clock that
// moves in steps: NTFS from the system time, which moves about every 15.6 ms
// and in a test kept the previous mtime on 177 of 200 quick rewrites of the
// same length; ext4 and xfs from the kernel's coarse clock, 1 to 10 ms; FAT
// to the even second. A rewrite of the same length inside one step keeps the
// stamp, so an index read less than this long after its mtime is read again
// on the next lookup. Once it is older, no later write can keep its stamp,
// and the cache answers from a stat. An mtime ahead of the clock counts as
// recent, as it does for git: it says nothing about when the file was last
// written, and reading it again costs time, not answers.
const codeBuddyIDE4RacyWindow = 2 * time.Second

type codeBuddyIDE4Workspace struct {
	mu      sync.Mutex
	stamp   string
	racy    bool
	entries map[string]codeBuddyIDE4Entry
}

// codeBuddyIDE4WorkspaceEntry is the workspace index's entry for one
// conversation. A missing or unreadable index is no entry: the conversation's
// own files are still the conversation.
func codeBuddyIDE4WorkspaceEntry(conversationIndex string) (codeBuddyIDE4Entry, bool) {
	wsIndex := filepath.Join(filepath.Dir(filepath.Dir(conversationIndex)), "index.json")
	id := filepath.Base(filepath.Dir(conversationIndex))
	fi, err := os.Lstat(wsIndex)
	if err != nil || !fi.Mode().IsRegular() {
		return codeBuddyIDE4Entry{}, false
	}
	stamp := fmt.Sprintf("%d|%d", fi.Size(), fi.ModTime().UnixNano())
	codeBuddyIDE4WorkspacesMu.Lock()
	c, ok := codeBuddyIDE4Workspaces[wsIndex]
	if !ok {
		c = &codeBuddyIDE4Workspace{}
		codeBuddyIDE4Workspaces[wsIndex] = c
	}
	codeBuddyIDE4WorkspacesMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil || c.stamp != stamp || c.racy {
		// Measured from the stat, which comes before the read: a write
		// after the read lands later still.
		c.stamp, c.racy, c.entries = stamp, time.Since(fi.ModTime()) < codeBuddyIDE4RacyWindow, map[string]codeBuddyIDE4Entry{}
		if b, err := codeBuddyIDE4ReadFile(wsIndex); err == nil {
			var idx struct {
				Conversations []codeBuddyIDE4Entry `json:"conversations"`
			}
			if json.Unmarshal(b, &idx) == nil {
				for _, e := range idx.Conversations {
					if _, seen := c.entries[e.ID]; !seen && e.ID != "" {
						c.entries[e.ID] = e
					}
				}
			}
		}
	}
	e, ok := c.entries[id]
	return e, ok
}

// codeBuddyIDE4ReadFile reads a store file no larger than codeBuddyIDE4FileMax.
func codeBuddyIDE4ReadFile(p string) ([]byte, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", p)
	}
	if fi.Size() > codeBuddyIDE4FileMax {
		return nil, fmt.Errorf("%s is %d bytes, over the %d a CodeBuddy IDE file can be", p, fi.Size(), codeBuddyIDE4FileMax)
	}
	return os.ReadFile(p)
}

// codeBuddyIDE4Sidecar fingerprints what the reader takes from beside the
// conversation index: every message file it lists, by name, size and mtime,
// the message directory, the conversation's entry in the workspace index —
// its type, title and both times — and the transcript that stands in for it
// once the 5.x IDE has converted it. The index file's own size and mtime are
// the file state; a change to any of these re-reads the conversation
// (#4446). A message file rewritten in place
// keeping its size and its mtime to the resolution of the filesystem is the
// one change this misses; the IDE writes a message while its reply streams,
// which grows it.
func codeBuddyIDE4Sidecar(p string) (size, stamp int64) {
	h := fnv.New64a()
	dir := filepath.Dir(p)
	msgs := filepath.Join(dir, "messages")
	add := func(name string, fi fs.FileInfo) {
		size += fi.Size()
		_, _ = fmt.Fprintf(h, "%s|%d|%d;", name, fi.Size(), fi.ModTime().UnixNano())
	}
	if fi, err := os.Lstat(msgs); err == nil {
		add("messages/", fi)
	}
	// Listing the directory once costs less than a stat a listed message,
	// and it sees a message the index does not list yet.
	if entries, err := os.ReadDir(msgs); err == nil {
		for _, e := range entries {
			if fi, err := e.Info(); err == nil && fi.Mode().IsRegular() {
				add(e.Name(), fi)
			}
		}
	}
	if e, ok := codeBuddyIDE4WorkspaceEntry(p); ok {
		_, _ = fmt.Fprintf(h, "entry|%s|%s|%v|%v;", e.Type, e.Name, e.CreatedAt, e.LastMessageAt)
	}
	if transcript := CodeBuddyIDE4ConvertedTranscript(p); transcript != "" {
		_, _ = fmt.Fprintf(h, "converted|%s;", transcript)
		if fi, err := os.Lstat(transcript); err == nil {
			add("transcript", fi)
		}
	}
	return size, int64(h.Sum64()) //nolint:gosec // a fingerprint; the bits are kept as they are
}

// walkGeneration counts the walks of the stores. A lookup that every file of
// a store asks the same question of is answered once a walk (BeginWalk).
var walkGeneration atomic.Int64

// codeBuddyIDE4ConvertedTTL bounds how long a lookup of the converted
// conversations is reused without a walk: a hook or the MCP server parses a
// file without walking, and a conversion made since has to be seen.
var codeBuddyIDE4ConvertedTTL = 2 * time.Second

// codeBuddyIDE4ConvertedIDs maps each conversation the 5.x IDE converted to
// the transcript it wrote: a CodeBuddy Code transcript <id>.jsonl that can be
// read, beside the sidecar directory <id>/ holding the converter's marker
// that names the same conversation. The marker is the converter's own record
// of where the transcript came from, so nothing else — a CLI session, a
// WorkBuddy store, the 5.x catalog database — decides it, and a machine with
// no 5.x at all reads every 4.x conversation.
//
// The answer is kept for one walk of the stores, and for no longer than
// codeBuddyIDE4ConvertedTTL: the fingerprint of every 4.x conversation asks
// it, and finding the markers lists every project folder. The map handed out
// is never written again, so the parses of one pass share it.
func codeBuddyIDE4ConvertedIDs() map[string]string {
	roots := CodeBuddyRoots()
	key := fmt.Sprintf("%d|%s", walkGeneration.Load(), strings.Join(roots, string(os.PathListSeparator)))
	codeBuddyIDE4ConvertedMu.Lock()
	defer codeBuddyIDE4ConvertedMu.Unlock()
	if codeBuddyIDE4ConvertedKey == key && codeBuddyIDE4ConvertedMap != nil && time.Since(codeBuddyIDE4ConvertedAt) < codeBuddyIDE4ConvertedTTL {
		return codeBuddyIDE4ConvertedMap
	}
	out := map[string]string{}
	for _, root := range roots {
		for _, m := range codeBuddyIDE4Markers(root) {
			sidecar := filepath.Dir(m)
			id := filepath.Base(sidecar)
			if !codeBuddyIDE4SafeID(id) || codeBuddyIDE4MarkerID(m) != id {
				continue
			}
			transcript := filepath.Join(filepath.Dir(sidecar), id+".jsonl")
			if codeBuddyIDE4Readable(transcript) {
				out[id] = transcript
			}
			// A team member's conversation goes into the lead's transcript as
			// subagents/agent-<member id>.jsonl (teamMemberSidecarPath), and
			// is read from there once the lead is converted.
			members, _ := filepath.Glob(filepath.Join(sidecar, "subagents", "agent-*.jsonl"))
			for _, m := range members {
				member := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(m), "agent-"), ".jsonl")
				if codeBuddyIDE4SafeID(member) && codeBuddyIDE4Readable(m) {
					if _, taken := out[member]; !taken {
						out[member] = m
					}
				}
			}
		}
	}
	codeBuddyIDE4ConvertedKey, codeBuddyIDE4ConvertedMap, codeBuddyIDE4ConvertedAt = key, out, time.Now()
	return out
}

var (
	codeBuddyIDE4ConvertedMu  sync.Mutex
	codeBuddyIDE4ConvertedKey string
	codeBuddyIDE4ConvertedMap map[string]string
	codeBuddyIDE4ConvertedAt  time.Time
)

// codeBuddyIDE4Markers lists the converter's markers under a CodeBuddy Code
// store root: <root>/<project>/<id>/<marker>, or <root>/<id>/<marker> for a
// root that names one project folder, as DEJA_CODEBUDDY_ROOTS may.
func codeBuddyIDE4Markers(root string) []string {
	out, _ := filepath.Glob(filepath.Join(root, "*", "*", codeBuddyIDE4Marker))
	one, _ := filepath.Glob(filepath.Join(root, "*", codeBuddyIDE4Marker))
	return append(out, one...)
}

// codeBuddyIDE4MarkerID is the conversation a converter marker names.
func codeBuddyIDE4MarkerID(p string) string {
	b, err := os.ReadFile(p)
	if err != nil || len(b) > 64<<10 {
		return ""
	}
	var m struct {
		ConversationID string `json:"conversationId"`
	}
	if json.Unmarshal(b, &m) != nil {
		return ""
	}
	return m.ConversationID
}

// codeBuddyIDE4Readable reports a transcript the CodeBuddy Code reader can
// open and that holds something. One that cannot be read yet leaves the 4.x
// copy as the conversation.
func codeBuddyIDE4Readable(p string) bool {
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() == 0 {
		return false
	}
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}
