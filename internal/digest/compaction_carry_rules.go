package digest

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The rules behind the "keep until closed" list. Go's \b is ASCII-only, so it
// stands only next to Latin words; Cyrillic word edges are the consuming
// boundaries below, and every position that matters is read from a submatch
// rather than the match start, which the left edge may have moved by one rune.
// Distances are in runes: the thresholds were tuned on Python str offsets.
const (
	carryLB = `(?:^|[^\p{L}\p{N}_])`
	carryRB = `(?:$|[^\p{L}\p{N}_])`
	// carryNear is how far a pending or closing word may sit from the #N it
	// binds to. Wider bound "#2261 ждёт CI, а #2262 смержен" both ways.
	carryNear = 40
)

const (
	carryPendWords  = `ждёт|ждет|ждут|жду|висит|висят|осталось|остаётся|остается|остался|остались|на тебя|за тобой|ожидает|ожидают|не смержен\p{L}*|не влит\p{L}*|открыт|открыт\p{L}* вопрос|open question|смержу|домержу|вмержу|will merge|merge on green|отложен|в очереди|pending|deferred|parked|waiting|awaiting|not merged|still open|open|blocked|needs review|in review`
	carryCloseWords = `смержен\p{L}*|смёржен\p{L}*|вмержен\p{L}*|смержил\p{L}*|вмержил\p{L}*|влит\p{L}*|закрыт\p{L}*|закрыл\p{L}*|merged|closed|landed|в main|в мейн|в проде|deployed|выкачен\p{L}*|задеплоен\p{L}*|слит|слиты|зашёл|зашел|зашла|заехал\p{L}*|уехал\p{L}*|went in`
	carryNegPend    = `(?:остаётся|остается|остался|осталось|остались)\s+(?:как есть|верн\p{L}*|прежн\p{L}*|точн\p{L}*|синхронн\p{L}*|без изменений|в силе решени\p{L}*)|stays as is`
)

