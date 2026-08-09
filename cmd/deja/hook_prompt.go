package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/redact"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// promptHookBudget keeps per-prompt injections small: this fires on every
// user message, so it must be a hint, not a payload.
const promptHookBudget = 1024

// dejaVuMaxMessages caps how large a session can be and still read as one
// rememberable episode. Marathon catch-all sessions rank into everything.
const dejaVuMaxMessages = 300

// dejaVuMinAge withholds work the user plausibly still remembers. Named so the
// benchmark can apply the same rule; a benchmark that skips a gate reports a
// recall nobody would see.
const dejaVuMinAge = 15 * time.Minute

type promptHookInput struct {
	Prompt    hookPromptText `json:"prompt"`
	SessionID string         `json:"session_id"`
	// CWD is what the harness says the project is. Reading only the
	// environment meant a host that sends the payload without exporting
	// CLAUDE_PROJECT_DIR recalled nothing (#759).
	CWD string `json:"cwd"`
}

// hookPromptText reads a prompt that arrives either as a string (Claude Code,
// Cursor) or as content parts (Kimi sends [{"type":"text","text":"…"}]). A
// harness whose shape we do not read looks exactly like one where recall never
// matches, so both are accepted rather than assumed.
type hookPromptText string

func (h *hookPromptText) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*h = hookPromptText(s)
		return nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(b, &parts); err != nil {
		return nil // an unknown shape means no prompt, not a broken hook
	}
	var sb strings.Builder
	for _, p := range parts {
		if p.Text == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(p.Text)
	}
	*h = hookPromptText(sb.String())
	return nil
}

// runHookPrompt is the UserPromptSubmit hook: search the user's own prompt
// against the index (relevance, not recency) and inject a compact hint only
// when something genuinely matches. Empty output means stay silent — a hook
// that talks every turn is wallpaper. It never builds or refreshes the index:
// this path runs on every prompt and must stay ~milliseconds.
func runHookPrompt(dir string, stdin io.Reader, stdout io.Writer) error {
	return runHookPromptMode(dir, stdin, stdout, false)
}

