// Package rustem is a Russian stemmer: the public Snowball Russian algorithm
// (snowballstem.org/algorithms/russian/stemmer.html), written from its
// description. All forms of one word reduce to one stem, so the index can key
// a lemma once and a query in any form finds it.
package rustem

import (
	"sort"
	"strings"
	"unicode/utf8"
)

type group struct {
	ends  [][]rune
	after []bool // the ending needs а or я before it
}

func newGroup(plain, afterAY string) *group {
	g := &group{}
	for _, s := range strings.Fields(plain) {
		g.ends = append(g.ends, []rune(s))
		g.after = append(g.after, false)
	}
	for _, s := range strings.Fields(afterAY) {
		g.ends = append(g.ends, []rune(s))
		g.after = append(g.after, true)
	}
	idx := make([]int, len(g.ends))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return len(g.ends[idx[a]]) > len(g.ends[idx[b]]) })
	ends := make([][]rune, len(idx))
	after := make([]bool, len(idx))
	for i, j := range idx {
		ends[i], after[i] = g.ends[j], g.after[j]
	}
	g.ends, g.after = ends, after
	return g
}

var (
	perfectiveGerund = newGroup("ив ивши ившись ыв ывши ывшись", "в вши вшись")
	reflexive        = newGroup("ся сь", "")
	adjective        = newGroup("ее ие ые ое ими ыми ей ий ый ой ем им ым ом его ого ему ому их ых ую юю ая яя ою ею", "")
	participle       = newGroup("ивш ывш ующ", "ем нн вш ющ щ")
	verb             = newGroup("ила ыла ена ейте уйте ите или ыли ей уй ил ыл им ым ен ило ыло ено ят ует уют ит ыт ены ить ыть ишь ую ю",
		"ла на ете йте ли й л ем н ло но ет ют ны ть ешь нно")
	noun  = newGroup("а ев ов ие ье е иями ями ами еи ии и ией ей ой ий й иям ям ием ем ам ом о у ах иях ях ы ь ию ью ю ия ья я", "")
	deriv = newGroup("ост ость", "")
)

func isVowel(r rune) bool {
	switch r {
	case 'а', 'е', 'и', 'о', 'у', 'ы', 'э', 'ю', 'я':
		return true
	}
	return false
}

// cut finds the longest ending of g that ends w[:n] and starts at or after
// lo. It reports the new length of w after removing it, or -1. As in Snowball,
// the longest match is the only candidate: when its а/я condition fails there
// is no fallback to a shorter ending.
func (g *group) cut(w []rune, n, lo int) int {
	for i, e := range g.ends {
		start := n - len(e)
		if start < lo {
			continue
		}
		match := true
		for k, r := range e {
			if w[start+k] != r {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		if g.after[i] && (start-1 < lo || (w[start-1] != 'а' && w[start-1] != 'я')) {
			return -1
		}
		return start
	}
	return -1
}

// past returns the index after the first rune at or after i whose vowel-ness
// is vowel, or -1.
func past(w []rune, i int, vowel bool) int {
	for ; i < len(w); i++ {
		if isVowel(w[i]) == vowel {
			return i + 1
		}
	}
	return -1
}

// Stem returns the Snowball Russian stem of a lowercase word. ё is read as е.
// A word with no vowel has no region to cut in and comes back as it is.
func Stem(word string) string {
	var buf [48]rune
	w := buf[:0]
	for _, r := range word {
		if r == 'ё' {
			r = 'е'
		}
		w = append(w, r)
	}
	n := len(w)

	// RV starts after the first vowel; R2 after the second vowel+consonant
	// pair counted from there.
	rv, r2 := n, n
	if p := past(w, 0, true); p >= 0 {
		rv = p
		if p = past(w, p, false); p >= 0 {
			if p = past(w, p, true); p >= 0 {
				if p = past(w, p, false); p >= 0 {
					r2 = p
				}
			}
		}
	}

	// Step 1: a perfective gerund, else an optional reflexive followed by the
	// first of adjectival, verb, noun that is there.
	if e := perfectiveGerund.cut(w, n, rv); e >= 0 {
		n = e
	} else {
		if e := reflexive.cut(w, n, rv); e >= 0 {
			n = e
		}
		if e := adjective.cut(w, n, rv); e >= 0 {
			n = e
			if e := participle.cut(w, n, rv); e >= 0 {
				n = e
			}
		} else if e := verb.cut(w, n, rv); e >= 0 {
			n = e
		} else if e := noun.cut(w, n, rv); e >= 0 {
			n = e
		}
	}
	// Step 2.
	if n > rv && w[n-1] == 'и' {
		n--
	}
	// Step 3: a derivational ending that lies wholly in R2.
	if e := deriv.cut(w, n, r2); e >= 0 {
		n = e
	}
	// Step 4: a superlative, or a doubled н, or a soft sign.
	switch {
	case endsWith(w, n, rv, "ейше"):
		n = undouble(w, n-4, rv)
	case endsWith(w, n, rv, "ейш"):
		n = undouble(w, n-3, rv)
	case endsWith(w, n, rv, "нн"):
		n--
	case endsWith(w, n, rv, "ь"):
		n--
	}
	return string(w[:n])
}

func endsWith(w []rune, n, lo int, suffix string) bool {
	e := []rune(suffix)
	start := n - len(e)
	if start < lo {
		return false
	}
	for k, r := range e {
		if w[start+k] != r {
			return false
		}
	}
	return true
}

// undouble drops one н from a final нн.
func undouble(w []rune, n, lo int) int {
	if endsWith(w, n, lo, "нн") {
		return n - 1
	}
	return n
}

// rootFloor is the shortest stem a second Snowball pass may leave, and
// rootStep the most letters it may take off. Running the stemmer on its own
// output keeps cutting (удален -> уд, зелен -> зел, телеграм -> телегр), and a
// stem that short joins words that share only a beginning.
const (
	rootFloor = 4
	rootStep  = 1
)

// Root is Stem, taken further while each pass stays inside those bounds.
// Snowball is not idempotent (лимиты stems to лимит, удаленная to удален and
// that to удал), so one pass leaves the forms of a word on different stems.
func Root(word string) string {
	st := Stem(word)
	for i := 0; i < 3; i++ {
		next := Stem(st)
		if next == st || utf8.RuneCountInString(next) < rootFloor ||
			utf8.RuneCountInString(st)-utf8.RuneCountInString(next) > rootStep {
			break
		}
		st = next
	}
	return st
}

// Keys are the stems a document word is filed under: its Root, the one more
// pass of Snowball when that moves it, and the word itself, because another
// form's Root can be exactly this word (лимиты roots to лимит, which is also a
// word). A query looks up the Root of its word, so every form finds every form
// without cutting each word down to three letters.
func Keys(word string) []string {
	r := Root(word)
	out := []string{r}
	if next := Stem(r); next != r && next != "" {
		out = append(out, next)
	}
	if word != r && word != out[len(out)-1] {
		out = append(out, word)
	}
	return out
}

// HasKey reports whether stem is one of the keys of word.
func HasKey(word, stem string) bool {
	for _, k := range Keys(word) {
		if k == stem {
			return true
		}
	}
	return false
}
