package main

import (
	"strings"
	"sync/atomic"
	"time"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// mcpClient is the clientInfo.name the host sent in initialize: one host per
// stdio server process.
var mcpClient atomic.Value

func setMCPClient(name string) { mcpClient.Store(name) }

func mcpClientName() string {
	s, _ := mcpClient.Load().(string)
	return s
}

// openclawAskerWindow is how recently the asking session has to have been
// written. An agent mid-turn wrote its transcript seconds ago; a session the
// user left a few minutes back is the one most likely to hold the answer, so
// the window stays short.
const openclawAskerWindow = 5 * time.Minute

// openclawUnstampedAsker names the session an OpenClaw MCP call comes from
// when no hook stamped it. `openclaw chat` and `openclaw tui --local` run no
// plugin or hook at all, so nothing marks the session live, and recall
// answered with the conversation asking (#5271). The one OpenClaw's store was
// written to last, in the last few minutes, is that conversation: its prompt
// went in before the model was asked.
//
// Unless a hook stamped some session about as recently. Where hooks run they
// name the asker themselves, and the newest write can be something else: a
// heartbeat poll on the main DM, which stamps nothing since #5270.
func openclawUnstampedAsker(dir string) string {
	if !strings.HasPrefix(strings.ToLower(mcpClientName()), "openclaw") {
		return ""
	}
	id, at := sources.OpenClawNewestSession(time.Now().Add(-openclawAskerWindow))
	if id == "" || liveSessionIDs(dir)[id] {
		return ""
	}
	for _, when := range readLiveSessions(dir) {
		if when.After(at.Add(-time.Minute)) {
			return ""
		}
	}
	return id
}
