package digest

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/model"
)

// decisionMarkers spot conclusion-bearing assistant messages in tool-heavy
// sessions, where 95% of the transcript is status chatter around a few
// sentences that actually explain what happened and why.
var decisionMarkers = []string{
	// "decision:" with the colon, which is how an agent labels one when it is
	// writing for a reader rather than talking: the bare word is ordinary
	// ("that decision is yours"), the labelled one is the line itself (#2526).
	"decision:", "решение:",
	"root cause", "because", "the fix", "fixed", "decided", "instead of",
	"turned out", "the problem was", "solution", "so the answer", "conclusion",
	"works now", "passes now", "merged", "released", "chose", "won't work",
	// Half the sessions on a real store are in Russian, and a list of English
	// phrases reads every line of them as a passing mention. These are the
	// same shapes: what was concluded, what turned out, what got fixed.
	"решили", "в итоге", "оказалось", "причина", "выяснилось", "вывод:", "мой вывод",
	"выбрали", "остановились", "исправил", "починил", "заработало",
	"не будем", "убрали", "переделали", "смержил", "выкатили",
	// A mirror of "works now" and "fixed", added when Russian lines were the
	// under-marked half. It went in beside "готово" and "зелёный"; those two
	// are gone and this one stays, because the three were measured apart and
	// only this one marks outcomes. Of the lines it alone marks, most read
	// like "механизм наконец заработал полностью" or "SSH заработал 3/3 с
	// keepalive" — what ended up working, which is the shape the list is for.
	//
	// "зелёный" alone marked 543 lines of 4901, and they were "CI зелёный, жду
	// ревьюера", "PR #1566 зелёный целиком": the status chatter this list
	// exists to see past, not what a session concluded. "готово" alone marked
	// 221, mostly checklist ticks and table cells — and it fires inside
	// "готового" and "полуготовое", where it means nothing at all (#2734).
	"заработал",
	// A decision is as often reported as the state something ended up in as by
	// the act of deciding. Measured over 4000 assistant lines from a real
	// store, the list above marks 4% of them, and lines like "прод-пины теперь
	// ложатся на релиз-ветку" or "бывшая ведущая становится ведомой" — plainly
	// the outcome of a decision — were read as passing mentions. With these the
	// share is 9%, the benchmark does not move, and one off-topic block of 58
	// live questions turns into a pointer.
	"теперь ", "стало ", "становится", "лежит в", "работает через",
	"по умолчанию", "переехал", "now lives", "now goes", "by default",
	"архитектура:", "картина:", "проксирует", "маршрутизируется", "для доступа к",
	// The English half was missing the shapes English actually uses to close
	// something. Counted over 41,084 real messages, the Russian markers fired
	// 149 times on "решили" and 47 on "в итоге" while their English mirrors —
	// "we settled on", "in the end we", "Decision:" at the start of a line —
	// fired once or twice each: the store is Russian-dominant, so the gap read
	// as rare rather than as a hole (#2340). It is a hole for a reader working
	// in English, and these are the same shapes as the entries above them.
	"settled on", "settled at", "settled that", "agreed to", "went with", "ended up", "in the end", "opted for",
	// Chinese decision, root-cause and outcome markers, in Simplified form
	// (Traditional is folded before matching).
	"决定：", "决定:", "结论：", "结论:", "原因：", "原因:", "根因：", "根因:",
	"原因是", "根因是", "最终决定", "决定采用", "改为", "改用", "修复了", "已修复",
	"修好了", "解决了", "已解决", "已合并", "已发布", "默认使用",
}

// CarriesDecision reports whether a line reads as something concluded rather
// than something mentioned in passing. The markers are the same ones the
// session-start digest uses to find what a session decided; per-prompt recall
// had no use for them and picked its line by where the query's words fell.
func CarriesDecision(text string) bool {
	return CarriesDecisionExcept(text, nil)
}

