package main

import (
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"io"
	"os"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// promptHookBudget keeps per-prompt injections small: this fires on every
// user message, so it must be a hint, not a payload.
const promptHookBudget = 1024

type promptHookInput struct {
	Prompt    string `json:"prompt"`
	SessionID string `json:"session_id"`
}

// runHookPrompt is the UserPromptSubmit hook: search the user's own prompt
// against the index (relevance, not recency) and inject a compact hint only
// when something genuinely matches. Empty output means stay silent — a hook
// that talks every turn is wallpaper. It never builds or refreshes the index:
// this path runs on every prompt and must stay ~milliseconds.
func runHookPrompt(stdin io.Reader, stdout io.Writer) error {
	var input promptHookInput
	_ = json.NewDecoder(io.LimitReader(stdin, 1<<20)).Decode(&input)
	terms := promptSearchTerms(input.Prompt)
	if len(terms) < 2 {
		return nil
	}
	dir := index.DefaultDir()
	if !index.HasManifest(dir) {
		return nil
	}
	// Materialize a wider candidate set, rank it properly, then trim: the
	// matched order of records is not relevance.
	cand, matched, err := index.FirstMatch(dir, promptCandidates(terms), 8)
	if err != nil || len(cand) == 0 {
		return nil
	}
	hits, err := search.Run(cand, search.Options{Query: matched})
	if err != nil || len(hits) == 0 {
		return nil
	}
	ss := make([]model.Session, 0, 2)
	seen := alreadyInjected(dir, input.SessionID)
	for _, h := range hits {
		s := h.Session
		// Never recall the session being written right now, or work so fresh
		// the user obviously remembers it — that is anti-magic.
		if s.ID == input.SessionID || (!s.Updated.IsZero() && time.Since(s.Updated) < 45*time.Minute) {
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
	digest := search.AutoRecallDigest(ss, promptHookBudget-recallFrameOverhead)
	if strings.TrimSpace(digest) == "" {
		return nil
	}
	lead := "deja found prior sessions matching this request. If one genuinely helps, use it and tell the user in one short line what deja-vu recalled; otherwise ignore silently.\n"
	out := frameRecall(lead + digest + citationLine(ss[0]))
	usage.RecordResult(dir, usage.KindHook, len(out), len(ss), false)
	var resp sessionStartHookResponse
	resp.HookSpecificOutput.HookEventName = "UserPromptSubmit"
	resp.HookSpecificOutput.AdditionalContext = out
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
	for _, f := range fields {
		if len(f) < 3 || search.IsStopWord(f) || seen[f] {
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

// promptCandidates orders the queries to try: the full AND, prefixes of the
// longest terms, then pairs — a rare term that never co-occurs with the rest
// must not poison every attempt. All candidates run under one index snapshot
// via index.FirstMatch, so more candidates cost bucket reads, not manifest
// reloads.
func promptCandidates(terms []string) []string {
	sorted := append([]string(nil), terms...)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if len([]rune(sorted[j])) > len([]rune(sorted[i])) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	if len(sorted) > 4 {
		sorted = sorted[:4]
	}
	var out []string
	for k := len(sorted); k >= 2; k-- {
		out = append(out, strings.Join(sorted[:k], " "))
	}
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			out = append(out, sorted[i]+" "+sorted[j])
		}
	}
	return out
}

// citationLine pre-writes the narration so the agent copies structure instead
// of having to follow an instruction — models do the former far more reliably.
func citationLine(s model.Session) string {
	title := ""
	for _, m := range s.Messages {
		if m.Role == "user" && !isAgentArtifact(m.Text) {
			tt := strings.TrimSpace(shareMessageText(m.Text))
			if tt == "" || strings.HasPrefix(tt, "Exit code") {
				continue
			}
			r := []rune(tt)[0]
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r >= 0x400) {
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
