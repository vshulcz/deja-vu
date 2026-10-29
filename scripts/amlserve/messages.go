package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// The default answer combines session-level outcomes with message-level
// retrieval: it opens with the best matching sessions (their task, how they
// ended, the files they touched and the code they changed), lists the
// remaining matching tasks in brief, and fills the rest of the budget with
// matching messages and their neighbours.
const (
	// msgIndexChars is how much of a message is indexed: a tool dump past it
	// adds words, not meaning.
	msgIndexChars = 20000
	// msgEditChars is how much of a code-edit message is shown so a function
	// replacement is not cut in half when ordinary tool output is trimmed.
	msgEditChars = 20000
)

// answerChars bounds the total text returned across all items of one search.
var answerChars = 412160

// msgShowChars is how much of one non-edit message an answer shows, and
// msgNeighbours how many messages on each side come with a hit.
var msgShowChars, msgNeighbours = 20000, 3

// briefTask and briefEnd are how much of a session's task and ending its
// brief shows.
var briefTask, briefEnd = 310, 220

// sessionWeight is how much the session's own match counts against the
// message's.
var sessionWeight = 1.0

// msgDejaWeight is how much deja's own order of the sessions counts in a
// session's match.
var msgDejaWeight = 1.0

type msg struct {
	doc  int
	idx  int
	text string
	edit bool
	tf   map[string]int
	n    int
}

func showMsg(m msg) string {
	if m.edit && msgEditChars > msgShowChars {
		return cut(m.text, msgEditChars)
	}
	return cut(m.text, msgShowChars)
}

func (s *server) messageAnswer(proj, idx, q string, topK int) []item {
	c := s.corpus(proj)
	if len(c.msgs) == 0 {
		return []item{}
	}
	qt := terms(q)
	all := zscore(bm25(c.docs, qt, func(d doc) (map[string]int, int) { return d.all, d.allLen }))
	end := zscore(bm25(c.docs, qt, func(d doc) (map[string]int, int) { return d.end, d.endLen }))
	ms := zscore(bm25Of(len(c.msgs), qt, func(i int) (map[string]int, int) { return c.msgs[i].tf, c.msgs[i].n }))
	if !c.mentions(qt) {
		return []item{}
	}
	sess := make([]float64, len(c.docs))
	for d := range sess {
		sess[d] = all[d] + endWeight*end[d]
	}
	if msgDejaWeight != 0 {
		if hits, err := rank(idx, q); err == nil {
			for d, z := range dejaZ(c, hits) {
				sess[d] += msgDejaWeight * z
			}
		}
	}
	for i, m := range c.msgs {
		ms[i] += sessionWeight * sess[m.doc]
	}
	out := []item{}
	used := 0
	docs := make([]int, len(c.docs))
	for i := range docs {
		docs[i] = i
	}
	sort.SliceStable(docs, func(a, b int) bool {
		return sess[docs[a]] > sess[docs[b]]
	})
	leadCount := 0
	addLead := func(d int) {
		if !mentioned(c.docs[d], qt) || len(out) >= topK {
			return
		}
		text, id := s.render(proj, c.docs[d].sid, nil, q, s.budget(leadCount))
		leadCount++
		if text == "" || used+len(text) > answerChars {
			return
		}
		used += len(text)
		out = append(out, item{ID: id, Content: text, Score: sess[d], CreatedAt: c.docs[d].started.UTC().Format(time.RFC3339)})
	}
	firstLeads := min(1, s.lead, len(docs), topK)
	if topK <= s.lead {
		firstLeads = min(s.lead, len(docs), topK)
	}
	for _, d := range docs[:firstLeads] {
		addLead(d)
	}
	if s.brief > 0 && len(out) < topK {
		if it, ok := c.briefItem(docs, firstLeads, s.brief, qt, sess); ok && used+len(it.Content) <= answerChars {
			used += len(it.Content)
			out = append(out, it)
		}
	}
	for _, d := range docs[firstLeads:min(s.lead, len(docs), topK)] {
		addLead(d)
	}
	var tailItems []item
	tailChars := 0
	if s.brief > 0 && topK >= 20 {
		var fb string
		tailItems, tailChars, fb = c.changeItems(docs, qt, sess)
		if fb != "" && len(out) > 0 && used+len(fb)+2 <= answerChars {
			out[len(out)-1].Content += "\n\n" + fb
			used += len(fb) + 2
		}
	}
	msgTopK := topK - len(tailItems)
	msgAnswerChars := answerChars - tailChars
	order := make([]int, len(ms))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return ms[order[a]] > ms[order[b]] })
	// Messages of one session sit next to each other in c.msgs, in order.
	seen := make([]bool, len(c.msgs))
	// Windows are packed into items in rank order, so top_k items can still
	// fill the whole answer.
	pack := (msgAnswerChars - used) / max(msgTopK-len(out), 1)
	var cur []string
	curLen, curScore, curDoc, lastDoc := 0, 0.0, -1, -1
	flush := func() {
		if len(cur) == 0 {
			return
		}
		d := c.docs[curDoc]
		out = append(out, item{ID: fmt.Sprintf("%s#%d", d.orig, len(out)), Content: strings.Join(cur, "\n\n"), Score: curScore, CreatedAt: d.started.UTC().Format(time.RFC3339)})
		cur, curLen = nil, 0
	}
	for _, k := range order {
		if len(out) >= msgTopK {
			break
		}
		m := c.msgs[k]
		var win []string
		for j := k - msgNeighbours; j <= k+msgNeighbours; j++ {
			if j < 0 || j >= len(c.msgs) || c.msgs[j].doc != m.doc || seen[j] {
				continue
			}
			seen[j] = true
			win = append(win, showMsg(c.msgs[j]))
		}
		if len(win) == 0 {
			continue
		}
		t := strings.Join(win, "\n")
		// Each window says which past task it comes from, once per run of
		// windows from one session.
		if len(cur) == 0 || m.doc != lastDoc {
			hdr := "From past session " + c.docs[m.doc].orig
			if len(c.docs[m.doc].files) > 0 {
				hdr += " (" + strings.Join(c.docs[m.doc].files[:min(len(c.docs[m.doc].files), 6)], ", ") + ")"
			}
			t = hdr + ", task: " + c.docs[m.doc].task + "\n" + t
		}
		lastDoc = m.doc
		if used+len(t)+2 > msgAnswerChars {
			break
		}
		used += len(t) + 2 // and the blank line that joins it
		if len(cur) == 0 {
			curScore, curDoc = ms[k], m.doc
		}
		cur = append(cur, t)
		if curLen += len(t); curLen >= pack {
			flush()
		}
	}
	if len(out) < msgTopK {
		flush()
	}
	for _, ti := range tailItems {
		if len(out) >= topK || used+len(ti.Content) > answerChars {
			break
		}
		used += len(ti.Content)
		out = append(out, ti)
	}
	for i := range out {
		out[i].Score = 1.0 / float64(i+1)
	}
	return out
}

