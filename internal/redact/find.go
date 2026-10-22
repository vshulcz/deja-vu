package redact

import (
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"
)

// The rules here are regexes RE2 cannot run with its fast engines: a
// case-folded alternation, a Unicode class in front of the key word. Asked for
// every match in a message, it walks the whole text through the NFA one rune
// at a time, and a gate that passes on an ordinary word ("token", "pass", a
// colon and one non-ASCII byte) puts that walk on most of a developer's
// history. Profiled on an outside user's cold build of 16k sessions, the walk
// was half of all CPU.
//
// A finder gets the same matches without the walk. Every match of its rule
// holds one of a few literals at a bounded distance from where it starts — the
// key word, the provider prefix, the `://`. The literals are found with
// strings.Index, each hit is checked against what must follow it, and the
// regex is only asked whether a match starts in the few positions that hit
// allows. Those positions are tried in order, so the first match found is the
// leftmost one, as FindAll would return it.
//
// A text the folding view cannot represent byte for byte (invalid UTF-8, or a
// rune such as the Kelvin sign whose fold changes its length) goes to the
// regex whole, as before.
type finder struct {
	re *regexp.Regexp
	// win tries the starts [p, p+span runes] after one rune of context, so
	// \b and ^ see the real text before p; win0 is the same at text start.
	win, win0 *regexp.Regexp
	span      int
	fold      bool     // lits are searched in the case-folded view
	lits      []string // folded when fold is set
	// region reports the starts a match holding the literal at k can have:
	// [lo, hi], or ok=false when what surrounds k rules every match out. It
	// must only reject what the rule cannot match, and lo must not decrease
	// as k grows.
	region func(v *view, k int) (lo, hi int, ok bool)
}

func newFinder(re *regexp.Regexp, span int, fold bool, lits []string, region func(v *view, k int) (int, int, bool)) *finder {
	expr := re.String()
	rep := ""
	if span > 0 {
		rep = `(?s:.){0,` + strconv.Itoa(span) + `}?`
	}
	f := &finder{
		re:     re,
		win:    regexp.MustCompile(`^(?s:.)` + rep + `(` + expr + `)`),
		win0:   regexp.MustCompile(`^` + rep + `(` + expr + `)`),
		span:   span,
		fold:   fold,
		region: region,
	}
	for _, l := range lits {
		if fold {
			l = foldString(l)
		}
		f.lits = append(f.lits, l)
	}
	return f
}

// view is the text a pass is working on, with its case-folded twin built on
// first use. f is aligned byte for byte with s.
type view struct {
	s       string
	f       string
	built   bool
	foldOK  bool
	validOK bool
	// floor is the first start a region may still need: every start below it
	// has been ruled out.
	floor int
	// Forward scans the regions repeat, remembered so a long line holding
	// many literals is read once rather than once per literal.
	space, at, intl scanMemo
}

// scanMemo remembers that the first byte at or after from that stop accepts
// is at pos.
type scanMemo struct {
	from, pos int
	set       bool
}

func (m *scanMemo) next(s string, i int, stop func(byte) bool) int {
	if m.set && m.from <= i && i <= m.pos {
		return m.pos
	}
	j := i
	for j < len(s) && !stop(s[j]) {
		j++
	}
	*m = scanMemo{from: i, pos: j, set: true}
	return j
}

func (v *view) reset(s string) {
	if v.built && v.s == s {
		return
	}
	*v = view{s: s}
}

func (v *view) build() {
	if v.built {
		return
	}
	v.built = true
	v.f, v.foldOK = foldAligned(v.s)
	v.validOK = v.foldOK || utf8.ValidString(v.s)
}

