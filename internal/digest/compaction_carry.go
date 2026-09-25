package digest

import (
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// The "keep until closed" list: what a host's compaction summary tends to drop,
// taken from the transcript without a model and carried from one compaction to
// the next until the transcript closes it. Measured on 189 real compactions:
// 55 of 60 sampled lines were right (92%); of the open #N items the summaries
// dropped and GitHub still had open, the list kept 56 of 75; the rendered list
// had a median of 273 tokens, p90 484.
const (
	carryAwaiting = "awaiting"
	carryOpen     = "open"
	carryVerdictK = "verdict"
	carryRecheck  = "recheck"

	carryMaxAwaiting = 3
	carryMaxRecheck  = 3
	carryMaxVerdicts = 4
	carryMaxLines    = 12
	// An open #N or a recheck not touched for two compactions is more often
	// stale than open: of the carried #N lines, 36% were already closed on
	// GitHub, against 23% of fresh ones.
	carryMaxAge = 2
	// The text of one line: 400 runes of Cyrillic fit, so a fresh line and the
	// same line carried from the last compaction still compare equal.
	carryTextBytes  = 1600
	carryRenderRune = 200
	carryHeader     = "Keep until closed (carried across compactions)"
)

var (
	carryCloseCmdRE  = regexp.MustCompile(`gh\s+(?:pr\s+merge|issue\s+close|pr\s+close)(?:\s|$)`)
	carryCmdNumRE    = regexp.MustCompile(`(?:^|[\s#"'(])(\d{2,6})(?:$|[\s;"')])`)
	carryUserNumRE   = regexp.MustCompile(`(?:^|[^\p{N}/.])#?(\d{3,6})(?:$|[^\p{N}/.%])`)
	carryUserCloseRE = regexp.MustCompile(`(?i)` + carryLB + `(смержил\p{L}*|вмержил\p{L}*|смерджил\p{L}*|влил\p{L}*|закрыл\p{L}*|merged|closed)` + carryRB)
	carryWaitMeRE    = regexp.MustCompile(`(?i)(мерж|мердж|merge|апрув|approv|добро|отмашк|твоего|твоё|твое|на тебя|за тобой)`)
	carryCIWaitRE    = regexp.MustCompile(`(?i)(` + carryLB + `(ci|чек\p{L}*|зелён\p{L}*|зелен\p{L}*|сборк\p{L}*|джоб\p{L}*)` + carryRB + `|\b(checks?|build|jobs?)\b)`)
	carryLinksRE     = regexp.MustCompile(`(?:closes|fixes|resolves|закрывает|чинит)\s+#(\d{2,6})`)
	carryLongWordRE  = regexp.MustCompile(`[\p{L}\p{N}_]{6,}`)
	carrySpaces      = strings.NewReplacer(" ", " ", " ", " ")
)

type carryEvent struct {
	kind byte // 'u' user, 'a' assistant, 't' command
	text string
}

type carryItem struct {
	kind string
	ns   []int
	text string
	j    int
	age  int
}

// ExtractCarry builds this compaction's list from the messages since the last
// one and the list the last one left. The segment starts after the newest
// compaction summary within the same 4 MiB scan the packet uses; a transcript
// read without that boundary is judged whole.
func ExtractCarry(messages []model.Message, prev []model.ContextCarry) []model.ContextCarry {
	var ev []carryEvent
	scanned := 0
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		scanned += len(m.Text)
		if scanned > maxContextScanBytes {
			break
		}
		text := strings.TrimSpace(m.Text)
		if m.Role == sources.RoleSummary || (m.Role == "user" && IsCompactionSummary(text)) {
			break
		}
		var kind byte
		switch {
		case m.Role == "assistant":
			kind = 'a'
		case m.Role == sources.RoleCommand:
			kind = 't'
		case m.Role == "user" && !IsAgentArtifact(text):
			kind = 'u'
		default:
			continue
		}
		ev = append(ev, carryEvent{kind: kind, text: carrySpaces.Replace(m.Text)})
	}
	slices.Reverse(ev)
	return carryBuild(ev, prev)
}

