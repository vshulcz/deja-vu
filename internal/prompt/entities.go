package prompt

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// nonEntityCapitalizedWords lists pronouns and calendar names written with an
// initial capital letter in ordinary prose without naming a person or place.
var nonEntityCapitalizedWords = map[string]bool{
	"i": true, "i'm": true, "i've": true, "i'll": true, "i'd": true,
	"monday": true, "tuesday": true, "wednesday": true, "thursday": true,
	"friday": true, "saturday": true, "sunday": true,
	"january": true, "february": true, "march": true, "april": true,
	"may": true, "june": true, "july": true, "august": true,
	"september": true, "october": true, "november": true, "december": true,
	"понедельник": true, "вторник": true, "среда": true, "среду": true,
	"четверг": true, "пятница": true, "пятницу": true, "суббота": true,
	"субботу": true, "воскресенье": true,
	"январь": true, "января": true, "февраль": true, "февраля": true,
	"март": true, "марта": true, "апрель": true, "апреля": true,
	"май": true, "мая": true, "июнь": true, "июня": true,
	"июль": true, "июля": true, "август": true, "августа": true,
	"сентябрь": true, "сентября": true, "октябрь": true, "октября": true,
	"ноябрь": true, "ноября": true, "декабрь": true, "декабря": true,
}

