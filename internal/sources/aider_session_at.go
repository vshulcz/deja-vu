package sources

import (
	"bufio"
	"os"
	"strings"
	"time"
)

// AiderSessionAt is the id ParseAiderFile gives the session an aider started
// at launch is writing, when the file was offset bytes long then: the one
// header past offset stamped from launch's second to a minute after it. ""
// when no header is there yet, or when two are: two aiders started together on
// one history, each a second or two before writing its header, and nothing
// says which is whose. A file shorter than offset was replaced, and its last
// session is the one being written.
//
// The id hangs on every header before it (two launches in one second count
// up), so the whole file is walked, with the parser's own rule for what a
// header is: the mark at the start of a line outside a code fence.
func AiderSessionAt(path string, offset int64, launch time.Time) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	if st, err := f.Stat(); err != nil || st.Size() < offset {
		offset = -1
	}
	starts := map[string]int{}
	inFence := false
	idx := 0
	last, found := "", ""
	from, until := launch.Truncate(time.Second), launch.Add(time.Minute)
	var pos int64
	r := bufio.NewReader(f)
	for {
		raw, readErr := r.ReadString('\n')
		if raw == "" && readErr != nil {
			break
		}
		at := pos
		pos += int64(len(raw))
		line := strings.TrimRight(strings.TrimPrefix(raw, "\ufeff"), " \t\r\n")
		if !inFence && strings.HasPrefix(line, aiderSessionMark) {
			idx++
			ts, _ := time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(strings.TrimPrefix(line, aiderSessionMark)), time.Local)
			id := aiderSessionID(path, ts, idx, starts)
			if offset >= 0 && at >= offset && !ts.Before(from) && !ts.After(until) {
				if found != "" {
					return ""
				}
				found = id
			}
			last = id
			inFence = false
			continue
		}
		// Before the first header the parser reads nothing, fences included.
		if idx > 0 && strings.HasPrefix(line, "```") {
			inFence = !inFence
		}
	}
	if offset < 0 {
		return last
	}
	return found
}
