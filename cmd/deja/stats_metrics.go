package main

import (
	"fmt"
	"strings"

	"github.com/vshulcz/deja-vu/internal/stats"
)

func statsHeadline(r stats.Report) string {
	parts := make([]string, 0, 3)
	if r.TotalSessions > 0 {
		parts = append(parts, fmt.Sprintf("%s sessions indexed", formatStatNumber(r.TotalSessions)))
	}
	if r.Recall.Recalls > 0 {
		parts = append(parts, fmt.Sprintf("memory served %s times", formatStatNumber(r.Recall.Recalls)))
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
