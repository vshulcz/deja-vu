package rulex

import (
	"strings"
	"unicode/utf8"
)

// MaxCandidates caps how many spellings one word is given: the candidates are
// OR-ed into a lookup, so a handful is enough and a long name must not blow up.
const MaxCandidates = 4

// Cyrillic to Latin, GOST 7.79 system B in spirit: ь and ъ are dropped, й is
// y, х kh, ц ts, ч ch, ш sh, щ shch, ю yu, я ya, ё and е e.
var toLatin = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "e",
	'ж': "zh", 'з': "z", 'и': "i", 'й': "y", 'к': "k", 'л': "l", 'м': "m",
	'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u",
	'ф': "f", 'х': "kh", 'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "shch",
	'ъ': "", 'ы': "y", 'ь': "", 'э': "e", 'ю': "yu", 'я': "ya",
}

// Latin to Cyrillic, longest match first, the same table read backwards.
var toCyr = []struct{ lat, cyr string }{
	{"shch", "щ"}, {"sch", "щ"},
	{"zh", "ж"}, {"kh", "х"}, {"ts", "ц"}, {"ch", "ч"}, {"sh", "ш"},
	{"yu", "ю"}, {"ya", "я"}, {"yo", "е"}, {"ye", "е"}, {"ju", "ю"}, {"ja", "я"},
	{"ph", "ф"}, {"th", "т"},
	{"a", "а"}, {"b", "б"}, {"c", "к"}, {"d", "д"}, {"e", "е"}, {"f", "ф"},
	{"g", "г"}, {"h", "х"}, {"i", "и"}, {"j", "дж"}, {"k", "к"}, {"l", "л"},
	{"m", "м"}, {"n", "н"}, {"o", "о"}, {"p", "п"}, {"q", "к"}, {"r", "р"},
	{"s", "с"}, {"t", "т"}, {"u", "у"}, {"v", "в"}, {"w", "в"}, {"x", "кс"},
	{"y", "й"}, {"z", "з"},
}

const softable = "нтлзсдр" // consonants a name softens before я/ю: Нетаньяху

// ToLatin spells a lower-case Cyrillic word in Latin. It returns the plain
// GOST spelling and the common variant that writes х as h (Нетаньяху is
// Netanyahu in the press, not Netanyakhu); nothing when the word is not wholly
// Cyrillic or is shorter than four letters.
func ToLatin(w string) []string {
	if utf8.RuneCountInString(w) < 4 {
		return nil
	}
	var b strings.Builder
	hasKh := false
	for _, r := range w {
		s, ok := toLatin[r]
		if !ok {
			return nil
		}
		if r == 'х' {
			hasKh = true
		}
		b.WriteString(s)
	}
	out := []string{b.String()}
	if hasKh {
		out = append(out, strings.ReplaceAll(out[0], "kh", "h"))
	}
	return out
}

// ToCyrillic spells a lower-case Latin word in Cyrillic, greedy longest match.
// ya and yu after a consonant a name softens also give the ь spelling, so
// netanyahu reaches нетаньяху as well as нетаняху. Nothing when the word has a
// digit or other non-letter, is not Latin, or is shorter than four letters.
func ToCyrillic(w string) []string {
	if len(w) < 4 {
		return nil
	}
	for i := 0; i < len(w); i++ {
		if w[i] < 'a' || w[i] > 'z' {
			return nil
		}
	}
	outs := []string{""}
	for i := 0; i < len(w); {
		step, cyr := 1, ""
		for _, p := range toCyr {
			if strings.HasPrefix(w[i:], p.lat) {
				step, cyr = len(p.lat), p.cyr
				break
			}
		}
		next := make([]string, 0, len(outs)*2)
		for _, o := range outs {
			next = append(next, o+cyr)
			if (cyr == "я" || cyr == "ю") && o != "" && strings.ContainsRune(softable, lastRune(o)) {
				next = append(next, o+"ь"+cyr)
			}
		}
		if len(next) > MaxCandidates {
			next = next[:MaxCandidates]
		}
		outs = next
		i += step
	}
	return outs
}

func lastRune(s string) rune {
	r, _ := utf8.DecodeLastRuneInString(s)
	return r
}
