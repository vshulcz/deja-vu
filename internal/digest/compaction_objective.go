package digest

import (
	"regexp"
	"strings"
)

// notARequest reports a user turn that cannot be the task being resumed, so the
// objective goes on to the request before it. Read off the objectives of 39 real
// packets across Claude Code, opencode and Hermes:
//
//   - a status poll: "ну что?" became the objective of a session whose last
//     request was a research task four turns up;
//   - a bare slash command: "/compact" is an instruction to the harness;
//   - a notice the harness writes into the user role: Hermes' "[ASYNC
//     DELEGATION BATCH COMPLETE — …]", "[IMPORTANT: You are running as a
//     scheduled cron job …]" and "[Your active task list was preserved across
//     context compression]". The user's own bracketed turns ("[выполни: …]",
//     "[29.06.2026 17:45] Vlad: …") do not open with a capitalised notice.
func notARequest(text string) bool {
	t := strings.TrimSpace(text)
	if objectiveSlashRE.MatchString(t) || objectiveNoticeRE.MatchString(t) ||
		strings.HasPrefix(t, "[Your active task list was preserved") {
		return true
	}
	return onlyPolls(t)
}

var (
	objectiveSlashRE  = regexp.MustCompile(`^/[a-z][\w:-]*$`)
	objectiveNoticeRE = regexp.MustCompile(`^\[[A-Z]{3,}(?:[ _-][A-Z]{2,})*\s*(?:[:\]—–-]|$)`)
)

// pollWords ask how the work is going and nothing else. They carry no task, so
// the request they follow is still the one being worked on.
var pollWords = []string{
	"ну что", "ну что там", "что там", "как там", "ну как", "как дела", "и что",
	"ну", "что", "status", "any update", "any updates", "update", "how's it going",
	"how is it going", "where are we", "what's the status",
}

func onlyPolls(text string) bool {
	low := strings.ToLower(strings.TrimSpace(text))
	low = strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		switch r {
		case ',', ';', '.', '!', '?', ':', '…':
			return ' '
		}
		return r
	}, low)), " ")
	if low == "" {
		return false
	}
	for low != "" {
		best := ""
		for _, w := range pollWords {
			if len(w) > len(best) && (low == w || strings.HasPrefix(low, w+" ")) {
				best = w
			}
		}
		if best == "" {
			return false
		}
		low = strings.TrimSpace(strings.TrimPrefix(low, best))
	}
	return true
}