// plain=true prints the bare digest for hosts that inject a hook's stdout
// verbatim — Kimi does that and reads no JSON field for context.
func runHookPromptMode(dir string, stdin io.Reader, stdout io.Writer, plain bool) error {
	var input promptHookInput
	// Bounded, like the session-start hooks: a host that opens stdin and holds
	// it without finishing the payload used to stall this hook until the
	// harness killed it — on every user message (#846). Still decoded rather
	// than unmarshalled, so a host that writes anything after the object (a
	// second line, a trailing NUL) keeps its recall instead of losing the
	// whole payload to a syntax error.
	_ = json.NewDecoder(bytes.NewReader(readHookPayload(stdin, hookStdinWait))).Decode(&input)
	adoptHookCWD(input.CWD)
	terms := promptSearchTerms(string(input.Prompt))
	if !promptTermsWorthAsking(terms) {
		return nil
	}
	// Version, not just presence: terms hash into buckets an index from
	// another format never wrote, so a stale store answers every prompt with
	// nothing and looks exactly like a user with no history (#777).
	if !index.HasManifest(dir) || !index.IsCurrentVersion(dir) || index.Damaged(dir) {
		requestWarmup(dir)
		return nil
	}
	cwd := os.Getenv("CLAUDE_PROJECT_DIR")
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	// Rank THIS project's sessions by how well they match the prompt terms
	// (IDF-weighted), rather than reconstructing an AND query — natural
	// prompts are full of filler that poisons an AND. n=8 to leave room after
	// excluding the current/too-fresh sessions.
	ranked, matched, strong, err := index.ProjectRelevant(dir, digest.ProjectNameCandidates(cwd), terms, 8)
	if err != nil || len(ranked) == 0 {
		return nil
	}
	ss := make([]model.Session, 0, 2)
	confident := false
	seen := alreadyInjected(dir, input.SessionID)
	pol := policy.Load()
	for i, s := range ranked {
		// Every other injection path asks the policy first; this one is a
		// per-prompt injection like any other, and imported projects reach
		// it (a local project name is a substring of "imported:<name>").
		if !pol.Allows(policy.ActivationAuto, s.Project) {
			continue
		}
		// matched counts informative terms; strong counts the rare ones — a
		// term that identifies something on its own rather than merely beating
		// the corpus average. Two informative terms earn the announcement. A
		// single term is a weak claim, so it only injects when that term is
		// strong: "pgbouncer" answers a question, "problem" does not, and this
		// hook pays its cost on every message the user sends. Measured on
		// cross-paired prompts whose answer is absent, the old bar injected on
		// 94% of them; half of those rested on one ordinary word.
		if matched[i] < 1 || (matched[i] < 2 && strong[i] < 1) {
			continue
		}
		if matched[i] >= 2 {
			confident = true
		}
		// A marathon session that touched everything matches everything, so
		// it used to be skipped whole. But the sessions people ask about are
		// exactly the long ones — measured here, only 2% of sessions cross
		// the line and they are the current work. The haystack argument is
		// about the session as a whole and not about the part that matched,
		// so narrow it to that part instead of dropping the answer.
		if len(s.Messages) > dejaVuMaxMessages {
			s = focusSession(s, terms)
			if len(s.Messages) == 0 {
				continue
			}
		}
		// The session being written right now is never worth recalling to
		// itself. Work merely fresh is a different case: "what did we just
		// change" is a question about the last ten minutes, so age alone no
		// longer withholds an answer — it only withholds the unprompted
		// déjà vu line below.
		if s.ID == input.SessionID {
			continue
		}
		if seen[s.ID] {
			continue
		}
		ss = append(ss, s)
		if len(ss) == 2 {
			break
		}
	}
	if len(ss) == 0 {
		return nil
	}
	// A rejected session is not an equal answer, and the mark has to travel
	// with it into the block the agent reads (#761).
	ss, rejectedWarning := orderForInjection(ss)
	rememberInjected(dir, input.SessionID, ss)
	// "You have been here" on the strength of one word teaches the user to
	// ignore the line. The recall itself still goes in.
	showLine := confident && dejaVuLineDue(dir)
	// The payload is sized to the claim. Two informative terms is a real match
	// and earns the digest; a single rare term is a hint, and a hint that costs
	// a full digest on every message is most of what deja spends. The pointer
	// keeps it discoverable — the agent learns there is history here and can
	// ask for it — for 134 bytes against the 0.5-1.3 KB a real digest measures.
	var body string
	if confident {
		body = search.AutoRecallDigest(ss, promptHookBudget-recallFrameOverhead)
		if strings.TrimSpace(body) == "" {
			return nil
		}
		body = promptHookLead + rejectedWarning + body + citationLine(ss[0])
	} else {
		body = weakRecallPointer(ss, terms) + rejectedWarning
	}
	out := frameRecall(body)
	usage.RecordDigestTerms(dir, usage.KindDejaVu, out, len(ss), rawSize(ss), terms, sessionIDs(ss)...)
	if plain {
		fmt.Fprintln(stdout, out)
		return nil
	}
	var resp sessionStartHookResponse
	resp.HookSpecificOutput.HookEventName = "UserPromptSubmit"
	resp.HookSpecificOutput.AdditionalContext = out
	if showLine {
		resp.SystemMessage = dejaVuLine(ss[0], terms...)
	}
	if resp.SystemMessage == "" {
		// No presentable topic — inject the context silently rather than
		// flashing harness plumbing at the user.
		b, err := json.Marshal(resp)
		if err != nil {
			return nil
		}
		fmt.Fprintln(stdout, string(b))
		return nil
	}
	b, err := json.Marshal(resp)
	if err != nil {
		return nil
	}
	fmt.Fprintln(stdout, string(b))
	return nil
}

