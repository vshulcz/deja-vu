package main

import (
	"strings"
	"testing"
)

// The moment a command fails is the one an agent never thinks to ask about, and
// tool.execute.after is the only seam opencode gives a plugin for it. The hook
// returns void, so the fix line cannot be handed to the model directly — it is
// appended to the tool's own output, which the next request carries as the tool
// result. Verified in a real opencode run: on an invented error whose repair
// cannot be guessed, the line lands in the tool message of the following
// request.
func TestOpencodePluginCarriesTheFixLineAfterAFailedCommand(t *testing.T) {
	js := opencodePluginJS("/bin/deja")
	compact := strings.Join(strings.Fields(js), "")

	if !strings.Contains(compact, `"tool.execute.after":async(input,output)=>`) {
		t.Fatal("the after-tool channel is not wired")
	}
	// Only bash: the error signature this reads is a shell one, and an edit or
	// a read carries no command that failed.
	if !strings.Contains(compact, `if(input?.tool!=="bash")return`) {
		t.Error("the channel is not scoped to bash")
	}
	// It calls the failure half of the pair, not the pre-tool half.
	if !strings.Contains(compact, `hook-tool-after`) {
		t.Error("the channel does not call hook-tool-after")
	}
	// The command's output is what carries the error, and it goes to deja under
	// the field the hook reads.
	if !strings.Contains(compact, "tool_response:{output:text}") {
		t.Error("the command output is not passed as the tool response")
	}
	// The line is appended to the output, not pushed as a new message: void is
	// all the hook can return, so the tool result is the only channel.
	if !strings.Contains(compact, "output.output=text+") {
		t.Error("the fix line is not folded into the tool output")
	}
	// It runs in the project, like every other call that ranks by history.
	if !strings.Contains(compact, "cwd,") {
		t.Error("the after-call does not carry the project directory")
	}
}
