package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/redact"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/prompt"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/usage"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
)

// promptHookBudget keeps per-prompt injections small: this fires on every
// user message, so it must be a hint, not a payload.
//
// 1024 cut the two quoted lines mid-sentence, and what it cut was the tail —
// the paths, names and numbers an answer reuses. Measured on a real store by
// replaying the sweep and comparing each block against what the agent actually
// answered next: blocks carrying words the answer went on to use rose from 20%
// to 25%, and blocks carrying a conclusion from 55 to 57 of 100, for 24 tokens
// a message. 2048 buys nothing further (25% again, 253 tokens).
const promptHookBudget = 1536

// dejaVuMaxMessages caps how large a session can be and still read as one
// rememberable episode. Marathon catch-all sessions rank into everything.
const dejaVuMaxMessages = 300

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
		*h = hookPromptText(withoutInjectedContext(s))
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
	*h = hookPromptText(withoutInjectedContext(sb.String()))
	return nil
}

// withoutInjectedContext drops context an earlier hook already put in front of
// the question. Gemini hands BeforeAgent the whole request, which by then
// carries the <hook_context> block SessionStart returned — deja's own recall.
// Left in, the six search terms all come from that block and none from the
// user, so every prompt recalls whatever the last one did. Gemini escapes the
// angle brackets of anything inside, so both spellings are removed.
func withoutInjectedContext(s string) string {
	for _, tag := range []struct{ open, close string }{
		{"<hook_context>", "</hook_context>"},
		{"&lt;hook_context&gt;", "&lt;/hook_context&gt;"},
		{"<deja-recall>", "</deja-recall>"},
		{"&lt;deja-recall&gt;", "&lt;/deja-recall&gt;"},
	} {
		for {
			start := strings.Index(s, tag.open)
			if start < 0 {
				break
			}
			end := strings.Index(s[start:], tag.close)
			if end < 0 {
				// An unclosed block is still not the user's words, and what
				// follows it cannot be told apart from more of the same.
				s = s[:start]
				break
			}
			s = s[:start] + s[start+end+len(tag.close):]
		}
	}
	return strings.TrimSpace(s)
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
	// The failure the user just reported is worth capturing whether or not
	// this prompt also earns a recall, so it is decided before the gates that
	// silence the recall path.
	nudge := failureNudge(dir, string(input.Prompt))
	terms := prompt.Terms(string(input.Prompt))
	if !promptTermsWorthAsking(terms) {
		return emitNudgeOnly(stdout, plain, nudge)
	}
	// Version, not just presence: terms hash into buckets an index from
	// another format never wrote, so a stale store answers every prompt with
	// nothing and looks exactly like a user with no history (#777).
	if !index.HasManifest(dir) || !index.IsCurrentVersion(dir) || index.Damaged(dir) {
		requestWarmup(dir)
		return emitNudgeOnly(stdout, plain, nudge)
	}
	// The payload first, then the export, then where the process stands: the
	// export is set once per process and will not change, so reading it alone
	// answered a second payload with the first one's project (#2182).
	cwd := hookCWD(input.CWD)
	// Rank THIS project's sessions by how well they match the prompt terms
	// (IDF-weighted), rather than reconstructing an AND query — natural
	// prompts are full of filler that poisons an AND.
	// The sessions this agent session has already been shown, and the one it is
	// writing, are discarded a few lines below on their id alone. Handing them
	// to the ranking means they are never read: on a real store that is 15 of
	// 26 candidates, each read from disk in full before being dropped.
	// What this agent session was already shown, by the text of the block
	// rather than by the session it came from. Banning the session banned
	// everything else it holds: measured on a real store, walking one working
	// session of eighty messages, the second half got three blocks where the
	// same messages without the ban got thirty-eight, and only seven of those
	// were word-for-word repeats. The same past session answers many questions
	// in a day, and after the first answer it went silent.
	seen := alreadyInjected(dir, input.SessionID)
	// A session shown a moment ago is skipped before it is read — that is what
	// keeps an answering call from reading every candidate off disk. A session
	// shown an hour ago is fair game again: the same past work answers many
	// questions in a day, and what stops it repeating itself is the block
	// fingerprint below, not a ban on where it came from.
	recent := recentlyInjected(dir, input.SessionID, injectionCooldown)
	skip := make(map[string]bool, len(recent)+1)
	for id := range recent {
		skip[id] = true
	}
	if input.SessionID != "" {
		skip[input.SessionID] = true
	}
	ranked, matched, strong, idfOf, err := index.ProjectRelevantSkipping(dir, digest.ProjectNameCandidates(cwd), terms, prompt.Candidates, skip)
	rankedAlreadyShown = 0
	for _, s := range ranked {
		if skip[s.ID] {
			rankedAlreadyShown++
		}
	}
	if err != nil || len(ranked) == 0 {
		return emitNudgeOnly(stdout, plain, nudge)
	}
	ss := make([]model.Session, 0, 2)
	confident := false
	// worthDigest is the payload decision, kept apart from the announcement.
	// A word rare enough to identify something is a real match — the bar above
	// says so and lets such a question through — but saying "you have been here"
	// on one word teaches the reader to ignore the line, so that stays at two.
	worthDigest := false
	// The one word of the question that identifies something, kept apart for
	// the test below: asking whether any term was spoken lets "показал" answer
	// for "v11", and nearly every session says a word like that.
	leadTerms := terms
	if ordered := byIdentifying(terms, idfOf); len(ordered) > 0 {
		leadTerms = ordered[:1]
	}
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
		if !search.RecallWorthShowing(terms, matched[i]) {
			continue
		}
		// A word rare enough to identify something is a real match on its own —
		// the bar just above says so in as many words, and lets such a question
		// through. The payload rule did not: it asked only for two informative
		// terms, so whether the reader got the memory or a pointer to it turned
		// on whether some ordinary word of the question happened to also appear
		// in that session.
		//
		// Measured on a real store over sixty subjects the project holds:
		// "напомни, что мы решали про X" returned content 63% of the time and
		// "what did we decide about X" returned it never — the same question,
		// the same answer sitting in the same session.
		if matched[i] >= 2 {
			confident = true
		}
		if matched[i] >= 2 || strong[i] >= 1 {
			worthDigest = true
		}
		// The session being written right now is never worth recalling to
		// itself. Work merely fresh is a different case: "what did we just
		// change" is a question about the last ten minutes, so age alone no
		// longer withholds an answer — it only withholds the unprompted
		// déjà vu line below.
		//
		// Asked before the narrowing below, not after: narrowing scans every
		// message, the session being written is usually the longest one on the
		// store, and it was scanned in full only to be dropped on the next
		// line. Measured on a 1149-session store, moving these two checks up
		// takes the hook's median from 181 ms to 136 ms.
		if s.ID == input.SessionID {
			continue
		}
		if !search.SpeechCarriesAnyTerm(s, leadTerms) {
			// The subject matched, but only where a tool printed it: a
			// failing job line, a bumped dependency, a pinned action
			// version. Nobody in that session said anything about it, and a
			// block built from those lines answers a different question
			// than the one asked.
			continue
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
		ss = append(ss, s)
		if len(ss) == 2 {
			break
		}
	}
	if len(ss) == 0 {
		return nil
	}
	// Of the two candidates, lead with the one that settled something about the
	// question rather than the one that merely mentioned it. The benchmark has
	// been applying this since the arm was written — and saying so in a comment
	// that claimed the hook did the same — while the hook did not, so the
	// concluded_session arm scored a behaviour the product never had.
	// Of the two candidates, lead with the one that settled something about the
	// question rather than the one that merely mentioned it. The benchmark has
	// been applying this since the arm was written — and saying so in a comment
	// that claimed the hook did the same — while the hook did not, so the
	// concluded_session arm scored a behaviour the product never had.
	// Of the two candidates, lead with the one that settled something about the
	// question rather than the one that merely mentioned it. The benchmark has
	// been applying this since the arm was written — and saying so in a comment
	// that claimed the hook did the same — while the hook did not.
	ss = search.LeadWithConclusion(ss, terms)
	// A rejected session is not an equal answer, and the mark has to travel
	// with it into the block the agent reads (#761).
	ss, rejectedWarning := orderForInjection(ss)

	// "You have been here" on the strength of one word teaches the user to
	// ignore the line. The recall itself still goes in.
	showLine := confident && dejaVuLineDue(dir, input.SessionID)
	// The payload is sized to the claim. Two informative terms is a real match
	// and earns the digest; a single rare term is a hint, and a hint that costs
	// a full digest on every message is most of what deja spends. The pointer
	// keeps it discoverable — the agent learns there is history here and can
	// ask for it — for 134 bytes against the 0.5-1.3 KB a real digest measures.
	var body string
	if worthDigest {
		// Hand the digest the query's words most-identifying first. It has no
		// way of its own to tell "mm_status" from "decide", and the session
		// that answers often says both — measured live, ten of the answers
		// this hook newly returns open on the ordinary word.
		digest := search.AutoRecallDigestForAsked(ss, digestBudget(confident), byIdentifying(terms, idfOf), string(input.Prompt))
		if strings.TrimSpace(digest) == "" {
			return emitNudgeOnly(stdout, plain, nudge)
		}
		tail := citationLine(ss[0], terms)
		if nudge != "" {
			tail += "\n" + nudge
		}
		body = promptHookLead + rejectedWarning + digest + tail
	} else {
		body = weakRecallPointer(ss, terms) + rejectedWarning
		if nudge != "" {
			body += "\n" + nudge
		}
	}
	// The same block twice is wallpaper, and the reader stops looking. Judged
	// on what would be shown, so a session whose other lines answer the next
	// question still gets to answer it.
	if seen[blockFingerprint(body)] {
		return emitNudgeOnly(stdout, plain, nudge)
	}
	out := frameRecall(body)
	rememberInjectedIDs(dir, input.SessionID, blockFingerprint(body))
	rememberInjected(dir, input.SessionID, ss)
	usage.RecordDigestFrom(dir, usage.KindDejaVu, out, input.SessionID, len(ss), rawSize(ss),
		terms, sessionProjects(ss), sessionIDs(ss))
	if plain {
		fmt.Fprintln(stdout, out)
		return nil
	}
	var resp sessionStartHookResponse
	resp.HookSpecificOutput.HookEventName = "UserPromptSubmit"
	resp.HookSpecificOutput.AdditionalContext = out
	if showLine {
		resp.SystemMessage = dejaVuLine(ss[0], viaTerms(out, terms)...)
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
	return len(terms) >= 2 || search.HasIdentifierTerm(terms)
}

// byIdentifying orders the query's terms by what the index says each is worth,
// rarest first, so a caller choosing one line out of a session can prefer the
// word that identified the match over the word that merely appeared in it.
func byIdentifying(terms []string, idf map[string]float64) []string {
	if len(idf) == 0 || len(terms) < 2 {
		return terms
	}
	out := append([]string(nil), terms...)
	sort.SliceStable(out, func(i, j int) bool {
		if idf[out[i]] != idf[out[j]] {
			return idf[out[i]] > idf[out[j]]
		}
		// A store of one or two sessions collapses every ratio to zero, so the
		// terms all tied and the word the session had to have spoken was
		// whichever the reader typed first — "seeing", in a question about
		// gateway_timeout. The only session such a store holds was then
		// dropped for not saying it (#2257). The rest of the ranking already
		// treats a tiny corpus as a special case; this is that case for the
		// lead term: prefer the word that reads like a term of art.
		// Shape before length, or "dashboards" would out-rank "s3": a word
		// carrying an underscore, a dot, a slash or a digit names something
		// whatever its length, and length is only the tiebreak among words
		// that look alike.
		if a, b := symbolShaped(out[i]), symbolShaped(out[j]); a != b {
			return a
		}
		if a, b := identifying(out[i]), identifying(out[j]); a != b {
			return a
		}
		return utf8.RuneCountInString(out[i]) > utf8.RuneCountInString(out[j])
	})
	return out
}

// identifying reports whether one term reads like a term of art — the same
// test the recall bar applies to a whole question, asked of a single word. It
// admits any ordinary word of three letters, so the length below settles the
// ties it leaves: "gateway_timeout" over "seeing".
func identifying(term string) bool { return search.HasIdentifierTerm([]string{term}) }

// symbolShaped reports whether a term is punctuated or numbered the way a
// symbol, a path or a version is — "gateway_timeout", "pkg/index", "v11", "s3".
func symbolShaped(term string) bool {
	for _, r := range term {
		if r == '_' || r == '.' || r == '/' || r == '-' || (r >= '0' && r <= '9') {
			return true
		}
	}
	return false
}

// citationLine pre-writes the narration so the agent copies structure instead
// of having to follow an instruction — models do the former far more reliably.
func citationLine(s model.Session, terms []string) string {
	// What the digest quoted, so the sentence the agent says names the thing
	// the user can see. Falls through to the session's opening when nothing
	// matched literally.
	title := search.MatchedUserLine(s, terms)
	for _, m := range s.Messages {
		if title != "" {
			break
		}
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

// hookseenKey makes an agent session id safe to be one field of a `.hookseen`
// line. The file is whitespace-separated and the id arrives in the hook
// payload, which is whatever the host sends. A space cost that session its
// dedup and handed its entries to whatever id its first word named, and a
// newline wrote a line of its own — so a payload could mark memory as already
// shown to any session it liked (#2167).
//
// Writing and reading go through this, so a mapped id matches itself, and every
// id a harness actually sends maps to itself, which is why no existing file
// needs converting.
func hookseenKey(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return '_'
		}
		return r
	}, s)
}