// promptSearchTerms extracts the informative tokens from a natural-language
// prompt: stop words and short fragments dropped, capped so the query stays
// specific.
func promptSearchTerms(prompt string) []string {
	fields := strings.FieldsFunc(strings.ToLower(prompt), func(r rune) bool {
		wordy := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.' || r == '/' || r >= 0x400
		return !wordy
	})
	var out []string
	seen := map[string]bool{}
	add := func(f string) bool {
		if seen[f] {
			return false
		}
		seen[f] = true
		out = append(out, f)
		return len(out) == 6
	}
	for _, f := range fields {
		if len(f) < 3 || search.IsStopWord(f) || seen[f] || !techTerm(f) {
			continue
		}
		if add(f) {
			return out
		}
	}
	// CJK carries no spaces, so FieldsFunc hands back a whole phrase as one
	// field, and techTerm rejects every rune above 127 — a Chinese, Japanese
	// or Korean prompt therefore yields no terms at all and auto-recall can
	// never fire for it. Fall back to the terms the relevance tier already
	// ranks on: per-run bigrams with pure-grammar pairs dropped. A bigram is
	// as specific as the identifiers techTerm looks for, and the caller still
	// demands two of them overlap before claiming a déjà vu.
	if hasCJKRune(prompt) {
		for _, t := range index.RelevanceTerms(prompt) {
			if !hasCJKRune(t) {
				continue
			}
			if add(t) {
				break
			}
		}
	}
	// Cyrillic hits the same wall for the same reason: techTerm rejects every
	// rune above 127, so a Russian prompt without an ASCII identifier yields
	// nothing and auto-recall stays silent. Unlike CJK it is space-separated,
	// so the words are already there — they just need a length floor and the
	// closed class removed, and the index matches their inflected forms.
	for _, f := range fields {
		if seen[f] || !cyrPromptTerm(f) {
			continue
		}
		if add(f) {
			break
		}
	}
	return out
}

// cyrPromptTerm reports whether a field is a Cyrillic word specific enough to
// search on. Short words carry no signal and the closed class is noise, so
// both are dropped; what is left is roughly what techTerm keeps for ASCII.
func cyrPromptTerm(f string) bool {
	n := 0
	for _, r := range f {
		if r < 0x400 || r > 0x4ff {
			return false
		}
		n++
	}
	return n >= 5 && !cyrPromptStop[f]
}

// The closed class plus the handful of verbs that open half of all questions.
var cyrPromptStop = map[string]bool{
	"который": true, "которая": true, "которые": true, "потому": true,
	"чтобы": true, "нужно": true, "надо": true, "можно": true, "нельзя": true,
	"когда": true, "почему": true, "зачем": true, "какой": true, "какая": true,
	"какие": true, "этот": true, "эта": true, "это": true, "тот": true,
	"такой": true, "также": true, "тоже": true, "очень": true, "просто": true,
	"сейчас": true, "сделать": true, "делать": true, "сделай": true,
	"работает": true, "работать": true, "показать": true, "посмотреть": true,
	"давай": true, "было": true, "были": true, "будет": true, "быть": true,
	"есть": true, "нет": true, "если": true, "или": true, "как": true,
	"что": true, "где": true, "там": true, "тут": true, "уже": true, "ещё": true,
}

func hasCJKRune(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
			unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) {
			return true
		}
	}
	return false
}

// promptTermsWorthAsking is the gate before the index is touched. Two terms
// were required, which silenced the sharpest prompt there is: "do we need
// pgbouncer here" reduces to one term and never reached the store, while "add
// pgbouncer to the stack" — the same question with a filler noun — did. The
// rule below already decides the single-term case (one informative match is
// served when the question named a term of art, without the déjà vu line);
// the floor returned first, so that rule could never see it.
func promptTermsWorthAsking(terms []string) bool {
	if len(terms) == 0 {
		return false
	}
	return len(terms) >= 2 || hasIdentifierTerm(terms)
}

// hasIdentifierTerm reports whether the question contains a word specific
// enough to carry a match on its own. In a small corpus even "file" clears the
// informativeness bar, so a single hit is only trusted when the question named
// something that reads like a term of art — long, or shaped like a symbol.
func hasIdentifierTerm(terms []string) bool {
	for _, t := range terms {
		if len(t) >= 6 {
			return true
		}
		for _, r := range t {
			if r == '_' || r == '.' || r == '/' || r == '-' || (r >= '0' && r <= '9') {
				return true
			}
		}
	}
	return false
}

