package main

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/model"
)

func statsHeadline(r statsReport) string {
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

// repeatQuestions is a corpus proxy because the usage sidecar does not store query text.
func repeatQuestions(ss []model.Session) int {
	// Exact stem match only: questionStemFor already folds case and
	// punctuation, and a pairwise similarity pass is quadratic in corpora
	// with tens of thousands of user messages.
	counts := map[string]int{}
	for _, s := range ss {
		seen := map[string]bool{}
		for _, m := range s.Messages {
			if m.Role != "user" {
				continue
			}
			stem := questionStemFor(m.Text)
			// Short acknowledgements ("ok", "continue") repeat across every
			// session; only substantial messages count as questions.
			if stem == "" || len(strings.Fields(stem)) < 4 || seen[stem] {
				continue
			}
			seen[stem] = true
			counts[stem]++
		}
	}
	count := 0
	for _, n := range counts {
		if n > 1 {
			count++
		}
	}
	return count
}

func questionStemFor(text string) string {
	if statNoise(text) {
		return ""
	}
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
