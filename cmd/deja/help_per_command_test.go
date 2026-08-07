package main

import (
	"strings"
	"testing"
)

// Every command rejected `--help` as an unknown flag, and a couple did worse:
// `deja statusline --help` printed a statusline and `deja mcp --help` started
// the server, which hangs the terminal (#1111).
func TestEveryCommandAnswersItsOwnHelp(t *testing.T) {
	for _, name := range []string{
		"doctor", "sources", "stats", "last", "share", "handoff", "promote",
		"forget", "sync", "view", "ctx", "install", "uninstall", "mcp",
		"update", "remember", "friction", "blame", "log", "index", "search",
		"statusline",
	} {
		h := helpForCommand(name)
		if h == "" {
			t.Errorf("%s has no help of its own", name)
			continue
		}
		if !strings.Contains(h, "deja "+name) {
			t.Errorf("%s help does not name it: %q", name, h)
		}
		// One command's help must not carry another's syntax.
		if name != "install" && name != "uninstall" && strings.Contains(h, "targets:") {
			t.Errorf("%s help leaked the install target list:\n%s", name, h)
		}
	}
	if helpForCommand("install") == helpForCommand("doctor") {
		t.Error("two commands answer with the same help")
	}
	if !strings.Contains(helpForCommand("install"), "aider") {
		t.Error("install help dropped the target list under it")
	}
	if h := helpForCommand("nosuchcommand"); h != "" {
		t.Errorf("an unknown word got help: %q", h)
	}
}

func TestWantsHelpStopsAtDoubleDash(t *testing.T) {
	if !wantsHelp([]string{"--json", "-h"}) {
		t.Error("-h not recognised")
	}
	if wantsHelp([]string{"--", "--help"}) {
		t.Error("--help after -- is a query word, not a request for help")
	}
	if wantsHelp([]string{"--json"}) {
		t.Error("help offered where none was asked for")
	}
}
