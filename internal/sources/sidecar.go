package sources

import (
	"encoding/json"
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// sidecarStat fingerprints the files beside a transcript that its reader
// opens for the title, workspace or clock: their sizes and mtimes summed, a
// missing one counted as nothing. The file state holds it, so a change to any
// of them re-reads the session (#4446).
func sidecarStat(paths ...string) (size, stamp int64) {
	for _, p := range paths {
		if fi, err := os.Lstat(p); err == nil && fi.Mode().IsRegular() {
			size += fi.Size()
			stamp += fi.ModTime().UnixNano()
		}
	}
	return size, stamp
}

func besideSidecar(name string) func(string) (int64, int64) {
	return func(p string) (int64, int64) { return sidecarStat(filepath.Join(filepath.Dir(p), name)) }
}

// clineSDKSidecar is the <id>.json manifest a Cline CLI rename rewrites alone
// (#4319), named after the session directory, the one the reader opens for
// every transcript in it, not after the transcript.
func clineSDKSidecar(p string) (int64, int64) {
	dir := filepath.Dir(p)
	return sidecarStat(filepath.Join(dir, filepath.Base(dir)+".json"))
}

func reasonixSidecar(p string) (int64, int64) {
	return sidecarStat(p+".meta", strings.TrimSuffix(p, ".jsonl")+".meta.json")
}

func kimiSidecar(p string) (int64, int64) {
	return sidecarStat(filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(p))), "state.json"))
}

// clineVSCodeSidecar fingerprints the task's own entry in state/taskHistory.json.
// That file is shared by every task and rewritten on each turn of any of them,
// so its size and mtime would re-read every task the extension ever ran; the
// fields the reader takes from the entry change only when the task does.
func clineVSCodeSidecar(p string) (int64, int64) {
	taskDir := filepath.Dir(p)
	history := filepath.Join(filepath.Dir(filepath.Dir(taskDir)), "state", "taskHistory.json")
	fi, err := os.Lstat(history)
	if err != nil || !fi.Mode().IsRegular() {
		return 0, 0
	}
	clineHistoryMu.Lock()
	defer clineHistoryMu.Unlock()
	c, ok := clineHistoryStamps[history]
	if !ok || c.size != fi.Size() || c.mtime != fi.ModTime().UnixNano() {
		c = clineHistoryStamp{size: fi.Size(), mtime: fi.ModTime().UnixNano(), entries: map[string]int64{}}
		if b, err := os.ReadFile(history); err == nil {
			var metas []clineTaskMeta
			if json.Unmarshal(b, &metas) == nil {
				for _, m := range metas {
					// The reader takes the first entry under an id.
					if _, seen := c.entries[m.ID]; seen {
						continue
					}
					h := fnv.New64a()
					_, _ = h.Write([]byte(strconv.FormatInt(m.TS, 10) + "\x00" + m.Task + "\x00" + m.CWD))
					c.entries[m.ID] = int64(h.Sum64())
				}
			}
		}
		clineHistoryStamps[history] = c
	}
	if stamp, ok := c.entries[filepath.Base(taskDir)]; ok {
		return 1, stamp
	}
	return 0, 0
}

type clineHistoryStamp struct {
	size, mtime int64
	entries     map[string]int64
}

var (
	clineHistoryMu     sync.Mutex
	clineHistoryStamps = map[string]clineHistoryStamp{}
)
