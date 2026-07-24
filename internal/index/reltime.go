package index

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/query"
)

// relativeTimeTerms turns relative-time phrases in a query into the month
// tokens the ingest writes for every message, so "which book did I finish a
// week ago" can meet sessions from that month structurally. Only the
// relevance tier consumes these — they are OR-scored hints, never an AND.
var relTimeRE = regexp.MustCompile(`(?i)\b(?:(a|an|one|two|three|four|five|six|seven|eight|nine|ten|\d{1,3})\s+(day|week|month|year)s?\s+ago|(yesterday)|last\s+(week|month|year|monday|tuesday|wednesday|thursday|friday|saturday|sunday))\b`)

var relTimeNums = map[string]int{"a": 1, "an": 1, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10}

func relativeTimeTerms(q string, now time.Time) []string {
	if now.IsZero() {
		now = time.Now()
	}
	seen := map[string]bool{}
	var out []string
	add := func(t time.Time) {
		for _, tok := range []string{t.Format("2006-01"), strings.ToLower(t.Month().String())} {
			if !seen[tok] {
				seen[tok] = true
				out = append(out, tok)
			}
		}
	}
	for _, m := range relTimeRE.FindAllStringSubmatch(q, -1) {
		switch {
		case m[3] != "": // yesterday
			add(now.AddDate(0, 0, -1))
		case m[4] != "": // last X
			switch m[4] {
			case "week":
				add(now.AddDate(0, 0, -7))
			case "month":
				add(now.AddDate(0, -1, 0))
			case "year":
				add(now.AddDate(-1, 0, 0))
			default: // weekday: within the previous 7 days
				add(now.AddDate(0, 0, -7))
			}
		default:
			n := relTimeNums[strings.ToLower(m[1])]
			if n == 0 {
				n, _ = strconv.Atoi(m[1])
			}
			if n == 0 {
				continue
			}
			switch strings.ToLower(m[2]) {
			case "day":
				add(now.AddDate(0, 0, -n))
			case "week":
				add(now.AddDate(0, 0, -7*n))
			case "month":
				add(now.AddDate(0, -n, 0))
			case "year":
				add(now.AddDate(-n, 0, 0))
			}
		}
	}
	return out
}

// RelevanceTermsWithTime is RelevanceTerms plus resolved relative-time month
// hints, exported so bench harnesses mirror the production expansion.
func RelevanceTermsWithTime(q string, now time.Time) []string {
	return append(RelevanceTerms(q), relativeTimeTerms(q, now)...)
}

var _ = query.TierRelevance