// promptFiller is the short English filler a four-character floor lets
// through. search.IsStopWord is deliberately not extended: it governs search
// queries too, where "about" typed on purpose should still match.
var promptFiller = map[string]bool{
	"about": true, "again": true, "after": true, "before": true, "still": true,
	"there": true, "their": true, "these": true, "those": true, "which": true,
	"would": true, "could": true, "should": true, "thing": true, "things": true,
	"really": true, "maybe": true, "into": true, "from": true, "with": true,
	"that": true, "this": true, "what": true, "when": true, "were": true,
	"have": true, "does": true, "just": true, "like": true, "make": true,
	"made": true, "want": true, "need": true, "know": true, "tell": true,
	"show": true, "look": true, "some": true, "more": true, "most": true,
	"then": true, "than": true, "here": true, "your": true, "ours": true,
	"going": true, "doing": true, "being": true, "used": true, "using": true,
	"give": true, "take": true, "come": true, "seen": true, "said": true,
}

// techTerm keeps tokens that can actually identify past work: identifiers,
// error codes, paths, or long plain-ASCII words. Ordinary prose — any
// language — matches by theme, not by task, and theme matches are what made
// déjà vu fire on every prompt.
func techTerm(f string) bool {
	if promptFiller[f] {
		return false
	}
	long := 0
	for _, r := range f {
		if r == '_' || r == '.' || r == '/' || r == '-' || (r >= '0' && r <= '9') {
			return true
		}
		if r > 127 {
			return false
		}
		long++
	}
	// Seven was a proxy for "looks like an identifier" and the wrong one:
	// etag, ttl, mutex, gzip, oauth and most of what people actually type is
	// shorter. Measured on `deja bench prompt`, dropping to four takes real
	// questions answered from 2/12 to 7/12 with precision unchanged at 1.00
	// and no false fire on any negative control; three scores higher still
	// but starts keeping words like "log" and "run", so four is where the
	// evidence stops being comfortable.
	return long >= 4
}

// citationLine pre-writes the narration so the agent copies structure instead
// of having to follow an instruction — models do the former far more reliably.
func citationLine(s model.Session) string {
	title := ""
	for _, m := range s.Messages {
		if m.Role == "user" && !digest.IsAgentArtifact(m.Text) {
			tt := strings.TrimSpace(digest.MessageText(m.Text))
			if tt == "" || strings.HasPrefix(tt, "Exit code") {
				continue
			}
			r := []rune(tt)[0]
			alpha := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r >= 0x400
			if !alpha {
				continue
			}
			title = tt
			break
		}
	}
	if title == "" {
		title = s.Title
	}
	// The digest body is display-safe (contextText strips it), but this title
	// is pulled straight from a message or the stored title and appended to the
	// same agent-facing block. An escape sequence or an invisible tag-block
	// character in a hostile session's title would otherwise ride into the
	// context unaltered — the injection the frame warns about, one layer down.
	// Collapse whitespace too so a newline cannot split the one-line citation.
	title = strings.Join(strings.Fields(redact.SafeForDisplay(title)), " ")
	if len([]rune(title)) > 60 {
		title = string([]rune(title)[:60]) + "…"
	}
	date := ""
	if !s.Updated.IsZero() {
		// With the year, when it is not this one. Every other date deja prints
		// carries it; the sentence the agent is told to say aloud did not, so
		// a decision from July 2025 was narrated to the user as "Jul 3" —
		// reading as five weeks ago on the one recall where the age is the
		// thing worth knowing (#R13).
		layout := "Jan 2"
		if s.Updated.Local().Year() != time.Now().Year() {
			layout = "Jan 2 2006"
		}
		date = ", " + s.Updated.Local().Format(layout)
	}
	// The citation carries the session id because it is the only place a recall
	// that actually helped becomes an observable fact: the agent's reply is
	// indexed like any other message, so `deja:<id>` in it links the credit back
	// to the session that earned it. Matching on title text instead was lexical
	// and unreliable — two sessions opening with the same question are common.
	return fmt.Sprintf("\nIf it helped, say: \"deja-vu recalled: %s (%s%s, deja:%s) — reusing it.\"",
		title, s.Harness, date, shortID(s.ID))
}

