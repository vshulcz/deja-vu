package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

func TestHookPromptInjectsOnRelevantHit(t *testing.T) {
	withStatsStores(t)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	in := strings.NewReader(`{"prompt":"the long beta session broke again"}`)
	if err := runHookPrompt(in, &out); err != nil {
		t.Fatal(err)
	}
	var resp struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("bad json %q: %v", out.String(), err)
	}
	if resp.HookSpecificOutput.HookEventName != "UserPromptSubmit" {
		t.Fatalf("event = %q", resp.HookSpecificOutput.HookEventName)
	}
	ctx := resp.HookSpecificOutput.AdditionalContext
	if !strings.Contains(ctx, "deja-vu recalled") && !strings.Contains(ctx, "deja found prior sessions") {
		t.Fatalf("context missing narration lead: %q", ctx)
	}
	if len(ctx) > promptHookBudget+256 {
		t.Fatalf("injection too large: %d", len(ctx))
	}
}

func TestHookPromptSilentPaths(t *testing.T) {
	withStatsStores(t)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	for name, prompt := range map[string]string{
		"no meaningful terms": `{"prompt":"ok do it"}`,
		"no matches":          `{"prompt":"quetzalcoatl zeppelin framework meltdown"}`,
		"empty":               `{}`,
		"garbage":             `not json at all`,
	} {
		var out bytes.Buffer
		if err := runHookPrompt(strings.NewReader(prompt), &out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if out.Len() != 0 {
			t.Fatalf("%s: expected silence, got %q", name, out.String())
		}
	}
}

func TestPromptSearchTerms(t *testing.T) {
	got := promptSearchTerms("Why is the connection pool exhausted again in the gateway???")
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "connection") || !strings.Contains(joined, "pool") || !strings.Contains(joined, "gateway") {
		t.Fatalf("terms = %v", got)
	}
	if len(promptSearchTerms("a of to")) != 0 {
		t.Fatal("stop words must not produce terms")
	}
}
