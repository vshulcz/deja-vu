package sources

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Cursor names a CLI project folder by blanking every character of the working
// directory outside [A-Za-z0-9] and cutting a long one with a hash, so a
// directory with a dot, a space or a non-ASCII name anywhere in its path
// cannot be read back from it: resume printed no `cd`, and `cursor-agent
// --resume <id>`, which looks only under chats/<md5 of the cwd>/<id>, did not
// find the chat (#4193). The chat's meta.json records the directory as it
// was, and the folder above it is md5 of that directory — cursor-agent's
// src/state/index.ts — which is how a meta.json is told apart from one copied
// in from elsewhere.

// CursorChatCWD is the directory the CLI chat a transcript belongs to ran in,
// from the chat's meta.json, and whether the chat sits under the md5 of it,
// which is where `cursor-agent --resume` looks. "" when there is no meta.json
// naming one.
func CursorChatCWD(transcript string) (cwd string, verified bool) {
	id := strings.TrimSuffix(filepath.Base(transcript), ".jsonl")
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return "", false
	}
	chats := filepath.Join(CursorCLIRoot(), "chats")
	for _, bucket := range cursorChatBuckets(chats, id) {
		b, err := os.ReadFile(filepath.Join(chats, bucket, id, "meta.json"))
		if err != nil {
			continue
		}
		var meta struct {
			CWD string `json:"cwd"`
		}
		if json.Unmarshal(b, &meta) != nil || meta.CWD == "" {
			continue
		}
		if strings.EqualFold(CursorChatBucket(meta.CWD), bucket) {
			return meta.CWD, true
		}
		if cwd == "" {
			cwd = meta.CWD
		}
	}
	return cwd, false
}

// CursorChatBucket is the chats/ folder cursor-agent keeps a directory's chats
// in.
func CursorChatBucket(cwd string) string {
	sum := md5.Sum([]byte(cwd))
	return hex.EncodeToString(sum[:])
}

// cursorChatIndex maps a chat id to the buckets holding it, built once per
// chats/ directory: every transcript parse asks, and listing chats/ for each
// of them was a directory read per bucket per transcript on a rebuild. A miss
// rescanned all of chats/ too, once per transcript whose chat is gone (#4226),
// so a miss is answered from the index while it is fresh: younger than
// cursorChatFresh, and chats/ not changed since the scan. A chat started in a
// new bucket changes chats/ and is found at once; one added to an existing
// bucket, which changes only that bucket, is found once the index ages out.
var cursorChatIndex struct {
	sync.Mutex
	root    string
	ids     map[string][]string
	mtime   time.Time // of chats/, as scanned
	scanned time.Time
	scans   int
}

// cursorChatFresh is how long a miss is answered from the index. It also
// covers a filesystem whose directory mtimes tick in seconds (HFS+, ext3,
// FAT), where a chat added in the second of the scan leaves chats/ as it was.
const cursorChatFresh = 2 * time.Second

// cursorChatTick is how close to the scan a chats/ mtime is treated as one
// the scan may not have seen all of, on a filesystem whose clock ticks in
// milliseconds.
const cursorChatTick = 10 * time.Millisecond

func cursorChatBuckets(chats, id string) []string {
	cursorChatIndex.Lock()
	defer cursorChatIndex.Unlock()
	if cursorChatIndex.root == chats {
		if b, ok := cursorChatIndex.ids[id]; ok {
			return b
		}
		if !cursorChatStale(chats) {
			return nil
		}
	}
	cursorChatIndex.scans++
	scanned := time.Now()
	var mtime time.Time
	if fi, err := os.Stat(chats); err == nil {
		mtime = fi.ModTime()
	}
	ids := map[string][]string{}
	buckets, _ := os.ReadDir(chats)
	for _, w := range buckets {
		entries, err := os.ReadDir(filepath.Join(chats, w.Name()))
		if err != nil {
			continue
		}
		for _, e := range entries {
			ids[e.Name()] = append(ids[e.Name()], w.Name())
		}
	}
	cursorChatIndex.root, cursorChatIndex.ids = chats, ids
	cursorChatIndex.mtime, cursorChatIndex.scanned = mtime, scanned
	return ids[id]
}

// cursorChatStale reports whether a miss has to look at chats/ again: the
// index has aged out, chats/ has changed since the scan, or it changed so
// close to the scan that the scan may have missed part of it.
func cursorChatStale(chats string) bool {
	ix := &cursorChatIndex
	if time.Since(ix.scanned) >= cursorChatFresh {
		return true
	}
	fi, err := os.Stat(chats)
	if err != nil {
		return !ix.mtime.IsZero()
	}
	if !fi.ModTime().Equal(ix.mtime) {
		return true
	}
	// Only an mtime near the scan, not one ahead of it: a ~/.cursor copied
	// from a machine whose clock ran fast, or a clock stepped back, would
	// otherwise rescan on every miss.
	d := ix.mtime.Sub(ix.scanned)
	return d >= -cursorChatTick && d <= cursorChatTick
}