var (
	carryFenceRE = regexp.MustCompile("(?s)```.*?```")
	carrySplitRE = regexp.MustCompile(`([.!?]\**)\s+([A-ZА-ЯЁ«"*#\[])`)
	carryLeadRE  = regexp.MustCompile(`^\s*(?:#{1,6}\s+|[-*+>]\s+|\d{1,2}[.)]\s+|\|\s*)*`)
	carryCodeRE  = regexp.MustCompile("`[^`]*`")
	carryLinkRE  = regexp.MustCompile(`\[([^\]]*)\]\([^)\s]*\)`)

	carryAskRE = regexp.MustCompile(`(?i)(` + carryLB + `(?:скажи|скажите|решай|решать тебе|решение за тобой|выбор за тобой|за тобой слово|твоё слово|твое слово|(?:жду|ждут|ждёт|ждет|нужно|нужен|нужна|нужны|требу\p{L}*|без)\s+(?:\p{L}+\s+)?(?:тво\p{L}+)\s+[«"]?(?:слов\p{L}*|ок|ok|решени\p{L}*|согласи\p{L}*|ответ\p{L}*|отмашк\p{L}*|разрешени\p{L}*|добр\p{L}*)|жду (?:ответа|решения|отмашки|ок)|хочешь,? чтобы|если хочешь|если согласен|согласен\?|ок\?|делать\?|мержить\?|берём\?|беру\?)` + carryRB +
		`|\b(?:your call|say the word|up to you|let me know|should I|shall I|do you want|want me to|waiting (?:for|on) you|need your (?:ok|go-ahead|call|decision|approval)|your (?:ok|go-ahead|approval)|if you (?:want|agree|prefer))\b)`)
	carrySelfQRE = regexp.MustCompile(`(?i)^(?:проверяю|смотрю|проверим|вопрос|гипотеза|checking|question|hypothesis)\s*[:—-]`)

	carryNumRE      = regexp.MustCompile(`(?:#|(?i:PR|issue)\s#?)(\d{2,6})(?:[^\p{N}]|$)`)
	carryPendRE     = regexp.MustCompile(`(?i)` + carryLB + `(` + carryPendWords + `)(?:$|[^\p{L}\p{N}_-])`)
	carryCloseRE    = regexp.MustCompile(`(?i)` + carryLB + `(` + carryCloseWords + `)` + carryRB)
	carryNegPendRE  = regexp.MustCompile(`(?i)` + carryNegPend)
	carryNegPendAt  = regexp.MustCompile(`(?i)^(?:` + carryNegPend + `)`)
	carryContrastRE = regexp.MustCompile(`(?i)(?:,\s*(?:а|но|однако|but|while)\s)`)
	carryHeadRE     = regexp.MustCompile(`(?i)^.{0,25}?` + carryLB + `(` + carryPendWords + `)(?:$|[^\p{L}\p{N}_-])[^:—]{0,30}(?::|—)`)
	carryCHeadRE    = regexp.MustCompile(`(?i)^.{0,25}?` + carryLB + `(` + carryCloseWords + `)` + carryRB + `[^:—]{0,30}(?::|—)`)
	carryRefPreRE   = regexp.MustCompile(`(?i)(?:(?:^|[^\p{L}])(?:из|с|со|от|после|благодаря|через|since|from|per|see|thanks to|via|см\.?)\s*(?:PR|issue)?\s*|(?:^|[^\p{L}])(?:из|от|from|see|cf\.?|ссылк\p{L}*\s+на)\s+[\w.-]+(?:/[\w.-]+)?|(?:^|[^\p{L}])(?:из|от|from)\s+(?:\p{L}+\s+){1,2}|(?:^|[^\p{L}])(?:как в|как и в|ссылк\p{L}*\s+на|as in|like in)\s*(?:PR|issue)?\s*)$`)
	carryPendRefRE  = regexp.MustCompile(`(?i)(?:осталось|остаётся|остается|остался|осталась|в очереди|вопрос|left|remaining|queued|question)\s+(?:из|от|с|со|from|of|with)\s*(?:PR|issue)?\s*$`)
	carryPastBefore = regexp.MustCompile(`(?i)(?:^|[^\p{L}])(?:ввёл|ввел|ввела|внёс|внес|внесла|сделан\p{L}*|готов\p{L}*|подготовлен\p{L}*|done|добавил\p{L}*|сделал\p{L}*|починил\p{L}*|исправил\p{L}*|вычистил\p{L}*|убрал\p{L}*|introduced|added|fixed|shipped)\s+(?:в\s+)?\(?\s*(?:PR\s+|issue\s+)?$`)
	carryPastAfter  = regexp.MustCompile(`^[\s*]*(?:\p{L}+(?:ил|ыл|ал|ял|ел|ул|ла|ли|лся|лась|лись|лось)|[a-z]+ed)(?:$|[^\p{L}\p{N}_])`)
	carryCiteRE     = regexp.MustCompile(`^\s*[—–-]\s`)
	carryOpenComma  = regexp.MustCompile(`\(([^()]*),\s*$`)
	carryRunGapRE   = regexp.MustCompile(`^\s*(?:\([^()]{0,80}\))?\s*(?:,|/|и|and|&)?\s*$`)
	carryDescRE     = regexp.MustCompile(`^\s*\([^()]{0,80}\)`)
	carryLatinRE    = regexp.MustCompile(`^[A-Za-z][A-Za-z ]*$`)
	carryIntentRE   = regexp.MustCompile(`(?i)^(?:смержу|домержу|вмержу|will merge|merge on green)$`)
	carryCyrRE      = regexp.MustCompile(`[А-Яа-яЁё]`)
	carryLatRE      = regexp.MustCompile(`[A-Za-z]`)
)

// carryUnits splits an assistant message into the sentences the rules judge.
func carryUnits(text string, fences bool) []string {
	if !fences {
		text = carryFenceRE.ReplaceAllString(text, " ")
	}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(carryLeadRE.ReplaceAllString(line, ""))
		if line == "" {
			continue
		}
		for _, s := range strings.Split(carrySplitRE.ReplaceAllString(line, "${1}\n${2}"), "\n") {
			s = strings.TrimSpace(strings.Trim(strings.TrimSpace(s), "|"))
			if n := utf8.RuneCountInString(s); n > 12 && n < 400 {
				out = append(out, s)
			}
		}
	}
	return out
}

func carryPlain(s string) string {
	return strings.ReplaceAll(carryLinkRE.ReplaceAllString(carryCodeRE.ReplaceAllString(s, " "), "$1"), "**", "")
}

// carryBNorm keeps the text inside backticks: "`#2261` ждёт CI" names the PR.
func carryBNorm(s string) string {
	s = strings.ReplaceAll(carryLinkRE.ReplaceAllString(s, "$1"), "**", "")
	return strings.ReplaceAll(s, "`", "")
}

// carryAsk reports whether the unit hands a decision to the user.
func carryAsk(s string) bool {
	p := carryPlain(s)
	m := carryAskRE.FindStringIndex(p)
	if m == nil || carrySelfQRE.MatchString(p) {
		return false
	}
	_, size := utf8.DecodeRuneInString(p[m[0]:])
	pre := p[:m[0]+size]
	return strings.Count(pre, "«") <= strings.Count(pre, "»")
}

