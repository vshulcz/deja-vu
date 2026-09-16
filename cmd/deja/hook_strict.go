package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// Some hosts validate the hook's JSON strictly: any key they do not recognise
// fails validation and the whole output is discarded — not the extra key, the
// whole thing, silently. ZCode is one, and the memory plugin that got there
// first records it as the number-one silent-failure mode of writing for it
// (volcengine/OpenViking, examples/agent-hook-plugin/DESIGN.md).
//
// deja's session-start response carries `systemMessage` beside the context: a
// one-line receipt, because silent success builds no habit. On a strict host
// that receipt costs the context it rides with, so the flag drops it and keeps
// what matters.
//
// A flag rather than an environment variable: a config-file hook is a command
// string with nowhere to put env, and the reason is then visible in the
// config a user reads.
var strictHookOutput bool

// emitHookResponse writes the session-start response, minus anything a strict
// host would reject.
func emitHookResponse(resp sessionStartHookResponse) {
	if strictHookOutput {
		resp.SystemMessage = ""
	}
	b, err := json.Marshal(resp)
	if err != nil {
		return
	}
	fmt.Fprintln(os.Stdout, string(b))
}
