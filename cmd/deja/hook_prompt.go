package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// promptHookBudget keeps per-prompt injections small: this fires on every
// user message, so it must be a hint, not a payload.
const promptHookBudget = 1024

type promptHookInput struct {
	Prompt string `json:"prompt"`
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
	ss := promptSearch(dir, terms)
	if len(ss) == 0 {
		return nil
	}
	digest := search.AutoRecallDigest(ss, promptHookBudget-recallFrameOverhead)
	if strings.TrimSpace(digest) == "" {
		return nil
	}
	lead := "deja found prior sessions matching this request. If one genuinely helps, use it and tell the user in one short line what deja-vu recalled; otherwise ignore silently.\n"
	out := frameRecall(lead + digest)
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
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' || r == '/' || r >= 0x400)
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

// promptSearch ANDs the full term set first, then backs off to the three
// longest terms — natural prompts rarely match six words verbatim.
func promptSearch(dir string, terms []string) []model.Session {
	if ss := quietSearch(dir, strings.Join(terms, " ")); len(ss) > 0 {
		return ss
	}
	longest := append([]string(nil), terms...)
	if len(longest) > 3 {
		for i := 0; i < len(longest); i++ {
			for j := i + 1; j < len(longest); j++ {
				if len(longest[j]) > len(longest[i]) {
					longest[i], longest[j] = longest[j], longest[i]
				}
			}
		}
		longest = longest[:3]
		return quietSearch(dir, strings.Join(longest, " "))
	}
	return nil
}

func quietSearch(dir, query string) []model.Session {
	ss, err := index.Search(dir, search.Options{Query: query})
	if err != nil || len(ss) == 0 {
		return nil
	}
	if len(ss) > 2 {
		ss = ss[:2]
	}
	return ss
}
