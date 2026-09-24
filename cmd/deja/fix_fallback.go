package main

import "strings"

// fixFallsBackToRecall answers a fix call that found no command with what the
// sessions said about the error instead.
//
// fix pairs an error with the command a session ran after it, so a session that
// wrote the remedy out in words holds no pair — and the answer was "no session
// on this machine ran a command after that error" over an index that held the
// fix, in the same words, one mode away. Measured in a 12-run A/B on a repo
// whose suite needs a token that is not in the tree: the runs with deja solved
// it 5 of 6, and the one failure is this — the model reached for fix, read the
// dead end and gave up, where recall on the same string answers (#3947).
//
// The lead line says which of the two answers this is. Under a fix call a page
// of sessions with no such line reads as commands to run, and these are not
// that: nobody ran them after this error.
func fixFallsBackToRecall(dir, errText string) (string, int) {
	const lead = "No session ran a command after that error. What the sessions that hit it said about it:\n\n"
	text, sessions, _, _, err := recallTextResult(dir, errText, "", 0, 0, recallMCPBudget-recallFrameOverhead-len(lead))
	if err != nil || sessions == 0 || strings.TrimSpace(text) == "" {
		return "", 0
	}
	return frameRecall(lead + text), sessions
}
