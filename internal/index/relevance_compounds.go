package index

import (
	"math"
	"strings"
)

func isGenericTouchedToken(w string) bool {
	switch w {
	case "models", "model", "views", "forms", "fields", "field",
		"tests", "test", "utils", "util", "common", "core",
		"base", "query", "index", "init", "admin", "contrib":
		return true
	}
	return false
}

func touchedPathsMatchCount(paths, terms []string, project string) int {
	if len(paths) == 0 || len(terms) == 0 {
		return 0
	}
	norm := make([]string, 0, len(paths))
	for _, p := range paths {
		if sp := stripTouchedPathPrefix(p, project); sp != "" {
			norm = append(norm, sp)
		}
	}
	if len(norm) == 0 {
		return 0
	}
	seen := make(map[string]bool, len(terms))
	matches := 0
	for _, t := range terms {
		if len(t) < 4 {
			continue
		}
		tl := strings.ToLower(t)
		if isGenericTouchedToken(tl) || seen[tl] {
			continue
		}
		seen[tl] = true
		for _, p := range norm {
			if strings.Contains(p, tl) {
				matches++
				break
			}
		}
		if matches >= 2 {
			return matches
		}
	}
	return matches
}

func stripTouchedPathPrefix(path, project string) string {
	low := strings.Trim(strings.ToLower(strings.ReplaceAll(path, "\\", "/")), "/")
	if low == "" {
		return ""
	}
	proj := strings.Trim(strings.ToLower(strings.ReplaceAll(project, "\\", "/")), "/")
	if proj != "" && strings.HasPrefix(low, proj+"/") {
		low = strings.TrimPrefix(low, proj+"/")
	}
	segs := strings.Split(low, "/")
	for len(segs) >= 3 && (segs[0] == "private" && (segs[1] == "tmp" || segs[1] == "var") ||
		((segs[0] == "users" || segs[0] == "home") && segs[1] != "")) {
		segs = segs[2:]
	}
	for len(segs) >= 2 && isContainerSegment(segs[0]) {
		segs = segs[1:]
	}
	if proj != "" {
		if idx := strings.LastIndex(proj, "/"); idx >= 0 {
			proj = proj[idx+1:]
		}
		if proj != "" && !isContainerSegment(proj) && len(segs) >= 2 && segs[0] == proj {
			segs = segs[1:]
		}
	}
	return strings.Join(segs, "/")
}

func isContainerSegment(seg string) bool {
	switch seg {
	case "testbed", "workspace", "work", "repo", "app", "tmp", "var", "opt":
		return true
	}
	return false
}

const (
	compoundPartWeight = 0.35
	maxCompoundParts   = 48
)

func isIdentByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

func hasASCIILetter(s string) bool {
	for i := range len(s) {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			return true
		}
	}
	return false
}

func isIgnoredCompoundSeg(s string) bool {
	switch s {
	case "py", "pyc", "pyo", "go", "js", "jsx", "ts", "tsx", "rs", "rb", "java",
		"cpp", "hpp", "html", "css", "json", "yaml", "yml", "toml", "md", "txt", "rst",
		"http", "https", "www", "com", "org", "net", "dev",
		"users", "home", "tmp", "var", "usr", "lib", "src", "bin",
		"site", "packages", "site-packages", "venv":
		return true
	}
	return false
}

func stripHTMLComments(s string) string {
	if !strings.Contains(s, "<!--") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		rel := strings.Index(s[i:], "<!--")
		if rel < 0 {
			b.WriteString(s[i:])
			break
		}
		start := i + rel
		b.WriteString(s[i:start])
		endRel := strings.Index(s[start+4:], "-->")
		if endRel < 0 {
			b.WriteString(s[start:])
			break
		}
		b.WriteByte(' ')
		i = start + 4 + endRel + 3
	}
	return b.String()
}

func isURLBoundary(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\v', '\f', ')', ']', '>', '"', '\'', '`':
		return true
	}
	return false
}

