package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The remedy for a wall the shell put up is the command that hit it, corrected,
// and it names nothing the error named — 107 of the 360 pairs served from a real
// store are that shape. Offered alone it reads as an unrelated command to trust.
func TestFixShowsWhatTheRepairedCommandRepaired(t *testing.T) {
	p := index.FixPair{
		Error:    "zsh:1: no matches found: --include=*.go",
		Command:  `grep -rn "quokkabloom" --include="*.go" internal`,
		Failed:   `grep -rn "quokkabloom" --include=*.go internal`,
		Repaired: true,
		When:     time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
	}
	var out bytes.Buffer
	if err := printFixPairs(&out, []index.FixPair{p}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"ran next: " + p.Command, "after this failed: " + p.Failed} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q lacks %q", got, want)
		}
	}
}

// A script applying a correction needs the same two halves the prose shows.
func TestFixJSONCarriesTheCommandThatFailed(t *testing.T) {
	p := index.FixPair{
		Error:    "zsh:1: no matches found: --include=*.go",
		Command:  `grep -rn "quokkabloom" --include="*.go" internal`,
		Failed:   `grep -rn "quokkabloom" --include=*.go internal`,
		Repaired: true,
	}
	var out bytes.Buffer
	if err := writeFixJSON(&out, []index.FixPair{p}); err != nil {
		t.Fatal(err)
	}
	var got fixJSON
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Fixes) != 1 || got.Fixes[0].Failed == "" {
		t.Fatalf("the row does not carry what was repaired: %s", out.String())
	}
	p.Failed, p.Repaired = "", false
	out.Reset()
	if err := writeFixJSON(&out, []index.FixPair{p}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "failed") {
		t.Errorf("an ordinary remedy carries a repair field: %s", out.String())
	}
}

// At the moment of the failure the reader has just run the failing command, so
// the line names the relationship instead of repeating it.
func TestFixLineSaysTheSameCommandWorked(t *testing.T) {
	p := index.FixPair{
		Error:    "zsh:1: no matches found: --include=*.go",
		Command:  `grep -rn "quokkabloom" --include="*.go" internal`,
		Failed:   `grep -rn "quokkabloom" --include=*.go internal`,
		Repaired: true,
		Project:  "deja-vu",
		When:     time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
	}
	got := fixLine(p, 3)
	if !strings.Contains(got, "the same command worked as: "+p.Command) {
		t.Errorf("line %q does not say the command is the reader's own, corrected", got)
	}
	if strings.Contains(got, "what followed it") {
		t.Errorf("a repair is offered as the next thing someone happened to run: %q", got)
	}
	p.Failed, p.Repaired = "", false
	if got := fixLine(p, 3); !strings.Contains(got, "what followed it") {
		t.Errorf("an ordinary remedy lost its wording: %q", got)
	}
}
