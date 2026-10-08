package sources

import (
	"regexp"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// onlySummaries reports whether ms holds nothing but compaction summaries,
// which is also true of no messages at all.
func onlySummaries(ms []model.Message) bool {
	for _, m := range ms {
		if m.Role != RoleSummary {
			return false
		}
	}
	return true
}

// antigravityCheckpointMarker is the line a CHECKPOINT step opens with.
var antigravityCheckpointMarker = regexp.MustCompile(`^\{\{ CHECKPOINT \d+ \}\}$`)

// antigravityCheckpointSummary is the summary a CHECKPOINT step carries,
// without the marker line and the two notices Antigravity wraps it in: that the
// earlier steps were truncated, and that the model must not acknowledge it. A
// checkpoint with nothing else in it is "".
func antigravityCheckpointSummary(content string) string {
	var keep []string
	for _, line := range strings.Split(content, "\n") {
		t := strings.TrimSpace(line)
		if antigravityCheckpointMarker.MatchString(t) ||
			strings.HasPrefix(t, "**The earlier parts of this conversation have been truncated") ||
			strings.HasPrefix(t, "**IMPORTANT: this summary is just for your reference") {
			continue
		}
		keep = append(keep, line)
	}
	return strings.TrimSpace(strings.Join(keep, "\n"))
}