// all returns every match of the finder's rule in v.s, as
// FindAllStringSubmatchIndex would.
func (f *finder) all(v *view) [][]int {
	v.build()
	src := v.s
	if f.fold {
		if !v.foldOK {
			return f.re.FindAllStringSubmatchIndex(v.s, -1)
		}
		src = v.f
	} else if !v.validOK {
		return f.re.FindAllStringSubmatchIndex(v.s, -1)
	}
	s := v.s
	var out [][]int
	next := make([]int, len(f.lits))
	for i := range next {
		next[i] = -2
	}
	checked, at := 0, 0
	for at <= len(src) {
		k := -1
		for i, l := range f.lits {
			if next[i] == -1 {
				continue
			}
			if next[i] < at {
				j := strings.Index(src[at:], l)
				if j < 0 {
					next[i] = -1
					continue
				}
				next[i] = at + j
			}
			if k < 0 || next[i] < k {
				k = next[i]
			}
		}
		if k < 0 {
			break
		}
		at = k + 1
		v.floor = checked
		lo, hi, ok := f.region(v, k)
		if !ok {
			continue
		}
		if lo < checked {
			lo = checked
		}
		// Back to a rune boundary. A start behind lo but past checked is still
		// one no earlier anchor could have, so trying it is harmless.
		for lo > checked && lo < len(s) && !utf8.RuneStart(s[lo]) {
			lo--
		}
		for lo <= hi {
			if m := f.try(s, lo); m != nil {
				out = append(out, m)
				checked = m[1]
				if at < checked {
					at = checked
				}
				break
			}
			lo = skipRunes(s, lo, f.span+1)
			checked = lo
		}
	}
	return out
}

// try returns the leftmost match starting in [p, p+span runes], with the
// rule's own submatch indexes.
func (f *finder) try(s string, p int) []int {
	base := 0
	var m []int
	if p == 0 {
		m = f.win0.FindStringSubmatchIndex(s)
	} else {
		_, w := utf8.DecodeLastRuneInString(s[:p])
		base = p - w
		m = f.win.FindStringSubmatchIndex(s[base:])
	}
	if m == nil {
		return nil
	}
	out := make([]int, len(m)-2)
	for i := range out {
		if x := m[i+2]; x >= 0 {
			out[i] = x + base
		} else {
			out[i] = -1
		}
	}
	return out
}

func skipRunes(s string, p, n int) int {
	for ; n > 0 && p < len(s); n-- {
		_, w := utf8.DecodeRuneInString(s[p:])
		p += w
	}
	if n > 0 {
		return len(s) + 1
	}
	return p
}

// ── folding ─────────────────────────────────────────────────────────────────

// foldRune maps a rune to the smallest one it case-folds with, the
// equivalence (?i) matches under.
func foldRune(r rune) rune {
	if r < utf8.RuneSelf {
		if 'a' <= r && r <= 'z' {
			return r - 'a' + 'A'
		}
		return r
	}
	lowest := r
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		if f < lowest {
			lowest = f
		}
	}
	return lowest
}

// foldPages caches foldRune for the Basic Multilingual Plane a page of 256
// runes at a time, filled on first use. Text in Cyrillic or full of arrows and
// dashes folds every rune, and three SimpleFold calls a rune were a sixth of
// what the finders cost.
var foldPages [256]atomic.Pointer[[256]uint16]

func foldCached(r rune) rune {
	if r > 0xFFFF {
		return foldRune(r)
	}
	pg := foldPages[r>>8].Load()
	if pg == nil {
		pg = new([256]uint16)
		base := r &^ 0xFF
		for i := range pg {
			f := foldRune(base + rune(i))
			if f > 0xFFFF {
				f = base + rune(i) // never happens in the BMP; keeps the table honest
			}
			pg[i] = uint16(f) //nolint:gosec // f <= 0xFFFF by the check above
		}
		foldPages[r>>8].Store(pg)
	}
	return rune(pg[r&0xFF])
}

func foldString(s string) string {
	var b strings.Builder
	for _, r := range s {
		b.WriteRune(foldRune(r))
	}
	return b.String()
}

// foldAligned folds s rune by rune, keeping every offset where it was. It
// fails on invalid UTF-8 and on a rune whose fold has another width.
func foldAligned(s string) (string, bool) {
	b := make([]byte, len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			if 'a' <= c && c <= 'z' {
				c -= 'a' - 'A'
			}
			b[i] = c
			i++
			continue
		}
		r, w := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && w == 1 {
			return "", false
		}
		fr := foldCached(r)
		if utf8.RuneLen(fr) != w {
			return "", false
		}
		utf8.EncodeRune(b[i:], fr)
		i += w
	}
	return string(b), true
}