// planAfterMarker are what follows a state word when the sentence is a plan
// rather than an outcome: "теперь давай", "now let's". The state markers were
// added because a decision is as often reported as the state something ended up
// in — but the same words open the next task. Measured by reading ten lines the
// rule counted as conclusions, four were plans: "Go-структуры готовы. Теперь
// SQL — 2 SELECT" and "PR открыт. Теперь главное — сделать так, чтобы деплой не
// мог соврать".
var planAfterMarker = []string{
	"теперь давай", "теперь главное", "теперь нужно", "теперь надо",
	"теперь буду", "теперь сделаю", "теперь я", "now let's", "now i'll",
	"now we need", "now i will",
	"现在让我们", "现在我来", "接下来我", "现在需要",
}

// blankOpeningNow removes "теперь" where it opens a clause, which is how a
// plan starts — "Теперь SQL — 2 SELECT" — and leaves it where it reports a
// state: "прод-пины теперь ложатся на релиз-ветку". Both readings were counted
// as decisions; reading ten such lines on a real store, four were plans.
func blankOpeningNow(low string) string {
	const word = "теперь"
	out := low
	for i := 0; ; {
		j := strings.Index(out[i:], word)
		if j < 0 {
			return out
		}
		at := i + j
		opens := true
		for k := at - 1; k >= 0; k-- {
			if out[k] == ' ' || out[k] == '\t' || out[k] == '*' || out[k] == '#' {
				continue
			}
			opens = !endsWord(out[:k+1])
			break
		}
		if opens {
			out = out[:at] + strings.Repeat(" ", len(word)) + out[at+len(word):]
		}
		i = at + len(word)
	}
}