func carryBuild(ev []carryEvent, prev []model.ContextCarry) []model.ContextCarry {
	lastU := -1
	closed := map[int][]int{}
	var texts []string
	var userMerged []int
	for j, e := range ev {
		texts = append(texts, strings.ToLower(e.text))
		if e.kind == 't' {
			for _, line := range strings.Split(e.text, "\n") {
				if carryCloseCmdRE.MatchString(line) { // "for n in 12 34; do gh pr merge $n" too
					for _, m := range carryCmdNumRE.FindAllStringSubmatch(line, -1) {
						closed[atoiCarry(m[1])] = append(closed[atoiCarry(m[1])], j)
					}
				}
			}
			continue
		}
		if e.kind == 'u' {
			lastU = j
			if carryUserCloseRE.MatchString(e.text) {
				ns := carryUserNumRE.FindAllStringSubmatch(e.text, -1)
				for _, m := range ns {
					closed[atoiCarry(m[1])] = append(closed[atoiCarry(m[1])], j)
				}
				if len(ns) == 0 {
					userMerged = append(userMerged, j)
				}
			}
		}
		for _, s := range carryUnits(e.text, true) { // status tables often sit in code blocks
			for _, st := range carryBState(carryBNorm(s)) {
				if st.closed {
					closed[st.n] = append(closed[st.n], j)
				}
			}
		}
	}
	// "Fixes #12" on a line whose PR is closed closes #12 with it.
	for _, low := range texts {
		// Matched on the lowered text with a case-sensitive pattern: the
		// case-folding one was half the extractor's time on 189 real segments.
		if !carryHasLinkWord(low) {
			continue
		}
		for _, line := range strings.Split(low, "\n") {
			var linked []int
			for _, m := range carryLinksRE.FindAllStringSubmatch(line, -1) {
				linked = append(linked, atoiCarry(m[1]))
			}
			if len(linked) == 0 {
				continue
			}
			for _, m := range carryNumRE.FindAllStringSubmatch(line, -1) {
				a := atoiCarry(m[1])
				if slices.Contains(linked, a) || len(closed[a]) == 0 {
					continue
				}
				for _, b := range linked {
					closed[b] = append(closed[b], slices.Max(closed[a]))
				}
			}
		}
	}
	closedAfter := func(n, j int) bool {
		return slices.ContainsFunc(closed[n], func(k int) bool { return k > j })
	}
	mergedAfter := func(j int) bool {
		return slices.ContainsFunc(userMerged, func(k int) bool { return k > j })
	}

	items := carryForward(prev, lastU >= 0, closed, texts, len(userMerged) > 0)
	for j, e := range ev {
		if e.kind != 'a' {
			continue
		}
		for _, s := range carryUnits(e.text, false) {
			text := ""
			add := func(kind string, ns []int) {
				if text == "" {
					text = limitContextText(redactContextText(s), carryTextBytes)
				}
				items = append(items, carryItem{kind: kind, ns: ns, text: text, j: j})
			}
			if j > lastU && carryAsk(s) {
				add(carryAwaiting, nil)
			}
			var ns []int
			for _, st := range carryBState(carryBNorm(s)) {
				if !st.closed && !closedAfter(st.n, j) {
					ns = append(ns, st.n)
				}
			}
			if len(ns) > 0 && carryWaitMeRE.MatchString(s) && mergedAfter(j) {
				ns = nil // "PR #7307 ждёт твоего мержа", then the user said "merged"
			}
			if len(ns) > 0 {
				add(carryOpen, ns)
			}
			if carryVerdict(s) {
				add(carryVerdictK, nil)
			}
			if carryDeferred(s) {
				add(carryRecheck, nil)
			}
		}
	}
	return carryAssemble(items)
}

func carryHasLinkWord(low string) bool {
	for _, w := range []string{"closes", "fixes", "resolves", "закрывает", "чинит"} {
		if strings.Contains(low, w) {
			return true
		}
	}
	return false
}

// carryForward keeps what the last compaction listed and this segment did not
// close. Carried lines sort before every fresh one.
func carryForward(prev []model.ContextCarry, hasUser bool, closed map[int][]int, texts []string, userMerged bool) []carryItem {
	var items []carryItem
	for _, p := range prev {
		it := carryItem{kind: p.Kind, text: p.Text, j: -1, age: p.Age + 1}
		switch p.Kind {
		case carryAwaiting:
			if hasUser { // the user has spoken since the question
				continue
			}
		case carryOpen:
			for _, k := range strings.Split(p.Key, ",") {
				if n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(k), "#")); err == nil && len(closed[n]) == 0 {
					it.ns = append(it.ns, n)
				}
			}
			if len(it.ns) == 0 || it.age > carryMaxAge || carryCIWaitRE.MatchString(p.Text) || (userMerged && carryWaitMeRE.MatchString(p.Text)) {
				continue
			}
		case carryRecheck:
			if it.age > carryMaxAge || carryCIWaitRE.MatchString(p.Text) || carryShortDRE.MatchString(carryPlain(p.Text)) || carryMentioned(p.Text, texts) {
				continue
			}
		case carryVerdictK:
		default:
			continue
		}
		items = append(items, it)
	}
	return items
}

