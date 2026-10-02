package sources

import (
	"os"
	"path/filepath"
	"strings"
)

// Cursor CLI writes a second store beside the transcripts:
//
//	~/.cursor/chats/<workspace-hash>/<chat-uuid>/meta.json
//	~/.cursor/chats/<workspace-hash>/<chat-uuid>/store.db
//
// store.db holds `blobs(id TEXT, data BLOB)` and `meta(key, value)`. The blobs
// are content-addressed and the tree is walkable without a schema: meta's one
// value is hex-encoded JSON naming `latestRootBlobId`, that blob is protobuf
// whose repeated field 1 is a list of 32-byte child digests in message order,
// and each child is plain JSON — `{"role","content"}`, the Vercel AI SDK shape,
// with `text`, `reasoning`, `tool-call` and `tool-result` parts.
//
// The transcript holds every turn the store does, bar the `<user_info>`
// environment preamble it leaves out on purpose (#3772), but none of the tool
// results; cursor_store.go reads those from here (#4187).
// meta.json's cwd gives the project and the resume directory
// (cursor_chat_cwd.go, #4193).
//
// What matters is the day that stops being true. If a Cursor release keeps
// writing chats and stops writing `agent-transcripts`, every CLI session goes
// missing with nothing on any screen saying so — so the count below is what
// doctor reports, and it is silent while the transcripts are still there.

// CursorChatStores lists the per-chat SQLite stores under ~/.cursor/chats.
func CursorChatStores() []string {
	root := filepath.Join(CursorCLIRoot(), "chats")
	var out []string
	walked, under := walkRoot(root)
	entries, err := os.ReadDir(walked)
	if err != nil {
		return nil
	}
	for _, ws := range entries {
		if !ws.IsDir() {
			continue
		}
		chats, err := os.ReadDir(filepath.Join(walked, ws.Name()))
		if err != nil {
			continue
		}
		for _, c := range chats {
			if !c.IsDir() {
				continue
			}
			p := filepath.Join(walked, ws.Name(), c.Name(), "store.db")
			if fileExists(p) {
				out = append(out, under(p))
			}
		}
	}
	return out
}

// CursorChatsWithoutTranscript counts the chats holding content that no
// transcript covers. A chat and its transcript share one uuid, so the JSONL
// file name is the whole comparison.
func CursorChatsWithoutTranscript() int {
	stores := CursorChatStores()
	if len(stores) == 0 {
		return 0
	}
	have := map[string]bool{}
	for _, p := range CursorTranscripts() {
		have[strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))] = true
	}
	var n int
	for _, db := range stores {
		if !have[filepath.Base(filepath.Dir(db))] {
			n++
		}
	}
	return n
}
