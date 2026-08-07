package main

import (
	"fmt"
	"strings"

	"github.com/vshulcz/deja-vu/internal/stats"
)

func statsHeadline(r stats.Report) string {
	parts := make([]string, 0, 3)
	if r.TotalSessions > 0 {
		parts = append(parts, fmt.Sprintf("%s session%s indexed", formatStatNumber(r.TotalSessions), pluralS(r.TotalSessions)))
	}
	// Recalls and injections both handed memory to an agent; the headline is
	// about that total, so it sums them rather than reading a field that used
	// to carry both.
	if handed := r.Recall.Recalls + r.Recall.Injections; handed > 0 {
		served := "times"
		if handed == 1 {
			served = "time"
		}
		parts = append(parts, fmt.Sprintf("memory served %s %s", formatStatNumber(handed), served))
	}
	if r.RepeatQuestions > 0 {
		parts = append(parts, fmt.Sprintf("%s questions asked more than once", formatStatNumber(r.RepeatQuestions)))
	}
	return strings.Join(parts, " · ")
}

func formatStatNumber(n int) string {
	s := fmt.Sprintf("%d", n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