// carryMentioned reports a recheck the segment already came back to: its #N
// appears anywhere, or most of its long words appear in one message.
func carryMentioned(text string, texts []string) bool {
	var ns []string
	for _, m := range carryNumRE.FindAllStringSubmatch(text, -1) {
		ns = append(ns, m[1])
	}
	words := map[string]bool{}
	for _, w := range carryLongWordRE.FindAllString(carryPlain(text), -1) {
		words[strings.ToLower(w)] = true
	}
	need := max(3, len(words)*2/3)
	for _, t := range texts {
		if len(ns) > 0 {
			for _, n := range ns {
				if strings.Contains(t, "#"+n) || strings.Contains(t, " "+n) {
					return true
				}
			}
			continue
		}
		if len(words) < 3 {
			continue
		}
		hit := 0
		for w := range words {
			if strings.Contains(t, w) {
				hit++
			}
		}
		if hit >= need {
			return true
		}
	}
	return false
}

// carryAssemble keeps the newest line per #N, one line per text, and orders the
// list awaiting, open, recheck, verdict under the caps.
func carryAssemble(items []carryItem) []model.ContextCarry {
	newestB := map[int]int{}
	for i, it := range items {
		for _, n := range it.ns {
			newestB[n] = i
		}
	}
	keepB := map[int]bool{}
	for _, i := range newestB {
		keepB[i] = true
	}
	type key struct{ kind, text string }
	at := map[key]int{}
	var kept []carryItem
	for i, it := range items {
		if it.kind == carryOpen && !keepB[i] {
			continue
		}
		k := key{it.kind, it.text}
		if pos, ok := at[k]; ok { // a fresh line replaces the same line carried
			kept[pos] = it
			continue
		}
		at[k] = len(kept)
		kept = append(kept, it)
	}
	sort.SliceStable(kept, func(a, b int) bool { return kept[a].j > kept[b].j })
	pick := func(kind string, limit int) []carryItem {
		var out []carryItem
		for _, it := range kept {
			if it.kind == kind && (limit <= 0 || len(out) < limit) {
				out = append(out, it)
			}
		}
		return out
	}
	var order []carryItem
	order = append(order, pick(carryAwaiting, carryMaxAwaiting)...)
	order = append(order, pick(carryOpen, 0)...)
	order = append(order, pick(carryRecheck, carryMaxRecheck)...)
	order = append(order, pick(carryVerdictK, carryMaxVerdicts)...)
	if len(order) > carryMaxLines {
		order = order[:carryMaxLines]
	}
	var out []model.ContextCarry
	for _, it := range order {
		c := model.ContextCarry{Kind: it.kind, Text: it.text, Age: it.age, Key: normalizedContextText(it.text)}
		if it.kind == carryOpen {
			keys := make([]string, len(it.ns))
			for i, n := range it.ns {
				keys[i] = "#" + strconv.Itoa(n)
			}
			c.Key = strings.Join(keys, ",")
		}
		out = append(out, c)
	}
	return out
}

var carryLabels = map[string]string{
	carryAwaiting: "awaiting you", carryOpen: "open", carryVerdictK: "verdict", carryRecheck: "recheck",
}

// carryLine is one rendered line, cut to 200 runes. An open line whose number
// fell past the cut gets it in front, since the number is what it is for.
func carryLine(c model.ContextCarry) string {
	label, ok := carryLabels[c.Kind]
	if !ok || strings.TrimSpace(c.Text) == "" {
		return ""
	}
	text := c.Text
	if utf8.RuneCountInString(text) > carryRenderRune {
		text = string([]rune(text)[:carryRenderRune]) + "…"
	}
	if c.Kind == carryOpen {
		var missing []string
		for _, k := range strings.Split(c.Key, ",") {
			if k != "" && !strings.Contains(text, k) {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			text = strings.Join(missing, " ") + " " + text
		}
	}
	return "- [" + label + "] " + text + "\n"
}

// RenderCarry is the list alone, for a host that folds it into its own
// compaction prompt. It is empty when nothing is carried.
func RenderCarry(items []model.ContextCarry) string {
	var b strings.Builder
	for _, c := range items {
		b.WriteString(carryLine(model.ContextCarry{Kind: c.Kind, Text: redactContextText(c.Text), Key: c.Key}))
	}
	if b.Len() == 0 {
		return ""
	}
	return carryHeader + "\n" + b.String()
}

// carrySharePercent is the most of a rendered packet the carried list may take.
const carrySharePercent = 40

func addCarrySection(b *strings.Builder, omitted *bool, limit int, items []model.ContextCarry) {
	var lines []string
	for _, c := range items {
		if line := carryLine(c); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return
	}
	// Printed first, so without a share of its own a long list would take the
	// objective and the conclusions with it: at p90 it is ~2.4 KB of Cyrillic
	// against a 4 KB recovery packet.
	if share := b.Len() + limit*carrySharePercent/100; share < limit {
		limit = share
	}
	if b.Len()+len("\n"+carryHeader+"\n") > limit {
		*omitted = true
		return
	}
	b.WriteString("\n" + carryHeader + "\n")
	for _, line := range lines {
		if b.Len()+len(line) > limit {
			*omitted = true
			return
		}
		b.WriteString(line)
	}
}