func stripEmbeddedURLs(s string) string {
	if !strings.Contains(s, "://") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		var schemeLen int
		if i+8 <= len(s) && strings.EqualFold(s[i:i+8], "https://") {
			schemeLen = 8
		} else if i+7 <= len(s) && strings.EqualFold(s[i:i+7], "http://") {
			schemeLen = 7
		}
		if schemeLen > 0 {
			j := i + schemeLen
			for j < len(s) && !isURLBoundary(s[j]) {
				j++
			}
			b.WriteByte(' ')
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func cleanIssueQuery(q string) string {
	if withoutComments := stripHTMLComments(q); withoutComments != q && len(RelevanceTerms(withoutComments)) >= 2 {
		q = withoutComments
	}
	if strings.Contains(q, "\n") || len(RelevanceTerms(q)) >= 8 {
		if withoutURLs := stripEmbeddedURLs(q); withoutURLs != q && len(RelevanceTerms(withoutURLs)) >= 2 {
			q = withoutURLs
		}
	}
	return q
}

func normDocTerm(t string) string {
	return strings.Trim(t, "._/-")
}

func isIssueSectionBoilerplate(t string) bool {
	switch t {
	case "references", "notes", "release", "fixes", "description", "comments", "other", "brief", "steps", "reproduce", "environment":
		return true
	}
	return false
}

func hasRawCodeShape(raw string) bool {
	s := strings.Trim(raw, ".,:;()[]{}'\"`")
	if len(s) < 3 {
		return false
	}
	if strings.ContainsAny(s, "_./") {
		return true
	}
	hasInternalUpper, hasLower, hasDigit, hasLetter := false, false, false, false
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z':
			hasLetter = true
			if i > 0 {
				hasInternalUpper = true
			}
		case c >= 'a' && c <= 'z':
			hasLetter = true
			hasLower = true
		case c >= '0' && c <= '9':
			hasDigit = true
		}
	}
	return (hasInternalUpper && hasLower) || (hasLetter && hasDigit)
}

func addTermDelimitedParts(terms []string, delim string, addPart func(string)) {
	for _, t := range terms {
		lowT := strings.ToLower(t)
		if strings.HasPrefix(lowT, "http://") || strings.HasPrefix(lowT, "https://") || strings.HasPrefix(lowT, "www.") {
			continue
		}
		if len(t) < 5 || !strings.ContainsAny(t, delim) {
			continue
		}
		for _, seg := range strings.FieldsFunc(t, func(r rune) bool {
			return strings.ContainsRune(delim, r)
		}) {
			addPart(seg)
		}
	}
}

func addAcronymCamelParts(tok string, addPart func(string)) {
	for p := 0; p+2 < len(tok); p++ {
		if tok[p] >= 'A' && tok[p] <= 'Z' &&
			(p == 0 || (tok[p-1] >= 'a' && tok[p-1] <= 'z') || (tok[p-1] >= '0' && tok[p-1] <= '9')) &&
			tok[p+1] >= 'A' && tok[p+1] <= 'Z' &&
			tok[p+2] >= 'a' && tok[p+2] <= 'z' {
			end := p + 3
			for end < len(tok) && ((tok[end] >= 'a' && tok[end] <= 'z') || (tok[end] >= '0' && tok[end] <= '9')) {
				end++
			}
			addPart(strings.ToLower(tok[p:end]))
		}
	}
}

func scanRawIdentCompoundParts(rawQuery string, terms []string, addPart func(string)) {
	scanText := stripEmbeddedURLs(stripHTMLComments(rawQuery))
	if strings.TrimSpace(scanText) == "" {
		scanText = strings.Join(terms, " ")
	}
	var k keyer
	start := -1
	for i := 0; i <= len(scanText); i++ {
		isIdent := i < len(scanText) && isIdentByte(scanText[i])
		switch {
		case isIdent && start < 0:
			start = i
		case !isIdent && start >= 0:
			tok := scanText[start:i]
			if len(tok) >= 6 {
				k.identifierParts(tok, func(b []byte) {
					if len(b) > 1 {
						addPart(string(b[1:]))
					}
				})
				addAcronymCamelParts(tok, addPart)
			}
			start = -1
		}
	}
}

