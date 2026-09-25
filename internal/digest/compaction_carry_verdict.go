package digest

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	carryVerdRE     = regexp.MustCompile(carryLB + `(?:(не подтвердил(?:ась|ся|ось|ись)|подтвердил(?:ась|ся|ось|ись)|не подтвержден[аоы]?|не подтверждён[аоы]?|подтвержден[аоы]?|подтверждён[аоы]?|отвергнут[аоы]?|опровергнут[аоы]?)|(REJECTED|CONFIRMED|REFUTED|rejected|confirmed|refuted|вердикт:|verdict:))` + carryRB)
	carrySubjIDRE   = regexp.MustCompile("(?:#\\d{2,6}|(?:^|[^\\p{L}])[HН]\\d{1,4}(?:$|[^\\p{N}])|«[^»]{2,}»|\"[^\"]{2,}\"|`[^`]{2,}`|\\[[^\\]]{2,}\\])")
	carryFillerRE   = regexp.MustCompile(`(?i)^(?:и|а|но|это|то|оно|она|он|моя|мой|твоя|твой|наша|наш|их|его|её|при этом|тоже|также|всё|все|так|значит|итак|вот|ты|прав|сегодня|теперь|the|my|your|it|this|that|and|but|also)$`)
	carryStatusesRE = regexp.MustCompile(`(?:^|[^A-Z_])(?:DONE|FAILED|PENDING|OPEN|FIXED|SUCCESS|PROCESSING|ACCEPTED|APPROVED)(?:$|[^A-Z_])`)
	carryHIDRE      = regexp.MustCompile(`(?:#\d{2,6}|(?:^|[^\p{L}])[HН]\d{1,4}(?:$|[^\p{N}]))`)
	carryTopicRE    = regexp.MustCompile(`(?i)^\p{L}*\s+(?:про|о|об|со|с|насчёт|что|по поводу|about|that|on|of)\s|^\p{L}*\s*[«"]|^\p{L}*,\s*что\s`)
	carryExplainRE  = regexp.MustCompile(`^[^.:—]{0,20}[:—]\s*\S.{12,}`)
	carryClaimRE    = regexp.MustCompile(`(?i)(гипотез|догадк|подозрени|предположени|утверждени|заявк|верси|теори|иде[яи]|опасени|вывод|цифр|числ|эффект|находк|диагноз|причин|claim|finding|hypothes|suspicion|theory)`)
	carryNumSubjRE  = regexp.MustCompile(`(?i)^(?:\d+|одна|один|исходная|исходное|две|два|три|четыре|пять|обе|оба|все|несколько|many|two|three|both|all)\s`)
	carryBareHeadRE = regexp.MustCompile(`(?i)^(?:что|чего|какие|какая|which|what)(?:$|[^\p{L}])`)
	carryWordRE     = regexp.MustCompile(`[\p{L}\p{N}]{3,}`)
	carrySentenceRE = regexp.MustCompile(`[.;!?]\s`)
	carryClauseRE   = regexp.MustCompile(`[,;:—–]\s`)
	carryThatRE     = regexp.MustCompile(`(?i)^(?:что|that)\s`)
	carryCondRE     = regexp.MustCompile(`(?i)(?:^|[^\p{L}])(?:если|if|when|когда)\s`)
	carrySepRE      = regexp.MustCompile(`[—–:-]\s*$`)
	carryEnumTail   = regexp.MustCompile(`^\s*[\d(–-]`)

	// Text right before a verdict word that makes it something else.
	carryVerdictSkips = []*regexp.Regexp{
		regexp.MustCompile(`(?:^|[\s,(])\d+\s*$`), // "10 REJECTED" is a count
		regexp.MustCompile(`(?i)(?:^|[^\p{L}])(?:один|одна|два|две|три|one|two|three|что|what|which|если|if)\s+$`),
		regexp.MustCompile(`[=_.('"]\s*$`),   // code enums, quoted literals
		regexp.MustCompile(`[\p{L}\p{N}]-$`), // "filter-rejected" is an identifier
		regexp.MustCompile(`(?i)(?:^|[^\p{L}])(?:был|была|было|были|was|were|had been)\s+$`), // past narrative
	}
)