func isSentenceEndingToken(raw string) bool {
	trimmed := strings.TrimRight(raw, "\"')]}»”’")
	if trimmed == "" {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(trimmed)
	return r == '.' || r == '!' || r == '?' || r == '\u3002' || r == '\uff01' || r == '\uff1f'
}

func stripEntityTokenPunct(raw string) string {
	cleaned := strings.TrimFunc(raw, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	cleaned = strings.ReplaceAll(cleaned, "’", "'")
	for _, suf := range []string{"'s", "'S"} {
		cleaned = strings.TrimSuffix(cleaned, suf)
	}
	return cleaned
}

func isCapitalizedEntityToken(tok string) bool {
	if nonEntityCapitalizedWords[strings.ToLower(tok)] {
		return false
	}
	var hasUpper, hasLower bool
	for i, r := range tok {
		if !unicode.IsLetter(r) {
			return false
		}
		if i == 0 {
			if !unicode.IsUpper(r) {
				return false
			}
			hasUpper = true
			continue
		}
		if unicode.IsLower(r) {
			hasLower = true
		}
	}
	return hasUpper && hasLower
}

func visitMidSentenceEntities(line string, yield func(token string) bool) {
	atSentenceStart := true
	i := 0
	for i < len(line) {
		for i < len(line) {
			r, size := utf8.DecodeRuneInString(line[i:])
			if !unicode.IsSpace(r) {
				break
			}
			if r == '\n' || r == '\r' {
				atSentenceStart = true
			}
			i += size
		}
		if i >= len(line) {
			break
		}
		start := i
		for i < len(line) {
			r, size := utf8.DecodeRuneInString(line[i:])
			if unicode.IsSpace(r) {
				break
			}
			i += size
		}
		raw := line[start:i]
		cleaned := stripEntityTokenPunct(raw)
		if cleaned != "" {
			if !atSentenceStart && isCapitalizedEntityToken(cleaned) {
				if !yield(cleaned) {
					return
				}
			}
			atSentenceStart = isSentenceEndingToken(raw)
		} else if isSentenceEndingToken(raw) {
			atSentenceStart = true
		}
	}
}

// CarriesMidSentenceEntity reports whether line contains a capitalized word in
// non-sentence-initial position (Latin or Cyrillic) that is not a pronoun or
// calendar name.
func CarriesMidSentenceEntity(line string) bool {
	found := false
	visitMidSentenceEntities(line, func(string) bool {
		found = true
		return false
	})
	return found
}

// MidSentenceEntityTerm reports whether term appears inside text as a
// capitalized non-sentence-initial entity token.
func MidSentenceEntityTerm(text, term string) bool {
	if text == "" || term == "" {
		return false
	}
	want := stripEntityTokenPunct(term)
	if want == "" {
		want = term
	}
	found := false
	visitMidSentenceEntities(text, func(tok string) bool {
		if strings.EqualFold(tok, want) {
			found = true
			return false
		}
		return true
	})
	return found
}

// HasBetweenCue reports whether ask frames a span between two events using
// "between" or its Russian equivalent "между".
func HasBetweenCue(ask string) bool {
	padded := " " + strings.Join(strings.Fields(strings.ToLower(ask)), " ") + " "
	return strings.Contains(padded, " between ") || strings.Contains(padded, " между ")
}

// PairedClauseSplit splits a paired-clause question on its rightmost temporal
// ("when" / "когда") or coordination ("and" / "и") boundary when both halves
// are non-empty.
func PairedClauseSplit(ask string) (left, right string, isTemporal, ok bool) {
	low := strings.ToLower(ask)
	src := ask
	if len(low) != len(ask) {
		src = low
	}
	bestIdx := -1
	bestSep := 0
	for _, sep := range []string{" when ", " когда "} {
		if idx := strings.LastIndex(low, sep); idx > bestIdx {
			bestIdx = idx
			bestSep = len(sep)
			isTemporal = true
		}
	}
	if bestIdx <= 0 {
		for _, sep := range []string{" and ", " и "} {
			if idx := strings.LastIndex(low, sep); idx > bestIdx {
				bestIdx = idx
				bestSep = len(sep)
				isTemporal = false
			}
		}
	}
	if bestIdx <= 0 || bestIdx+bestSep >= len(src) {
		return "", "", false, false
	}
	return src[:bestIdx], src[bestIdx+bestSep:], isTemporal, true
}

// IsDisjunctiveQuery reports whether asked coordinates alternative subjects
// with "or" / "или", excluding temporal "before or after" / "до или после".
func IsDisjunctiveQuery(asked string) bool {
	norm := " " + strings.Join(strings.Fields(strings.ToLower(asked)), " ") + " "
	norm = strings.ReplaceAll(norm, " before or after ", " ")
	norm = strings.ReplaceAll(norm, " до или после ", " ")
	return strings.Contains(norm, " or ") || strings.Contains(norm, " или ")
}

var openAdviceMarkers = map[string]bool{
	"advice": true, "recommend": true, "recommendation": true, "recommendations": true,
	"suggest": true, "suggestion": true, "suggestions": true, "tip": true, "tips": true,
	"совет": true, "советы": true, "совета": true, "посоветуй": true, "посоветуйте": true,
	"рекомендация": true, "рекомендации": true, "порекомендуй": true, "порекомендуйте": true,
	"подскажи": true, "подскажите": true, "подсказка": true, "подсказки": true,
}

var historicalRecallCues = map[string]bool{
	"again": true, "already": true, "before": true, "decide": true, "decided": true,
	"discussed": true, "earlier": true, "last": true, "mentioned": true,
	"previous": true, "previously": true, "recommended": true, "remind": true,
	"settled": true, "suggested": true,
	"снова": true, "опять": true, "уже": true, "раньше": true, "ранее": true,
	"решили": true, "решали": true, "обсуждали": true, "упоминал": true, "упоминали": true,
	"напомни": true, "договорились": true, "прошлый": true, "прошлом": true,
	"советовал": true, "советовали": true, "рекомендовал": true, "рекомендовали": true,
}

// IsOpenAdviceQuery reports whether asked requests general advice or
// suggestions without referencing an earlier discussion or decision.
func IsOpenAdviceQuery(asked string, terms []string) bool {
	hasAdviceWord := false
	for _, t := range terms {
		if openAdviceMarkers[t] {
			hasAdviceWord = true
		}
		if historicalRecallCues[t] {
			return false
		}
	}
	hasWord := false
	for _, w := range strings.FieldsFunc(strings.ToLower(asked), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if openAdviceMarkers[w] {
			hasAdviceWord = true
		}
		if historicalRecallCues[w] {
			return false
		}
		hasWord = true
	}
	return hasAdviceWord && (hasWord || len(terms) > 0)
}

// framingModifiers holds temporal units, aspectual adverbs, and light verbs
// that frame a question without naming its domain subject, so subject ranking
// and multi-subject conjunction checks prioritize concrete entities and terms.
var framingModifiers = map[string]bool{
	// Temporal and quantity framing (EN + RU).
	"ago": true, "already": true, "also": true, "around": true, "back": true,
	"between": true, "currently": true, "day": true, "days": true, "during": true,
	"earlier": true, "eventually": true, "ever": true, "far": true, "first": true,
	"hour": true, "hours": true, "last": true, "later": true, "long": true,
	"many": true, "minute": true, "minutes": true, "month": true, "months": true,
	"more": true, "most": true, "much": true, "next": true, "now": true, "often": true,
	"old": true, "once": true, "originally": true, "passed": true, "past": true,
	"previously": true, "recent": true, "recently": true, "since": true,
	"soon": true, "spend": true, "spent": true, "still": true, "then": true,
	"there": true, "time": true, "times": true, "today": true, "tonight": true,
	"total": true, "upcoming": true, "usually": true, "week": true, "weeks": true,
	"year": true, "years": true, "yesterday": true,
	"день": true, "дня": true, "дней": true, "неделя": true, "недели": true,
	"недель": true, "неделю": true, "месяц": true, "месяца": true, "месяцев": true,
	"год": true, "года": true, "лет": true, "час": true, "часа": true, "часов": true,
	"минута": true, "минуты": true, "минут": true, "раз": true, "раза": true,
	"часто": true, "сейчас": true, "теперь": true, "недавно": true, "раньше": true,
	"сегодня": true, "вчера": true, "уже": true, "снова": true, "опять": true,
	"всего": true, "много": true, "сколько": true,
	// Aspectual, state-transition, advice, and light verbs (EN + RU).
	"acquire": true, "acquired": true, "acquiring": true, "advice": true, "attend": true, "attended": true, "attending": true,
	"became": true, "become": true, "becoming": true, "better": true, "bit": true,
	"bought": true, "buy": true, "buying": true, "changed": true, "complete": true,
	"completed": true, "completing": true, "current": true, "even": true,
	"finish": true, "finished": true, "finishing": true, "fixed": true, "fixes": true,
	"fixing": true, "gave": true, "getting": true,
	"give": true, "going": true, "gone": true, "good": true, "got": true,
	"happened": true, "help": true, "idea": true, "ideas": true, "improve": true,
	"keep": true, "keeping": true, "learn": true, "learning": true, "left": true,
	"listen": true, "listened": true, "live": true, "lived": true, "living": true,
	"local": true, "made": true, "main": true, "make": true, "making": true,
	"move": true, "moved": true, "moving": true, "new": true, "option": true,
	"options": true, "own": true, "owned": true, "plan": true, "planned": true,
	"pointer": true, "pointers": true, "purchase": true, "purchased": true, "purchasing": true, "read": true, "recommend": true,
	"recommendation": true, "recommendations": true, "recommended": true,
	"relocate": true, "relocated": true, "resource": true, "resources": true,
	"result": true, "results": true, "said": true, "seen": true, "show": true,
	"start": true, "started": true, "starting": true, "struggling": true,
	"suggest": true, "suggested": true, "suggestion": true, "suggestions": true,
	"switch": true, "switched": true, "take": true, "taken": true, "taking": true,
	"thinking": true, "thought": true, "thoughts": true, "tip": true, "tips": true,
	"told": true, "took": true, "used": true, "very": true, "visit": true,
	"visited": true, "want": true, "wanted": true, "watch": true, "watched": true,
	"watching": true, "ways": true, "well": true, "went": true, "worked": true,
	"working": true,
	"купил":   true, "купила": true, "купили": true, "купить": true, "приобрел": true, "приобрела": true, "приобрели": true, "приобрести": true, "ходил": true, "ходила": true,
	"ходили": true, "посетил": true, "посетила": true, "посетили": true,
	"переехал": true, "переехала": true, "переехали": true, "живу": true,
	"живет": true, "работал": true, "работала": true, "смотрел": true,
	"смотрела": true, "посмотрел": true, "читал": true, "прочитал": true,
	"слушал": true, "начал": true, "начала": true, "закончил": true,
	"закончила": true, "исправил": true, "исправила": true, "исправили": true,
	"починил": true, "починила": true, "починили": true,
	"совет": true, "советы": true, "рекомендации": true,
}

// IsFramingModifier reports whether term acts as a temporal, aspectual, or
// light-verb modifier rather than a domain subject.
func IsFramingModifier(term string) bool {
	return framingModifiers[strings.ToLower(term)]
}

var genericFollowupNouns = map[string]bool{
	"advice": true, "idea": true, "ideas": true, "option": true, "options": true,
	"pointer": true, "pointers": true, "recommendation": true, "recommendations": true,
	"suggestion": true, "suggestions": true, "thought": true, "thoughts": true,
	"tip": true, "tips": true,
	"совет": true, "советы": true, "идея": true, "идеи": true,
	"вариант": true, "варианты": true, "рекомендация": true, "рекомендации": true,
	"подсказка": true, "подсказки": true,
}

// askAnchoredTerms collects non-plain identifiers and mid-sentence entities
// from ask so term capping preserves concrete subjects over framing words.
func askAnchoredTerms(ask string) map[string]bool {
	rawFields := strings.FieldsFunc(ask, func(r rune) bool {
		wordy := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.' || r == '/' || r >= 0x400
		return !wordy
	})
	anchored := make(map[string]bool, len(rawFields))
	for _, raw := range rawFields {
		raw = strings.Trim(raw, "._-/")
		if raw == "" {
			continue
		}
		hasSymOrDigit := false
		hasInnerUpper := false
		runeIdx := 0
		for _, r := range raw {
			if r == '_' || r == '.' || r == '/' || r == '-' || (r >= '0' && r <= '9') {
				hasSymOrDigit = true
				break
			}
			if runeIdx > 0 && unicode.IsUpper(r) {
				hasInnerUpper = true
			}
			runeIdx++
		}
		if hasSymOrDigit || hasInnerUpper {
			anchored[strings.ToLower(raw)] = true
		}
	}
	visitMidSentenceEntities(ask, func(tok string) bool {
		if low := strings.ToLower(tok); low != "" {
			anchored[low] = true
		}
		return true
	})
	return anchored
}
