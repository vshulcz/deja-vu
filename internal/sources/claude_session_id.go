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
// sessionId. ok is false for a record the parser skips; a record it reads that
// carries no id gives "" and ok, and leaves the identity as it was.
func claudeSessionKey(line []byte) (key string, ok bool) {
	line = trimJSONSpace(line)
	if !bytes.Contains(line, []byte(`"type"`)) {
		return "", false
	}
	var v claudeLine
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
	// check reports whether line names the session, noting one that takes part.
	check := func(line []byte) bool {
		k, ok := claudeSessionKey(line)
		if ok && k != "" {
			key = k
			return true
		}
		found = found || ok
		return false
	}
	// The pieces of a line that runs on past the chunk being read, in file
	// order. Joined once when its start is found, so a line of many megabytes
	// is copied once rather than once per chunk. A line past the buffer is not
	// held at all: once its start is found it is streamed from the file, as the
	// parse streams it (#5414). lineEnd is where the carried line ends.
	var carry [][]byte
	carried, lineEnd, long := 0, int64(0), false
	held := func(start int64, piece []byte) []byte {
		if long {
			long, carry, carried = false, nil, 0
			return streamedLineAt(f, start, lineEnd)
		}
		if len(carry) > 0 {
			piece = bytes.Join(append([][]byte{piece}, carry...), nil)
			carry, carried = nil, 0
		}
		return piece
	}
	pos := end
	// spare is the last chunk when nothing carried kept it: past a long line
	// every chunk is read into the same one.
	var spare []byte
	for pos > 0 {
		n := min(int64(64<<10), pos)
		buf := spare[:0:cap(spare)]
		if int64(cap(buf)) < n {
			buf = make([]byte, n)
		}
		buf, spare = buf[:n], nil
		if _, err := f.ReadAt(buf, pos-n); err != nil && err != io.EOF {
			return "", false
		}
		pos -= n
		stop := len(buf)
		for {
			i := bytes.LastIndexByte(buf[:stop], '\n')
			if i < 0 {
				break
			}
			if check(held(pos+int64(i)+1, buf[i+1:stop])) {
				return key, true
			}
			stop = i
		}
		if len(carry) == 0 && !long {
			lineEnd = pos + int64(stop)
		}
		if !long {
			carry = append([][]byte{buf[:stop]}, carry...)
			if carried += stop; carried > jsonlBuffer {
				long, carry = true, nil
			}
		}
		if long || stop == 0 {
			spare = buf
		}
	}
	if (long || len(carry) > 0) && check(held(0, nil)) {
		return key, true
	}
	return "", found
}