// queryCompoundParts extracts sub-tokens from CamelCase and snake_case
// identifiers (plus hyphen/dot/slash compounds) that were not already listed
// in terms so DiagonalMatrix can match or rescue is_diagonal + MatrixSymbol.
func queryCompoundParts(rawQuery string, terms []string) []string {
	termSet := make(map[string]bool, len(terms))
	for _, t := range terms {
		termSet[t] = true
	}
	out := make([]string, 0, maxCompoundParts)
	seen := map[string]bool{}
	addPart := func(part string) {
		if part == "" || termSet[part] || seen[part] || len(out) >= maxCompoundParts {
			return
		}
		if len([]rune(part)) < 3 || !hasASCIILetter(part) || isIgnoredCompoundSeg(part) || len(RelevanceTerms(part)) != 1 {
			return
		}
		seen[part] = true
		out = append(out, part)
	}
	// Preserve snake_case identifiers inside dotted/slashed paths and calls
	// before breaking identifiers on underscores.
	addTermDelimitedParts(terms, "./", addPart)
	scanRawIdentCompoundParts(rawQuery, terms, addPart)
	addTermDelimitedParts(terms, "-./_", addPart)
	return out
}

// docQueryTermWeights computes bounded query-term salience multipliers from raw
// issue/document text: log-saturated query TF (clamped to [1, 8]), a title
// bonus for terms on the first non-empty line, and a code-identifier bonus.
func docQueryTermWeights(rawQuery string, terms []string) (map[string]float64, map[string]bool) {
	if len(terms) == 0 {
		return nil, nil
	}
	qtf := make(map[string]int, len(terms))
	titleTerms := map[string]bool{}
	codeTerm := map[string]bool{}
	cleaned := cleanIssueQuery(rawQuery)
	if cleaned != "" {
		for field := range strings.FieldsSeq(cleaned) {
			lowF := strings.ToLower(field)
			if strings.HasPrefix(lowF, "http://") || strings.HasPrefix(lowF, "https://") || strings.HasPrefix(lowF, "www.") {
				continue
			}
			fTerms := RelevanceTerms(field)
			if len(fTerms) == 0 {
				continue
			}
			fieldSeen := map[string]bool{}
			bump := func(key string) {
				if key != "" && !fieldSeen[key] {
					fieldSeen[key] = true
					qtf[key]++
				}
			}
			isCode := hasRawCodeShape(field)
			for _, t := range fTerms {
				bump(t)
				norm := normDocTerm(t)
				bump(norm)
				if isCode {
					codeTerm[t] = true
					if norm != "" {
						codeTerm[norm] = true
					}
				}
			}
			for _, p := range queryCompoundParts(field, fTerms) {
				bump(p)
				if isCode {
					codeTerm[p] = true
				}
			}
		}
		for line := range strings.SplitSeq(cleaned, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			lineTerms := RelevanceTerms(line)
			for _, t := range lineTerms {
				titleTerms[t] = true
				if norm := normDocTerm(t); norm != "" {
					titleTerms[norm] = true
				}
			}
			for _, p := range queryCompoundParts(line, lineTerms) {
				titleTerms[p] = true
			}
			break
		}
	}
	parts := queryCompoundParts(cleaned, terms)
	keys := make([]string, 0, 32)
	for _, t := range terms {
		keys = append(keys, t)
		if norm := normDocTerm(t); norm != "" && norm != t {
			keys = append(keys, norm)
		}
	}
	keys = append(keys, parts...)
	out := make(map[string]float64, len(keys))
	for _, key := range keys {
		if _, done := out[key]; done {
			continue
		}
		c := qtf[key]
		if c < 1 {
			c = 1
		} else if c > 8 {
			c = 8
		}
		if isIssueSectionBoilerplate(key) {
			c = 1
		}
		w := 1.0 + 0.15*math.Log2(float64(c))
		if titleTerms[key] && !isIssueSectionBoilerplate(key) {
			w += 0.45
		}
		if codeTerm[key] {
			w += 0.20
		}
		out[key] = w
	}
	return out, titleTerms
}

type compoundRescueOff struct {
	place int32
	off   int64
	idf   float64
}

func (a *relevanceAcc) collectCompoundPartHits(part string) ([][]posting, []int32) {
	orKeys := a.stemKeys(part, []string{"t" + part})
	lists := make([][]posting, 0, 4)
	hits := a.hits[:0]
	for _, key := range orKeys {
		posts, err := a.postings(key)
		if err != nil || len(posts) == 0 {
			continue
		}
		lists = append(lists, posts)
		for _, pp := range posts {
			place := a.placeOf(pp.Sid)
			if place < 0 {
				continue
			}
			a.noteTool(pp, place)
			if a.termTF[place] == 0 {
				hits = append(hits, place)
			}
			a.termTF[place]++
		}
	}
	return lists, hits
}