// ── what the literals need around them ─────────────────────────────────────

func isASCIIWord(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}

// isRESpace is RE2's \s.
func isRESpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r'
}

// wordBefore reports whether the byte before k is an ASCII word character,
// which rules out both \b and a [^\p{L}\p{N}_] in front of a key word.
func wordBefore(s string, k int) bool { return k > 0 && isASCIIWord(s[k-1]) }

// runeStartBefore is where the rune ending at k begins.
func runeStartBefore(s string, k int) int {
	if k == 0 {
		return 0
	}
	_, w := utf8.DecodeLastRuneInString(s[:k])
	return k - w
}

// hasAt reports whether the folded view holds the folded literal at i.
func hasAt(f string, i int, lit string) bool {
	return i >= 0 && i+len(lit) <= len(f) && f[i:i+len(lit)] == lit
}

// kwEnd is where one of the folded words begins at k ends, or -1.
func kwEnd(f string, k int, words []string) int {
	for _, w := range words {
		if hasAt(f, k, w) {
			return k + len(w)
		}
	}
	return -1
}

// delimAt reports whether s[i:] opens with `\\*['"]?\s*[:=]`, or with
// `\\*['"]?=` when eqOnly. It returns the index after the delimiter.
func delimAt(s string, i int, eqOnly bool) (int, bool) {
	for i < len(s) && s[i] == '\\' {
		i++
	}
	if i < len(s) && (s[i] == '\'' || s[i] == '"') {
		i++
	}
	if !eqOnly {
		for i < len(s) && isRESpace(s[i]) {
			i++
		}
	}
	if i < len(s) && (s[i] == '=' || !eqOnly && s[i] == ':') {
		return i + 1, true
	}
	return 0, false
}

// firstOf is the first byte of set in s[i:i+limit], or 0.
func firstOf(s string, i, limit int, set string) (int, byte) {
	end := min(i+limit, len(s))
	if j := strings.IndexAny(s[i:end], set); j >= 0 {
		return i + j, s[i+j]
	}
	return -1, 0
}

// runBack is the start of the run of bytes in set that ends at k.
func runBack(s string, k int, in func(byte) bool) int {
	for k > 0 && in(s[k-1]) {
		k--
	}
	return k
}

// runBackMax is runBack that looks at most n bytes back.
func runBackMax(s string, k, n int, in func(byte) bool) int {
	stop := max(k-n, 0)
	for k > stop && in(s[k-1]) {
		k--
	}
	return k
}

func isNameByte(c byte) bool { return isASCIIWord(c) || c == '.' || c == '-' }

func isSchemeByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '+' || c == '.' || c == '-'
}

// unique is a region of one start, for rules whose leading context fixes it.
func unique(p int) (int, int, bool) { return p, p, true }

var (
	fwPassw  = []string{foldString("password"), foldString("passwd")}
	fwPwdAll = []string{foldString("password"), foldString("passwd"), foldString("pwd")}
)

// apiKeyWord is where `api[_-]?key` (or `api[\s_-]?key` with space) begins
// when its "key" sits at k, or -1.
func apiKeyBefore(f string, k int, space bool) int {
	if hasAt(f, k-3, "API") {
		return k - 3
	}
	if k >= 4 && hasAt(f, k-4, "API") {
		c := f[k-1]
		if c == '_' || c == '-' || space && isRESpace(c) {
			return k - 4
		}
	}
	return -1
}

// ── one finder per rule ─────────────────────────────────────────────────────

