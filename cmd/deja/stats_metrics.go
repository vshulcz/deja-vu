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
	stems := make([]questionStem, 0)
	counts := map[string]int{}
	for _, s := range ss {
		seen := map[string]bool{}
		for _, m := range s.Messages {
			if m.Role != "user" {
				continue
			}
			stem := questionStemFor(m.Text)
			if stem == "" || seen[stem] {
				continue
			}
			seen[stem] = true
			if _, ok := counts[stem]; !ok {
				stems = append(stems, questionStem{value: stem, tokens: strings.Fields(stem)})
			}
			counts[stem]++
		}
	}
	count := 0
	for _, stem := range stems {
		if counts[stem.value] > 1 {
			count++
			continue
		}
		for _, other := range stems {
			if stem.value != other.value && counts[other.value] == 1 && closeQuestion(stem.tokens, other.tokens) {
				count++
				break
			}
		}
	}
	return count
}

type questionStem struct {
	value  string
	tokens []string
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

func closeQuestion(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	seen := make(map[string]bool, len(a))
	for _, token := range a {
		seen[token] = true
	}
	common := 0
	for _, token := range b {
		if seen[token] {
			common++
		}
	}
	union := len(seen)
	for _, token := range b {
		if !seen[token] {
			union++
		}
	}
	return common*100 >= union*80
}
