package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// snapshotIfOnlyEmptyBlocksDiffer gives back deja's snapshot when the config
// an uninstall is about to write differs from it only by empty containers.
// The writers decide on their own whether an emptied block goes with deja's
// entry, and most decided wrong one way or the other: the hook writers, zed,
// VS Code, grok, prime and amp deleted a `"hooks": {}` or `"servers": {}` the
// reader had written, and opencode and kilo kept the `"mcp": {}` they had added
// to an empty `{}` (#4562). The snapshot is what the file was before deja, so
// it settles which.
//
// Only deja's own snapshot, only when nothing but empty blocks differs — a
// server added since, a changed value, and the writer's result stands — and
// only when the comments in the file are the snapshot's, so restoring it does
// not take back a comment written since.
func snapshotIfOnlyEmptyBlocksDiffer(path string, old, next []byte) []byte {
	start := firstSeenThisRun(path, old)
	b, ok := ownSnapshot(path)
	if !ok {
		return next
	}
	want, ok := decodeJSONCExact(next)
	if !ok {
		return next
	}
	have, ok := decodeJSONCExact(b)
	if !ok || reflect.DeepEqual(want, have) {
		return next
	}
	if jsonCommentText(string(b)) != jsonCommentText(lfText(old)) {
		return next
	}
	if !reflect.DeepEqual(withoutEmptyContainers(want), withoutEmptyContainers(have)) {
		return next
	}
	// And only the empty blocks deja's own edit explains: one the reader added
	// or took out since install is theirs, and the snapshot would undo it.
	// Measured against the file as this run found it, not as the last write
	// left it: qwen-auto and codebuddy-auto take hooks, server and status line
	// out of one settings.json in turn, and the write that emptied the hooks
	// block had already dropped it by the last one, which then read it as the
	// reader's doing.
	prev, ok := decodeJSONCExact(start)
	if !ok || !emptyBlocksDifferByDeja(want, have, prev) {
		return next
	}
	return b
}

// emptyBlocksDifferByDeja reports whether the empty blocks between the result
// and the snapshot are the writer's doing: one the result has and the snapshot
// lacks either held deja's entry before this uninstall or was not in the file
// at all, and one the snapshot has and the result lacks was still in the file.
// An empty block in the file that the snapshot lacks, or a block gone from
// both, is the reader's change since install.
func emptyBlocksDifferByDeja(next, snap, old any) bool {
	n, ok := next.(map[string]any)
	s, ok2 := snap.(map[string]any)
	if !ok || !ok2 {
		return true
	}
	o, _ := old.(map[string]any)
	for k, v := range n {
		sv, inSnap := s[k]
		if !inSnap {
			if ov, had := o[k]; had && isEmptyContainer(ov) {
				return false
			}
			continue
		}
		if !emptyBlocksDifferByDeja(v, sv, o[k]) {
			return false
		}
	}
	for k := range s {
		if _, inNext := n[k]; inNext {
			continue
		}
		if _, had := o[k]; !had {
			return false
		}
	}
	return true
}

// ownSnapshot reads the .bak deja took of path, without its byte order mark.
func ownSnapshot(path string) ([]byte, bool) {
	bak := path + ".bak"
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		bak = resolved + ".bak"
	}
	if !snapshotTaken(path) {
		return nil, false
	}
	b, err := os.ReadFile(bak)
	if err != nil {
		return nil, false
	}
	return bytes.TrimPrefix(b, utf8BOM), true
}

// decodeJSONCExact is decodeJSONExact for a config that may carry comments and
// trailing commas.
func decodeJSONCExact(b []byte) (any, bool) {
	return decodeJSONExact([]byte(jsoncToJSON(lfText(b))))
}

// jsonCommentText is the comments of a JSONC text, in order.
func jsonCommentText(s string) string {
	stripped := stripJSONComments(s)
	var out strings.Builder
	for i := 0; i < len(s) && i < len(stripped); i++ {
		if s[i] != stripped[i] {
			out.WriteByte(s[i])
		}
	}
	return out.String()
}

// withoutEmptyContainers drops every object key whose value is, or prunes to,
// an empty object or array.
func withoutEmptyContainers(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, child := range t {
			child = withoutEmptyContainers(child)
			if isEmptyContainer(child) {
				continue
			}
			out[k] = child
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, child := range t {
			out[i] = withoutEmptyContainers(child)
		}
		return out
	}
	return v
}

func isEmptyContainer(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		return len(t) == 0
	case []any:
		return len(t) == 0
	}
	return false
}

// runFirstSeen is each config as an uninstall run first found it, and what
// deja's last write in the run left there. Reset with readBytes at the start
// of every install and uninstall run; outside one it is nil and the caller's
// own bytes stand.
var runFirstSeen map[string]*runSeen

type runSeen struct{ start, last []byte }

// firstSeenThisRun is what the empty-block check measures against: the file
// as the run found it while every change since is deja's own, and otherwise
// what the writer read. A client that saved in between — the retry of #4561,
// or qwen writing its settings between deja's three edits — starts it again,
// so a block the client added or took out stays its change.
func firstSeenThisRun(path string, old []byte) []byte {
	readBytesMu.Lock()
	defer readBytesMu.Unlock()
	if runFirstSeen == nil {
		return old
	}
	if e, ok := runFirstSeen[path]; ok && e.last != nil && bytes.Equal(e.last, old) {
		return e.start
	}
	runFirstSeen[path] = &runSeen{start: append([]byte{}, old...)}
	return old
}

// noteRunWrite records what deja's write put in path, for firstSeenThisRun.
func noteRunWrite(path string, written []byte) {
	readBytesMu.Lock()
	defer readBytesMu.Unlock()
	if e, ok := runFirstSeen[path]; ok {
		e.last = append([]byte{}, bytes.TrimPrefix(written, utf8BOM)...)
	}
}