func (a *relevanceAcc) applyCompoundRescues(rescued, rescueParts []int32, rescueScore []float64, rescueOffs []compoundRescueOff) {
	for _, ro := range rescueOffs {
		if rescueParts[ro.place] < 2 {
			continue
		}
		msg := &a.msgs[a.message(ro.off, ro.place)]
		if !msg.matched {
			msg.matched = true
			a.sess[ro.place].messages++
		}
		msg.terms++
		msg.idf += ro.idf
	}
	for _, p := range rescued {
		s := &a.sess[p]
		if !s.credited {
			s.credited = true
			a.credited = append(a.credited, p)
		}
		s.score += rescueScore[p]
		s.focus += rescueScore[p]
		s.any = 2
		s.matched = 1
	}
}

// addCompoundParts credits informative identifier parts to sessions already
// matching a query term without inflating queryTerms/identifyingTerms/
// termsKnown/s.matched/s.strong, and optionally rescues sessions hitting >=2
// distinct informative parts.
func (a *relevanceAcc) addCompoundParts(parts []string, allowRescue bool) {
	if len(parts) == 0 || a.readErr != nil {
		return
	}
	var rescueParts []int32
	var rescueScore []float64
	var rescued []int32
	var rescueOffs []compoundRescueOff
	if allowRescue {
		rescueParts = make([]int32, len(a.sess))
		rescueScore = make([]float64, len(a.sess))
	}
	for _, part := range parts {
		lists, hits := a.collectCompoundPartHits(part)
		if len(hits) == 0 {
			continue
		}
		union := a.unionPostings(lists)
		df, minSess := a.countDF(union)
		rank := rankIDF(len(a.m.Sessions), minSess)
		idf := gateIDF(a.totalDocs, df, rank)
		if idf <= 0 && minSess > 2 {
			for _, p := range hits {
				a.termTF[p] = 0
			}
			continue
		}
		if idf <= 0 {
			idf = 0.1
		}
		if rank <= 0 {
			rank = 0.1
		}
		if _, exists := a.idfOf[part]; !exists {
			a.idfOf[part] = idf
		}
		if a.sessionIDF != nil {
			if _, exists := a.sessionIDF[part]; !exists {
				a.sessionIDF[part] = rank
			}
		}
		if idf < dejaVuIDFFloor && minSess > 2 {
			for _, p := range hits {
				a.termTF[p] = 0
			}
			continue
		}
		if idf >= InformativeIDF && len(part) >= 4 && !isGenericTouchedToken(part) {
			a.informativeTerms = append(a.informativeTerms, part)
		}
		wPart := compoundPartWeight
		if len(part) >= 6 && rank >= 2.5 {
			wPart = 0.55
		}
		partRank := rank * wPart
		if qMult := a.docWeights[part]; qMult > 1.0 {
			partRank *= qMult
		}
		for _, p := range hits {
			a.inHit[p] = true
		}
		for _, pp := range union {
			place := a.placeOf(pp.Sid)
			if place < 0 || !a.inHit[place] {
				continue
			}
			if a.sess[place].any > 0 {
				if !pp.Tool && !a.msgs[a.message(pp.Off, place)].tool {
					a.spoken[place] = true
				}
			} else if allowRescue {
				if !pp.Tool && !a.msgs[a.message(pp.Off, place)].tool {
					a.spoken[place] = true
				}
				rescueOffs = append(rescueOffs, compoundRescueOff{place: place, off: pp.Off, idf: partRank})
			}
		}
		for _, p := range hits {
			s := &a.sess[p]
			weighted := partRank * (1 + 0.25*math.Log2(float64(a.termTF[p])))
			if !a.spoken[p] {
				weighted *= toolMatchWeight
			}
			if s.any > 0 {
				s.score += weighted
				s.focus += weighted
			} else if allowRescue {
				rescueParts[p]++
				rescueScore[p] += weighted
				if rescueParts[p] == 2 {
					rescued = append(rescued, p)
				}
			}
			a.inHit[p] = false
			a.spoken[p] = false
			a.termTF[p] = 0
		}
		if a.readErr != nil {
			break
		}
	}
	if !allowRescue || len(rescued) == 0 {
		return
	}
	a.applyCompoundRescues(rescued, rescueParts, rescueScore, rescueOffs)
}
