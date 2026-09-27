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

// sessionKey is the id deja files this session's injections under. The
// sidecar is told only a host-local id ("boot-1" on 1.39.1), so the real one
// is read off the session store: the one sessions-v4 directory in this
// workspace written since the session began. With none or several, a key of
// the sidecar's own stands in — still one reader, just not one deja can match
// to the transcript it will later index.
func (x *rxExt) sessionKey() string {
	x.mu.Lock()
	s, workspace := x.sess, x.workspace
	if s.realKey {
		defer x.mu.Unlock()
		return s.key
	}
	since := s.boundary
	x.mu.Unlock()
	id := reasonixLiveSession(sources.ReasonixWorkspaceStore(workspace), since.Add(-rxSessionRaceWindow))
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.sess != s {
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
// transcript or manifest changed at or after since, or "" when there is not
// exactly one.
func reasonixLiveSession(store string, since time.Time) string {
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
		dir := filepath.Join(store, e.Name())
		fresh := false
		for _, name := range []string{"events.frames", "manifest.json"} {
			if fi, err := os.Stat(filepath.Join(dir, name)); err == nil && !fi.ModTime().Before(since) {
				fresh = true
			}
		}
		if !fresh {
			continue
		}
		if found != "" {
			return ""
		}
		found = e.Name()
		var m struct {
			SessionID string `json:"sessionId"`
		}
		if b, err := os.ReadFile(filepath.Join(dir, "manifest.json")); err == nil && json.Unmarshal(b, &m) == nil && m.SessionID != "" {
			found = m.SessionID
		}
	}
	return found
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
