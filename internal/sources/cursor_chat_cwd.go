package sources

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Cursor names a CLI project folder by blanking every character of the working
// directory outside [A-Za-z0-9] and cutting a long one with a hash, so a
// directory with a dot, a space or a non-ASCII name anywhere in its path
// cannot be read back from it: resume printed no `cd`, and cursor-agent, which
// finds a chat under chats/<md5 of the cwd>/<id>, did not find it (#4193).
// The chat's meta.json records the directory as it was, and the folder above
// it is md5 of that directory — cursor-agent's src/state/index.ts — which is
// how a meta.json is told apart from one copied in from elsewhere.

// CursorChatCWD is the directory the CLI chat a transcript belongs to ran in,
// from the chat's meta.json; "" when there is none.
func CursorChatCWD(transcript string) string {
	id := strings.TrimSuffix(filepath.Base(transcript), ".jsonl")
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return ""
	}
	chats := filepath.Join(CursorCLIRoot(), "chats")
	ws, err := os.ReadDir(chats)
	if err != nil {
		return ""
	}
	fallback := ""
	for _, w := range ws {
		if !w.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(chats, w.Name(), id, "meta.json"))
		if err != nil {
			continue
		}
		var meta struct {
			CWD string `json:"cwd"`
		}
		if json.Unmarshal(b, &meta) != nil || meta.CWD == "" {
			continue
		}
		sum := md5.Sum([]byte(meta.CWD))
		if strings.EqualFold(hex.EncodeToString(sum[:]), w.Name()) {
			return meta.CWD
		}
		if fallback == "" {
			fallback = meta.CWD
		}
	}
	return fallback
}
