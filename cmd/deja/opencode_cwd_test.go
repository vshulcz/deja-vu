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

	if !strings.Contains(compact, "async({$,client,directory})") {
		t.Error("the factory does not take the session's directory")
	}
	if !strings.Contains(compact, "constcwd=directory||process.cwd()") {
		t.Error("no fallback for a host that hands over no directory")
	}
	// The two calls that carry a payload say where they are inside it; the
	// bare one is run from there.
	if !strings.Contains(compact, `cd${cwd}&&"/bin/deja"hook-context`) {
		t.Error("hook-context does not run in the project")
	}
	if !strings.Contains(compact, "session_id:sessionID,cwd})") {
		t.Error("the per-prompt payload does not carry the project")
	}
	if !strings.Contains(compact, "session_id:input.sessionID||\"\",cwd,") {
		t.Error("the spawn payload does not carry the project")
	}
	// warmup-status is about the machine, not a project: naming one there
	// would suggest the build is per project.
	at := strings.Index(compact, `"/bin/deja"warmup-status`)
	if at < 0 {
		t.Fatal("warmup-status is gone, so this test guards nothing")
	}
	if strings.HasSuffix(compact[:at], "cd${cwd}&&") {
		t.Error("warmup-status was scoped to a project")
	}
}
