package main

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// A past session is scored on two fields as well as deja's own order: its
// whole transcript, and how it ended — the last prose turn from the assistant,
// which is where an agent states the root cause and the fix without tool-output
// dilution. Compound identifiers (ModelChoiceField, separability_matrix) are
// indexed both whole and split into their component words.
const (
	bm25K1      = 1.5
	bm25B       = 0.75
	idfFloorMul = 0.25
)

// endWeight and dejaWeight are how much the ending and deja's order count
// against the transcript's BM25.
var endWeight, dejaWeight = 1.0, 1.0

type doc struct {
	sid  string
	orig string
	// task is the start of the session's first user message, on one line;
	// taskLine is all of it and endLine how the session ended, on one line.
	task     string
	taskLine string
	endLine  string
	files    []string
	changes  []change
	started  time.Time
	all      map[string]int
	allLen   int
	end      map[string]int
	endLen   int
	// msgFrom and msgTo bound the session's messages in corpus.msgs.
	msgFrom, msgTo int
}

type corpus struct {
	sig  string
	docs []doc
	// msgs is every message of every session, for message and window retrieval.
	msgs []msg
	byID map[string]int
	// q and sc cache the messages' scores for the last query.
	q  string
	sc []float64
}

// msgScores is every message's BM25 for the query, kept for the request.
func (c *corpus) msgScores(q string) []float64 {
	if c.sc == nil || c.q != q {
		qt := terms(q)
		c.q, c.sc = q, bm25Of(len(c.msgs), qt, func(i int) (map[string]int, int) { return c.msgs[i].tf, c.msgs[i].n })
	}
	return c.sc
}

// corpora caches one user's documents until a transcript changes. Guarded by
// server.mu, which every request holds.
var corpora = map[string]*corpus{}

// maxCorpora is how many users' corpora stay in memory.
const maxCorpora = 32

var word = regexp.MustCompile(`[\p{L}\p{N}_]+`)

// han is a run of CJK ideographs, which carry no spaces between words.
var han = regexp.MustCompile(`\p{Han}+`)

