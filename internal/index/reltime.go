package index

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/query"
)

// relTimeRE matches the relative-time phrases relativeTimeTerms understands.
var relTimeRE = regexp.MustCompile(`(?i)\b(?:(a|an|one|two|three|four|five|six|seven|eight|nine|ten|\d{1,3})\s+(day|week|month|year)s?\s+ago|(yesterday)|last\s+(week|month|year|monday|tuesday|wednesday|thursday|friday|saturday|sunday))\b`)

var relTimeNums = map[string]int{"a": 1, "an": 1, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10}

// monthNames maps what people type to a month. Russian is here because the
// tool is used in both languages in the same session, and someone asking "что
// делали в мае" means the same thing as "what did we do in may".
var monthNames = map[string]time.Month{
	"january": time.January, "february": time.February, "march": time.March,
	"april": time.April, "may": time.May, "june": time.June, "july": time.July,
	"august": time.August, "september": time.September, "october": time.October,
	"november": time.November, "december": time.December,
	"jan": time.January, "feb": time.February, "mar": time.March, "apr": time.April,
	"jun": time.June, "jul": time.July, "aug": time.August, "sep": time.September,
	"sept": time.September, "oct": time.October, "nov": time.November, "dec": time.December,
	"январь": time.January, "января": time.January, "январе": time.January,
	"февраль": time.February, "февраля": time.February, "феврале": time.February,
	"март": time.March, "марта": time.March, "марте": time.March,
	"апрель": time.April, "апреля": time.April, "апреле": time.April,
	"май": time.May, "мая": time.May, "мае": time.May,
	"июнь": time.June, "июня": time.June, "июне": time.June,
	"июль": time.July, "июля": time.July, "июле": time.July,
	"август": time.August, "августа": time.August, "августе": time.August,
	"сентябрь": time.September, "сентября": time.September, "сентябре": time.September,
	"октябрь": time.October, "октября": time.October, "октябре": time.October,
	"ноябрь": time.November, "ноября": time.November, "ноябре": time.November,
	"декабрь": time.December, "декабря": time.December, "декабре": time.December,
}

var wordRE = regexp.MustCompile(`[\p{L}]+`)

// monthOccurrence resolves a bare month name to its most recent occurrence:
// asking about may in july means this may, not next year's.
func monthOccurrence(m time.Month, now time.Time) time.Time {
	year := now.Year()
	if m > now.Month() {
		year--
	}
	return time.Date(year, m, 1, 0, 0, 0, 0, now.Location())
}

// relativeTimeTerms turns relative-time phrases in a query into the month
// tokens the ingest writes for every message, so "which book did I finish a
// week ago" can meet sessions from that month structurally. Only the
// relevance tier consumes these — they are OR-scored hints, never an AND.
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
	for _, w := range wordRE.FindAllString(strings.ToLower(q), -1) {
		if month, ok := monthNames[w]; ok {
			add(monthOccurrence(month, now))
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
