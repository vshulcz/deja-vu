package sources

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// claudeSessionKey is the identity a record gives the session of its file, as
// the parser reads it: a sidechain's agent and the parent it names, or the
// sessionId. ok is false for a record that leaves the identity alone; a
// record that takes part with no id gives "" and ok, which the parser reads as
// the name the file started with.
func claudeSessionKey(line []byte) (key string, ok bool) {
	if !bytes.Contains(line, []byte(`"type"`)) {
		return "", false
	}
	var v struct {
		Type        string `json:"type"`
		IsMeta      bool   `json:"isMeta"`
		IsSidechain bool   `json:"isSidechain"`
		AgentID     string `json:"agentId"`
		SessionID   string `json:"sessionId"`
	}
	if json.Unmarshal(line, &v) != nil || (v.Type != "user" && v.Type != "assistant") ||
		(v.Type == "user" && v.IsMeta) {
		return "", false
	}
	if v.IsSidechain && v.AgentID != "" {
		return "agent:" + v.AgentID + "\x00" + v.SessionID, true
	}
	return v.SessionID, true
}

// claudeResumes is whether a Claude-format tail can be appended: it answers
// no call stored already (#4443) and keeps the file's session (#5379).
func claudeResumes(path string, offset int64) bool {
	return claudeExitResumes(path, offset) && claudeSameSession(path, offset)
}

// claudeSameSession reports whether the tail from offset leaves the file
// under the session its first offset bytes were filed under. The parser names
// a file by the last sessionId it holds, so a tail that carries another one
// files the whole transcript under it in a rebuild; appended, the turns already
// indexed stayed under the old id and only the tail went to the new (#5379).
// Such a tail is read whole instead.
func claudeSameSession(path string, offset int64) bool {
	if offset <= 0 {
		return true
	}
	stem := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	tail, seen := "", false
	_ = scanJSONLBytes(path, offset, func(line []byte) {
		if k, ok := claudeSessionKey(line); ok {
			seen = true
			if k != "" {
				tail = k
			}
		}
	})
	if !seen {
		return true
	}
	head, found := lastClaudeKeyBefore(path, offset)
	if !found {
		// Nothing before the tail named the session, so nothing is filed yet.
		return true
	}
	if head == "" {
		head = stem
	}
	if tail == "" {
		tail = stem
	}
	return head == tail
}

// lastClaudeKeyBefore is the identity the bytes before end leave the file
// with, read backwards from end so a long transcript is not read from its first
// byte. found is false when no record before end takes part; key is "" when
// the ones that do carry no id.
func lastClaudeKeyBefore(path string, end int64) (key string, found bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()
	var partial []byte
	pos := end
	for pos > 0 {
		n := min(int64(64<<10), pos)
		buf := make([]byte, n, n+int64(len(partial)))
		if _, err := f.ReadAt(buf, pos-n); err != nil && err != io.EOF {
			return "", false
		}
		buf = append(buf, partial...)
		pos -= n
		// The bytes before the first newline may be the end of a line that
		// starts in the next chunk back, unless this chunk reached the file's
		// start.
		first := 0
		if pos > 0 {
			i := bytes.IndexByte(buf, '\n')
			if i < 0 {
				partial = buf
				continue
			}
			first = i + 1
		}
		lines := bytes.Split(buf[first:], []byte{'\n'})
		for i := len(lines) - 1; i >= 0; i-- {
			if k, ok := claudeSessionKey(bytes.TrimSpace(lines[i])); ok && k != "" {
				return k, true
			} else if ok {
				found = true
			}
		}
		partial = buf[:first]
	}
	return "", found
}
