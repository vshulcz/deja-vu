package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Which session the sidecar is serving, and what it tells the person about it.

// sessionKey is the id deja files this session's injections under: the one a
// session event named, else a key of the sidecar's own until one does.
//
// It used to look for the session in the workspace's sessions-v4 store first,
// for a 1.x host whose session event could land after the first turn. Reasonix
// 2.x keeps no sessions-v4 (projects/<slug>/sessions/<id>.jsonl instead) and
// sends session.start or session.load, naming the transcript, ahead of every
// input.receive; on a 2.30 stand every injection went in under the real id, so
// the scan only ever read a directory that is not there (#4795).
func (x *rxExt) sessionKey() string {
	x.mu.Lock()
	defer x.mu.Unlock()
	s := x.sess
	if s.key == "" {
		s.key = "reasonix-" + strconv.Itoa(os.Getppid()) + "-" + strconv.FormatInt(s.boundary.UnixNano(), 36)
	}
	return s.key
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
		for line := range strings.SplitSeq(b, "\n") {
			line = strings.TrimSpace(line)
			if (strings.HasPrefix(line, "- **") || strings.HasPrefix(line, "- Session: **")) && strings.Contains(line, "`") {
				n++
			}
		}
	}
	return n
}