// lastSplit is the last piece of s split on rx, Python's re.split(rx, s)[-1].
func lastSplit(rx *regexp.Regexp, s string) string {
	locs := rx.FindAllStringIndex(s, -1)
	if len(locs) == 0 {
		return s
	}
	return s[locs[len(locs)-1][1]:]
}

func carryWords(s string) []string {
	var out []string
	for _, w := range carryWordRE.FindAllString(s, -1) {
		if !carryFillerRE.MatchString(w) {
			out = append(out, w)
		}
	}
	return out
}

// carryVerdict reports a verdict word with an identifiable subject before it on
// the same line: "H487 отвергнута", "Cache hypothesis — REJECTED". A verdict
// word alone is not enough; on the 189 compactions most of them were counts,
// status enums, quotes or questions.
func carryVerdict(s string) bool {
	raw := carryCodeRE.ReplaceAllString(strings.ReplaceAll(s, "**", ""), " ")
	tail := strings.TrimRight(raw, " *")
	if strings.HasSuffix(tail, "?") {
		return false
	}
	if carryBareHeadRE.MatchString(raw) && strings.HasSuffix(tail, ":") { // "Что из #2337 подтверждено ...:"
		return false
	}
next:
	for _, m := range carryVerdRE.FindAllStringSubmatchIndex(raw, -1) {
		a, e := m[2], m[3]
		russian := a >= 0
		if !russian {
			a, e = m[4], m[5]
		}
		w, before := raw[a:e], raw[:a]
		for _, rx := range carryVerdictSkips {
			if rx.MatchString(before) {
				continue next
			}
		}
		if carryCondRE.MatchString(lastSplit(carrySentenceRE, before)) ||
			strings.Count(before, "«") > strings.Count(before, "»") || strings.Count(before, "(") > strings.Count(before, ")") {
			continue
		}
		after := strings.Trim(raw[e:], " *.")
		if after == ":" || (strings.HasSuffix(tail, ":") && utf8.RuneCountInString(after) < 3) { // "...подтверждены замером:"
			continue
		}
		after = strings.Trim(after, ":")
		sep := carrySepRE.MatchString(before)
		subj := strings.Trim(lastSplit(carrySentenceRE, before), " —–-:*#")
		if subj == "" || (carryBareHeadRE.MatchString(subj) && after == "") { // "Что не подтвердилось:"
			continue
		}
		upper := carryIsUpper(w)
		if upper && (carryEnumTail.MatchString(raw[e:]) || carryStatusesRE.MatchString(raw)) { // status enums in stats
			continue
		}
		switch {
		case sep:
			if carrySubjIDRE.MatchString(subj) || len(carryWords(carryCodeRE.ReplaceAllString(subj, " "))) > 0 {
				return true
			}
		case upper || !russian:
			continue
		default:
			parts := carryClauseRE.Split(subj, -1)
			clause := strings.TrimSpace(parts[len(parts)-1])
			if carryThatRE.MatchString(clause) && len(parts) > 1 {
				clause = strings.TrimSpace(parts[len(parts)-2]) + " " + clause
			}
			if carryNumSubjRE.MatchString(clause) { // "две гипотезы", "исходная гипотеза не подтвердилась"
				continue
			}
			if carryHIDRE.MatchString(clause) {
				return true
			}
			cm := carryClaimRE.FindStringIndex(clause)
			if cm != nil && len(carryWords(clause)) >= 2 &&
				(carryTopicRE.MatchString(clause[cm[1]:]) || carryExplainRE.MatchString(strings.Trim(raw[e:], " *"))) {
				return true
			}
		}
	}
	return false
}

