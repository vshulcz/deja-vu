package ctxcache

import "strings"

// Incomplete scans always need validation, even if their partial digest is
// unchanged. Required gaps survive packet trimming. Remove only our own gap
// once the current scan is complete; caller-authored gaps remain intact.
func worktreeValidationGap(state *State, digest string) {
	const subject = "incomplete worktree fingerprint"
	const source = "git://local/fingerprint"
	gaps := state.Gaps[:0]
	for _, gap := range state.Gaps {
		if gap.Subject != subject || gap.Source != source {
			gaps = append(gaps, gap)
		}
	}
	state.Gaps = gaps
	if strings.HasPrefix(digest, "partial:") {
		state.Gaps = append(state.Gaps, Gap{
			Subject: subject, Severity: "required", Source: source,
			Reason:        "the Git scan was incomplete or untracked content exceeded the file or byte limits; worktree freshness is unverified",
			RetrievalHint: "inspect relevant changes, exclude generated artifacts with Git ignore rules, then checkpoint validated state",
		})
	}
}
