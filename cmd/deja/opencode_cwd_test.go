package main

import (
	"strings"
	"testing"
)

// deja ranks by project, and the project is a directory. The plugin took the
// server process's cwd, which is where opencode was launched rather than where
// the work is — measured on this machine, the same question asked from the
// repository, from the home directory and from /tmp returned that project's
// sessions, another project's sessions, and nothing at all.
//
// opencode hands the plugin the session's own directory. Every call that ranks
// by project has to say so.
func TestOpencodePluginRunsInTheProjectDirectory(t *testing.T) {
	js := opencodePluginJS("/bin/deja")
	compact := strings.Join(strings.Fields(js), "")

	if !strings.Contains(compact, "ctx.location?.directory") {
		t.Error("the plugin does not read the instance's location")
	}
	if !strings.Contains(compact, "constcwd=ctx.location?.directory||process.cwd()") {
		t.Error("no fallback for a host that hands over no directory")
	}
	// Every call says where it is inside its payload, and hook-context is
	// also run from there.
	if !strings.Contains(compact, `runHook("hook-context",JSON.stringify({session_id:event.sessionID||"",parent_session_id:awaitparentOf(event.sessionID),cwd}),cwd)`) {
		t.Error("hook-context does not run in the project")
	}
	if !strings.Contains(compact, `session_id:sid,parent_session_id:awaitparentOf(sid),cwd}`) {
		t.Error("the per-prompt payload does not carry the project")
	}
	if !strings.Contains(compact, `session_id:event.sessionID||"",cwd,`) {
		t.Error("the spawn payload does not carry the project")
	}
}
