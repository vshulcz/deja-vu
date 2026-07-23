package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/model"

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

type promptHookInput struct {
	Prompt    string `json:"prompt"`
	SessionID string `json:"session_id"`
}

// runHookPrompt is the UserPromptSubmit hook: search the user's own prompt
// against the index (relevance, not recency) and inject a compact hint only
// when something genuinely matches. Empty output means stay silent — a hook
// that talks every turn is wallpaper. It never builds or refreshes the index:
// this path runs on every prompt and must stay ~milliseconds.
func runHookPrompt(dir string, stdin io.Reader, stdout io.Writer) error {
	var input promptHookInput
	_ = json.NewDecoder(io.LimitReader(stdin, 1<<20)).Decode(&input)
	terms := promptSearchTerms(input.Prompt)
	if len(terms) < 2 {
		return nil
	}
	if !index.HasManifest(dir) {
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
	ranked, matched, err := index.ProjectRelevant(dir, digest.ProjectNameCandidates(cwd), terms, 8)
	if err != nil || len(ranked) == 0 {
		return nil
	}
	ss := make([]model.Session, 0, 2)
	seen := alreadyInjected(dir, input.SessionID)
	for i, s := range ranked {
		// One lucky rare word is not a déjà vu. Demand real overlap before
		// claiming "you have been here" — a false moment teaches the user to
		// ignore the true ones.
		if matched[i] < 2 {
			continue
		}
		// Marathon sessions that touched everything match everything; "you
		// have been here" is about a focused episode, not a haystack.
		if len(s.Messages) > dejaVuMaxMessages {
			continue
		}
		// Never recall the session being written right now, or work fresh
		// enough the user still remembers it — that is anti-magic.
		if s.ID == input.SessionID || (!s.Updated.IsZero() && time.Since(s.Updated) < 15*time.Minute) {
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
	rememberInjected(dir, input.SessionID, ss)
	showLine := dejaVuLineDue(dir)
	digest := search.AutoRecallDigest(ss, promptHookBudget-recallFrameOverhead)
	if strings.TrimSpace(digest) == "" {
		return nil
	}
	lead := "deja found prior sessions matching this request. If one genuinely helps, use it and tell the user in one digest.Short line what deja-vu recalled; otherwise ignore silently.\n"
	out := frameRecall(lead + digest + citationLine(ss[0]))
	usage.RecordDigestTerms(dir, usage.KindDejaVu, out, len(ss), rawSize(ss), terms)
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
// prompt: stop words and digest.Short fragments dropped, capped so the query stays
// specific.
func promptSearchTerms(prompt string) []string {
	fields := strings.FieldsFunc(strings.ToLower(prompt), func(r rune) bool {
		wordy := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.' || r == '/' || r >= 0x400
		return !wordy
	})
	var out []string
	seen := map[string]bool{}
	for _, f := range fields {
		if len(f) < 3 || search.IsStopWord(f) || seen[f] || !techTerm(f) {
			continue
		}
		seen[f] = true
		out = append(out, f)
		if len(out) == 6 {
			break
		}
	}
	return out
}

// techTerm keeps tokens that can actually identify past work: identifiers,
// error codes, paths, or long plain-ASCII words. Ordinary prose — any
// language — matches by theme, not by task, and theme matches are what made
// déjà vu fire on every prompt.
func techTerm(f string) bool {
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
	return long >= 7
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
	title = strings.TrimSpace(title)
	if len([]rune(title)) > 60 {
		title = string([]rune(title)[:60]) + "…"
	}
	date := ""
	if !s.Updated.IsZero() {
		date = ", " + s.Updated.Format("Jan 2")
	}
	return fmt.Sprintf("\nIf it helped, say: \"deja-vu recalled: %s (%s%s) — reusing it.\"", title, s.Harness, date)
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
	f, err := os.OpenFile(dir+".hookseen", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() > 1<<20 {
		return // advisory state; stop growing rather than rotate under a hook
	}
	for _, s := range ss {
		fmt.Fprintf(f, "%s %s\n", sid, s.ID)
	}
}

// dejaVuLine is the one visible line a déjà vu moment earns: which past
// session answered, and how old it is.
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
	return fmt.Sprintf("deja-vu: you have been here — %q (%s%s)", topic, search.RelativeDate(s.Updated), why)
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
