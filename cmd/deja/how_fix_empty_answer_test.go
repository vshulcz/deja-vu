package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The last two surfaces from #2862's list. "No command on this machine mentions
// X" and "No session on this machine ran a command after that error" are honest
// about the machine and silent about the store: on a first run they read as a
// real absence, and an agent concludes the work was never done — the mistake
// #680 named and #2862 and #2863 fixed for recall and blame.
func TestHowAndFixSayTheStoreIsEmpty(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ tool, args string }{
		{"how", `{"what":"deploy"}`},
		{"fix", `{"error":"ld: symbol(s) not found for architecture arm64"}`},
	} {
		text, err := callMCPTool(dir, tc.tool, json.RawMessage(tc.args))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(text, "no indexed history") {
			t.Errorf("%s: an empty store answers as though the thing was never done:\n%s", tc.tool, text)
		}
	}
}

// A store with sessions keeps its own answer: it is a real absence there.
func TestHowAndFixOnAStoreWithHistoryDoNotCallItEmpty(t *testing.T) {
	dir := manySessionStore(t, 3)
	for _, tc := range []struct{ tool, args string }{
		{"how", `{"what":"nothing-like-this-was-ever-run"}`},
		{"fix", `{"error":"ld: symbol(s) not found for architecture arm64"}`},
	} {
		text, err := callMCPTool(dir, tc.tool, json.RawMessage(tc.args))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, "no indexed history") {
			t.Errorf("%s: a store with sessions in it was called empty:\n%s", tc.tool, text)
		}
	}
}
