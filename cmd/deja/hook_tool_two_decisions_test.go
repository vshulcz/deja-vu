package main

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// A file with a broad rule and a narrow exception. The exception is newer
// almost by definition — it is an exception to something — so "newest wins"
// hands the agent the half that reverses the rule, with nothing to say the rule
// exists (#2524).
func TestTheEditLineCarriesBothDecisionsWhenTheyFit(t *testing.T) {
	tmp := hermeticEnv(t)
	_ = tmp
	now := time.Now().UTC()
	if err := sources.AppendPromoted("app", "the general rule",
		"every retry path in retry.go goes through the shared budget",
		"claude:broad", "accepted", now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := sources.AppendPromoted("app", "the exception",
		"the webhook replay is the one exception: it retries twice on its own",
		"claude:narrow", "accepted", now); err != nil {
		t.Fatal(err)
	}
	metas := []index.SessionMeta{
		{ID: "narrow", Harness: "claude", Project: "app", Updated: now},
		{ID: "broad", Harness: "claude", Project: "app", Updated: now.Add(-48 * time.Hour)},
	}

	got := promotedDecisionFor(metas)
	if !strings.Contains(got, "webhook replay") {
		t.Errorf("the newest decision is missing: %q", got)
	}
	if !strings.Contains(got, "shared budget") {
		t.Errorf("the rule the exception qualifies is missing, so the line reverses it: %q", got)
	}
}

// When the second does not fit, the line says one is missing rather than
// leaving the reader with the narrower half alone.
func TestTheEditLineSaysWhenADecisionDidNotFit(t *testing.T) {
	hermeticEnv(t)
	now := time.Now().UTC()
	long := "every retry path in retry.go goes through the shared budget, which the pool owns, " +
		"and nothing in this package may open its own client or set its own deadline for any reason"
	if err := sources.AppendPromoted("app", "the general rule", long, "claude:broad", "accepted", now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := sources.AppendPromoted("app", "the exception",
		"the webhook replay is the one exception: it retries twice on its own",
		"claude:narrow", "accepted", now); err != nil {
		t.Fatal(err)
	}
	metas := []index.SessionMeta{
		{ID: "narrow", Harness: "claude", Project: "app", Updated: now},
		{ID: "broad", Harness: "claude", Project: "app", Updated: now.Add(-48 * time.Hour)},
	}

	got := promotedDecisionFor(metas)
	if !strings.Contains(got, "webhook replay") {
		t.Errorf("the newest decision is missing: %q", got)
	}
	if !strings.Contains(got, "1 more") {
		t.Errorf("the line does not say that another standing decision was left out: %q", got)
	}
	if len(got) > toolHookDecisionBudget+40 {
		t.Errorf("the line is %d bytes, well past its budget: %q", len(got), got)
	}
}

// One decision reads exactly as it did.
func TestTheEditLineWithOneDecisionIsUnchanged(t *testing.T) {
	hermeticEnv(t)
	now := time.Now().UTC()
	if err := sources.AppendPromoted("app", "t", "the retry budget stays at 5", "claude:a", "accepted", now); err != nil {
		t.Fatal(err)
	}
	metas := []index.SessionMeta{{ID: "a", Harness: "claude", Project: "app", Updated: now}}
	if got := promotedDecisionFor(metas); got != "the retry budget stays at 5" {
		t.Errorf("a single decision now reads as %q", got)
	}
}
