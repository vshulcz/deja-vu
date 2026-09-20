package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

const aReason = "the saving is not where I claimed it was: most of the time goes on the candidate walk, which runs before the shortcut, so the check has to move earlier"

func sessionOf(msgs ...model.Message) model.Session {
	return model.Session{Harness: "claude", ID: "s1", Project: "app", Messages: msgs}
}

// The session's conclusion says nothing about the change — 0 of 81 lines in
// #3722. The turn the edit sits in does, about half the time, and that is what
// the line answer quotes (#3723).
func TestTheTurnAnEditSitsInIsQuoted(t *testing.T) {
	s := sessionOf(
		model.Message{Role: "user", Text: "make the fuzzy tier cheaper"},
		model.Message{Role: "assistant", Text: aReason},
		model.Message{Role: sources.RoleEdit, Text: "/w/pool.go\nmatches := closeTokens(term, idx)"},
	)
	if got := saidBefore(s, 2); got != aReason {
		t.Fatalf("said = %q", got)
	}
	// The user's own instruction is not it: the question is what the session
	// decided, and "make it cheaper" is what it was asked.
	if got := saidBefore(sessionOf(
		model.Message{Role: "user", Text: aReason},
		model.Message{Role: sources.RoleEdit, Text: "/w/pool.go\nx := 1"},
	), 1); got != "" {
		t.Fatalf("a user turn was quoted as the session's own words: %q", got)
	}
}

// Half of these turns are handovers — "now the shared writer" — and they carry
// no reason at all. The floor is what keeps them out; it is a length, because
// the lexical test #3722 used caught 1 reason in 40.
func TestAHandoverLineIsNotQuotedAsAReason(t *testing.T) {
	short := "Now the shared writer."
	if got := saidBefore(sessionOf(
		model.Message{Role: "assistant", Text: short},
		model.Message{Role: sources.RoleEdit, Text: "/w/pool.go\nx := 1"},
	), 1); got != "" {
		t.Fatalf("a handover line was quoted as a reason: %q", got)
	}
	// And the search keeps going past it: the reason is often one turn above
	// the announcement of the edit.
	if got := saidBefore(sessionOf(
		model.Message{Role: "assistant", Text: aReason},
		model.Message{Role: "assistant", Text: short},
		model.Message{Role: sources.RoleEdit, Text: "/w/pool.go\nx := 1"},
	), 2); got != aReason {
		t.Fatalf("said = %q, want the turn above the handover", got)
	}
}

// A session that ran deja keeps what deja printed, and this reads the messages
// around an edit rather than the ones blame already filters. Its own answer
// must not come back as the reason for the line it was about (#1330, #3723).
func TestDejaOwnAnswerIsNotQuotedAsAReason(t *testing.T) {
	own := "pool.go:3 last changed in abcdef12 · 2026-09-01 · fix: one pool per process\n" +
		"written in claude · 2915986c-9f7 · goprojects/deja-vu\n" +
		"why, in full: deja ctx 2915986c-9f7"
	if got := saidBefore(sessionOf(
		model.Message{Role: "assistant", Text: own},
		model.Message{Role: sources.RoleEdit, Text: "/w/pool.go\nx := 1"},
	), 1); got != "" {
		t.Fatalf("deja quoted its own answer back as the reason: %q", got)
	}
	// Including the shape this very line prints, which is the loop one turn
	// further out.
	quoted := saidBeforePrefix + aReason
	if got := saidBefore(sessionOf(
		model.Message{Role: "assistant", Text: quoted},
		model.Message{Role: sources.RoleEdit, Text: "/w/pool.go\nx := 1"},
	), 1); got != "" {
		t.Fatalf("the quoted turn came back as a reason of its own: %q", got)
	}
}

func TestTheQuotedTurnIsBoundedAndFarTurnsAreLeftAlone(t *testing.T) {
	long := strings.Repeat("a reason spelled out at length. ", 40)
	got := saidBefore(sessionOf(
		model.Message{Role: "assistant", Text: long},
		model.Message{Role: sources.RoleEdit, Text: "/w/pool.go\nx := 1"},
	), 1)
	if r := []rune(got); len(r) != saidBeforeMax+1 || !strings.HasSuffix(got, "…") {
		t.Fatalf("quoted %d runes, want %d and a marker", len([]rune(got)), saidBeforeMax+1)
	}
	msgs := []model.Message{{Role: "assistant", Text: aReason}}
	for range saidBeforeLookback + 5 {
		msgs = append(msgs, model.Message{Role: "user", Text: "go on"})
	}
	msgs = append(msgs, model.Message{Role: sources.RoleEdit, Text: "/w/pool.go\nx := 1"})
	if got := saidBefore(sessionOf(msgs...), len(msgs)-1); got != "" {
		t.Fatalf("a turn from earlier work was quoted: %q", got)
	}
}

// Both renderings say what the quote literally is, because it is a reason only
// about half the time.
func TestBothRenderingsLabelTheQuoteForWhatItIs(t *testing.T) {
	target := search.BlameTarget{Base: "pool.go", Line: 42, FullPath: "/w/pool.go"}
	commit := lineCommit{SHA: "abcdef1234567890", Subject: "fix: one pool per process", When: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)}
	author := lineAuthor{
		Session: model.Session{Harness: "claude", ID: "2915986c9f79", Project: "deja-vu"},
		Matched: "cfg.MaxConns = int32(size)",
		Said:    aReason,
		Wrote:   true,
	}
	var prose bytes.Buffer
	printLineAuthor(&prose, target, commit, author, true)
	if !strings.Contains(prose.String(), saidBeforePrefix+aReason) {
		t.Fatalf("prose:\n%s", prose.String())
	}
	var js bytes.Buffer
	if err := writeLineAnswerJSON(&js, buildLineAnswer(target, commit, author, true, "")); err != nil {
		t.Fatal(err)
	}
	var got lineAnswerJSON
	if err := json.Unmarshal(js.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.SaidBefore != aReason {
		t.Fatalf("said_before = %q", got.SaidBefore)
	}
	// Still one line: the recogniser drops one, and this field carries
	// transcript text that can be anything.
	if n := strings.Count(strings.TrimRight(js.String(), "\n"), "\n"); n != 0 {
		t.Fatalf("the answer is %d lines", n+1)
	}
}