var (
	// The key word of genericKVRE, followed straight by its delimiter, with up
	// to 64 name bytes in front of it.
	genericKVFind = newFinder(genericKVRE, 64, true,
		[]string{"key", "secret", "token", "passw", "authorization"},
		func(v *view, k int) (int, int, bool) {
			start := k
			var end int
			switch {
			case hasAt(v.f, k, "KEY"):
				start = apiKeyBefore(v.f, k, false)
				end = k + 3
			case hasAt(v.f, k, "PASSW"):
				end = kwEnd(v.f, k, fwPassw)
			default:
				end = kwEnd(v.f, k, []string{"SECRET", "TOKEN", "AUTHORIZATION"})
			}
			if start < 0 || end < 0 {
				return 0, 0, false
			}
			if _, ok := delimAt(v.s, end, false); !ok {
				return 0, 0, false
			}
			return runBackMax(v.s, start, 64, isNameByte), start, true
		})

	envKeyFind = newFinder(envKeyRE, 0, false, []string{"_KEY"},
		func(v *view, k int) (int, int, bool) {
			if _, ok := delimAt(v.s, k+4, false); !ok {
				return 0, 0, false
			}
			return unique(runBack(v.s, k, isASCIIWord))
		})

	passwordAssignFind = newFinder(passwordAssignRE, 64, true, []string{"passw", "pwd"},
		func(v *view, k int) (int, int, bool) {
			end := kwEnd(v.f, k, fwPwdAll)
			if end < 0 {
				return 0, 0, false
			}
			if _, ok := delimAt(v.s, end, true); !ok {
				return 0, 0, false
			}
			return runBackMax(v.s, k, 64, isNameByte), k, true
		})

	// `[\w.-]*[_.\-]pass` behind a character that is not one of those: the
	// start is that character, and nothing else can be.
	passSuffixFind = newFinder(passSuffixAssignRE, 0, true, []string{"pass"},
		func(v *view, k int) (int, int, bool) {
			if k == 0 || !strings.ContainsRune("_.-", rune(v.s[k-1])) {
				return 0, 0, false
			}
			if _, ok := delimAt(v.s, k+4, true); !ok {
				return 0, 0, false
			}
			run := runBack(v.s, k, isNameByte)
			if run == 0 {
				return unique(0)
			}
			return unique(runeStartBefore(v.s, run))
		})

	quotedSecretFind = newFinder(quotedSecretRE, 0, true, []string{"passw", "pwd", "secret", "token", "key"},
		func(v *view, k int) (int, int, bool) {
			start, end := quotedKeyWord(v.f, k)
			if start < 0 || wordBefore(v.s, start) {
				return 0, 0, false
			}
			i := end
			if i >= len(v.s) || !isRESpace(v.s[i]) {
				return 0, 0, false
			}
			for i < len(v.s) && isRESpace(v.s[i]) {
				i++
			}
			if e := kwEnd(v.f, i, []string{"IS", "WAS", "FOR"}); e >= 0 && e < len(v.s) && isRESpace(v.s[e]) {
				for e < len(v.s) && isRESpace(v.s[e]) {
					e++
				}
				i = e
			}
			if !quoteAfter(v.s, i) {
				return 0, 0, false
			}
			return unique(start)
		})

	quotedAssignedFind = newFinder(quotedAssignedSecretRE, 0, true, []string{"passw", "pwd", "secret", "token", "key"},
		func(v *view, k int) (int, int, bool) {
			start, end := quotedKeyWord(v.f, k)
			if start < 0 || wordBefore(v.s, start) {
				return 0, 0, false
			}
			i, ok := delimAt(v.s, end, false)
			if !ok {
				return 0, 0, false
			}
			for i < len(v.s) && isRESpace(v.s[i]) {
				i++
			}
			if !quoteAfter(v.s, i) {
				return 0, 0, false
			}
			return unique(start)
		})

	providerFind = newFinder(providerRE, 0, false, providerHints,
		func(v *view, k int) (int, int, bool) {
			if wordBefore(v.s, k) {
				return 0, 0, false
			}
			return unique(k)
		})

	// `\b<scheme>://`, then a ':' before any space, '/' or '@', then an '@'
	// with something on both sides before the next space.
	connURLFind = newFinder(connURLRE, 64, false, []string{"://"},
		func(v *view, k int) (int, int, bool) {
			s := v.s
			i := k + 3
			for i < len(s) && !isRESpace(s[i]) && s[i] != '/' && s[i] != '@' && s[i] != ':' {
				i++
			}
			if i >= len(s) || s[i] != ':' {
				return 0, 0, false
			}
			i++
			j := v.space.next(s, i, isRESpace)
			// An '@' with a byte of password before it and of host after.
			if j-i < 3 || v.at.next(s, i+1, func(c byte) bool { return c == '@' }) >= j-1 {
				return 0, 0, false
			}
			// What follows the scheme does not depend on where it starts, so
			// the match starts at the first place a scheme can: a letter with
			// \b in front of it.
			p := max(runBack(s, k, isSchemeByte), v.floor)
			for ; p < k; p++ {
				c := s[p]
				if (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') && !wordBefore(s, p) {
					return unique(p)
				}
			}
			return 0, 0, false
		})

	bearerFind = newFinder(bearerRE, 0, true, []string{"bearer", "basic"},
		func(v *view, k int) (int, int, bool) {
			end := kwEnd(v.f, k, []string{"BEARER", "BASIC"})
			if end < 0 || wordBefore(v.s, k) || end >= len(v.s) || !isRESpace(v.s[end]) {
				return 0, 0, false
			}
			return unique(k)
		})

	passwordFlagFind = newFinder(passwordFlagRE, 2, true, flagHints,
		func(_ *view, k int) (int, int, bool) {
			lo := max(k-2, 0)
			return lo, k, true
		})

	loginFind = newFinder(loginPassRE, 0, true, []string{"login", "redis-cli", "ftp", "smbclient"},
		func(v *view, k int) (int, int, bool) {
			if wordBefore(v.s, k) {
				return 0, 0, false
			}
			return unique(k)
		})

	prosePasswordFind = newFinder(prosePasswordRE, 0, true, []string{"pass", "парол"},
		func(v *view, k int) (int, int, bool) {
			if wordBefore(v.s, k) {
				return 0, 0, false
			}
			return unique(runeStartBefore(v.s, k))
		})

	// Key words in other languages, then the first ':' or '=' — no line break
	// or '[' before it — within the 33 runes the filler allows.
	intlFillerFind = newFinder(genericKVIntlFillerRE, 0, true, kvIntlHintsBase,
		func(v *view, k int) (int, int, bool) {
			if wordBefore(v.s, k) {
				return 0, 0, false
			}
			if _, c := firstOf(v.s, k, 200, ":=\n["); c != ':' && c != '=' {
				return 0, 0, false
			}
			return unique(runeStartBefore(v.s, k))
		})

	// A key word, the first ':' or '=' after it on the line, and a value
	// with a non-ASCII byte in it.
	intlValueFind = newFinder(intlValueRE, 0, true,
		append(append([]string{}, kvIntlHintsBase...), "api", "secret", "token", "passw"),
		func(v *view, k int) (int, int, bool) {
			s := v.s
			if wordBefore(s, k) {
				return 0, 0, false
			}
			d, c := firstOf(s, k, 200, ":=\n")
			if c != ':' && c != '=' {
				return 0, 0, false
			}
			i := d + 1
			for i < len(s) && isRESpace(s[i]) {
				i++
			}
			for i < len(s) && s[i] == '\\' {
				i++
			}
			if i < len(s) && (s[i] == '\'' || s[i] == '"') {
				i++
			}
			if j := v.intl.next(s, i, func(c byte) bool {
				return c >= utf8.RuneSelf || isRESpace(c) || c == '\'' || c == '"'
			}); j < len(s) && s[j] >= utf8.RuneSelf {
				return unique(runeStartBefore(s, k))
			}
			return 0, 0, false
		})
)