// alreadyInjected returns the session ids this hook already injected into the
// given agent session, so follow-up prompts do not repeat the same memory.
func alreadyInjected(dir, sid string) map[string]bool {
	out := map[string]bool{}
	if sid == "" {
		return out
	}
	b, err := os.ReadFile(dir + ".hookseen")
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[0] == sid {
			out[parts[1]] = true
		}
	}
	return out
}

func rememberInjected(dir, sid string, ss []model.Session) {
	if sid == "" {
		return
	}
	p := dir + ".hookseen"
	// Past 1MB the dedup file used to stop writing entirely, which permanently
	// broke dedup — the per-prompt hook then re-injected the same sessions turn
	// after turn. Rotate instead: keep this session's own lines (so its dedup
	// survives) plus the recent tail, then append. Rare (once per 1MB) and
	// atomic, so concurrent hooks lose a few advisory entries at worst.
	if fi, err := os.Stat(p); err == nil && fi.Size() > 1<<20 {
		rotateHookseen(p, sid)
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	for _, s := range ss {
		fmt.Fprintf(f, "%s %s\n", sid, s.ID)
	}
}

// rotateHookseen shrinks the dedup file to this session's lines plus the most
// recent tail, written atomically so a concurrent hook never sees a torn file.
func rotateHookseen(p, sid string) {
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	lines := strings.Split(string(b), "\n")
	// The tail budget is well under the 1MB cap so rotation is infrequent.
	const tailLines = 4000
	start := 0
	if len(lines) > tailLines {
		start = len(lines) - tailLines
	}
	var keep []string
	prefix := sid + " "
	for i, ln := range lines {
		if ln == "" {
			continue
		}
		if i >= start || strings.HasPrefix(ln, prefix) {
			keep = append(keep, ln)
		}
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(keep, "\n")+"\n"), 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, p)
}

// dejaVuLineDue rate-limits the visible line: a déjà vu that fires every
// prompt is wallpaper, and wallpaper trains the user to ignore the real
// moments. Context still flows to the agent regardless.
func dejaVuLineDue(dir string) bool {
	p := dir + ".dejavu"
	if b, err := os.ReadFile(p); err == nil {
		if ts, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil && time.Since(time.Unix(ts, 0)) < 20*time.Minute {
			return false
		}
	}
	_ = os.WriteFile(p, []byte(strconv.FormatInt(time.Now().Unix(), 10)), 0o600)
	return true
}

// dejaVuLine is the one visible line a déjà vu moment earns: which past
// session answered, and how old it is.
func dejaVuLine(s model.Session, terms ...string) string {
	topic := dejaVuTopic(s)
	if topic == "" {
		return ""
	}
	r := []rune(topic)
	if len(r) > 48 {
		topic = strings.TrimSpace(string(r[:48])) + "…"
	}
	// Name the terms that triggered the moment: "you have been here" with no
	// visible reason reads as noise the first time it misfires.
	why := ""
	if len(terms) > 0 {
		if len(terms) > 3 {
			terms = terms[:3]
		}
		why = " · via: " + strings.Join(terms, ", ")
	}
	// "you have been here" for a session that arrived from another machine
	// claims the reader did work they have never seen — the context block
	// below labels it `imported:` and this line contradicted it (#1001).
	who := "you have been here"
	if strings.HasPrefix(s.Project, "imported:") {
		who = "this was done on another machine"
	}
	return fmt.Sprintf("deja-vu: %s — %q (%s%s)", who, topic, search.RelativeDate(s.Updated), why)
}

// dejaVuTopic picks something a human actually typed. Session titles are the
// first user message, which for some harnesses is injected plumbing
// ("# AGENTS.md instructions <INSTRUCTIONS>...") — showing that as "you have
// been here" reads as a glitch, not a memory.
func dejaVuTopic(s model.Session) string {
	if t := strings.TrimSpace(s.Title); t != "" && presentableTopic(t) {
		return t
	}
	for _, m := range s.Messages {
		if m.Role != "user" || digest.IsAgentArtifact(m.Text) {
			continue
		}
		t := strings.TrimSpace(digest.MessageText(m.Text))
		if t != "" && presentableTopic(t) {
			return t
		}
	}
	return ""
}