var (
	carryDVerbRE = regexp.MustCompile(`(?i)(` + carryLB + `(проверю|посмотрю|перемеряю|перепроверю|вернусь|гляну|перезапущу|сверю|досмотрю|перепрогоню|прогоню|проверить|посмотреть|перемерить|вернуться|сверить)` + carryRB + `|\b(check|re-?check|look|revisit|verify|re-?run|come back|circle back)\b)`)
	carryDTimeRE = regexp.MustCompile(`(?i)(` + carryLB + `(завтра|послезавтра|позже|попозже|завтра утром|через\s+(?:\d+\s*)?(?:мин\p{L}*|час\p{L}*|день|дня|дней|сутки|недел\p{L}*|месяц\p{L}*|пару\s+\p{L}+)|в\s+(?:понедельник|вторник|среду|четверг|пятницу|субботу|воскресенье)|на следующей неделе|когда\s+(?:ci|чеки|проверки|ревью|релиз|мейнтейнер\p{L}*|выйдет|смержат|зазеленеет|пройдут|пройдёт|появится|ответ\p{L}*)|после\s+(?:ci|мержа|мерджа|релиза|ревью|выхода|деплоя))` + carryRB +
		`|\b(tomorrow|later today|later this week|next week|on (?:monday|tuesday|wednesday|thursday|friday)|in (?:a|an|one|two|\d+) (?:minutes?|hours?|days?|weeks?)|(?:once|when|after) (?:ci|the ci|checks|the checks|the merge|merge|it merges|release|the release|review|the review|the maintainer)\b))`)
	// "минутой позже" compares two moments; it defers nothing. It was the one
	// miss in a 15-line recheck sample.
	carryRelLaterRE = regexp.MustCompile(`(?i)(?:секунд\p{L}*|минут\p{L}*|час\p{L}*|дн\p{L}*|недел\p{L}*|намного|гораздо|чуть)\s+(?:позже|попозже)$`)
	carryShortDRE   = regexp.MustCompile(`(?i)через\s+(?:\d+\s*)?(?:пару\s+)?(?:мин\p{L}*|час\p{L}*)|in (?:a|an|one|two|\d+) (?:minutes?|hours?)|later today`)
	carryDCondRE    = regexp.MustCompile(`(?i)(?:^|[^\p{L}])(?:если|скажешь|скажи|когда понадобится|if|in case|should you)(?:$|[^\p{L}])`)
	carryNowRE      = regexp.MustCompile(`(?i)(` + carryLB + `(сначала|сперва|пока|сейчас|прямо сейчас)` + carryRB + `|\b(now|first|right away)\b)`)
)

// carryDeferred reports a check put off to a named later time: a verb and a
// time marker within 80 runes, not conditional and not "first X, then later".
// "потом", "утром" and "вечером" are not markers: they mostly narrate order.
func carryDeferred(s string) bool {
	p := carryPlain(s)
	qs := carryQuoteSpans(p)
	// The first verb and marker outside quotes: «Вернусь к этому позже» quoted
	// as an example defers nothing.
	first := func(rx *regexp.Regexp) []int {
		for _, m := range rx.FindAllStringSubmatchIndex(p, -1) {
			at := m[0]
			if m[4] >= 0 && m[4] < m[5] {
				at = m[4]
			} else if m[6] >= 0 && m[6] < m[7] {
				at = m[6]
			}
			if !carryQuoted(qs, at) {
				return m
			}
		}
		return nil
	}
	v, t := first(carryDVerbRE), first(carryDTimeRE)
	if t != nil {
		end := t[1]
		if t[4] >= 0 {
			end = t[5]
		}
		if carryRelLaterRE.MatchString(p[:end]) {
			t = nil
		}
	}
	if v == nil || t == nil {
		return false
	}
	if utf8.RuneCountInString(p[min(v[0], t[0]):max(v[0], t[0])]) > 80 {
		return false
	}
	if c := carryDCondRE.FindStringIndex(p); c != nil && c[0] < max(v[0], t[0]) {
		return false
	}
	if n := carryNowRE.FindStringIndex(p); n != nil && n[0] < max(v[1], t[1]) {
		return false
	}
	return true
}
