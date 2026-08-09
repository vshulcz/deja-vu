package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Negative knowledge — "we tried X, it did not work" — is the most expensive
// thing a store can lose, and deja already models it: `promote --state
// rejected`. On a real 1165-session store, exactly zero sessions carry that
// state, because it is set by hand after the fact and nobody goes back.
//
// Asking for it is hopeless too: "what did we decide" is 0.4% of user turns on
// that store. People do not query decisions; they state outcomes, in the
// moment, in the middle of the work — 2.2% of sessions say in so many words
// that something was backed out.
//
// So the nudge goes where the evidence appears: the sentence where the user
// says it failed. One line, once every twenty minutes at most, and only when
// the user's own words carry the report. It asks for `remember` rather than
// `promote --state rejected` because in that moment the session that failed is
// the one being written and has no id yet.

const nudgeGap = 20 * time.Minute

// failureNudge returns the line to add when the user has just reported backing
// something out, or "" for the silence that is the default.
func failureNudge(dir, prompt string) string {
	if !reportsFailure(prompt) {
		return ""
	}
	if !nudgeDue(dir) {
		return ""
	}
	return "The user just said an approach did not hold. Dead ends are the one thing this store never records — if that is what happened, save it now with `deja remember \"<what was tried, why it failed>\"`, in one line, before moving on."
}

// reportsFailure reads the user's own words, line by line, using the same rule
// the index uses when it marks a session as reporting a dead end.
func reportsFailure(prompt string) bool {
	for _, line := range strings.Split(prompt, "\n") {
		if _, ok := index.GiveUpLine(line); ok {
			return true
		}
	}
	return false
}

// nudgeDue rate-limits the line the way the déjà vu line is rate-limited: a
// prompt hook is paid on every message, and advice repeated on every message
// is noise within a minute.
func nudgeDue(dir string) bool {
	p := dir + ".nudge"
	if b, err := os.ReadFile(p); err == nil {
		if ts, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil && time.Since(time.Unix(ts, 0)) < nudgeGap {
			return false
		}
	}
	_ = os.WriteFile(p, []byte(strconv.FormatInt(time.Now().Unix(), 10)), 0o600)
	return true
}

// emitNudgeOnly writes the nudge when there is no recall to carry it. It
// returns nil either way: this hook's miss contract is silence.
func emitNudgeOnly(stdout io.Writer, plain bool, nudge string) error {
	if nudge == "" {
		return nil
	}
	out := frameRecall(nudge)
	if plain {
		fmt.Fprintln(stdout, out)
		return nil
	}
	var resp sessionStartHookResponse
	resp.HookSpecificOutput.HookEventName = "UserPromptSubmit"
	resp.HookSpecificOutput.AdditionalContext = out
	b, err := json.Marshal(resp)
	if err != nil {
		return nil
	}
	fmt.Fprintln(stdout, string(b))
	return nil
}