// hookseenField reports whether a value can be one field of a line as it
// stands. The session id above is mapped because it is only ever compared with
// itself; a value is not, because it is compared with the index's own ids —
// so one that cannot be a field is left out of the file instead.
func hookseenField(s string) bool { return s != "" && hookseenKey(s) == s }

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
	key := hookseenKey(sid)
	for _, line := range strings.Split(string(b), "\n") {
		// Two fields when the entry is a block fingerprint, three when it is a
		// session with the time it was shown.
		parts := strings.Fields(line)
		if len(parts) >= 2 && parts[0] == key {
			out[parts[1]] = true
		}
	}
	return out
}

// blockFingerprint identifies what a block says, so the same words are not
// shown to the same agent session twice while the session they came from stays
// available for what else it holds.
func blockFingerprint(body string) string {
	sum := sha256.Sum256([]byte(strings.Join(strings.Fields(body), " ")))
	return hex.EncodeToString(sum[:8])
}

// injectionCooldown is how many later injections a session sits out after
// being shown. Counted in injections rather than minutes because that is what
// the reader experiences: a run of messages about one thing should not keep
// re-reading the same session, and a day that comes back to it should get it
// again. Ten is the length of a short stretch of work.
const injectionCooldown = 10

// recentlyInjected is alreadyInjected narrowed to the last few things this
// agent session was shown.
func recentlyInjected(dir, sid string, window int) map[string]bool {
	out := map[string]bool{}
	if sid == "" || window <= 0 {
		return out
	}
	b, err := os.ReadFile(dir + ".hookseen")
	if err != nil {
		return out
	}
	lines := strings.Split(string(b), "\n")
	key := hookseenKey(sid)
	// Newest first: the window counts injections, and the file is append-only.
	kept := 0
	for i := len(lines) - 1; i >= 0 && kept < window; i-- {
		parts := strings.Fields(lines[i])
		if len(parts) < 2 || parts[0] != key {
			continue
		}
		kept++
		out[parts[1]] = true
	}
	return out
}

