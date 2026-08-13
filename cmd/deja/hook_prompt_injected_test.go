package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Gemini hands its prompt hook the whole request, and by then the request
// carries whatever SessionStart injected — deja's own recall. Searching that
// text finds the sessions the last recall named, on every prompt, no matter
// what the user asked.
func TestPromptDropsContextAnEarlierHookInjected(t *testing.T) {
	for _, tc := range []struct {
		name, raw, want string
	}{
		{
			name: "gemini wraps and escapes the block it injected",
			raw: "<hook_context>&lt;deja-recall&gt;\n  - Assistant: the AND across query " +
				"words is why plain questions come back empty\n&lt;/deja-recall&gt;</hook_context>" +
				"\n\nHow often does the deploy token rotate?",
			want: "How often does the deploy token rotate?",
		},
		{
			name: "a plain recall block",
			raw:  "<deja-recall>pgbouncer pool sizing</deja-recall>\nwhy is the build slow?",
			want: "why is the build slow?",
		},
		{
			name: "nothing to strip",
			raw:  "why is the build slow?",
			want: "why is the build slow?",
		},
		{
			name: "an unclosed block takes what follows it with it",
			raw:  "ask about caching\n<hook_context>half a block",
			want: "ask about caching",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got hookPromptText
			payload, err := json.Marshal(tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			if err := got.UnmarshalJSON(payload); err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("prompt = %q, want %q", got, tc.want)
			}
		})
	}
}

// The terms are what the search actually runs on, so the check that matters is
// that none of them come from the injected block.
func TestSearchTermsComeFromTheUserNotTheLastRecall(t *testing.T) {
	raw := "<hook_context>&lt;deja-recall&gt;\n  - User: have a look at internal/index/retrieval.go\n" +
		"  - Assistant: the AND across query words is why plain questions come back empty\n" +
		"&lt;/deja-recall&gt;</hook_context>\n\nHow often does the pgbouncer certificate rotate?"
	var prompt hookPromptText
	payload, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := prompt.UnmarshalJSON(payload); err != nil {
		t.Fatal(err)
	}
	terms := promptSearchTerms(string(prompt))
	if len(terms) == 0 {
		t.Fatal("the user's own question produced no search terms")
	}
	for _, term := range terms {
		if strings.Contains("retrieval.go internal/index deja-recall hook_context", term) {
			t.Errorf("term %q came from the block a previous hook injected, not "+
				"from the question: %v", term, terms)
		}
	}
	var found bool
	for _, term := range terms {
		if term == "pgbouncer" {
			found = true
		}
	}
	if !found {
		t.Errorf("the word that identifies the question is missing: %v", terms)
	}
}