// The rest are rarer, and their gates mostly keep them off; a finder each
// all the same, so no rule is left walking a text its gate let through.
var (
	wordStart = func(v *view, k int) (int, int, bool) {
		if wordBefore(v.s, k) {
			return 0, 0, false
		}
		return unique(k)
	}
	prevRuneStart = func(v *view, k int) (int, int, bool) {
		if wordBefore(v.s, k) {
			return 0, 0, false
		}
		return unique(runeStartBefore(v.s, k))
	}

	intlKVFind       = newFinder(genericKVIntlRE, 0, true, kvIntlHintsBase, prevRuneStart)
	netrcFind        = newFinder(netrcRE, 0, true, []string{"machine", "login"}, wordStart)
	sshpassFind      = newFinder(sshpassRE, 0, true, []string{"sshpass"}, wordStart)
	mysqlFind        = newFinder(mysqlPassRE, 0, true, []string{"mysql", "mariadb"}, wordStart)
	awsSecretFind    = newFinder(awsSecretRE, 0, true, []string{"aws"}, wordStart)
	authTokenFind    = newFinder(authTokenRE, 0, true, []string{"authorization"}, wordStart)
	jwtFind          = newFinder(jwtRE, 0, false, []string{"eyJ"}, wordStart)
	awsAccessKeyFind = newFinder(awsAccessKeyRE, 0, false, []string{"AKIA", "ASIA"},
		func(_ *view, k int) (int, int, bool) { return unique(k) })
	webhookFind = newFinder(webhookPathRE, 0, false, []string{"/services/", "/workflows/", "/triggers/", "/api/webhooks/"},
		func(_ *view, k int) (int, int, bool) { return unique(k) })
	// `(?:^|\s)(?:-u|--user)`: the space sits one or two bytes before the "-u".
	userPairFind = newFinder(userPairRE, 2, true, []string{"-u"},
		func(_ *view, k int) (int, int, bool) { return max(k-2, 0), k, true })
	// `\b(?:set-)?cookie:` starts at the "set-" when there is one.
	cookieFind = newFinder(cookieRE, 4, true, []string{"cookie:"},
		func(_ *view, k int) (int, int, bool) { return max(k-4, 0), k, true })
	// The digits before ":A" are the whole run behind a non-digit, five to
	// sixteen of them.
	telegramFind = newFinder(telegramBotTokenRE, 0, false, []string{":A"},
		func(v *view, k int) (int, int, bool) {
			run := runBack(v.s, k, func(c byte) bool { return c >= '0' && c <= '9' })
			if n := k - run; n < 5 || n > 16 {
				return 0, 0, false
			}
			return unique(runeStartBefore(v.s, run))
		})
)

