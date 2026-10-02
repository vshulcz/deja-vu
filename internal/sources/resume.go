package sources

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
)

// The helpers here serve the Resumes hooks and the offset reads that need to
// know what came before the offset: the reply a streamed chunk joins, the
// time a record takes from the one before it. Both walks stop at the first
// line that answers, so a long transcript is not read from its first byte.

// eachLineBefore hands fn the complete lines that end at or before end, last
// first, until fn returns false.
func eachLineBefore(path string, end int64, fn func(line []byte) bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	var carry []byte // a line whose head is in a chunk not read yet
	for pos := end; pos > 0; {
		n := min(int64(64<<10), pos)
		buf := make([]byte, n, n+int64(len(carry)))
		if _, err := f.ReadAt(buf, pos-n); err != nil && err != io.EOF {
			return
		}
		pos -= n
		buf = append(buf, carry...)
		for {
			i := bytes.LastIndexByte(buf, '\n')
			if i < 0 {
				break
			}
			if line := trimJSONSpace(buf[i+1:]); len(line) > 0 && !fn(line) {
				return
			}
			buf = buf[:i]
		}
		carry = buf
	}
	if line := trimJSONSpace(carry); len(line) > 0 {
		fn(line)
	}
}

// eachLineFrom hands fn the lines from offset on, until fn returns false.
func eachLineFrom(path string, offset int64, fn func(line []byte) bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReader(io.NewSectionReader(f, offset, 1<<62))
	for {
		line, err := r.ReadBytes('\n')
		if line = trimJSONSpace(line); len(line) > 0 && !fn(line) {
			return
		}
		if err != nil {
			return
		}
	}
}

// decodeJSONLine decodes one line the way scanJSONLFromOffset does, numbers
// kept as json.Number. nil when it is not an object.
func decodeJSONLine(line []byte) map[string]any {
	var m map[string]any
	d := json.NewDecoder(bytes.NewReader(line))
	d.UseNumber()
	if d.Decode(&m) != nil {
		return nil
	}
	return m
}

// resumesUnlessJoined is the Resumes answer for a reader that joins a
// streamed reply's records by a key. key reports the join key of a line that
// starts or continues a message, ok=false for a line that does neither. The
// tail can be appended on its own unless its first such line joins the
// message the stored part ended on: that join needs the stored half, and
// appending kept the halves as two replies (#4445).
func resumesUnlessJoined(path string, offset int64, key func(line []byte) (string, bool)) bool {
	if offset <= 0 {
		return true
	}
	last := ""
	eachLineBefore(path, offset, func(line []byte) bool {
		k, ok := key(line)
		if ok {
			last = k
		}
		return !ok
	})
	if last == "" {
		return true
	}
	next := ""
	eachLineFrom(path, offset, func(line []byte) bool {
		k, ok := key(line)
		if ok {
			next = k
		}
		return !ok
	})
	return next != last
}
