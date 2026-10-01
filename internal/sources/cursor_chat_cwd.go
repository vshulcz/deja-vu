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
// of them was a directory read per bucket per transcript on a rebuild. An id
// not in it is looked for again only when chats/ or one of its buckets has
// changed since, which is how a chat started after the scan shows up in a
// long-lived process; rescanning on every miss read all of chats/ once per
// transcript whose chat is gone (#4226).
var cursorChatIndex struct {
	sync.Mutex
	root   string
	ids    map[string][]string
	mtimes map[string]time.Time // chats/ ("") and each bucket, as scanned
	scans  int
}

func cursorChatBuckets(chats, id string) []string {
	cursorChatIndex.Lock()
	defer cursorChatIndex.Unlock()
	if cursorChatIndex.root == chats {
		if b, ok := cursorChatIndex.ids[id]; ok {
			return b
		}
		if !cursorChatChanged(chats) {
			return nil
		}
	}
	cursorChatIndex.scans++
	ids := map[string][]string{}
	mtimes := map[string]time.Time{}
	if fi, err := os.Stat(chats); err == nil {
		mtimes[""] = fi.ModTime()
	}
	buckets, _ := os.ReadDir(chats)
	for _, w := range buckets {
		dir := filepath.Join(chats, w.Name())
		fi, err := os.Stat(dir)
		if err != nil {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		mtimes[w.Name()] = fi.ModTime()
		for _, e := range entries {
			ids[e.Name()] = append(ids[e.Name()], w.Name())
		}
	}
	cursorChatIndex.root, cursorChatIndex.ids = chats, ids
	cursorChatIndex.mtimes = mtimes
	return ids[id]
}

// cursorChatChanged reports whether chats/ may hold a chat the index has not
// seen: chats/ or a bucket in it has a different mtime from the scan. A new
// chat lands in a new bucket, which changes chats/, or in an existing one,
// which changes that bucket.
func cursorChatChanged(chats string) bool {
	for name, was := range cursorChatIndex.mtimes {
		fi, err := os.Stat(filepath.Join(chats, name))
		if err != nil || !fi.ModTime().Equal(was) {
			return true
		}
	}
	return len(cursorChatIndex.mtimes) == 0
}
