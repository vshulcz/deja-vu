package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Which session the sidecar is serving, and what it tells the person about it.

// sessionKey is the id deja files this session's injections under: the one a
// session event named, else the one sessions-v4 directory in this workspace
// whose manifest says it was created after this session began and that no
// earlier session in this process was filed under. With none or several, a
// key of the sidecar's own stands in — still one reader, just not one deja
// can match to the transcript it will later index.
func (x *rxExt) sessionKey() string {
	x.mu.Lock()
	s, workspace := x.sess, x.workspace
	if s.realKey {
		defer x.mu.Unlock()
		return s.key
	}
	since := s.boundary
	exclude := make(map[string]bool, len(x.retired))
	for k := range x.retired {
		exclude[k] = true
	}
	x.mu.Unlock()
	id := reasonixLiveSession(sources.ReasonixWorkspaceStore(workspace), since, exclude)
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.sess != s {
		return s.key
	}
	if s.realKey {
		return s.key
	}
	if id != "" {
		s.key, s.realKey = id, true
		return s.key
	}
	if s.key == "" {
		s.key = "reasonix-" + strconv.Itoa(os.Getppid()) + "-" + strconv.FormatInt(s.boundary.UnixNano(), 36)
	}
	return s.key
}

// reasonixLiveSession is the id of the one session directory in store whose
// manifest records a creation at or after since, leaving out the ids in
// exclude, or "" when there is not exactly one. A write time says nothing
// here: the session a /new replaced is saved as it ends, and a parallel
// Reasonix in the same workspace writes whenever it likes.
func reasonixLiveSession(store string, since time.Time, exclude map[string]bool) string {
	if store == "" {
		return ""
	}
	entries, err := os.ReadDir(store)
	if err != nil {
		return ""
	}
	found := ""
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		var m struct {
			SessionID string    `json:"sessionId"`
			CreatedAt time.Time `json:"createdAt"`
		}
		b, err := os.ReadFile(filepath.Join(store, e.Name(), "manifest.json"))
		if err != nil || json.Unmarshal(b, &m) != nil || m.CreatedAt.IsZero() || m.CreatedAt.Before(since) {
			continue
		}
		id := m.SessionID
		if id == "" {
			id = e.Name()
		}
		if exclude[id] {
			continue
		}
		if found != "" {
			return ""
		}
		found = id
	}
	return found
}

// reasonixSessionIDFrom reads a session event's sessionPath: a bare session
// id on the 1.x binding, or a path — a sessions-v4 directory, whose manifest
// holds the id, or a JSONL transcript, named for it.
func reasonixSessionIDFrom(path string) string {
	v := strings.TrimSpace(path)
	if v == "" {
		return ""
	}
	if !strings.ContainsAny(v, `/\`) {
		return v
	}
	dir := v
	if filepath.Base(v) == "events.frames" || filepath.Base(v) == "manifest.json" {
		dir = filepath.Dir(v)
	}
	var m struct {
		SessionID string `json:"sessionId"`
	}
	if b, err := os.ReadFile(filepath.Join(dir, "manifest.json")); err == nil && json.Unmarshal(b, &m) == nil && m.SessionID != "" {
		return m.SessionID
	}
	return strings.TrimSuffix(filepath.Base(dir), ".jsonl")
}

// surface tells the person what deja did, on the host's own status and
// notification lines: while the first index builds, and once per session
// when recall first lands.
func (x *rxExt) surface(s *rxSession, blocks []string) {
	x.mu.Lock()
	ui, sessionID, generation := x.ui, x.hostSession, x.generation
	x.mu.Unlock()
	if !ui || sessionID == "" {
		return
	}
	notice := strings.TrimPrefix(buildNotice(x.dir), "deja: ")
	x.mu.Lock()
	publishStatus := ""
	switch {
	case notice != "" && notice != x.lastStatus:
		publishStatus, x.lastStatus = notice, notice
	case notice == "" && x.lastStatus != "" && x.lastStatus != rxIndexReady:
		publishStatus, x.lastStatus = rxIndexReady, rxIndexReady
	}
	announce := 0
	if !s.announced {
		if n := recalledSessionCount(blocks); n > 0 {
			announce, s.announced = n, true
		}
	}
	x.mu.Unlock()
	if publishStatus != "" {
		x.conn.notify("host/ui/publish", map[string]any{
			"surfaceId": "deja-index", "sessionId": sessionID, "generation": generation, "kind": "status",
			"payload": map[string]string{"label": "index", "detail": publishStatus, "severity": "info"},
		})
	}
	if announce > 0 {
		x.conn.notify("host/ui/publish", map[string]any{
			"surfaceId": "deja-recall", "sessionId": sessionID, "generation": generation, "kind": "notification",
			"payload": map[string]string{"title": fmt.Sprintf("recalled %d prior session%s", announce, pluralS(announce)), "severity": "info"},
		})
	}
}

const rxIndexReady = "index ready — recall is on"

// recalledSessionCount counts the sessions recall blocks name. Each one is a
// bullet with the project in bold and the short id in backticks: at the top
// level in the per-prompt block, under "Session:" in the digest.
func recalledSessionCount(blocks []string) int {
	n := 0
	for _, b := range blocks {
		for _, line := range strings.Split(b, "\n") {
			line = strings.TrimSpace(line)
			if (strings.HasPrefix(line, "- **") || strings.HasPrefix(line, "- Session: **")) && strings.Contains(line, "`") {
				n++
			}
		}
	}
	return n
}