// carryQuoteMax is the longest quote, in runes, that counts as one.
const carryQuoteMax = 200

// carryQuoteSpans are the quoted parts of a unit, marks included: «…», “…”,
// and "…" when the unit's straight quotes pair up. A quote is cited, not said:
// "агент обещает: «смержу, когда CI пройдёт»" promises nothing, and a live
// transcript turned that sentence into an open item and a recheck.
func carryQuoteSpans(s string) [][2]int {
	var out [][2]int
	for _, q := range [][2]string{{"«", "»"}, {"“", "”"}} {
		o, c := q[0], q[1]
		for i := 0; ; {
			a := strings.Index(s[i:], o)
			if a < 0 {
				break
			}
			a += i
			b := strings.Index(s[a+len(o):], c)
			if b < 0 {
				break
			}
			b += a + len(o)
			inner := s[a+len(o) : b]
			if k := strings.LastIndex(inner, o); k >= 0 { // nested opener: start again from it
				i = a + len(o) + k
				continue
			}
			if utf8.RuneCountInString(inner) <= carryQuoteMax {
				out = append(out, [2]int{a, b + len(c)})
			}
			i = b + len(c)
		}
	}
	var straight []int
	for i := 0; i < len(s); i++ {
		if s[i] == '"' {
			straight = append(straight, i)
		}
	}
	if len(straight)%2 == 0 {
		for k := 0; k+1 < len(straight); k += 2 {
			a, b := straight[k], straight[k+1]
			if utf8.RuneCountInString(s[a+1:b]) <= carryQuoteMax {
				out = append(out, [2]int{a, b + 1})
			}
		}
	}
	return out
}

func carryQuoted(spans [][2]int, i int) bool {
	for _, sp := range spans {
		if sp[0] <= i && i < sp[1] {
			return true
		}
	}
	return false
}

type carryNum struct {
	n      int
	closed bool
}

type carryGroup struct {
	a, b  int
	ns    []int
	verbs [][2]int // gap, 1 when closing
	ref   bool
}

// carryBState is the state each #N in the unit is left in: a pending or
// closing word binds to the nearest #N run within carryNear runes.
func carryBState(s string) []carryNum {
	qs := carryQuoteSpans(s)
	var ms [][]int
	for _, m := range carryNumRE.FindAllStringSubmatchIndex(s, -1) {
		if !carryQuoted(qs, m[0]) {
			ms = append(ms, m)
		}
	}
	var groups []*carryGroup
	for i, m := range ms {
		pre, post := s[:m[0]], s[m[3]:]
		cp := carryOpenComma.FindStringSubmatch(pre)
		ref := (carryRefPreRE.MatchString(pre) && !carryPendRefRE.MatchString(pre)) ||
			carryPastBefore.MatchString(pre) || carryPastAfter.MatchString(post) ||
			(strings.HasSuffix(strings.TrimRightFunc(pre, unicode.IsSpace), "(") && carryCiteRE.MatchString(post)) || // "(#840 — why)"
			(cp != nil && !carryNumRE.MatchString(cp[1]) && strings.HasPrefix(strings.TrimLeftFunc(post, unicode.IsSpace), ")")) // "(cause, #2019)"
		n := atoiCarry(s[m[2]:m[3]])
		// A reference can still be closed ("#3878 merged"), never opened.
		a, b := m[0], m[1]
		if !ref {
			a, b = carryRunSpan(s, ms, i)
		}
		merged := false
		for _, g := range groups {
			if !ref && !g.ref && g.a == a && g.b == b {
				g.ns = append(g.ns, n)
				merged = true
				break
			}
		}
		if !merged {
			groups = append(groups, &carryGroup{a: a, b: b, ns: []int{n}, ref: ref})
		}
	}
	if len(groups) == 0 {
		return nil
	}
	cyr := len(carryCyrRE.FindAllStringIndex(s, -1)) > len(carryLatRE.FindAllStringIndex(s, -1))
	for kind, rx := range []*regexp.Regexp{carryPendRE, carryCloseRE} {
		for _, w := range rx.FindAllStringSubmatchIndex(s, -1) {
			ws, we := w[2], w[3]
			word := s[ws:we]
			if (rx == carryPendRE && carryNegPendAt.MatchString(s[ws:])) || carryQuoted(qs, ws) {
				continue
			}
			if carryIsUpper(word) && carrySlashNear(s, ws, we) { // "FIXED/OPEN" is a status enum
				continue
			}
			latin := carryLatinRE.MatchString(word)
			// An English word inside Russian prose is an identifier unless it is glued to the #N.
			tight := latin && (cyr || strings.EqualFold(word, "open"))
			var best *carryGroup
			bestGap := 0
			for _, g := range groups {
				d, ok := carryGap(s, g, ws, we)
				inParen := strings.LastIndex(s[:g.a], "(") > strings.LastIndex(s[:g.a], ")")
				if tight && (inParen || (ok && d > 3)) {
					continue
				}
				limit := carryNear
				if len(groups) == 1 && !inParen && carryIntentRE.MatchString(word) {
					limit = 3 * carryNear
				}
				if ok && d <= limit && (best == nil || d < bestGap) {
					best, bestGap = g, d
				}
			}
			if best != nil {
				best.verbs = append(best.verbs, [2]int{bestGap, kind})
			}
		}
	}
	hm := carryHeadRE.FindStringSubmatchIndex(s)
	head := hm != nil && !carryNegPendRE.MatchString(s[hm[0]:hm[1]]) && !carryQuoted(qs, hm[2])
	cm := carryCHeadRE.FindStringSubmatchIndex(s)
	chead := cm != nil && !carryQuoted(qs, cm[2])
	var out []carryNum
	at := map[int]int{}
	for _, g := range groups {
		// The nearest bound word decides; a tie goes to the closing one.
		nearest := -1
		for i, v := range g.verbs {
			if nearest < 0 || v[0] < g.verbs[nearest][0] || (v[0] == g.verbs[nearest][0] && v[1] == 1 && g.verbs[nearest][1] == 0) {
				nearest = i
			}
		}
		st := -1 // 0 open, 1 closed
		switch {
		case nearest >= 0:
			st = g.verbs[nearest][1]
		case chead:
			st = 1
		case head:
			st = 0
		}
		if g.ref && st == 0 {
			st = -1
		}
		if st < 0 {
			continue
		}
		for _, n := range g.ns {
			if i, ok := at[n]; ok {
				if !out[i].closed {
					out[i].closed = st == 1
				}
				continue
			}
			at[n] = len(out)
			out = append(out, carryNum{n: n, closed: st == 1})
		}
	}
	return out
}