// kvIntlHintsBase are the stems of the non-English key words; every
// alternative the international rules spell starts with one.
var kvIntlHintsBase = []string{
	"парол", "токен", "секрет", "ключ",
	"contraseña", "senha", "passwort",
	"密码", "密碼", "パスワード", "비밀번호",
}

// quotedKeyWord finds the key word of the quoted-secret rules around the
// literal at k: (password|passwd|pwd|secret|token|api[\s_-]?key).
func quotedKeyWord(f string, k int) (start, end int) {
	if hasAt(f, k, "KEY") {
		if st := apiKeyBefore(f, k, true); st >= 0 {
			return st, k + 3
		}
		return -1, -1
	}
	if e := kwEnd(f, k, append([]string{"SECRET", "TOKEN"}, fwPwdAll...)); e >= 0 {
		return k, e
	}
	return -1, -1
}

// quoteAfter reports whether s[i:] opens with `\\*["'` + "`" + `]`.
func quoteAfter(s string, i int) bool {
	for i < len(s) && s[i] == '\\' {
		i++
	}
	return i < len(s) && (s[i] == '"' || s[i] == '\'' || s[i] == '`')
}

// finders maps each rule that has one to its finder.
var finders = map[*regexp.Regexp]*finder{
	genericKVRE: genericKVFind, envKeyRE: envKeyFind,
	passwordAssignRE: passwordAssignFind, passSuffixAssignRE: passSuffixFind,
	quotedSecretRE: quotedSecretFind, quotedAssignedSecretRE: quotedAssignedFind,
	providerRE: providerFind, connURLRE: connURLFind, bearerRE: bearerFind,
	passwordFlagRE: passwordFlagFind, loginPassRE: loginFind,
	prosePasswordRE: prosePasswordFind, genericKVIntlFillerRE: intlFillerFind,
	intlValueRE:     intlValueFind,
	genericKVIntlRE: intlKVFind, netrcRE: netrcFind, sshpassRE: sshpassFind,
	mysqlPassRE: mysqlFind, awsSecretRE: awsSecretFind, authTokenRE: authTokenFind,
	jwtRE: jwtFind, awsAccessKeyRE: awsAccessKeyFind, webhookPathRE: webhookFind,
	userPairRE: userPairFind, cookieRE: cookieFind, telegramBotTokenRE: telegramFind,
}