func (c *corpus) briefItem(docs []int, from, count int, qt []string, sess []float64) (item, bool) {
	var b strings.Builder
	first := -1
	shown := 0
	for _, d := range docs[from:min(from+count, len(docs))] {
		if !mentioned(c.docs[d], qt) {
			continue
		}
		if first < 0 {
			first = d
			b.WriteString("Other matching past tasks:\n")
		}
		fmt.Fprintf(&b, "\nPast task %s\nTask: %s\n", c.docs[d].orig, clip(c.docs[d].taskLine, briefTask))
		if shown < 4 && len(c.docs[d].files) > 0 {
			fmt.Fprintf(&b, "Files: %s\n", strings.Join(c.docs[d].files[:min(len(c.docs[d].files), 5)], ", "))
		}
		if e := clip(c.docs[d].endLine, briefEnd); e != "" {
			fmt.Fprintf(&b, "How it ended: %s\n", e)
		}
		shown++
	}
	if first < 0 {
		return item{}, false
	}
	return item{ID: c.docs[first].orig + "#brief", Content: b.String(), Score: sess[first], CreatedAt: c.docs[first].started.UTC().Format(time.RFC3339)}, true
}

func (c *corpus) changeItems(docs []int, qt []string, sess []float64) ([]item, int, string) {
	var items []item
	var fb strings.Builder
	chars := 0
	for _, d := range docs[:min(14, len(docs))] {
		if !mentioned(c.docs[d], qt) {
			continue
		}
		if len(c.docs[d].files) > 0 {
			fmt.Fprintf(&fb, "Past task %s files: %s\n", c.docs[d].orig, strings.Join(c.docs[d].files, ", "))
		}
		var cb strings.Builder
		fmt.Fprintf(&cb, "Past task %s changes\n", c.docs[d].orig)
		if len(c.docs[d].files) > 0 {
			fmt.Fprintf(&cb, "Files: %s\n", strings.Join(c.docs[d].files, ", "))
		}
		for _, ch := range c.docs[d].changes {
			if cb.Len() >= 11000 {
				break
			}
			fmt.Fprintf(&cb, "%s:\n%s\n", ch.file, clip(ch.new, 8000))
		}
		text := cut(cb.String(), 12000)
		if len(text) == 0 || chars+len(text) > answerChars/3 {
			continue
		}
		chars += len(text)
		items = append(items, item{ID: c.docs[d].orig + "#changes", Content: text, Score: sess[d], CreatedAt: c.docs[d].started.UTC().Format(time.RFC3339)})
	}
	return items, chars, fb.String()
}

// mentions is whether any session holds any of the terms.
func (c *corpus) mentions(qt []string) bool {
	for _, d := range c.docs {
		if mentioned(d, qt) {
			return true
		}
	}
	return false
}