// carryRunSpan widens a #N to the run it stands in: "#a, #b и #c", items that
// carry a short "(desc)", or a parenthetical holding two or more numbers.
func carryRunSpan(s string, ms [][]int, i int) (int, int) {
	a, b := i, i
	for a > 0 && carryRunGapRE.MatchString(s[ms[a-1][3]:ms[a][0]]) {
		a--
	}
	for b < len(ms)-1 && carryRunGapRE.MatchString(s[ms[b][3]:ms[b+1][0]]) {
		b++
	}
	lo, hi := ms[a][0], ms[b][3]
	o := strings.LastIndex(s[:lo], "(")
	c := strings.Index(s[hi:], ")")
	if o >= 0 && c >= 0 {
		c += hi
		if !strings.Contains(s[o:lo], ")") && !strings.Contains(s[hi:c], "(") && len(carryNumRE.FindAllStringIndex(s[o:c+1], -1)) >= 2 {
			lo, hi = o, c+1
		}
	}
	if a != b {
		if t := carryDescRE.FindStringIndex(s[hi:]); t != nil {
			hi += t[1]
		}
	}
	return lo, hi
}

// carryGap is the rune distance between a group and a word, false when a ';'
// or a contrast clause stands between them.
func carryGap(s string, g *carryGroup, a, b int) (int, bool) {
	if b <= g.b && a >= g.a {
		return 0, true
	}
	var d int
	if a >= g.b {
		d = utf8.RuneCountInString(s[g.b:a])
	} else if b <= g.a {
		d = utf8.RuneCountInString(s[b:g.a])
	} else {
		d = -utf8.RuneCountInString(s[g.a:b])
	}
	lo, hi := min(g.b, b), max(g.a, a)
	if lo < hi {
		mid := s[lo:hi]
		if strings.Contains(mid, ";") || carryContrastRE.MatchString(mid) {
			return 0, false
		}
	}
	return d, true
}

func carrySlashNear(s string, a, b int) bool {
	if r, _ := utf8.DecodeLastRuneInString(s[:a]); r == '/' {
		return true
	}
	r, _ := utf8.DecodeRuneInString(s[b:])
	return r == '/'
}

// carryIsUpper is Python's str.isupper: at least one cased rune, none lower.
func carryIsUpper(s string) bool {
	cased := false
	for _, r := range s {
		if unicode.IsLower(r) {
			return false
		}
		if unicode.IsUpper(r) {
			cased = true
		}
	}
	return cased
}

func atoiCarry(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}