// forgetInjected drops one agent session's entries from the seen list, so
// recall may send it again what it was already shown. Used when the harness
// truncates the conversation and the blocks go with it.
func forgetInjected(dir, sid string) {
	// An early return, not a rule: with no id the filter below matches nothing
	// and the file is rewritten unchanged, so this saves a read and a write.
	if sid == "" {
		return
	}
	p := dir + ".hookseen"
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	key := hookseenKey(sid)
	var kept []string
	for _, line := range strings.Split(string(b), "\n") {
		if line == "" {
			continue
		}
		if parts := strings.Fields(line); len(parts) >= 2 && parts[0] == key {
			continue
		}
		kept = append(kept, line)
	}
	out := ""
	if len(kept) > 0 {
		out = strings.Join(kept, "\n") + "\n"
	}
	_ = atomicfile.Write(p, []byte(out), 0o600)
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
	// O_RDWR so the append can read the last byte first: a hook killed before
	// its newline leaves half a line, and writing onto it costs the record
	// written after — a session deja then does not know it has shown, which is
	// this file's whole job (#1967).
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	if atomicfile.EndsMidLine(f) {
		_, _ = f.WriteString("\n")
	}
	stamp := time.Now().UTC().Format(time.RFC3339)
	for _, s := range ss {
		// The value stays byte-exact: it is looked up against the index's own
		// ids, and a mapped one would match nothing there. One that cannot be
		// a field is dropped instead — a missing dedup entry costs a second
		// showing, where a broken line costs the entry written after it.
		if !hookseenField(s.ID) {
			continue
		}
		fmt.Fprintf(f, "%s %s %s\n", hookseenKey(sid), s.ID, stamp)
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
	// The caller's own lines are kept beyond the tail so its dedup survives the
	// rotation — but only as many as the tail itself. Keeping all of them left
	// a long session able to fill the file with its own entries, after which
	// every write rotated a megabyte again: the tool hooks write a token per
	// call, so that is reachable rather than theoretical (#2164).
	var keep, mine []string
	prefix := hookseenKey(sid) + " "
	for i, ln := range lines {
		if ln == "" {
			continue
		}
		switch {
		case i >= start:
			keep = append(keep, ln)
		case strings.HasPrefix(ln, prefix):
			mine = append(mine, ln)
		}
	}
	if len(mine) > tailLines {
		mine = mine[len(mine)-tailLines:]
	}
	_ = atomicfile.Write(p, []byte(strings.Join(append(mine, keep...), "\n")+"\n"), 0o600)
}

// rememberInjectedIDs records arbitrary dedupe tokens against a session, so a
// hook that injects something other than a session (a command, a file line)
// can avoid repeating it to the same agent turn after turn.
func rememberInjectedIDs(dir, sid string, ids ...string) {
	if sid == "" {
		return
	}
	p := dir + ".hookseen"
	// Rotate rather than stop, for the reason rememberInjected does: a full
	// file used to end the dedup for good, and the hooks that write tokens are
	// the ones whose whole job is not repeating themselves to the same agent.
	// Stopping here left that to whenever a session injection happened to
	// rotate the file, and on a machine whose hooks inject tokens rather than
	// sessions, nothing ever did (#2164).
	if fi, err := os.Stat(p); err == nil && fi.Size() > 1<<20 {
		rotateHookseen(p, sid)
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	if atomicfile.EndsMidLine(f) {
		_, _ = f.WriteString("\n")
	}
	for _, id := range ids {
		if !hookseenField(id) {
			continue
		}
		fmt.Fprintf(f, "%s %s\n", hookseenKey(sid), id)
	}
}

// dejaVuLineDue rate-limits the visible line: a déjà vu that fires every
// prompt is wallpaper, and wallpaper trains the user to ignore the real
// moments. Context still flows to the agent regardless.
//
// The limit is per session, not per machine. Keyed on the index alone it also
// silenced sessions that had never shown a line at all: on one index, four
// fresh sessions inside twenty minutes received recall and one of them said
// so. The people deja is for run several agents at once, so that was the
// common case rather than the corner. A session with no id — a host that
// sends none — falls back to the machine-wide window, which is the old
// behaviour and still better than talking on every prompt.
func dejaVuLineDue(dir, sid string) bool {
	p := dir + ".dejavu"
	now := time.Now()
	key := dejaVuKey(sid)
	if b, err := os.ReadFile(p); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 || fields[0] != key {
				continue
			}
			if ts, err := strconv.ParseInt(fields[1], 10, 64); err == nil &&
				now.Sub(time.Unix(ts, 0)) < dejaVuLineWindow {
				return false
			}
		}
	}
	return recordDejaVuLine(p, key, now)
}