func presentableTopic(t string) bool {
	if digest.IsAgentArtifact(t) {
		return false
	}
	r := []rune(t)[0]
	if r == '#' || r == '<' || r == '{' || r == '[' {
		return false
	}
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r >= 0x400
}

// focusSession narrows a long session to the neighbourhood of the messages
// that matched, so a haystack contributes its relevant part rather than being
// dropped or flooding the digest. Two messages either side keeps the exchange
// readable: a question without its answer recalls nothing useful.
func focusSession(s model.Session, terms []string) model.Session {
	const window = 2
	keep := map[int]bool{}
	for i, m := range s.Messages {
		text := strings.ToLower(m.Text)
		hits := 0
		for _, t := range terms {
			if strings.Contains(text, strings.ToLower(t)) {
				hits++
			}
		}
		if hits == 0 {
			continue
		}
		for j := i - window; j <= i+window; j++ {
			if j >= 0 && j < len(s.Messages) {
				keep[j] = true
			}
		}
	}
	if len(keep) == 0 {
		s.Messages = nil
		return s
	}
	focused := make([]model.Message, 0, len(keep))
	for i, m := range s.Messages {
		if keep[i] {
			focused = append(focused, m)
		}
	}
	// A term common enough to appear throughout keeps most of the session,
	// which is the haystack again. Rather than give up, keep the densest
	// windows — the places where the most distinct terms land together are
	// where the answer is, and the rest is the session's background noise.
	if len(focused) > dejaVuMaxMessages {
		s.Messages = densestMessages(s.Messages, terms, dejaVuMaxMessages)
		return s
	}
	s.Messages = focused
	return s
}

// densestMessages keeps the cap best messages by how many distinct terms each
// one carries, in original order so the exchange still reads as a conversation.
func densestMessages(msgs []model.Message, terms []string, cap int) []model.Message {
	type scored struct {
		i, hits int
	}
	var ranked []scored
	for i, m := range msgs {
		text := strings.ToLower(m.Text)
		hits := 0
		for _, t := range terms {
			if strings.Contains(text, strings.ToLower(t)) {
				hits++
			}
		}
		if hits > 0 {
			ranked = append(ranked, scored{i, hits})
		}
	}
	sort.SliceStable(ranked, func(a, b int) bool { return ranked[a].hits > ranked[b].hits })
	if len(ranked) > cap {
		ranked = ranked[:cap]
	}
	keep := make(map[int]bool, len(ranked))
	for _, r := range ranked {
		keep[r.i] = true
	}
	out := make([]model.Message, 0, len(keep))
	for i, m := range msgs {
		if keep[i] {
			out = append(out, m)
		}
	}
	return out
}

// sessionIDs is what the usage log needs to attribute a déjà vu moment to the
// sessions it was about.
func sessionIDs(ss []model.Session) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if s.ID != "" {
			out = append(out, s.ID)
		}
	}
	return out
}

const promptHookLead = "deja found prior sessions matching this request. If one genuinely helps, use it and tell the user in one short line what deja-vu recalled; otherwise ignore silently.\n"

// weakRecallPointer is what an unconfident match injects instead of a digest:
// one line saying history exists and how to reach it. A single rare term is a
// hint, not an answer, and this hook runs on every message — spending a full
// digest on a hint is where most of deja's unprompted context cost went.
// Naming the topic and the date is what makes the pointer worth following.
func weakRecallPointer(ss []model.Session, terms []string) string {
	if len(ss) == 0 {
		return ""
	}
	s := ss[0]
	topic := dejaVuTopic(s)
	if topic == "" {
		topic = strings.Join(terms, " ")
	}
	when := "earlier"
	if !s.Updated.IsZero() {
		when = search.RelativeDate(s.Updated)
	}
	more := ""
	if len(ss) > 1 {
		more = fmt.Sprintf(" (+%d more)", len(ss)-1)
	}
	return fmt.Sprintf("deja: this project has history on %q from %s%s — call recall with a specific token if it matters here.\n",
		search.SafeLine(topic), when, more)
}
