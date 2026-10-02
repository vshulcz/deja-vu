package main

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"time"
)

// claudeEventSince is the first Claude Code release that knows an event, for
// the events newer than the rest. Before it the settings schema is a closed
// list, and one unknown key fails the whole settings.json: Claude Code 2.0.55
// ignores the file, the user's permissions and model with it (#4488).
var claudeEventSince = map[string][3]int{"PostToolUseFailure": {2, 0, 56}}

// claudeVersionReal is the version of the claude on PATH; ok is false when
// there is none to ask, and then the current release is assumed.
var claudeVersionReal = func() (v [3]int, ok bool) {
	exe, err := exec.LookPath("claude")
	if err != nil {
		return v, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, "--version").Output()
	if err != nil {
		return v, false
	}
	m := regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`).FindSubmatch(out)
	if m == nil {
		return v, false
	}
	for i := range v {
		v[i], _ = strconv.Atoi(string(m[i+1]))
	}
	return v, true
}

// claudeVersion is what install and doctor ask, indirected so a test can say
// which Claude Code is installed.
var claudeVersion = claudeVersionReal

// claudeWiring is claudeHookWiring less the events the installed Claude Code
// would reject.
func claudeWiring() []struct{ Event, Sub, Matcher string } {
	v, ok := claudeVersion()
	if !ok {
		return claudeHookWiring
	}
	var out []struct{ Event, Sub, Matcher string }
	for _, h := range claudeHookWiring {
		if since, newer := claudeEventSince[h.Event]; newer && versionBefore(v, since) {
			continue
		}
		out = append(out, h)
	}
	return out
}

func versionBefore(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
