package main

import (
	"os"
	"strings"
	"testing"
)

// The budget beside hookAdvice is only worth writing if a hook added later has
// to appear in it. Both lists are the same set: every hook a person can type,
// and every hook with a number to be held to (#2668).
func TestEveryHookHasABudgetLine(t *testing.T) {
	src, err := os.ReadFile("hook_typed_by_hand.go")
	if err != nil {
		t.Fatal(err)
	}
	budget, _, ok := strings.Cut(string(src), "// hookAdvice is what to reach for")
	if !ok {
		t.Fatal("the budget comment is gone; it lives above hookAdvice")
	}
	for name := range hookAdvice {
		if !strings.Contains(budget, name) {
			t.Errorf("%s has advice but no budget line", name)
		}
	}
	// And the one hook that is not in hookAdvice, because nobody types it by
	// hand expecting a screen — it still has a number.
	if !strings.Contains(budget, "hook-context") {
		t.Error("hook-context has no budget line")
	}
}

// The dispatcher is the authority on which hooks exist; a new one must reach
// the budget rather than be discovered by a reader wondering what it costs.
func TestEveryDispatchedHookIsInTheBudget(t *testing.T) {
	src, err := os.ReadFile("hook_typed_by_hand.go")
	if err != nil {
		t.Fatal(err)
	}
	budget, _, _ := strings.Cut(string(src), "// hookAdvice is what to reach for")
	for name := range commands {
		if !strings.HasPrefix(name, "hook-") {
			continue
		}
		if !strings.Contains(budget, name) {
			t.Errorf("%s is dispatched but has no budget line", name)
		}
	}
}