// dejaVuKey is how a session is named in `.dejavu`, which is two fields to a
// line and holds the machine-wide fallback under "-".
//
// Mapped, for the reason hookseenKey is: the id comes from the hook payload, a
// space made the line three fields so nothing ever matched it — the notice then
// fired on every prompt, which the limit exists to prevent — and a newline
// wrote a line under whatever followed it, spending another session's window.
// Prefixed, so an agent calling itself "-" cannot share the fallback's window
// with a host that sent no id at all. Entries age out inside twenty minutes, so
// the shape can change without converting anything (#2170).
func dejaVuKey(sid string) string {
	if sid == "" {
		return "-"
	}
	return "s" + hookseenKey(sid)
}

const (
	dejaVuLineWindow = 20 * time.Minute
	// dejaVuLineKeep bounds the file: one line per session that saw a notice
	// recently, and an agent fleet is tens rather than thousands. Entries
	// older than the window are dropped on every write, so this only caps a
	// burst.
	dejaVuLineKeep = 64
)

// recordDejaVuLine stamps this session and rewrites the file without the
// entries that have aged out. It reports true so callers can return it
// directly: failing to write is not a reason to withhold the line.
func recordDejaVuLine(path, sid string, now time.Time) bool {
	kept := []string{sid + " " + strconv.FormatInt(now.Unix(), 10)}
	if b, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 || fields[0] == sid {
				continue
			}
			ts, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil || now.Sub(time.Unix(ts, 0)) >= dejaVuLineWindow {
				continue
			}
			if len(kept) >= dejaVuLineKeep {
				break
			}
			kept = append(kept, line)
		}
	}
	_ = os.WriteFile(path, []byte(strings.Join(kept, "\n")+"\n"), 0o600)
	return true
}

