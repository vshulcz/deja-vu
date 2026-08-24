package main

import (
	"os"
	"strings"
	"testing"
)

// Empty is how "no filter" is spelled internally, so a flag given an empty
// value used to reach the search as no filter at all — `--project ""` from a
// script with an unset variable searched the whole store and read as a scoped
// answer (#1612).
func TestFilterFlagsRefuseAnEmptyValue(t *testing.T) {
	for _, flag := range []string{"--harness", "--project", "--role", "--session"} {
		o, err := parseSearch([]string{"retry", flag, ""})
		if err == nil {
			t.Errorf("%s \"\" was accepted: harness=%q project=%q role=%q session=%q",
				flag, o.Harness, o.Project, o.Role, o.Session)
			continue
		}
		if !strings.Contains(err.Error(), flag) {
			t.Errorf("%s: error does not name the flag: %v", flag, err)
		}
	}
	// The control: real values still parse, and a flag left out stays unset.
	o, err := parseSearch([]string{"retry", "--harness", "claude", "--role", "user"})
	if err != nil {
		t.Fatalf("parseSearch with real values: %v", err)
	}
	if o.Harness != "claude" || o.Role != "user" || o.Project != "" {
		t.Errorf("harness=%q role=%q project=%q", o.Harness, o.Role, o.Project)
	}
}

// `deja last` parses its own flags, and had the same hole.
func TestLastFlagsRefuseAnEmptyValue(t *testing.T) {
	for _, flag := range []string{"--harness", "--project", "--role", "--from"} {
		if _, _, _, err := parseLast([]string{flag, ""}); err == nil {
			t.Errorf("last %s \"\" was accepted", flag)
		}
	}
	if _, o, _, err := parseLast([]string{"--harness", "claude"}); err != nil || o.Harness != "claude" {
		t.Errorf("last --harness claude = %q, %v", o.Harness, err)
	}
}

// The other four parsers had the same block. Review found them after the first
// fix landed on `deja` and `deja last` only (#1612).
func TestEveryFilterParserRefusesAnEmptyValue(t *testing.T) {
	if _, err := parseShow([]string{"abc", "--harness", ""}); err == nil {
		t.Error("show --harness \"\" was accepted")
	}
	if _, err := parseShow([]string{"abc", "--harness", "claude"}); err != nil {
		t.Errorf("show --harness claude: %v", err)
	}
	for _, flag := range []string{"--harness", "--project"} {
		if _, _, _, err := parseBlame([]string{"main.go", flag, ""}); err == nil {
			t.Errorf("blame %s \"\" was accepted", flag)
		}
	}
	if _, _, _, err := parseBlame([]string{"main.go", "--harness", "claude"}); err != nil {
		t.Errorf("blame --harness claude: %v", err)
	}
}

// stats and forget parse their flags inside their run functions, so they need
// the store to reach the guard. Both were named as untested in review.
func TestStatsAndForgetRefuseAnEmptyValue(t *testing.T) {
	hermeticEnv(t)
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := runStats(dir, []string{"--harness", ""}); err == nil {
		t.Error("stats --harness \"\" was accepted")
	}
	if err := runForget(dir, []string{"--project", "", "--dry-run"}); err == nil {
		t.Error("forget --project \"\" was accepted")
	}
	// The control: --unforget keeps its own refusal, which asks for an id
	// rather than offering the flags that forget instead of restoring.
	err := runForget(dir, []string{"--unforget", ""})
	if err == nil || !strings.Contains(err.Error(), "needs an id") {
		t.Errorf("--unforget \"\" lost its own refusal: %v", err)
	}
}