// endsWord says whether text ends inside a word rather than at punctuation.
//
// This read the last byte and called anything >= 0x80 part of a word, on the
// grounds that Cyrillic is multi-byte. So is Russian punctuation: «», the em
// dash, the ellipsis. A line reading «Теперь SQL — два SELECT» has its state
// word preceded by a guillemet, which the byte test called a letter, so the
// word did not read as opening a clause and a plan was promoted as an outcome —
// the exact case blankOpeningNow exists to catch (#2734).
func endsWord(text string) bool {
	r, size := utf8.DecodeLastRuneInString(text)
	if size == 0 || r == utf8.RuneError {
		return false
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// CarriesDecisionExcept is the same, ignoring markers the asker used. A marker
// that is also a word of the question makes the check circular: "в итоге" is
// both, so a line matched on that phrase then counted as a conclusion because
// of it. Measured on the benchmark, the question "по чему у нас в итоге
// шардирование" promoted a session about something else entirely.
func CarriesDecisionExcept(text string, asked []string) bool {
	low := strings.ToLower(text)
	if cjkfold.HasCJK(low) {
		low = cjkfold.String(low)
	}
	// deja asks an agent to credit a recall it used — "déjà vu: … — reusing
	// it" — and that sentence reports what a past session decided, so every
	// decision marker in the quoted line fires on it. Found at the file line:
	// `doctor_auto.go … prior decision: deja-vu recalled:` — deja quoting
	// itself quoting a session, offered as the decision about a file.
	if quotesDeja(low) {
		return false
	}
	if announcesItself(low) {
		return false
	}
	for _, p := range planAfterMarker {
		// A plan may still report an outcome elsewhere in the line, so this
		// blanks the plan wording rather than disqualifying the whole line.
		low = strings.ReplaceAll(low, p, " ")
	}
	low = blankOpeningNow(low)
	for _, d := range decisionMarkers {
		if !marksLine(low, d) {
			continue
		}
		skip := false
		for _, a := range asked {
			if a != "" && strings.Contains(d, cjkfold.String(strings.ToLower(a))) {
				skip = true
				break
			}
		}
		if !skip {
			return true
		}
	}
	return false
}

// planOpeners are how a line announces what is about to happen rather than
// reporting what came of it. planAfterMarker handles the same idea mid-sentence
// ("готово. теперь давай"); this is its sibling for the opening clause, where
// the marker the decision rule fires on sits inside the thing being planned:
// "Проверяю, что видео теперь играет" fires on "теперь", "I'll look at the git
// history for the Gemini extension to find the fix" on "the fix".
//
// Counted over 985 distinct assistant opening lines from a real store, 54 were
// read as decisions and 7 of those — 13% — were announcements of intent. None of
// the seven also reported an outcome, which is why the opener disqualifies the
// line rather than being blanked out of it.
var planOpeners = []string{
	"i'll ", "i will ", "i'm going to", "i am going to", "let me ", "let's ",
	"going to ", "about to ",
	"ищу", "проверяю", "смотрю", "читаю", "реализую", "добавляю", "пишу ",
	"делаю", "начинаю", "перемеряю", "спавню", "запускаю", "собираю", "сейчас ",
	"пересобираю", "пересоберу", "понял, пересобираю", "понял, сейчас", "замерю", "замеряю",
	"我先", "让我先", "让我看看", "我来看看", "我先看看", "正在检查", "我现在",
}

// AnnouncesPlan reports whether text opens by announcing what the speaker is
// about to do rather than stating an outcome.
func AnnouncesPlan(text string) bool {
	return announcesItself(strings.ToLower(strings.TrimSpace(text)))
}

// userCorrectionMarkers mark user turns that explicitly correct an agent's
// confusion or disambiguate distinct systems/configs.
var userCorrectionMarkers = []string{
	"причем тут", "причему тут", "при чём тут", "при чем тут",
	"не путай", "путаешь", "перепутал",
	"у меня впн для", "для доступа к", "это для ",
	"проксирует", "два туннеля", "два разных", "разные впн", "разных впн",
	"один до ", "второй до ",
	"don't confuse", "do not confuse", "nothing to do with",
}

// CarriesUserCorrection reports whether a user message states a correction or
// architectural disambiguation between entities.
func CarriesUserCorrection(text string) bool {
	low := strings.ToLower(text)
	for _, m := range userCorrectionMarkers {
		if marksLine(low, m) {
			return true
		}
	}
	return false
}

// announcesItself reports whether the line opens by saying what the speaker is
// about to do. Only at the opening: a plan mentioned later in a line that
// reports an outcome is what planAfterMarker is for.
func announcesItself(low string) bool {
	trimmed := strings.TrimLeft(low, "*#->•· \t")
	for _, opener := range planOpeners {
		if strings.HasPrefix(trimmed, opener) {
			return true
		}
	}
	return false
}

// quotesDeja reports whether the line is an agent repeating what deja said.
// The credit line deja asks for is the common case; the block headers are what
// a pasted injection looks like.
func quotesDeja(low string) bool {
	for _, p := range []string{
		"déjà vu:", "deja vu:", "deja-vu recalled", "deja recalled",
		"recalled from this machine", "recalled history from prior sessions",
		"<deja-recall", "deja found sessions",
	} {
		if strings.Contains(low, p) {
			return true
		}
	}
	return false
}

// withoutDejaLines drops the lines of a message that quote deja — the credit
// line, a recall echoed back — and keeps the rest, which is the agent's own
// work. An answer that is all echo comes back empty.
func withoutDejaLines(text string) string {
	if !quotesDeja(strings.ToLower(text)) {
		return text
	}
	var kept []string
	for line := range strings.SplitSeq(text, "\n") {
		if !quotesDeja(strings.ToLower(line)) {
			kept = append(kept, line)
		}
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// acknowledgements are replies that close a turn without saying anything a
// later session could use.
var acknowledgements = map[string]bool{
	"ok": true, "okay": true, "done": true, "ready": true, "yes": true, "no": true,
	"none": true, "sure": true, "got it": true, "ping": true, "pong": true,
	"no response requested": true, "review complete": true,
	"ок": true, "да": true, "нет": true, "готово": true, "понял": true, "принято": true,
	"好": true, "好的": true, "行": true, "可以": true, "收到": true, "明白": true,
	"搞定": true, "完成": true, "没问题": true, "是的": true, "对": true, "嗯": true,
}

func acknowledgement(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	if len(t) > 40 {
		return false
	}
	t = strings.TrimSpace(strings.TrimFunc(t, func(r rune) bool {
		return strings.ContainsRune(".!,:;*`'\"。！，：；", r)
	}))
	if cjkfold.HasCJK(t) {
		t = cjkfold.String(t)
	}
	return acknowledgements[t]
}

// marksLine reports whether a marker fires on a line, which it does only where
// it begins a word.
//
// Markers were plain substrings, so they fired inside longer words that mean
// something else or the opposite: "released" inside "[Unreleased]", "decision:"
// inside "reviewDecision:", "solution" inside "pollution", "fixed" inside
// "unfixed". Measured on 85k assistant lines from a real store, 82 promoted
// lines were promoted on nothing else (#2734).
//
// The end of the word is deliberately left open. Half the markers are Russian
// verbs whose inflections are how they are actually written — "решили",
// "исправили", "заработала" — and pinning the tail would drop the forms the
// list was extended for.
func marksLine(low, marker string) bool {
	if marker == "" {
		return false
	}
	// The first rune, not the first byte: every Cyrillic marker starts with a
	// multi-byte one, and reading a single byte of it decoded as an error and
	// took the boundary rule out of play for half the list.
	if first, _ := utf8.DecodeRuneInString(marker); (!unicode.IsLetter(first) && !unicode.IsDigit(first)) || cjkfold.Unspaced(first) {
		return strings.Contains(low, marker)
	}
	for i := 0; ; {
		j := strings.Index(low[i:], marker)
		if j < 0 {
			return false
		}
		at := i + j
		if at == 0 || !endsWord(low[:at]) {
			return true
		}
		i = at + 1
	}
}

// selectConclusions keeps assistant messages that carry a decision marker,
// plus the final message (the outcome), in transcript order. Conversational
// sessions where nothing matches keep everything — the filter only kicks in
// when it has something better to offer.
func selectConclusions(ms []model.Message) []model.Message {
	if len(ms) <= 2 {
		return ms
	}
	var keep []model.Message
	for i, m := range ms {
		low := strings.ToLower(m.Text)
		if cjkfold.HasCJK(low) {
			low = cjkfold.String(low)
		}
		marked := false
		for _, d := range decisionMarkers {
			if marksLine(low, d) {
				marked = true
				break
			}
		}
		if marked || strings.Contains(m.Text, "```") || i == len(ms)-1 {
			keep = append(keep, m)
		}
	}
	if len(keep) < 2 {
		return ms
	}
	return keep
}

// dedupeStatus drops messages that repeat an earlier message's opening —
// agent loops emit the same status line dozens of times and each survives
// the noise filters individually.
func dedupeStatus(ms []model.Message) []model.Message {
	seen := map[string]bool{}
	var out []model.Message
	for _, m := range ms {
		key := strings.ToLower(strings.Join(strings.Fields(m.Text), " "))
		if r := []rune(key); len(r) > 60 {
			key = string(r[:60])
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, m)
	}
	return out
}

// Conclusions is the short "what this session concluded" line-set recall shows
// under its best hit. A recall answer used to be excerpts alone — the passages
// where the query words appeared — so an agent had to open the session to learn
// what came of it. These are the assistant's decision-carrying sentences, the
// same ones `share` puts under "What the assistant concluded", trimmed to one or
// two lines each so the whole block costs a few hundred bytes.
func Conclusions(s model.Session, budget int, limit int) []string {
	return conclusions(s, budget, limit, false)
}

// SubstantialConclusions is Conclusions for a reader who has nothing else from
// the session: the session-start digest, a recall page, a recap. A line that
// says nothing on its own — "Fixed the same way.", "работает", a greeting, an
// announcement of the next step — is passed over for one that does.
//
// The surfaces that pair the line with something the reader already holds keep
// Conclusions: under the command an agent is about to run, "tests pass" is the
// answer, not filler.
func SubstantialConclusions(s model.Session, budget int, limit int) []string {
	return conclusions(s, budget, limit, true)
}

func conclusions(s model.Session, budget int, limit int, substantial bool) []string {
	if budget <= 0 || limit <= 0 {
		return nil
	}
	var assistants []model.Message
	for _, m := range s.Messages {
		if m.Role != "assistant" || noisyMessage(m.Text) || IsAgentArtifact(m.Text) {
			continue
		}
		// The last message is a candidate whatever it says, so these had to go
		// before selection rather than in it: on a 2,285-session store 132
		// served conclusions were deja's own recall echoed back and 104 were
		// "ok", "done" and the like.
		m.Text = withoutDejaLines(m.Text)
		if m.Text == "" || acknowledgement(m.Text) {
			continue
		}
		assistants = append(assistants, m)
	}
	if len(assistants) == 0 {
		return nil
	}
	picked := concludingFirst(dedupeStatus(selectConclusions(assistants)))
	// Newest first: the last thing concluded outranks the first thing tried.
	var out []string
	spent := 0
	for i := len(picked) - 1; i >= 0 && len(out) < limit; i-- {
		line := decisionLead(MessageText(picked[i].Text), substantial)
		if line == "" {
			continue
		}
		if spent+len(line) > budget {
			// Whole sentences or nothing. Cutting here left a conclusion that
			// reads as a finished thought while the part that fell off was the
			// end of it — "we decided to cap retries at three after weighing the
			// options" arrived without the "and then reverted that" it ended on,
			// which is the opposite of what the session concluded (#1336).
			if substantial {
				line = firstSubstantialSentence(line)
			} else {
				line = firstSentences(line, 1)
			}
			if spent+len(line) > budget {
				// Unless one line is the whole answer. Measured on this
				// machine's index at the tool hook's budget: of 120 sessions,
				// 29 yielded no conclusion and 16 of those had one — a sentence
				// a few bytes too long, answered with silence (#2518). A caller
				// asking for one line is the shape where nothing follows the
				// cut, so it can be marked the way every other surface marks
				// one; a caller asking for several keeps the old rule, since
				// there text would follow the marker.
				// The condition is that nothing follows the marker, and an
				// empty out is that whatever limit was asked for: this breaks
				// immediately after, so the cut is the last line either way.
				// Keyed on limit alone, a session whose only conclusion is one
				// long sentence answered a caller asking for three lines with
				// silence and a caller asking for one with the sentence —
				// 9 of 452 sessions on a real store.
				if len(out) == 0 {
					if cut := markedCut(line, budget); cut != "" {
						out = append(out, cut)
					}
				}
				break
			}
			if line == "" {
				continue
			}
		}
		out = append(out, line)
		spent += len(line)
		if spent >= budget {
			break
		}
	}
	return out
}

// concludingFirst puts the messages that settle something last, so the
// newest-first walk reaches them before the budget is gone.
//
// selectConclusions keeps a message for carrying a decision marker, for
// containing a code fence, or for being the last one — three reasons, and only
// the first is about concluding anything. A message kept for its code fence
// that happens to be newer than the real conclusion took the slot: on a real
// store that was 28 of the 62 sessions whose block still missed what they
// settled (#2243).
//
// Order within each group is untouched, so "the last thing concluded outranks
// the first thing tried" still holds — among the messages that concluded
// anything, and then among the rest.
func concludingFirst(ms []model.Message) []model.Message {
	var plain, concluding []model.Message
	for _, m := range ms {
		if CarriesDecision(MessageText(m.Text)) {
			concluding = append(concluding, m)
			continue
		}
		plain = append(plain, m)
	}
	// No guard for the all-one-group cases: appending an empty half leaves the
	// order exactly as it was, and a guard that cannot change an answer is a
	// line no test can hold.
	return append(plain, concluding...)
}

// decisionLead is the part of a picked message the block quotes: its opening
// two sentences, unless the words that make it a conclusion are further in.
//
// A reply is diagnosis-first, so the opening is the right default and #1336's
// whole-sentences rule is built on it. But the head is not always where the
// conclusion lives, and when it is not, the block quoted the diagnosis and
// dropped the outcome — measured on a real store, 211 of 490 sessions that
// settled something handed over a block that no longer read as one (#2243).
//
// One sentence, not the head plus it: the budget is the reason the conclusion
// fell off in the first place.
func decisionLead(text string, substantial bool) string {
	head := firstSentences(text, 2)
	if substantial {
		head = substantialLead(text, head)
	}
	if head == "" || CarriesDecision(head) {
		return head
	}
	for _, sent := range sentencesOf(text) {
		// "Fixed the same way." carries a marker and nothing else.
		if CarriesDecision(sent) && (!substantial || !thinSentence(sent)) {
			return sent
		}
	}
	return head
}

// sentencesOf splits on the same stops firstSentences counts, so the two agree
// on what a sentence is — including the space-after rule that keeps "v1.2"
// whole and the CJK stops that have no space after them.
func sentencesOf(s string) []string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if s == "" {
		return nil
	}
	var out []string
	start := 0
	for i, r := range s {
		switch {
		case r == '.' || r == '!' || r == '?':
			if i+1 < len(s) && s[i+1] != ' ' {
				continue
			}
		case isCJKSentenceEnd(r):
		default:
			continue
		}
		end := i + utf8.RuneLen(r)
		if r == '.' && trailingListMarker(s[start:end]) {
			continue
		}
		sent := strings.TrimSpace(s[start:end])
		// A list item's number is not a sentence of its own, and the item
		// after it reads the same without it.
		if sent != "" && !listMarker(sent) {
			out = append(out, sent)
		}
		start = end
	}
	if sent := strings.TrimSpace(s[start:]); sent != "" {
		out = append(out, sent)
	}
	return out
}

// isCJKSentenceEnd is the full stop, exclamation and question mark Chinese and
// Japanese write. They are separate characters from the ASCII ones, so a
// message in either script contained no sentence end as far as this file was
// concerned (#1319).
//
// The fullwidth full stop U+FF0E is deliberately absent: it is also how a
// decimal point is written in fullwidth digits, and the space rule that keeps
// "v1.2" together cannot help here — these scripts put no space after a stop,
// so the rule has to be dropped for them. The enumeration comma and the
// fullwidth semicolon are absent for the plainer reason that neither ends a
// sentence.
func isCJKSentenceEnd(r rune) bool {
	switch r {
	case '\u3002', '\uff01', '\uff1f':
		return true
	}
	return false
}

// markedCut is a conclusion held to a budget it does not fit, ending in the
// marker that says so. Rune-safe, and it gives back nothing when the budget
// leaves no room for a readable line rather than a bare marker.
func markedCut(line string, budget int) string {
	const mark = "…"
	if budget <= len(mark)+8 {
		return ""
	}
	cut := strings.TrimRight(UTF8SafeCut(line, budget-len(mark)), " \t")
	if cut == "" {
		return ""
	}
	return cut + mark
}

// firstSentences keeps the opening n sentences of a message: a conclusion
// states itself up front and then explains, and recall pays for every byte.
func firstSentences(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if s == "" {
		return ""
	}
	count, start := 0, 0
	for i, r := range s {
		switch {
		case r == '.' || r == '!' || r == '?':
			// "v1.2" and "e.g." are not sentence ends: require a space after.
			if i+1 < len(s) && s[i+1] != ' ' {
				continue
			}
		case isCJKSentenceEnd(r):
			// No space rule here: these scripts put none after the stop, so
			// requiring one would find no sentence at all.
		default:
			continue
		}
		end := i + utf8.RuneLen(r)
		if listMarker(s[start:end]) || (r == '.' && trailingListMarker(s[start:end])) {
			continue
		}
		start = end
		count++
		if count == n {
			return strings.TrimSpace(s[:i+utf8.RuneLen(r)])
		}
	}
	const most = 240
	if len(s) > most {
		return strings.TrimSpace(UTF8SafeCut(s, most)) + "…"
	}
	return s
}
