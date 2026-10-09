package main

import (
	"io"
	"strings"
	"testing"
)

// One refusal, in one shape, from every command that was converted (#1829).
// Before this, `deja search --limt x` named the flag you meant and the other
// 22 sites just said no — and search was the only one that left its own name
// out, so two refusals side by side did not say which parser had spoken.

func TestUnknownFlagNamesTheCommandAndTheNearMiss(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command string
		known   []string
		typed   string
		want    string
	}{
		{"dropped letter", "show", showFlags, "--limt", `show: unknown flag "--limt" — did you mean --limit?`},
		{"doubled letter", "stats", statsFlags, "--jsson", `stats: unknown flag "--jsson" — did you mean --json?`},
		{"transposition", "log", logFlags, "--lsat", `log: unknown flag "--lsat" — did you mean --last?`},
		{"truncation", "promote", promoteFlags, "--stat", `promote: unknown flag "--stat" — did you mean --state?`},
		{"search is named too", "search", searchFlags, "--limt", `search: unknown flag "--limt" — did you mean --limit?`},
		{"nothing close", "doctor", doctorFlags, "--nope", "doctor: unknown flag \"--nope\" — `deja doctor --help` lists its flags"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := unknownFlag(tc.command, tc.typed, tc.known).Error(); got != tc.want {
				t.Errorf("unknownFlag(%q, %q) = %q, want %q", tc.command, tc.typed, got, tc.want)
			}
		})
	}
}

// A suggestion is only worth making when it is probably right. These are the
// tokens that must not collect one: a bare word is not a flag at all, a single
// dash may be a value, and a token too short to be a name would match anything
// by prefix.
func TestNoSuggestionForWhatIsNotANearMiss(t *testing.T) {
	for _, typed := range []string{"nonsense", "-x", "--", "--j", "--json"} {
		if got := nearestKnownFlag(typed, showFlags); got != "" {
			t.Errorf("nearestKnownFlag(%q) = %q, want silence", typed, got)
		}
		if got := unknownFlag("show", typed, showFlags).Error(); strings.Contains(got, "did you mean") {
			t.Errorf("unknownFlag(%q) offered a suggestion: %q", typed, got)
		}
	}
}

// The refusals as the commands themselves produce them. Each parser refuses on
// the first argument, so none of these runs reaches the work the command does.
func TestConvertedCommandsRefuseWithTheirOwnName(t *testing.T) {
	for _, tc := range []struct {
		command string
		run     func(dir string, args []string) error
		typed   string
		suggest string
	}{
		{"index", cmdIndex, "--rebuidl", "--rebuild"},
		{"forget", runForget, "--sesion", "--session"},
		{"stats", runStats, "--jsn", "--json"},
		{"view", runView, "--ou", "--out"},
		{"remember", runRemember, "--projct", "--project"},
		{"doctor", func(dir string, args []string) error {
			return runDoctor(io.Discard, args, nil, dir)
		}, "--depe", "--deep"},
		{"log", func(dir string, args []string) error {
			return runLogTo(io.Discard, dir, args)
		}, "--jsonn", "--json"},
		{"restore", func(dir string, args []string) error {
			return runRestore(dir, args, io.Discard)
		}, "--forc", "--force"},
		{"promote", func(dir string, args []string) error {
			return runPromote(dir, args, io.Discard)
		}, "--nte", "--note"},
		{"handoff", func(dir string, args []string) error {
			return runHandoff(dir, args, io.Discard)
		}, "--exe", "--exec"},
	} {
		t.Run(tc.command, func(t *testing.T) {
			err := tc.run(t.TempDir(), []string{tc.typed})
			if err == nil {
				t.Fatalf("%s accepted %q", tc.command, tc.typed)
			}
			want := tc.command + ": unknown flag"
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s said %q, want it to start with %q", tc.command, err, want)
			}
			if !strings.Contains(err.Error(), "did you mean "+tc.suggest) {
				t.Errorf("%s said %q, want it to suggest %s", tc.command, err, tc.suggest)
			}
		})
	}
}

// `deja show` takes an id, so a word is not a flag and must not be answered as
// one; the refusal it gets is about the id, not about flags.
func TestShowStillRefusesASecondIdAsAnId(t *testing.T) {
	if _, err := parseShow([]string{"abc", "def"}); err == nil ||
		strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("parseShow(two ids) = %v, want the one-id refusal", err)
	}
}

// Search is the one command where the answer also decides whether a dashed
// token is a query term: a token nowhere near a flag still searches (#755).
func TestSearchStillTakesADashedQueryTerm(t *testing.T) {
	o, err := parseSearch([]string{"--retry budget"})
	if err != nil {
		t.Fatalf("parseSearch(dashed term) = %v, want it treated as a query", err)
	}
	if !strings.Contains(o.Query, "retry budget") {
		t.Fatalf("query = %q, want the dashed term in it", o.Query)
	}
}