func hasHan(s string) bool {
	for _, r := range s {
		if r >= 0x3400 && unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// terms is the text's words lowercased, each compound identifier followed by
// its parts: "ModelChoiceField" gives modelchoicefield, model, choice, field.
// A run of Chinese gives its characters and their pairs instead.
func terms(s string) []string {
	checkHan := hasHan(s)
	var out []string
	for _, w := range word.FindAllString(s, -1) {
		if checkHan && hasHan(w) {
			for _, r := range han.FindAllString(w, -1) {
				rs := []rune(r)
				for i := range rs {
					out = append(out, string(rs[i]))
					if i+1 < len(rs) {
						out = append(out, string(rs[i:i+2]))
					}
				}
			}
			for rest := range strings.FieldsSeq(han.ReplaceAllString(w, " ")) {
				out = append(out, strings.ToLower(rest))
			}
			continue
		}
		out = append(out, strings.ToLower(w))
		if parts := identParts(w); len(parts) > 1 {
			out = append(out, parts...)
		}
	}
	return out
}

func identParts(w string) []string {
	var parts []string
	var cur []rune
	rs := []rune(w)
	flush := func() {
		if len(cur) > 0 {
			parts = append(parts, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	for i, r := range rs {
		if r == '_' {
			flush()
			continue
		}
		if unicode.IsUpper(r) && i > 0 && (unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1])) {
			flush()
		}
		cur = append(cur, r)
	}
	flush()
	return parts
}

func counts(ts []string) map[string]int {
	m := make(map[string]int, len(ts)/2)
	for _, t := range ts {
		m[t]++
	}
	return m
}

// ending is the session's last assistant turn that is prose rather than a
// tool call or a thought.
func ending(turns []turn) string {
	for _, tr := range slices.Backward(turns) {
		t := strings.TrimSpace(tr.text)
		if tr.role == "assistant" && t != "" && !strings.HasPrefix(t, "[tool") && !strings.HasPrefix(t, "[thinking") {
			return t
		}
	}
	return ""
}

func isEditTurn(s string) bool {
	return strings.Contains(s, `"old_str`) || strings.Contains(s, `"new_str`) ||
		strings.Contains(s, "[tool_use Edit]") || strings.Contains(s, "[tool_use MultiEdit]") ||
		strings.Contains(s, "[tool_use Write]")
}

func (s *server) corpus(proj string) *corpus {
	files, _ := filepath.Glob(filepath.Join(proj, "*.sid"))
	sort.Strings(files)
	var sig strings.Builder
	for _, f := range files {
		src := strings.TrimSuffix(f, ".sid") + ".jsonl"
		if st, err := os.Stat(src); err == nil {
			sig.WriteString(src + st.ModTime().String() + "\n")
		}
	}
	if c := corpora[proj]; c != nil && c.sig == sig.String() {
		return c
	}
	c := &corpus{sig: sig.String(), byID: map[string]int{}}
	for _, f := range files {
		sid := strings.TrimSuffix(filepath.Base(f), ".sid")
		src := strings.TrimSuffix(f, ".sid") + ".jsonl"
		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		turns := readTurns(data)
		var all strings.Builder
		for _, t := range turns {
			all.WriteString(t.text)
			all.WriteByte('\n')
		}
		task, end, final, touched := sessionChanges(turns)
		at, et := terms(all.String()), terms(end)
		st, _ := os.Stat(src)
		orig, _ := os.ReadFile(f)
		oneTask := strings.Join(strings.Fields(task), " ")
		oneEnd := strings.Join(strings.Fields(end), " ")
		c.docs = append(c.docs, doc{
			sid:      sid,
			orig:     string(orig),
			task:     clip(oneTask, 100),
			taskLine: clip(oneTask, 4000),
			endLine:  clip(oneEnd, 4000),
			files:    touched,
			changes:  allEdits(turns, final),
			started:  st.ModTime(),
			all:      counts(at),
			allLen:   len(at),
			end:      counts(et),
			endLen:   len(et),
			msgFrom:  len(c.msgs),
		})
		c.byID[sid] = len(c.docs) - 1
		if s.messages || s.windows {
			for i, t := range turns {
				body := "[" + t.role + "] " + t.text
				mt := terms(cut(body, msgIndexChars))
				edit := t.role == "assistant" && isEditTurn(t.text)
				c.msgs = append(c.msgs, msg{doc: len(c.docs) - 1, idx: i, text: body, edit: edit, tf: counts(mt), n: len(mt)})
			}
		}
		c.docs[len(c.docs)-1].msgTo = len(c.msgs)
	}
	// A corpus holds every message of its user; keep a few users, not all.
	if len(corpora) >= maxCorpora {
		clear(corpora)
	}
	corpora[proj] = c
	return c
}

// bm25 scores every document's field against the query the way the Okapi
// formula does, with negative idf floored at a quarter of the mean idf.
func bm25(docs []doc, q []string, field func(d doc) (map[string]int, int)) []float64 {
	return bm25Of(len(docs), q, func(i int) (map[string]int, int) { return field(docs[i]) })
}

func bm25Of(size int, q []string, field func(i int) (map[string]int, int)) []float64 {
	n := float64(size)
	df := map[string]int{}
	var total float64
	for i := range size {
		m, l := field(i)
		total += float64(l)
		for t := range m {
			df[t]++
		}
	}
	avg := total / math.Max(n, 1)
	idf := make(map[string]float64, len(df))
	var sum float64
	var neg []string
	for t, c := range df {
		v := math.Log(n-float64(c)+0.5) - math.Log(float64(c)+0.5)
		idf[t] = v
		sum += v
		if v < 0 {
			neg = append(neg, t)
		}
	}
	floor := idfFloorMul * sum / math.Max(float64(len(df)), 1)
	for _, t := range neg {
		idf[t] = floor
	}
	out := make([]float64, size)
	type qterm struct {
		t string
		w float64
	}
	var qs []qterm
	at := map[string]int{}
	for _, t := range q {
		i, ok := at[t]
		if !ok {
			at[t] = len(qs)
			qs = append(qs, qterm{t, 1})
		} else {
			qs[i].w++
		}
	}
	for i := range out {
		m, l := field(i)
		norm := bm25K1 * (1 - bm25B + bm25B*float64(l)/math.Max(avg, 1))
		for _, qt := range qs {
			tf := float64(m[qt.t])
			if tf > 0 {
				out[i] += qt.w * idf[qt.t] * tf * (bm25K1 + 1) / (tf + norm)
			}
		}
	}
	return out
}

func zscore(v []float64) []float64 {
	var mean, sq float64
	for _, x := range v {
		mean += x
	}
	mean /= math.Max(float64(len(v)), 1)
	for _, x := range v {
		sq += (x - mean) * (x - mean)
	}
	sd := math.Sqrt(sq/math.Max(float64(len(v)), 1)) + 1e-9
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = (x - mean) / sd
	}
	return out
}

// rerank orders every session of the user by the two fields and deja's rank.
// Sessions deja did not return come in with no snippets.
func (s *server) rerank(proj, q string, hits []search.Hit) []search.Hit {
	c := s.corpus(proj)
	if len(c.docs) == 0 {
		return hits
	}
	qt := terms(q)
	all := zscore(bm25(c.docs, qt, func(d doc) (map[string]int, int) { return d.all, d.allLen }))
	end := zscore(bm25(c.docs, qt, func(d doc) (map[string]int, int) { return d.end, d.endLen }))
	dz := dejaZ(c, hits)
	byID := map[string]search.Hit{}
	for _, h := range hits {
		if _, ok := byID[h.Session.ID]; !ok {
			byID[h.Session.ID] = h
		}
	}
	type scored struct {
		h search.Hit
		v float64
	}
	var out []scored
	for i, d := range c.docs {
		h, ok := byID[d.sid]
		if !ok && !mentioned(d, qt) {
			// Neither deja nor BM25 matched it: not an answer.
			continue
		}
		if !ok {
			h = search.Hit{Session: model.Session{ID: d.sid, Started: d.started}}
		}
		v := all[i] + endWeight*end[i] + dejaWeight*dz[i]
		h.Score = v
		out = append(out, scored{h, v})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].v > out[j].v })
	res := make([]search.Hit, len(out))
	for i, o := range out {
		res[i] = o.h
	}
	return res
}

func mentioned(d doc, qt []string) bool {
	for _, t := range qt {
		if d.all[t] > 0 {
			return true
		}
	}
	return false
}

// dejaZ is deja's order of the user's sessions as a z-score, best highest;
// a session deja did not return ranks last.
func dejaZ(c *corpus, hits []search.Hit) []float64 {
	pos := map[string]int{}
	for i, h := range hits {
		if _, ok := pos[h.Session.ID]; !ok {
			pos[h.Session.ID] = i
		}
	}
	dr := make([]float64, len(c.docs))
	for i, d := range c.docs {
		p, ok := pos[d.sid]
		if !ok {
			p = len(c.docs)
		}
		dr[i] = -float64(p)
	}
	return zscore(dr)
}
