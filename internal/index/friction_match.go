package index

import (
	"strings"
	"sync"
	"unicode/utf8"
)

// What frictionPhrasesIn found in a line.
const (
	hitPhrase       = 1 << iota // one of frictionPhrases
	hitDoesNotExist             // "does not exist", which needs an error marker
	hitNoSuchTable              // "no such table:", which needs noSuchTableRE
)

// frictionMatcher finds every friction phrase in one pass over a line. Looking
// for them one strings.Contains at a time walked each line of tool output
// seventy times, after lowering a copy of it: on a 40k-session store that was
// 72 of the 103 CPU-seconds isFriction took (#5586).
//
// It is an Aho-Corasick automaton over byte classes. The phrases are lowercase
// ASCII, and an ASCII capital is given its lowercase letter's class, so an
// ASCII line is matched as strings.ToLower would have lowered it without
// lowering it.
type frictionMatcher struct {
	class [256]uint8
	next  [][]uint16
	out   []uint8
}

var frictionMatch = sync.OnceValue(func() *frictionMatcher {
	return newFrictionMatcher(map[string]uint8{
		"does not exist": hitDoesNotExist,
		"no such table:": hitNoSuchTable,
	}, frictionPhrases)
})

func newFrictionMatcher(aux map[string]uint8, phrases []string) *frictionMatcher {
	m := &frictionMatcher{}
	pats := map[string]uint8{}
	for _, p := range phrases {
		pats[p] |= hitPhrase
	}
	for p, bit := range aux {
		pats[p] |= bit
	}
	classes := uint8(1) // 0 is every byte no phrase holds
	for p := range pats {
		for i := range len(p) {
			if c := p[i]; m.class[c] == 0 {
				m.class[c] = classes
				classes++
			}
		}
	}
	for c := 'A'; c <= 'Z'; c++ {
		m.class[c] = m.class[c+'a'-'A']
	}
	// The trie, then the failure links breadth first, folding each state's
	// output and missing moves in from the state it falls back to.
	m.next = [][]uint16{make([]uint16, classes)}
	m.out = []uint8{0}
	for p, bit := range pats {
		s := 0
		for i := range len(p) {
			c := m.class[p[i]]
			if m.next[s][c] == 0 {
				m.next = append(m.next, make([]uint16, classes))
				m.out = append(m.out, 0)
				m.next[s][c] = uint16(len(m.next) - 1) //nolint:gosec // states are bounded by the fixed phrase list
			}
			s = int(m.next[s][c])
		}
		m.out[s] |= bit
	}
	fail := make([]uint16, len(m.next))
	var queue []uint16
	for c := range m.next[0] {
		if t := m.next[0][c]; t != 0 {
			queue = append(queue, t)
		}
	}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		m.out[s] |= m.out[fail[s]]
		for c := range m.next[s] {
			t := m.next[s][c]
			if t == 0 {
				m.next[s][c] = m.next[fail[s]][c]
				continue
			}
			fail[t] = m.next[fail[s]][c]
			queue = append(queue, t)
		}
	}
	return m
}

// frictionPhrasesIn reports which phrases strings.ToLower(l) contains. A
// phrase hit ends the walk: nothing else is asked of a line that has one.
func frictionPhrasesIn(l string) uint8 {
	m := frictionMatch()
	for i := range len(l) {
		if l[i] >= utf8.RuneSelf {
			// Lowering can move a rune into ASCII (U+212A KELVIN SIGN is k),
			// so a line that is not ASCII is lowered first, as it was.
			return m.match(strings.ToLower(l))
		}
	}
	return m.match(l)
}

func (m *frictionMatcher) match(s string) uint8 {
	var hit uint8
	state := uint16(0)
	for i := range len(s) {
		state = m.next[state][m.class[s[i]]]
		if o := m.out[state]; o != 0 {
			hit |= o
			if hit&hitPhrase != 0 {
				return hit
			}
		}
	}
	return hit
}