// viaTerms picks the three query terms the headline will name. Terms the block
// underneath actually carries come first: the line is the reason given for the
// recall, and naming words the reader cannot find below it reads as a misfire.
// Measured on a real store, the block opened on a line carrying a term the
// product used in 94 cases of 94, while the first three terms in extraction
// order agreed with it in 71.
func viaTerms(block string, terms []string) []string {
	if len(terms) <= 3 {
		return terms
	}
	out := make([]string, 0, 3)
	for _, t := range terms {
		if len(out) == 3 {
			break
		}
		if search.TextCarriesTerm(block, t) {
			out = append(out, t)
		}
	}
	// Short of three, fill from the rest in the order they were asked, so the
	// line still says what the question was about.
	for _, t := range terms {
		if len(out) == 3 {
			break
		}
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

// dejaVuLine is the one visible line a déjà vu moment earns: which past
// session answered, and how old it is.
func dejaVuLine(s model.Session, terms ...string) string {
	topic := dejaVuTopic(s, terms...)
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
		// Callers pass terms already chosen by viaTerms; the cap stays here so
		// a caller that has not is still held to three.
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
	// No colon after the name, for the reason the session-start receipts lost
	// theirs: the host introduces the line itself. Claude Code renders this as
	// "UserPromptSubmit says: …", which read "says: deja-vu: you have been
	// here" — seen on screen, two colons in four words.
	return fmt.Sprintf("deja-vu — %s: %q (%s%s)", who, topic, search.RelativeDate(s.Updated), why)
}

// dejaVuTopic picks something a human actually typed. Session titles are the
// first user message, which for some harnesses is injected plumbing
// ("# AGENTS.md instructions <INSTRUCTIONS>...") — showing that as "you have
// been here" reads as a glitch, not a memory.
func dejaVuTopic(s model.Session, terms ...string) string {
	// What the recall actually matched, before the session's title or its
	// opening line. This is the one line a person reads, and on a long session
	// narrowed to its matching region the opening line is whatever chatter
	// began that window: seen on screen as "you have been here — \"migration
	// locked the table\"" answering a question about token rotation.
	if t := strings.TrimSpace(search.MatchedUserLine(s, terms)); t != "" && presentableTopic(t) {
		return t
	}
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

// rankedAlreadyShown counts candidates the ranking returned despite having been
// injected already. A seam, like focusCalls below: such a session is read from
// disk in full and then dropped on its id, and nothing in the output changes
// either way, so only a count can hold the skip in place.
var rankedAlreadyShown int

// focusCalls counts how many sessions were narrowed. A seam, like
// rebuildInProgress above: narrowing walks every message of a session that can
// run to sixteen thousand of them, and the two cheap reasons to discard a
// candidate are checked before it. Nothing else can hold that order in place —
// the output is identical either way and only the time differs, and a test that
// watches a clock is a test that fails on someone else's machine.
var focusCalls int

// focusSession narrows a long session to the neighbourhood of the messages
// that matched, so a haystack contributes its relevant part rather than being
// dropped or flooding the digest. Two messages either side keeps the exchange
// readable: a question without its answer recalls nothing useful.
func focusSession(s model.Session, terms []string) model.Session {
	focusCalls++
	const window = 2
	keep := map[int]bool{}
	// Lowercase every message once and hand the result to both passes: the
	// narrowing below and, when the session stays too big, the density pass
	// after it. Each of them used to lowercase the whole session again, and a
	// marathon session is thousands of messages.
	low := make([]string, len(s.Messages))
	for i, m := range s.Messages {
		low[i] = strings.ToLower(m.Text)
	}
	for i := range s.Messages {
		// The same rule the block is built with. This step spelled the word
		// the way the question happened to spell it, while the ranking that
		// chose the session and the digest that shows a line from it both fold
		// the ending — so a session whose only mention was in another case was
		// narrowed to nothing and dropped whole.
		//
		// Asked for the whole query at once: per term, each call lowercased the
		// entire message again.
		if search.TermHitsLowered(low[i], terms) == 0 {
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
		s.Messages = densestMessages(s.Messages, low, terms, dejaVuMaxMessages)
		return s
	}
	s.Messages = focused
	return s
}

// densestMessages keeps the cap best messages by how many distinct terms each
// one carries, in original order so the exchange still reads as a conversation.
func densestMessages(msgs []model.Message, low, terms []string, cap int) []model.Message {
	type scored struct {
		i, hits int
	}
	var ranked []scored
	for i := range msgs {
		hits := 0
		for _, t := range terms {
			if strings.Contains(low[i], t) {
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

// sessionProjects names the projects behind a served digest, deduped in order.
// The digest log records them so a rule tightened later can be applied to the
// stored text without the sessions still being in the index (#2324).
func sessionProjects(ss []model.Session) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if s.Project == "" || seen[s.Project] {
			continue
		}
		seen[s.Project] = true
		out = append(out, s.Project)
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

// digestBudget is how much room the block gets. A match resting on a single
// rare word is a weaker claim than one resting on two, and it is where most of
// the injections on unrelated prompts come from: measured on cross-paired
// LongMemEval prompts, halving that case takes the average injected block from
// 1077 to 977 characters. It costs nothing that can be measured on real
// questions — 52 answers of 58 either way — because the block that answers a
// single-word question rarely needed the second half.
func digestBudget(confident bool) int {
	budget := promptHookBudget - recallFrameOverhead
	if !confident {
		budget /= 2
	}
	return budget
}

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
